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

// Bridge routes audio between SVXReflector and YSF reflector.
//
// Audio flow:
//   SVXReflector (OPUS/UDP) → decode OPUS → PCM 8kHz → AGC → encode AMBE+2 → YSFD → YSF Reflector
//   YSF Reflector (YSFD/AMBE+2) → decode AMBE+2 → PCM 8kHz → AGC → encode OPUS → SVXReflector (UDP)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.Println("YSF Bridge starting...")

	// --- Configuration ---
	svxHost := envRequired("REFLECTOR_HOST")
	svxPort := envInt("REFLECTOR_PORT", 5300)
	svxAuthKey := envRequired("REFLECTOR_AUTH_KEY")
	svxTG := uint32(envInt("REFLECTOR_TG", 1))
	callsign := envRequired("CALLSIGN")

	ysfHost := envRequired("YSF_HOST")
	ysfPort := envInt("YSF_PORT", YSFPort)
	ysfCallsign := envDefault("YSF_CALLSIGN", callsign)
	ysfDGID := byte(envInt("YSF_DGID", 0) & 0x7F)

	nodeLocation := envDefault("NODE_LOCATION", "")
	sysop := envDefault("SYSOP", "")

	redisURL := os.Getenv("REDIS_URL")

	log.Printf("Config: SVX=%s:%d TG=%d | YSF=%s:%d callsign=%s DG-ID=%d | svx_cs=%s",
		svxHost, svxPort, svxTG, ysfHost, ysfPort, ysfCallsign, ysfDGID, callsign)

	// --- Initialize vocoders (separate for encode/decode) ---
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
	log.Println("AMBE+2 vocoders initialized (encode + decode)")

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
			ysfHost, ysfPort, ysfCallsign, ysfDGID,
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
	ysfHost string, ysfPort int, ysfCallsign string, ysfDGID byte,
	redisURL string, vocEnc *Vocoder, vocDec *Vocoder, opusDec *opus.Decoder, opusEnc *opus.Encoder,
	sigCh <-chan os.Signal,
) error {

	var (
		svxTalking bool
		ysfTalking bool
		svxTalkMu  sync.Mutex
		ysfTalkMu  sync.Mutex
		// Buffer PCM for AMBE encoding (need 5 x 160 = 800 samples per YSF packet)
		ambeBuffer []int16
		ambeBufMu  sync.Mutex
		// Buffer PCM for OPUS encoding
		pcmBuffer []int16
		pcmBufMu  sync.Mutex
		// AGC
		agcSvxToYsf = NewAGCFromEnv("AGC_SVX_TO_EXT_")
		agcYsfToSvx = NewAGCFromEnv("AGC_EXT_TO_SVX_")
		// Voice bandpass filters
		filterSvxToYsf = NewVoiceFilterFromEnv("FILTER_SVX_TO_EXT_", PCMSampleRate)
		filterYsfToSvx = NewVoiceFilterFromEnv("FILTER_EXT_TO_SVX_", PCMSampleRate)
	)

	// --- Redis client for YSF RX metadata (events page, dashboard talker chips) ---
	var redisCli *RedisClient
	if redisURL != "" {
		rc, err := ParseRedisURL(redisURL)
		if err != nil {
			log.Printf("[Redis] URL parse error: %v (YSF RX publishing disabled)", err)
		} else if err := rc.Connect(); err != nil {
			log.Printf("[Redis] Connect error: %v (YSF RX publishing disabled)", err)
		} else {
			redisCli = rc
			log.Println("[Redis] Connected for YSF RX publishing")
		}
	}
	redisKey := "ysf_rx:" + strings.TrimSpace(callsign)

	svx := NewSVXLinkClient(svxHost, svxPort, svxAuthKey, callsign, nodeLocation, sysop)
	svx.SetExtraNodeInfo(map[string]interface{}{
		"remoteHost": ysfHost,
	})
	ysf := NewYSFClient(ysfHost, ysfPort, ysfCallsign, ysfDGID)

	// --- SVXReflector → YSF audio path ---
	// OPUS → PCM → AMBE+2 (5 frames per YSF packet)
	svx.SetAudioCallback(func(opusFrame []byte) {
		ysfTalkMu.Lock()
		if ysfTalking {
			ysfTalkMu.Unlock()
			return
		}
		ysfTalkMu.Unlock()

		pcm := make([]int16, 960)
		n, err := opusDec.Decode(opusFrame, pcm)
		if err != nil {
			log.Printf("[SVX→YSF] OPUS decode error: %v", err)
			return
		}
		pcm = pcm[:n]
		filterSvxToYsf.Process(pcm)
		agcSvxToYsf.Process(pcm)

		ambeBufMu.Lock()
		ambeBuffer = append(ambeBuffer, pcm...)

		// YSF sends 5 AMBE frames per packet (5 x 160 = 800 samples)
		for len(ambeBuffer) >= 5*PCMFrameSize {
			var frames [5][YSFAMBEFrameSize]byte
			for i := 0; i < 5; i++ {
				var chunk [PCMFrameSize]int16
				copy(chunk[:], ambeBuffer[i*PCMFrameSize:(i+1)*PCMFrameSize])
				frames[i] = vocEnc.Encode(chunk)
			}
			ambeBuffer = ambeBuffer[5*PCMFrameSize:]
			ambeBufMu.Unlock()

			if err := ysf.SendVoice(frames); err != nil {
				log.Printf("[SVX→YSF] SendVoice error: %v", err)
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

		log.Printf("[SVX→YSF] Talker start: %s on TG %d", cs, tg)
		filterSvxToYsf.Reset()
		agcSvxToYsf.Reset()

		ambeBufMu.Lock()
		ambeBuffer = ambeBuffer[:0]
		ambeBufMu.Unlock()

		ysf.StartTX()
	})

	svx.SetTalkerStopCallback(func(tg uint32, cs string) {
		if tg != svxTG || strings.EqualFold(cs, callsign) {
			return
		}

		svxTalkMu.Lock()
		svxTalking = false
		svxTalkMu.Unlock()

		log.Printf("[SVX→YSF] Talker stop: %s", cs)

		// Flush remaining buffer
		ambeBufMu.Lock()
		if len(ambeBuffer) > 0 {
			for len(ambeBuffer) < 5*PCMFrameSize {
				ambeBuffer = append(ambeBuffer, 0)
			}
			var frames [5][YSFAMBEFrameSize]byte
			for i := 0; i < 5; i++ {
				var chunk [PCMFrameSize]int16
				copy(chunk[:], ambeBuffer[i*PCMFrameSize:(i+1)*PCMFrameSize])
				frames[i] = vocEnc.Encode(chunk)
			}
			ambeBuffer = ambeBuffer[:0]
			ambeBufMu.Unlock()
			ysf.SendVoice(frames)
		} else {
			ambeBufMu.Unlock()
		}

		if err := ysf.StopTX(); err != nil {
			log.Printf("[SVX→YSF] StopTX error: %v", err)
		}
	})

	// --- YSF → SVXReflector audio path ---
	var (
		ysfStreamActive bool
		ysfRxLastFrame  time.Time
	)

	// ysfRxJSON renders the talker metadata picked up by reflector_listener and
	// rendered as the pink "src_radio" pill on /events.
	ysfRxJSON := func(srcRadio, srcGateway string, dgid byte) string {
		return fmt.Sprintf(`{"src_radio":%q,"src_gateway":%q,"dgid":%d}`,
			strings.TrimSpace(srcRadio), strings.TrimSpace(srcGateway), dgid)
	}

	// stopYsfRxStream finishes the YSF→SVX transmission: flushes the last
	// partial OPUS frame, clears the state flags, and tells SVX the talker
	// has dropped. Safe to call when no stream is active (no-op).
	stopYsfRxStream := func() {
		ysfTalkMu.Lock()
		if !ysfStreamActive {
			ysfTalkMu.Unlock()
			return
		}
		ysfStreamActive = false
		ysfTalking = false
		ysfTalkMu.Unlock()

		log.Println("[YSF→SVX] Voice end")

		pcmBufMu.Lock()
		if len(pcmBuffer) > 0 {
			samples := pcmBuffer
			pcmBuffer = nil
			pcmBufMu.Unlock()
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

		if redisCli != nil {
			if err := redisCli.Del(redisKey); err != nil {
				log.Printf("[Redis] DEL error: %v", err)
			}
		}
	}

	ysf.SetVoiceCallback(func(frame *YSFVoiceFrame) {
		svxTalkMu.Lock()
		if svxTalking {
			svxTalkMu.Unlock()
			return
		}
		svxTalkMu.Unlock()

		// Open a new RX stream on HEADER, or on a COMM frame that arrives
		// while we're idle (in case the HEADER was lost in transit).
		ysfTalkMu.Lock()
		ysfRxLastFrame = time.Now()
		if !ysfStreamActive && (frame.FI == YSF_FI_HEADER || frame.FI == YSF_FI_COMM) {
			ysfStreamActive = true
			ysfTalking = true
			ysfTalkMu.Unlock()

			srcCS := strings.TrimSpace(frame.SrcRadio)
			srcGW := strings.TrimSpace(frame.SrcGateway)
			log.Printf("[YSF→SVX] Voice from %s (FI=%d gw=%s)", srcCS, frame.FI, srcGW)
			svx.SendTalkerStart(svxTG, callsign)
			filterYsfToSvx.Reset()
			agcYsfToSvx.Reset()

			pcmBufMu.Lock()
			pcmBuffer = pcmBuffer[:0]
			pcmBufMu.Unlock()

			if redisCli != nil && srcCS != "" {
				if err := redisCli.SetEX(redisKey, 30, ysfRxJSON(srcCS, srcGW, ysfDGID)); err != nil {
					log.Printf("[Redis] SETEX error: %v", err)
				}
			}
		} else {
			ysfTalkMu.Unlock()
		}

		// Only COMM frames carry voice. HEADER/TERM payloads are call-setup
		// data, not AMBE, so feeding them to the vocoder produces garbage.
		if frame.FI == YSF_FI_COMM {
			// Refresh ysf_rx TTL so a long QSO doesn't expire mid-call.
			if redisCli != nil {
				srcCS := strings.TrimSpace(frame.SrcRadio)
				if srcCS != "" {
					if err := redisCli.SetEX(redisKey, 30, ysfRxJSON(srcCS, strings.TrimSpace(frame.SrcGateway), ysfDGID)); err != nil {
						log.Printf("[Redis] SETEX refresh error: %v", err)
					}
				}
			}

			ambeFrames := ExtractVD2AMBE(frame.Payload)
			for _, ambe := range ambeFrames {
				pcm := vocDec.Decode(ambe)
				pcmSlice := pcm[:]
				filterYsfToSvx.Process(pcmSlice)
				agcYsfToSvx.Process(pcmSlice)

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
						log.Printf("[YSF→SVX] OPUS encode error: %v", err)
						continue
					}

					if err := svx.SendAudio(opusBuf[:n]); err != nil {
						log.Printf("[YSF→SVX] SendAudio error: %v", err)
					}
				} else {
					pcmBufMu.Unlock()
				}
			}
		}

		// End on TERM or EOT bit.
		if frame.FI == YSF_FI_TERM || frame.EOT {
			stopYsfRxStream()
		}
	})

	// RX watchdog: real radios sometimes drop the TERM packet, leaving the
	// bridge stuck "talking" on SVX forever. Force-stop after 1s of silence.
	rxWatchdogStop := make(chan struct{})
	defer close(rxWatchdogStop)
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				ysfTalkMu.Lock()
				stale := ysfStreamActive && !ysfRxLastFrame.IsZero() && time.Since(ysfRxLastFrame) > time.Second
				ysfTalkMu.Unlock()
				if stale {
					log.Println("[YSF→SVX] RX timeout (no frames for 1s)")
					stopYsfRxStream()
				}
			case <-rxWatchdogStop:
				return
			}
		}
	}()

	// --- Connect both sides ---
	if err := svx.Connect(); err != nil {
		return fmt.Errorf("SVX connect: %w", err)
	}
	if err := svx.SelectTG(svxTG); err != nil {
		svx.Close()
		return fmt.Errorf("SVX SelectTG: %w", err)
	}

	if err := ysf.Connect(); err != nil {
		svx.Close()
		return fmt.Errorf("YSF connect: %w", err)
	}

	// --- Start background goroutines ---
	go svx.RunTCPReader()
	go svx.RunTCPHeartbeat()
	go svx.RunUDPReader()
	go svx.RunUDPHeartbeat()
	go ysf.RunReader()
	go ysf.RunKeepalive()

	log.Printf("Bridge active: SVX TG %d ↔ YSF %s:%d", svxTG, ysfHost, ysfPort)

	// --- Wait for disconnect or shutdown ---
	var result error
	select {
	case <-svx.Done():
		log.Println("SVXReflector connection lost")
		result = fmt.Errorf("SVX connection lost")
	case <-ysf.Done():
		log.Println("YSF connection lost")
		result = fmt.Errorf("YSF connection lost")
	case <-sigCh:
		log.Println("Shutting down...")
		result = errShutdown
	}

	ysf.Close()
	svx.Close()
	if redisCli != nil {
		redisCli.Del(redisKey)
		redisCli.Close()
	}

	return result
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

func envFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			log.Fatalf("Invalid float for %s: %v", key, err)
		}
		return f
	}
	return fallback
}
