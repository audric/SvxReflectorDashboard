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
	SVXSampleRate   = 48000
	SVXFrameSize    = 960  // 20ms at 48kHz
	ZelloDecodeRate = 48000 // decode Zello OPUS at 48kHz (OPUS resamples internally)
	ZelloEncodeRate = 16000
	ZelloEncFrame   = 960  // 60ms at 16kHz
	SVXPerZello     = 3    // 60ms / 20ms = 3 SVX frames per Zello frame
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.Println("Zello Bridge starting...")

	// --- Configuration ---
	svxHost := envRequired("REFLECTOR_HOST")
	svxPort := envInt("REFLECTOR_PORT", 5300)
	svxAuthKey := envRequired("REFLECTOR_AUTH_KEY")
	svxTG := uint32(envInt("REFLECTOR_TG", 1))
	callsign := envRequired("CALLSIGN")
	nodeLocation := envDefault("NODE_LOCATION", "")
	sysop := envDefault("SYSOP", "")

	zelloUser := envRequired("ZELLO_USERNAME")
	zelloPass := envRequired("ZELLO_PASSWORD")
	zelloChannel := envRequired("ZELLO_CHANNEL")
	zelloChannelPass := envDefault("ZELLO_CHANNEL_PASSWORD", "")
	zelloIssuerID := envRequired("ZELLO_ISSUER_ID")
	zelloKeyFile := envDefault("ZELLO_PRIVATE_KEY_FILE", "/etc/zello/private_key.pem")

	redisURL := os.Getenv("REDIS_URL")

	log.Printf("Config: SVX=%s:%d TG=%d | Zello user=%s channel=%s", svxHost, svxPort, svxTG, zelloUser, zelloChannel)

	// Resolve private key from env var or file
	keyPath, err := ParsePrivateKeyEnvOrFile("ZELLO_PRIVATE_KEY", zelloKeyFile)
	if err != nil {
		log.Fatalf("Private key: %v", err)
	}

	// --- OPUS codecs ---
	// SVX→Zello: decode SVX OPUS at 16kHz (OPUS resamples internally), encode for Zello at 16kHz
	svxDec, err := opus.NewDecoder(ZelloEncodeRate, 1)
	if err != nil {
		log.Fatalf("OPUS decoder (SVX→Zello) init: %v", err)
	}
	zelloEnc, err := opus.NewEncoder(ZelloEncodeRate, 1, opus.AppVoIP)
	if err != nil {
		log.Fatalf("OPUS encoder (Zello) init: %v", err)
	}
	zelloEnc.SetBitrate(16000)
	zelloEnc.SetComplexity(5)

	// Zello→SVX: decode Zello OPUS at 48kHz (OPUS resamples internally), encode for SVX at 48kHz
	zelloDec, err := opus.NewDecoder(SVXSampleRate, 1)
	if err != nil {
		log.Fatalf("OPUS decoder (Zello→SVX) init: %v", err)
	}
	svxEnc, err := opus.NewEncoder(SVXSampleRate, 1, opus.AppVoIP)
	if err != nil {
		log.Fatalf("OPUS encoder (SVX) init: %v", err)
	}
	svxEnc.SetBitrate(24000)
	svxEnc.SetComplexity(5)

	log.Println("OPUS codecs initialized (4 instances: 2 decoders + 2 encoders)")

	// --- Shutdown signal ---
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// --- Reconnect loop ---
	backoff := 2 * time.Second
	maxBackoff := time.Minute

	for {
		err := runBridge(svxHost, svxPort, svxAuthKey, svxTG, callsign, nodeLocation, sysop,
			zelloUser, zelloPass, zelloChannel, zelloChannelPass, zelloIssuerID, keyPath,
			redisURL, svxDec, svxEnc, zelloDec, zelloEnc, sigCh)

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
	svxHost string, svxPort int, svxAuthKey string, svxTG uint32, callsign string, nodeLocation string, sysop string,
	zelloUser, zelloPass, zelloChannel, zelloChannelPass, zelloIssuerID, zelloKeyPath string,
	redisURL string,
	svxDec *opus.Decoder, svxEnc *opus.Encoder, zelloDec *opus.Decoder, zelloEnc *opus.Encoder,
	sigCh <-chan os.Signal,
) error {

	var (
		svxTalking    bool
		zelloTalking  bool
		svxTalkMu     sync.Mutex
		zelloTalkMu   sync.Mutex
		// Buffer for accumulating PCM before encoding to Zello (need 60ms = 960 samples at 16kHz)
		zelloBuffer   []int16
		zelloBufMu    sync.Mutex
		agcSvxToZello = NewAGCFromEnv("AGC_SVX_TO_EXT_")
		agcZelloToSvx = NewAGCFromEnv("AGC_EXT_TO_SVX_")
		// Voice bandpass filters
		filterSvxToZello = NewVoiceFilterFromEnv("FILTER_SVX_TO_EXT_", float64(SVXSampleRate))
		filterZelloToSvx = NewVoiceFilterFromEnv("FILTER_EXT_TO_SVX_", float64(SVXSampleRate))
	)

	// --- Redis (optional, for metadata) ---
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

	// --- SVX Reflector client ---
	svx := NewSVXLinkClient(svxHost, svxPort, svxAuthKey, callsign, nodeLocation, sysop)
	svx.SetExtraNodeInfo(map[string]interface{}{
		"remoteHost": "zello.io",
		"links": []map[string]interface{}{
			{"localTg": svxTG, "remoteTg": zelloChannel},
		},
	})

	// --- Zello client ---
	zello, err := NewZelloClient(zelloUser, zelloPass, zelloChannel, zelloChannelPass, zelloIssuerID, zelloKeyPath)
	if err != nil {
		return fmt.Errorf("Zello client init: %w", err)
	}

	// --- SVX → Zello audio path ---
	// Receive OPUS 48kHz from SVX, decode to PCM 16kHz, buffer 60ms, encode OPUS 16kHz for Zello
	svx.SetAudioCallback(func(opusFrame []byte) {
		zelloTalkMu.Lock()
		if zelloTalking {
			zelloTalkMu.Unlock()
			return
		}
		zelloTalkMu.Unlock()

		// Decode SVX OPUS to 16kHz PCM (OPUS handles internal resampling)
		pcm := make([]int16, ZelloEncFrame) // max output
		n, err := svxDec.Decode(opusFrame, pcm)
		if err != nil {
			log.Printf("[SVX→Zello] OPUS decode error: %v", err)
			return
		}
		if n == 0 {
			return
		}
		pcm = pcm[:n]
		filterSvxToZello.Process(pcm)
		agcSvxToZello.Process(pcm)

		zelloBufMu.Lock()
		zelloBuffer = append(zelloBuffer, pcm...)

		// Encode and send when we have a full 60ms frame (960 samples at 16kHz)
		for len(zelloBuffer) >= ZelloEncFrame {
			chunk := make([]int16, ZelloEncFrame)
			copy(chunk, zelloBuffer[:ZelloEncFrame])
			zelloBuffer = zelloBuffer[ZelloEncFrame:]
			zelloBufMu.Unlock()

			opusBuf := make([]byte, 512)
			n, err := zelloEnc.Encode(chunk, opusBuf)
			if err != nil {
				log.Printf("[SVX→Zello] OPUS encode error: %v", err)
				zelloBufMu.Lock()
				continue
			}

			if err := zello.SendAudio(opusBuf[:n]); err != nil {
				log.Printf("[SVX→Zello] SendAudio error: %v", err)
			}

			zelloBufMu.Lock()
		}
		zelloBufMu.Unlock()
	})

	svx.SetTalkerStartCallback(func(tg uint32, cs string) {
		if tg != svxTG || strings.EqualFold(cs, callsign) {
			return
		}

		svxTalkMu.Lock()
		svxTalking = true
		svxTalkMu.Unlock()

		log.Printf("[SVX→Zello] Talker start: %s on TG %d", cs, tg)
		filterSvxToZello.Reset()
		agcSvxToZello.Reset()
		zelloBufMu.Lock()
		zelloBuffer = zelloBuffer[:0]
		zelloBufMu.Unlock()

		streamID, err := zello.StartStream()
		if err != nil {
			log.Printf("[SVX→Zello] StartStream error: %v", err)
		} else {
			log.Printf("[SVX→Zello] Zello stream started (id=%d)", streamID)
		}
	})

	svx.SetTalkerStopCallback(func(tg uint32, cs string) {
		if tg != svxTG || strings.EqualFold(cs, callsign) {
			return
		}

		svxTalkMu.Lock()
		svxTalking = false
		svxTalkMu.Unlock()

		log.Printf("[SVX→Zello] Talker stop: %s", cs)

		// Flush remaining buffer
		zelloBufMu.Lock()
		if len(zelloBuffer) > 0 {
			for len(zelloBuffer) < ZelloEncFrame {
				zelloBuffer = append(zelloBuffer, 0)
			}
			chunk := make([]int16, ZelloEncFrame)
			copy(chunk, zelloBuffer[:ZelloEncFrame])
			zelloBuffer = zelloBuffer[:0]
			zelloBufMu.Unlock()

			opusBuf := make([]byte, 512)
			n, err := zelloEnc.Encode(chunk, opusBuf)
			if err == nil {
				zello.SendAudio(opusBuf[:n])
			}
		} else {
			zelloBufMu.Unlock()
		}

		if err := zello.StopStream(); err != nil {
			log.Printf("[SVX→Zello] StopStream error: %v", err)
		}
	})

	// --- Zello → SVX audio path ---
	// Receive OPUS 16kHz/60ms from Zello, decode to PCM 48kHz, split into 20ms SVX frames

	zello.SetStreamStartCallback(func(streamID uint32, senderName string) {
		svxTalkMu.Lock()
		if svxTalking {
			svxTalkMu.Unlock()
			log.Printf("[Zello→SVX] Ignoring stream %d from %s (SVX is talking)", streamID, senderName)
			return
		}
		svxTalkMu.Unlock()

		zelloTalkMu.Lock()
		zelloTalking = true
		zelloTalkMu.Unlock()

		log.Printf("[Zello→SVX] Stream start from %s (id=%d)", senderName, streamID)
		filterZelloToSvx.Reset()
		agcZelloToSvx.Reset()
		svx.SendTalkerStart(svxTG, callsign)

		// Publish Zello caller info to Redis
		if redisCli != nil {
			val, _ := json.Marshal(map[string]string{"from": senderName, "channel": zelloChannel})
			redisCli.SetEX("zello_rx:"+strings.TrimSpace(callsign), 30, string(val))
		}
	})

	zello.SetStreamDataCallback(func(streamID uint32, packetID uint32, opusData []byte) {
		svxTalkMu.Lock()
		if svxTalking {
			svxTalkMu.Unlock()
			return
		}
		svxTalkMu.Unlock()

		// Decode Zello OPUS at 48kHz (OPUS handles 16kHz→48kHz internally)
		// Buffer large enough for up to 120ms (Zello may send multi-frame packets)
		pcm := make([]int16, SVXSampleRate/1000*120) // 5760 samples max
		n, err := zelloDec.Decode(opusData, pcm)
		if err != nil {
			log.Printf("[Zello→SVX] OPUS decode error: %v", err)
			return
		}
		if n == 0 {
			return
		}
		pcm = pcm[:n]
		filterZelloToSvx.Process(pcm)
		agcZelloToSvx.Process(pcm)

		// Split into 20ms SVX frames (960 samples each at 48kHz)
		for len(pcm) >= SVXFrameSize {
			chunk := pcm[:SVXFrameSize]
			pcm = pcm[SVXFrameSize:]

			opusBuf := make([]byte, 512)
			nn, err := svxEnc.Encode(chunk, opusBuf)
			if err != nil {
				log.Printf("[Zello→SVX] OPUS encode error: %v", err)
				continue
			}

			if err := svx.SendAudio(opusBuf[:nn]); err != nil {
				log.Printf("[Zello→SVX] SendAudio error: %v", err)
			}
		}
	})

	zello.SetStreamStopCallback(func(streamID uint32) {
		zelloTalkMu.Lock()
		zelloTalking = false
		zelloTalkMu.Unlock()

		log.Printf("[Zello→SVX] Stream stop (id=%d)", streamID)
		svx.SendTalkerStop(svxTG, callsign)

		if redisCli != nil {
			redisCli.Del("zello_rx:" + strings.TrimSpace(callsign))
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

	if err := zello.Connect(); err != nil {
		svx.Close()
		return fmt.Errorf("Zello connect: %w", err)
	}

	// --- Start goroutines ---
	go svx.RunTCPReader()
	go svx.RunTCPHeartbeat()
	go svx.RunUDPReader()
	go svx.RunUDPHeartbeat()
	go zello.RunReader()

	log.Printf("Bridge active: SVX TG %d ↔ Zello channel %q", svxTG, zelloChannel)

	// --- Wait for disconnect or shutdown ---
	var result error
	select {
	case <-svx.Done():
		log.Println("SVXReflector connection lost")
		result = fmt.Errorf("SVX connection lost")
	case <-zello.Done():
		log.Println("Zello connection lost")
		result = fmt.Errorf("Zello connection lost")
	case <-sigCh:
		log.Println("Shutting down...")
		result = errShutdown
	}

	zello.Close()
	svx.Close()
	if redisCli != nil {
		redisCli.Del("zello_rx:" + strings.TrimSpace(callsign))
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
