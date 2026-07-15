package main

import (
	"encoding/json"
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

const (
	SVXSampleRate = 16000 // SvxLink ecosystem decodes/encodes Opus at 16kHz (matches every other bridge)
	SVXFrameSize  = 320   // 20ms at 16kHz
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.Println("USRP Bridge starting...")

	// --- Configuration ---
	svxHost := envRequired("REFLECTOR_HOST")
	svxPort := envInt("REFLECTOR_PORT", 5300)
	svxAuthKey := envRequired("REFLECTOR_AUTH_KEY")
	svxTG := uint32(envInt("REFLECTOR_TG", 1))
	callsign := envRequired("CALLSIGN")
	nodeLocation := envDefault("NODE_LOCATION", "")
	sysop := envDefault("SYSOP", "")

	usrpHost := envRequired("USRP_HOST")
	usrpTxPort := envInt("USRP_TX_PORT", 41234)
	usrpRxPort := envInt("USRP_RX_PORT", 41233)

	redisURL := os.Getenv("REDIS_URL")

	log.Printf("Config: SVX=%s:%d TG=%d | USRP peer=%s tx=%d rx=%d",
		svxHost, svxPort, svxTG, usrpHost, usrpTxPort, usrpRxPort)

	// --- OPUS codecs (reflector side only; USRP is raw PCM) ---
	svxDec, err := opus.NewDecoder(SVXSampleRate, 1)
	if err != nil {
		log.Fatalf("OPUS decoder init: %v", err)
	}
	svxEnc, err := opus.NewEncoder(SVXSampleRate, 1, opus.AppVoIP)
	if err != nil {
		log.Fatalf("OPUS encoder init: %v", err)
	}
	svxEnc.SetBitrate(16000)
	svxEnc.SetComplexity(5)

	// --- Shutdown signal ---
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// --- Reconnect loop ---
	backoff := 2 * time.Second
	maxBackoff := time.Minute
	for {
		err := runBridge(svxHost, svxPort, svxAuthKey, svxTG, callsign, nodeLocation, sysop,
			usrpHost, usrpTxPort, usrpRxPort, redisURL, svxDec, svxEnc, sigCh)
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
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

var errShutdown = fmt.Errorf("shutdown")

func runBridge(
	svxHost string, svxPort int, svxAuthKey string, svxTG uint32, callsign, nodeLocation, sysop string,
	usrpHost string, usrpTxPort, usrpRxPort int, redisURL string,
	svxDec *opus.Decoder, svxEnc *opus.Encoder,
	sigCh <-chan os.Signal,
) error {

	var (
		svxTalking  bool
		usrpTalking bool
		talkMu      sync.Mutex

		// SVX→USRP: 8kHz PCM accumulated until a full 160-sample USRP frame is ready.
		usrpBuf   []int16
		usrpBufMu sync.Mutex

		// USRP→SVX: 16kHz PCM accumulated until a full 320-sample OPUS frame is ready.
		svxBuf []int16
		up     upsampler

		// Last talker callsign learned from an inbound USRP metadata TLV.
		lastRxCall   string
		lastRxCallMu sync.Mutex

		// Audio-inactivity watchdog for USRP→SVX. USRP peers do not always send a
		// clean keyup=0 release, so fall back to forcing TalkerStop after silence.
		usrpAudioTimer *time.Timer

		agcSvxToUsrp = NewAGCFromEnv("AGC_SVX_TO_EXT_")
		agcUsrpToSvx = NewAGCFromEnv("AGC_EXT_TO_SVX_")

		filterSvxToUsrp = NewVoiceFilterFromEnv("FILTER_SVX_TO_EXT_", float64(SVXSampleRate))
		filterUsrpToSvx = NewVoiceFilterFromEnv("FILTER_EXT_TO_SVX_", float64(SVXSampleRate))
	)
	const usrpStreamWatchdog = 1200 * time.Millisecond

	// --- Redis (optional, for RX talker metadata) ---
	var redisCli *RedisClient
	if redisURL != "" {
		rc, err := ParseRedisURL(redisURL)
		if err != nil {
			log.Printf("[Redis] URL parse error: %v (disabled)", err)
		} else if err := rc.Connect(); err != nil {
			log.Printf("[Redis] Connect error: %v (disabled)", err)
		} else {
			redisCli = rc
			log.Println("[Redis] Connected")
		}
	}
	rxKey := "usrp_rx:" + strings.TrimSpace(callsign)

	// --- SVX Reflector client ---
	svx := NewSVXLinkClient(svxHost, svxPort, svxAuthKey, callsign, nodeLocation, sysop)
	svx.SetExtraNodeInfo(map[string]interface{}{
		"remoteHost": usrpHost,
		"links": []map[string]interface{}{
			{"localTg": svxTG, "remoteTg": "USRP"},
		},
	})

	// --- USRP client ---
	usrp := NewUSRPClient(usrpHost, usrpTxPort, usrpRxPort, svxTG)

	// ── SVX → USRP audio path ──────────────────────────────────────────────
	// OPUS 16kHz → PCM 16kHz → filter/AGC → downsample 8kHz → 160-sample frames.
	svx.SetAudioCallback(func(opusFrame []byte) {
		talkMu.Lock()
		if usrpTalking {
			talkMu.Unlock()
			return
		}
		talkMu.Unlock()

		pcm := make([]int16, SVXFrameSize*4)
		n, err := svxDec.Decode(opusFrame, pcm)
		if err != nil {
			log.Printf("[SVX→USRP] OPUS decode error: %v", err)
			return
		}
		if n == 0 {
			return
		}
		pcm = pcm[:n]
		filterSvxToUsrp.Process(pcm)
		agcSvxToUsrp.Process(pcm)
		pcm8 := downsample16to8(pcm)

		usrpBufMu.Lock()
		usrpBuf = append(usrpBuf, pcm8...)
		for len(usrpBuf) >= USRPAudioSamples {
			frame := make([]int16, USRPAudioSamples)
			copy(frame, usrpBuf[:USRPAudioSamples])
			usrpBuf = usrpBuf[USRPAudioSamples:]
			usrpBufMu.Unlock()
			if err := usrp.SendVoice(frame); err != nil {
				log.Printf("[SVX→USRP] SendVoice error: %v", err)
			}
			usrpBufMu.Lock()
		}
		usrpBufMu.Unlock()
	})

	svx.SetTalkerStartCallback(func(tg uint32, cs string) {
		if tg != svxTG || strings.EqualFold(cs, callsign) {
			return
		}
		talkMu.Lock()
		svxTalking = true
		talkMu.Unlock()

		log.Printf("[SVX→USRP] Talker start: %s on TG %d", cs, tg)
		filterSvxToUsrp.Reset()
		agcSvxToUsrp.Reset()
		usrpBufMu.Lock()
		usrpBuf = usrpBuf[:0]
		usrpBufMu.Unlock()

		if err := usrp.SendMetadata(cs); err != nil {
			log.Printf("[SVX→USRP] SendMetadata error: %v", err)
		}
	})

	svx.SetTalkerStopCallback(func(tg uint32, cs string) {
		if tg != svxTG || strings.EqualFold(cs, callsign) {
			return
		}
		talkMu.Lock()
		svxTalking = false
		talkMu.Unlock()

		log.Printf("[SVX→USRP] Talker stop: %s", cs)

		// Flush any partial frame, zero-padded to 160 samples.
		usrpBufMu.Lock()
		if len(usrpBuf) > 0 {
			for len(usrpBuf) < USRPAudioSamples {
				usrpBuf = append(usrpBuf, 0)
			}
			frame := make([]int16, USRPAudioSamples)
			copy(frame, usrpBuf[:USRPAudioSamples])
			usrpBuf = usrpBuf[:0]
			usrpBufMu.Unlock()
			usrp.SendVoice(frame)
		} else {
			usrpBufMu.Unlock()
		}
		if err := usrp.SendStop(); err != nil {
			log.Printf("[SVX→USRP] SendStop error: %v", err)
		}
	})

	// ── USRP → SVX audio path ──────────────────────────────────────────────
	// 160-sample 8kHz frames → upsample 16kHz → filter/AGC → 320-sample OPUS.
	stopUsrpTalk := func() {
		if usrpAudioTimer != nil {
			usrpAudioTimer.Stop()
		}
		talkMu.Lock()
		if !usrpTalking {
			talkMu.Unlock()
			return
		}
		usrpTalking = false
		talkMu.Unlock()
		log.Println("[USRP→SVX] Talk stop")
		svx.SendTalkerStop(svxTG, callsign)
		if redisCli != nil {
			redisCli.Del(rxKey)
		}
	}

	usrp.SetMetadataCallback(func(cs string) {
		lastRxCallMu.Lock()
		lastRxCall = strings.TrimSpace(cs)
		lastRxCallMu.Unlock()
		log.Printf("[USRP→SVX] Metadata: talker=%s", cs)
	})

	usrp.SetVoiceCallback(func(pcm8 []int16, keyup bool) {
		talkMu.Lock()
		if svxTalking {
			talkMu.Unlock()
			return
		}
		talking := usrpTalking
		talkMu.Unlock()

		if !keyup {
			if talking {
				stopUsrpTalk()
			}
			return
		}

		if !talking {
			talkMu.Lock()
			usrpTalking = true
			talkMu.Unlock()
			up.reset()
			filterUsrpToSvx.Reset()
			agcUsrpToSvx.Reset()
			svxBuf = svxBuf[:0]

			lastRxCallMu.Lock()
			from := lastRxCall
			lastRxCallMu.Unlock()
			if from == "" {
				from = "USRP"
			}
			log.Printf("[USRP→SVX] Talk start: %s", from)
			svx.SendTalkerStart(svxTG, callsign)
			if redisCli != nil {
				val, _ := json.Marshal(map[string]interface{}{"from": from, "tg": svxTG})
				redisCli.SetEX(rxKey, 30, string(val))
			}
			usrpAudioTimer = time.AfterFunc(usrpStreamWatchdog, func() {
				log.Println("[USRP→SVX] Stream watchdog timeout — forcing TalkerStop")
				stopUsrpTalk()
			})
		} else if usrpAudioTimer != nil {
			usrpAudioTimer.Reset(usrpStreamWatchdog)
		}

		if len(pcm8) == 0 {
			return
		}
		pcm16 := up.process(pcm8)
		filterUsrpToSvx.Process(pcm16)
		agcUsrpToSvx.Process(pcm16)

		svxBuf = append(svxBuf, pcm16...)
		for len(svxBuf) >= SVXFrameSize {
			chunk := svxBuf[:SVXFrameSize]
			svxBuf = svxBuf[SVXFrameSize:]
			opusBuf := make([]byte, 512)
			nn, err := svxEnc.Encode(chunk, opusBuf)
			if err != nil {
				log.Printf("[USRP→SVX] OPUS encode error: %v", err)
				continue
			}
			if err := svx.SendAudio(opusBuf[:nn]); err != nil {
				log.Printf("[USRP→SVX] SendAudio error: %v", err)
			}
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
	if err := usrp.Connect(); err != nil {
		svx.Close()
		return fmt.Errorf("USRP connect: %w", err)
	}

	// --- Start goroutines ---
	go svx.RunTCPReader()
	go svx.RunTCPHeartbeat()
	go svx.RunUDPReader()
	go svx.RunUDPHeartbeat()
	go usrp.RunReader()

	log.Printf("Bridge active: SVX TG %d ↔ USRP %s:%d", svxTG, usrpHost, usrpTxPort)

	// --- Wait for disconnect or shutdown ---
	var result error
	select {
	case <-svx.Done():
		log.Println("SVXReflector connection lost")
		result = fmt.Errorf("SVX connection lost")
	case <-usrp.Done():
		log.Println("USRP connection lost")
		result = fmt.Errorf("USRP connection lost")
	case <-sigCh:
		log.Println("Shutting down...")
		result = errShutdown
	}

	usrp.Close()
	svx.Close()
	if redisCli != nil {
		redisCli.Del(rxKey)
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
		n := 0
		fmt.Sscanf(v, "%d", &n)
		if n > 0 {
			return n
		}
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
