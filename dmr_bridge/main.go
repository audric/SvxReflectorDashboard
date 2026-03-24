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

// Bridge routes audio between SVXReflector and DMR network.
//
// Audio flow:
//   SVXReflector (OPUS/UDP) → decode OPUS → PCM 8kHz → AGC → encode AMBE+2 → DMRD → DMR Master
//   DMR Master (DMRD/AMBE+2) → decode AMBE+2 → PCM 8kHz → AGC → encode OPUS → SVXReflector (UDP)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.Println("DMR Bridge starting...")

	// --- Configuration ---
	svxHost := envRequired("REFLECTOR_HOST")
	svxPort := envInt("REFLECTOR_PORT", 5300)
	svxAuthKey := envRequired("REFLECTOR_AUTH_KEY")
	svxTG := uint32(envInt("REFLECTOR_TG", 1))
	callsign := envRequired("CALLSIGN")

	dmrHost := envRequired("DMR_HOST")
	dmrPort := envInt("DMR_PORT", DMRDPort)
	dmrID := uint32(envInt("DMR_ID", 0))
	dmrPassword := envRequired("DMR_PASSWORD")
	dmrTalkgroup := uint32(envInt("DMR_TALKGROUP", 9990))
	dmrTimeslot := byte(envInt("DMR_TIMESLOT", 2) - 1) // 1-based → 0-based
	dmrColorCode := byte(envInt("DMR_COLOR_CODE", 1))
	dmrCallsign := envDefault("DMR_CALLSIGN", callsign)
	nodeLocation := envDefault("NODE_LOCATION", "")
	sysop := envDefault("SYSOP", "")

	redisURL := os.Getenv("REDIS_URL")

	log.Printf("Config: SVX=%s:%d TG=%d | DMR=%s:%d ID=%d TG=%d TS=%d CC=%d | cs=%s",
		svxHost, svxPort, svxTG, dmrHost, dmrPort, dmrID, dmrTalkgroup, dmrTimeslot+1, dmrColorCode, callsign)

	// --- Initialize vocoder ---
	voc, err := NewVocoder()
	if err != nil {
		log.Fatalf("Vocoder init failed: %v", err)
	}
	defer voc.Close()
	log.Println("DMR AMBE+2 vocoder initialized")

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
			dmrHost, dmrPort, dmrID, dmrPassword, dmrCallsign, dmrTalkgroup, dmrTimeslot, dmrColorCode,
			redisURL, voc, opusDec, opusEnc, sigCh)

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
	dmrHost string, dmrPort int, dmrID uint32, dmrPassword string, dmrCallsign string,
	dmrTalkgroup uint32, dmrTimeslot byte, dmrColorCode byte,
	redisURL string, voc *Vocoder, opusDec *opus.Decoder, opusEnc *opus.Encoder,
	sigCh <-chan os.Signal,
) error {

	var (
		svxTalking bool
		dmrTalking bool
		svxTalkMu  sync.Mutex
		dmrTalkMu  sync.Mutex
		// Buffer PCM samples for AMBE encoding (one frame = 160 samples = 20ms)
		ambeBuffer []int16
		ambeBufMu  sync.Mutex
		// Buffer PCM samples for OPUS encoding
		pcmBuffer []int16
		pcmBufMu  sync.Mutex
		// AGC instances for each direction
		agcSvxToDmr = NewAGCFromEnv("AGC_SVX_TO_EXT_")
		agcDmrToSvx = NewAGCFromEnv("AGC_EXT_TO_SVX_")
		// Track current DMR source for Redis
		currentSrcID uint32
	)

	// --- Redis client for DMR RX metadata ---
	var redisCli *RedisClient
	if redisURL != "" {
		rc, err := ParseRedisURL(redisURL)
		if err != nil {
			log.Printf("[Redis] URL parse error: %v (DMR RX publishing disabled)", err)
		} else if err := rc.Connect(); err != nil {
			log.Printf("[Redis] Connect error: %v (DMR RX publishing disabled)", err)
		} else {
			redisCli = rc
			log.Println("[Redis] Connected for DMR RX publishing")
		}
	}
	redisKey := "dmr_rx:" + strings.TrimSpace(callsign)

	svx := NewSVXLinkClient(svxHost, svxPort, svxAuthKey, callsign, nodeLocation, sysop)
	svx.SetExtraNodeInfo(map[string]interface{}{
		"nodeClass":  "dmr",
		"remoteHost": dmrHost,
		"links": []map[string]interface{}{
			{"localTg": svxTG, "remoteTg": fmt.Sprintf("DMR TG%d TS%d", dmrTalkgroup, dmrTimeslot+1)},
		},
	})

	dmr := NewDMRClient(dmrHost, dmrPort, dmrID, dmrPassword, dmrCallsign,
		dmrTalkgroup, dmrTimeslot, dmrColorCode)

	// --- SVXReflector → DMR audio path ---
	// OPUS → PCM → AMBE+2 (3 frames buffered per DMRD packet)
	svx.SetAudioCallback(func(opusFrame []byte) {
		dmrTalkMu.Lock()
		if dmrTalking {
			dmrTalkMu.Unlock()
			return
		}
		dmrTalkMu.Unlock()

		pcm := make([]int16, 960)
		n, err := opusDec.Decode(opusFrame, pcm)
		if err != nil {
			log.Printf("[SVX→DMR] OPUS decode error: %v", err)
			return
		}
		pcm = pcm[:n]

		// Normalize audio level before AMBE encoding
		agcSvxToDmr.Process(pcm)

		ambeBufMu.Lock()
		ambeBuffer = append(ambeBuffer, pcm...)

		// Encode PCM to AMBE+2 one frame at a time (160 samples = 20ms)
		for len(ambeBuffer) >= PCMFrameSize {
			var chunk [PCMFrameSize]int16
			copy(chunk[:], ambeBuffer[:PCMFrameSize])
			ambeBuffer = ambeBuffer[PCMFrameSize:]
			ambeBufMu.Unlock()

			ambe := voc.Encode(chunk)
			if err := dmr.SendVoice(ambe); err != nil {
				log.Printf("[SVX→DMR] SendVoice error: %v", err)
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

		log.Printf("[SVX→DMR] Talker start: %s on TG %d", cs, tg)

		agcSvxToDmr.Reset()
		ambeBufMu.Lock()
		ambeBuffer = ambeBuffer[:0]
		ambeBufMu.Unlock()

		dmr.StartTX()
	})

	svx.SetTalkerStopCallback(func(tg uint32, cs string) {
		if tg != svxTG || strings.EqualFold(cs, callsign) {
			return
		}

		svxTalkMu.Lock()
		svxTalking = false
		svxTalkMu.Unlock()

		log.Printf("[SVX→DMR] Talker stop: %s", cs)

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
			ambe := voc.Encode(chunk)
			dmr.SendVoice(ambe)
		} else {
			ambeBufMu.Unlock()
		}

		if err := dmr.StopTX(); err != nil {
			log.Printf("[SVX→DMR] StopTX error: %v", err)
		}
	})

	// --- DMR → SVXReflector audio path ---
	// AMBE+2 → PCM → OPUS (3 AMBE frames per DMRD packet)
	dmr.SetCallStartCallback(func(srcID, dstID uint32) {
		svxTalkMu.Lock()
		if svxTalking {
			svxTalkMu.Unlock()
			return
		}
		svxTalkMu.Unlock()

		dmrTalkMu.Lock()
		dmrTalking = true
		currentSrcID = srcID
		dmrTalkMu.Unlock()

		log.Printf("[DMR→SVX] Call from %d to TG %d", srcID, dstID)

		svx.SendTalkerStart(svxTG, callsign)

		agcDmrToSvx.Reset()
		pcmBufMu.Lock()
		pcmBuffer = pcmBuffer[:0]
		pcmBufMu.Unlock()

		// Publish DMR RX info to Redis
		if redisCli != nil {
			val := dmrRxJSON(srcID, dmrTalkgroup, dmrTimeslot)
			if err := redisCli.SetEX(redisKey, 30, val); err != nil {
				log.Printf("[Redis] SETEX error: %v", err)
			}
		}
	})

	dmr.SetVoiceCallback(func(srcID uint32, frames [3][9]byte) {
		svxTalkMu.Lock()
		if svxTalking {
			svxTalkMu.Unlock()
			return
		}
		svxTalkMu.Unlock()

		// Decode all 3 AMBE frames to PCM
		for _, ambe := range frames {
			pcm := voc.Decode(ambe)
			pcmSlice := pcm[:]
			agcDmrToSvx.Process(pcmSlice)

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
					log.Printf("[DMR→SVX] OPUS encode error: %v", err)
					continue
				}

				if err := svx.SendAudio(opusBuf[:n]); err != nil {
					log.Printf("[DMR→SVX] SendAudio error: %v", err)
				}
			} else {
				pcmBufMu.Unlock()
			}
		}

		// Refresh Redis TTL
		if redisCli != nil {
			val := dmrRxJSON(srcID, dmrTalkgroup, dmrTimeslot)
			if err := redisCli.SetEX(redisKey, 30, val); err != nil {
				log.Printf("[Redis] SETEX error: %v", err)
			}
		}
	})

	dmr.SetCallEndCallback(func(srcID uint32) {
		dmrTalkMu.Lock()
		dmrTalking = false
		currentSrcID = 0
		dmrTalkMu.Unlock()

		log.Printf("[DMR→SVX] Call end from %d", srcID)

		// Clear DMR RX data from Redis
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
	})

	// --- Connect both sides ---
	if err := svx.Connect(); err != nil {
		return fmt.Errorf("SVX connect: %w", err)
	}
	if err := svx.SelectTG(svxTG); err != nil {
		svx.Close()
		return fmt.Errorf("SVX SelectTG: %w", err)
	}

	if err := dmr.Connect(); err != nil {
		svx.Close()
		return fmt.Errorf("DMR connect: %w", err)
	}

	// --- Start background goroutines ---
	go svx.RunTCPReader()
	go svx.RunTCPHeartbeat()
	go svx.RunUDPReader()
	go svx.RunUDPHeartbeat()
	go dmr.RunReader()

	log.Printf("Bridge active: SVX TG %d ↔ DMR TG %d TS %d", svxTG, dmrTalkgroup, dmrTimeslot+1)

	// Suppress unused variable warning
	_ = currentSrcID

	// --- Wait for disconnect or shutdown ---
	var result error
	select {
	case <-svx.Done():
		log.Println("SVXReflector connection lost")
		result = fmt.Errorf("SVX connection lost")
	case <-dmr.Done():
		log.Println("DMR connection lost")
		result = fmt.Errorf("DMR connection lost")
	case <-sigCh:
		log.Println("Shutting down...")
		result = errShutdown
	}

	dmr.Close()
	svx.Close()
	if redisCli != nil {
		redisCli.Del(redisKey)
		redisCli.Close()
	}

	return result
}

func dmrRxJSON(srcID, talkgroup uint32, timeslot byte) string {
	return fmt.Sprintf(`{"src_id":%d,"talkgroup":%d,"timeslot":%d}`, srcID, talkgroup, timeslot+1)
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
