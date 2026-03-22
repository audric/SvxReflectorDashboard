package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"gopkg.in/hraban/opus.v2"
)

// Bridge routes audio between SVXReflector and XLX reflector.
//
// Audio flow:
//   SVXReflector (OPUS/UDP) → decode OPUS → PCM 8kHz → encode AMBE → DCS → XLX
//   XLX (DCS/AMBE) → decode AMBE → PCM 8kHz → encode OPUS → SVXReflector (UDP)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.Println("XLX Bridge starting...")

	// --- Configuration ---
	svxHost := envRequired("REFLECTOR_HOST")
	svxPort := envInt("REFLECTOR_PORT", 5300)
	svxAuthKey := envRequired("REFLECTOR_AUTH_KEY")
	svxTG := uint32(envInt("REFLECTOR_TG", 1))
	callsign := envRequired("CALLSIGN")

	xlxHost := envRequired("XLX_HOST")
	xlxPort := envInt("XLX_PORT", DCSPort)
	xlxModule := envDefault("XLX_MODULE", "A")[0]
	xlxReflectorName := envDefault("XLX_REFLECTOR_NAME", "XLX585")
	dcsCallsign := envDefault("DCS_CALLSIGN", callsign)
	dcsMycall := envDefault("DCS_MYCALL", callsign)
	dcsMycallSuffix := envDefault("DCS_MYCALL_SUFFIX", "AMBE")
	nodeLocation := envDefault("NODE_LOCATION", "")
	sysop := envDefault("SYSOP", "")

	redisURL := os.Getenv("REDIS_URL")

	log.Printf("Config: SVX=%s:%d TG=%d | XLX=%s:%d module=%c (%s) | svx_cs=%s dcs_cs=%q mycall=%s",
		svxHost, svxPort, svxTG, xlxHost, xlxPort, xlxModule, xlxReflectorName, callsign, dcsCallsign, dcsMycall)

	// --- Initialize vocoders (separate instances for encode/decode to preserve state) ---
	vocEnc, err := NewVocoder()
	if err != nil {
		log.Fatalf("Vocoder (encode) init failed: %v", err)
	}
	defer vocEnc.Close()
	vocDec, err := NewVocoder()
	if err != nil {
		log.Fatalf("Vocoder (decode) init failed: %v", err)
	}
	defer vocDec.Close()
	log.Println("D-STAR AMBE vocoders initialized (encode + decode)")

	// --- Initialize OPUS codec ---
	opusDec, err := opus.NewDecoder(PCMSampleRate, 1)
	if err != nil {
		log.Fatalf("OPUS decoder init failed: %v", err)
	}

	opusEnc, err := opus.NewEncoder(PCMSampleRate, 1, opus.AppVoIP)
	if err != nil {
		log.Fatalf("OPUS encoder init failed: %v", err)
	}
	opusEnc.SetBitrate(16000)
	opusEnc.SetComplexity(5)

	// --- Shutdown signal ---
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// --- Reconnect loop ---
	backoff := time.Second * 2
	maxBackoff := time.Minute

	for {
		err := runBridge(svxHost, svxPort, svxAuthKey, svxTG, callsign, nodeLocation, sysop,
			xlxHost, xlxPort, xlxModule, xlxReflectorName, dcsCallsign, dcsMycall, dcsMycallSuffix,
			redisURL, vocEnc, vocDec, opusDec, opusEnc, sigCh)

		if err == errShutdown {
			log.Println("Goodbye")
			return
		}

		if err != nil {
			log.Printf("Bridge error: %v", err)
		}

		log.Printf("Reconnecting in %s...", backoff)
		select {
		case <-sigCh:
			log.Println("Shutdown during reconnect wait")
			return
		case <-time.After(backoff):
		}

		backoff = backoff * 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

var errShutdown = fmt.Errorf("shutdown")

func runBridge(
	svxHost string, svxPort int, svxAuthKey string, svxTG uint32, callsign string, nodeLocation string, sysop string,
	xlxHost string, xlxPort int, xlxModule byte, xlxReflectorName string, dcsCallsign string, dcsMycall string, dcsMycallSuffix string,
	redisURL string, vocEnc *Vocoder, vocDec *Vocoder, opusDec *opus.Decoder, opusEnc *opus.Encoder,
	sigCh <-chan os.Signal,
) error {

	var (
		svxTalking    bool
		svxTalkStopAt time.Time // when svxTalking last went false (echo grace window)
		xlxTalking    bool
		svxTalkMu     sync.Mutex
		xlxTalkMu     sync.Mutex
		// Buffer PCM samples for AMBE encoding (one frame = 160 samples = 20ms)
		ambeBuffer []int16
		ambeBufMu  sync.Mutex
		// Buffer PCM samples for OPUS encoding
		pcmBuffer []int16
		pcmBufMu  sync.Mutex
		// AGC instances for each direction
		agcSvxToXlx = NewAGC()
		agcXlxToSvx = NewAGC()
		// Safety timer: auto-reset xlxTalking if no DCS frames for 2s
		// (protects against lost last-frame over UDP)
		xlxTalkTimer *time.Timer
	)

	// --- Redis client for D-STAR RX metadata ---
	var redisCli *RedisClient
	if redisURL != "" {
		rc, err := ParseRedisURL(redisURL)
		if err != nil {
			log.Printf("[Redis] URL parse error: %v (D-STAR RX publishing disabled)", err)
		} else if err := rc.Connect(); err != nil {
			log.Printf("[Redis] Connect error: %v (D-STAR RX publishing disabled)", err)
		} else {
			redisCli = rc
			log.Println("[Redis] Connected for D-STAR RX publishing")
		}
	}
	redisKey := "dstar_rx:" + strings.TrimSpace(callsign)

	svx := NewSVXLinkClient(svxHost, svxPort, svxAuthKey, callsign, nodeLocation, sysop)
	svx.SetExtraNodeInfo(map[string]interface{}{
		"remoteHost": xlxHost,
		"links": []map[string]interface{}{
			{"localTg": svxTG, "remoteTg": fmt.Sprintf("%s %c", xlxReflectorName, xlxModule)},
		},
	})
	dcs := NewDCSClient(xlxHost, xlxPort, dcsCallsign, xlxModule, xlxReflectorName, dcsMycall, dcsMycallSuffix)

	// --- SVXReflector → XLX audio path ---
	// OPUS → PCM → AMBE (one frame at a time via DCS)
	var svxAudioDropped uint64
	var svxAudioProcessed uint64
	svx.SetAudioCallback(func(opusFrame []byte) {
		xlxTalkMu.Lock()
		if xlxTalking {
			xlxTalkMu.Unlock()
			svxAudioDropped++
			if svxAudioDropped == 1 || svxAudioDropped%50 == 0 {
				log.Printf("[SVX→XLX] Audio dropped (xlxTalking=true), total=%d", svxAudioDropped)
			}
			return
		}
		xlxTalkMu.Unlock()
		svxAudioDropped = 0

		pcm := make([]int16, 960)
		n, err := opusDec.Decode(opusFrame, pcm)
		if err != nil {
			log.Printf("[SVX→XLX] OPUS decode error: %v (frame %d bytes)", err, len(opusFrame))
			return
		}
		if n == 0 {
			log.Printf("[SVX→XLX] OPUS decode returned 0 samples (frame %d bytes)", len(opusFrame))
			return
		}
		pcm = pcm[:n]
		svxAudioProcessed++
		if svxAudioProcessed <= 3 {
			log.Printf("[SVX→XLX] OPUS decoded: %d samples from %d bytes", n, len(opusFrame))
		}

		// Normalize audio level before AMBE encoding
		agcSvxToXlx.Process(pcm)

		ambeBufMu.Lock()
		ambeBuffer = append(ambeBuffer, pcm...)

		// DCS sends one AMBE frame per packet (160 samples = 20ms each)
		for len(ambeBuffer) >= PCMFrameSize {
			var chunk [PCMFrameSize]int16
			copy(chunk[:], ambeBuffer[:PCMFrameSize])
			ambeBuffer = ambeBuffer[PCMFrameSize:]
			ambeBufMu.Unlock()

			ambe := vocEnc.Encode(chunk)
			if err := dcs.SendVoice(ambe); err != nil {
				log.Printf("[SVX→XLX] SendVoice error: %v", err)
			}

			ambeBufMu.Lock()
		}
		ambeBufMu.Unlock()
	})

	svx.SetTalkerStartCallback(func(tg uint32, cs string) {
		if tg != svxTG || strings.EqualFold(cs, callsign) {
			return
		}

		svxTalkMu.Lock()
		svxTalking = true
		svxTalkMu.Unlock()

		log.Printf("[SVX→XLX] Talker start: %s on TG %d", cs, tg)
		svxAudioProcessed = 0

		agcSvxToXlx.Reset()
		ambeBufMu.Lock()
		ambeBuffer = ambeBuffer[:0]
		ambeBufMu.Unlock()

		// Set originating callsign as MYCALL and slow data text for D-STAR users
		dcs.SetTXOrigin(cs)
		dcs.StartTX()
	})

	svx.SetTalkerStopCallback(func(tg uint32, cs string) {
		if tg != svxTG || strings.EqualFold(cs, callsign) {
			return
		}

		svxTalkMu.Lock()
		svxTalking = false
		svxTalkStopAt = time.Now()
		svxTalkMu.Unlock()

		log.Printf("[SVX→XLX] Talker stop: %s", cs)

		// Flush remaining buffer
		ambeBufMu.Lock()
		if len(ambeBuffer) > 0 {
			for len(ambeBuffer) < PCMFrameSize {
				ambeBuffer = append(ambeBuffer, 0)
			}
			var chunk [PCMFrameSize]int16
			copy(chunk[:], ambeBuffer[:PCMFrameSize])
			ambeBuffer = ambeBuffer[:0]
			ambeBufMu.Unlock()
			ambe := vocEnc.Encode(chunk)
			dcs.SendVoice(ambe)
		} else {
			ambeBufMu.Unlock()
		}

		if err := dcs.StopTX(); err != nil {
			log.Printf("[SVX→XLX] StopTX error: %v", err)
		}
	})

	// --- XLX → SVXReflector audio path ---
	// AMBE → PCM → OPUS (one frame at a time)
	var xlxCurrentStream uint16
	var slowDecoder SlowDataDecoder
	var lastSlowText string

	dcs.SetVoiceCallback(func(frame *DCSVoiceFrame) {
		svxTalkMu.Lock()
		isSvxTalking := svxTalking
		echoGrace := !svxTalking && !svxTalkStopAt.IsZero() && time.Since(svxTalkStopAt) < 3*time.Second
		svxTalkMu.Unlock()

		if isSvxTalking {
			return
		}

		// Track stream changes
		xlxTalkMu.Lock()
		if frame.StreamID != xlxCurrentStream {
			// Ignore new DCS streams that arrive shortly after our own SVX→XLX TX
			// ended — these are XLX echo frames with a potentially different stream ID.
			if echoGrace {
				xlxTalkMu.Unlock()
				log.Printf("[XLX→SVX] Ignoring echo stream %04X (within 3s of SVX TX stop)", frame.StreamID)
				return
			}

			xlxCurrentStream = frame.StreamID
			xlxTalking = true
			xlxTalkMu.Unlock()

			srcCS := strings.TrimSpace(frame.MYCall)
			srcSuffix := strings.TrimSpace(frame.MYSuffix)
			srcRpt := strings.TrimSpace(frame.RPT1)
			log.Printf("[XLX→SVX] Voice from %s via %s (stream %04X)", srcCS, srcRpt, frame.StreamID)
			svx.SendTalkerStart(svxTG, callsign)

			agcXlxToSvx.Reset()
			pcmBufMu.Lock()
			pcmBuffer = pcmBuffer[:0]
			pcmBufMu.Unlock()

			// Reset slow data decoder and publish initial RX info
			slowDecoder.Reset()
			lastSlowText = ""
			if redisCli != nil && srcCS != "" {
				val := dstarRxJSON(srcCS, srcSuffix, srcRpt, "")
				if err := redisCli.SetEX(redisKey, 30, val); err != nil {
					log.Printf("[Redis] SETEX error: %v", err)
				}
			}

			// Start safety timer for this stream
			if xlxTalkTimer != nil {
				xlxTalkTimer.Stop()
			}
			xlxTalkTimer = time.AfterFunc(2*time.Second, func() {
				xlxTalkMu.Lock()
				if xlxTalking {
					log.Printf("[XLX→SVX] Talk timeout — resetting xlxTalking (last frame likely lost)")
					xlxTalking = false
					xlxCurrentStream = 0
				}
				xlxTalkMu.Unlock()
			})
		} else {
			xlxTalkMu.Unlock()
		}

		// Reset safety timer on every frame (keeps it alive during active stream)
		if xlxTalkTimer != nil {
			xlxTalkTimer.Reset(2 * time.Second)
		}

		// Feed slow data to decoder (every frame except last)
		if !frame.IsLastFrame() {
			slowDecoder.Feed(frame.SlowData, frame.FrameIndex())
			if text := slowDecoder.Text(); text != "" && text != lastSlowText {
				lastSlowText = text
				log.Printf("[XLX→SVX] Slow data text: %q", text)
				if redisCli != nil {
					srcCS := strings.TrimSpace(frame.MYCall)
					srcSuffix := strings.TrimSpace(frame.MYSuffix)
					srcRpt := strings.TrimSpace(frame.RPT1)
					val := dstarRxJSON(srcCS, srcSuffix, srcRpt, text)
					if err := redisCli.SetEX(redisKey, 30, val); err != nil {
						log.Printf("[Redis] SETEX error: %v", err)
					}
				}
			}
		}

		// Last frame = end of transmission
		if frame.IsLastFrame() {
			if xlxTalkTimer != nil {
				xlxTalkTimer.Stop()
			}

			xlxTalkMu.Lock()
			xlxTalking = false
			xlxCurrentStream = 0
			xlxTalkMu.Unlock()

			log.Printf("[XLX→SVX] Voice end (stream %04X)", frame.StreamID)

			// Clear D-STAR RX data from Redis
			if redisCli != nil {
				if err := redisCli.Del(redisKey); err != nil {
					log.Printf("[Redis] DEL error: %v", err)
				}
			}

			// Flush remaining PCM
			pcmBufMu.Lock()
			if len(pcmBuffer) > 0 {
				samples := pcmBuffer
				pcmBuffer = nil
				pcmBufMu.Unlock()
				// Pad to valid OPUS frame size
				for len(samples) < 480 {
					samples = append(samples, 0)
				}
				opusBuf := make([]byte, 256)
				n, err := opusEnc.Encode(samples[:480], opusBuf)
				if err == nil {
					svx.SendAudio(opusBuf[:n])
				}
			} else {
				pcmBufMu.Unlock()
			}

			svx.SendTalkerStop(svxTG, callsign)
			return
		}

		// Decode AMBE to PCM and normalize level
		pcm := vocDec.Decode(frame.AMBE)
		pcmSlice := pcm[:]
		agcXlxToSvx.Process(pcmSlice)

		pcmBufMu.Lock()
		pcmBuffer = append(pcmBuffer, pcmSlice...)

		// Encode to OPUS in 60ms chunks (480 samples)
		if len(pcmBuffer) >= 480 {
			samples := make([]int16, 480)
			copy(samples, pcmBuffer[:480])
			pcmBuffer = pcmBuffer[480:]
			pcmBufMu.Unlock()

			opusBuf := make([]byte, 256)
			n, err := opusEnc.Encode(samples, opusBuf)
			if err != nil {
				log.Printf("[XLX→SVX] OPUS encode error: %v", err)
				return
			}

			if err := svx.SendAudio(opusBuf[:n]); err != nil {
				log.Printf("[XLX→SVX] SendAudio error: %v", err)
			}
		} else {
			pcmBufMu.Unlock()
		}
	})

	// --- Connect both sides ---
	if err := svx.Connect(); err != nil {
		return fmt.Errorf("SVX connect: %w", err)
	}
	if err := svx.SelectTG(svxTG); err != nil {
		svx.Close()
		return fmt.Errorf("SVX SelectTG: %w", err)
	}

	if err := dcs.Connect(); err != nil {
		svx.Close()
		return fmt.Errorf("DCS connect: %w", err)
	}

	// --- Start background goroutines ---
	go svx.RunTCPReader()
	go svx.RunTCPHeartbeat()
	go svx.RunUDPReader()
	go svx.RunUDPHeartbeat()
	go dcs.RunReader()
	go dcs.RunKeepalive()

	log.Printf("Bridge active: SVX TG %d ↔ %s module %c (DCS)", svxTG, xlxReflectorName, xlxModule)

	// --- Wait for disconnect or shutdown ---
	var result error
	select {
	case <-svx.Done():
		log.Println("SVXReflector connection lost")
		result = fmt.Errorf("SVX connection lost")
	case <-dcs.Done():
		log.Println("DCS connection lost")
		result = fmt.Errorf("DCS connection lost")
	case <-sigCh:
		log.Println("Shutting down...")
		result = errShutdown
	}

	dcs.Close()
	svx.Close()
	if redisCli != nil {
		redisCli.Del(redisKey)
		redisCli.Close()
	}

	return result
}

func dstarRxJSON(mycall, suffix, rpt, text string) string {
	s := `{"mycall":"` + mycall + `","suffix":"` + suffix + `","rpt":"` + rpt + `"`
	if text != "" {
		s += `,"text":"` + text + `"`
	}
	return s + `}`
}

func envRequired(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("Required env var %s is not set", key)
	}
	return v
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			log.Fatalf("Invalid integer for %s: %v", key, err)
		}
		return n
	}
	return fallback
}
