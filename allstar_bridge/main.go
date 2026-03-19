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

// Bridge routes audio between SVXReflector and AllStar node.
//
// Audio flow:
//   SVXReflector (OPUS/UDP) → decode OPUS → PCM 8kHz → AGC → ulaw → IAX2 → AllStar
//   AllStar (IAX2/ulaw) → PCM 8kHz → AGC → encode OPUS → SVXReflector (UDP)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.Println("AllStar Bridge starting...")

	// --- Configuration ---
	svxHost := envRequired("REFLECTOR_HOST")
	svxPort := envInt("REFLECTOR_PORT", 5300)
	svxAuthKey := envRequired("REFLECTOR_AUTH_KEY")
	svxTG := uint32(envInt("REFLECTOR_TG", 1))
	callsign := envRequired("CALLSIGN")

	asNode := envRequired("ALLSTAR_NODE")
	asPassword := envRequired("ALLSTAR_PASSWORD")
	asServer := envRequired("ALLSTAR_SERVER")
	asPort := envInt("ALLSTAR_PORT", IAX2Port)

	nodeLocation := envDefault("NODE_LOCATION", "")
	sysop := envDefault("SYSOP", "")

	log.Printf("Config: SVX=%s:%d TG=%d | AllStar=%s:%d node=%s | callsign=%s",
		svxHost, svxPort, svxTG, asServer, asPort, asNode, callsign)

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
			asServer, asPort, asNode, asPassword,
			opusDec, opusEnc, sigCh)

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
	asServer string, asPort int, asNode string, asPassword string,
	opusDec *opus.Decoder, opusEnc *opus.Encoder,
	sigCh <-chan os.Signal,
) error {

	var (
		svxTalking bool
		asTalking  bool
		svxTalkMu  sync.Mutex
		asTalkMu   sync.Mutex
		// Buffer PCM for ulaw encoding
		ulawBuffer []int16
		ulawBufMu  sync.Mutex
		// Buffer PCM for OPUS encoding
		pcmBuffer []int16
		pcmBufMu  sync.Mutex
		// AGC
		agcSvxToAs = NewAGC()
		agcAsToSvx = NewAGC()
	)

	svx := NewSVXLinkClient(svxHost, svxPort, svxAuthKey, callsign, nodeLocation, sysop)
	svx.SetExtraNodeInfo(map[string]interface{}{
		"remoteHost": asServer,
	})
	iax := NewIAX2Client(asServer, asPort, asNode, asPassword, asServer, callsign)

	// --- SVXReflector → AllStar audio path ---
	// OPUS → PCM → ulaw → IAX2 mini frames
	svx.SetAudioCallback(func(opusFrame []byte) {
		asTalkMu.Lock()
		if asTalking {
			asTalkMu.Unlock()
			return
		}
		asTalkMu.Unlock()

		pcm := make([]int16, 960)
		n, err := opusDec.Decode(opusFrame, pcm)
		if err != nil {
			log.Printf("[SVX→AS] OPUS decode error: %v", err)
			return
		}
		pcm = pcm[:n]
		agcSvxToAs.Process(pcm)

		ulawBufMu.Lock()
		ulawBuffer = append(ulawBuffer, pcm...)

		// Send 160-sample ulaw frames (20ms each)
		for len(ulawBuffer) >= PCMFrameSize {
			chunk := ulawBuffer[:PCMFrameSize]
			ulawBuffer = ulawBuffer[PCMFrameSize:]
			ulawBufMu.Unlock()

			ulaw := PCMToUlaw(chunk)
			if err := iax.SendAudio(ulaw); err != nil {
				log.Printf("[SVX→AS] SendAudio error: %v", err)
			}

			ulawBufMu.Lock()
		}
		ulawBufMu.Unlock()
	})

	svx.SetTalkerStartCallback(func(tg uint32, cs string) {
		if tg != svxTG || strings.EqualFold(cs, callsign) {
			return
		}

		svxTalkMu.Lock()
		svxTalking = true
		svxTalkMu.Unlock()

		log.Printf("[SVX→AS] Talker start: %s on TG %d", cs, tg)
		agcSvxToAs.Reset()

		ulawBufMu.Lock()
		ulawBuffer = ulawBuffer[:0]
		ulawBufMu.Unlock()

		iax.SendKey()
	})

	svx.SetTalkerStopCallback(func(tg uint32, cs string) {
		if tg != svxTG || strings.EqualFold(cs, callsign) {
			return
		}

		svxTalkMu.Lock()
		svxTalking = false
		svxTalkMu.Unlock()

		log.Printf("[SVX→AS] Talker stop: %s", cs)

		// Flush remaining buffer
		ulawBufMu.Lock()
		if len(ulawBuffer) > 0 {
			for len(ulawBuffer) < PCMFrameSize {
				ulawBuffer = append(ulawBuffer, 0)
			}
			chunk := ulawBuffer[:PCMFrameSize]
			ulawBuffer = ulawBuffer[:0]
			ulawBufMu.Unlock()
			ulaw := PCMToUlaw(chunk)
			iax.SendAudio(ulaw)
		} else {
			ulawBufMu.Unlock()
		}

		iax.SendUnkey()
	})

	// --- AllStar → SVXReflector audio path ---
	// ulaw → PCM → OPUS
	var asStreamActive bool

	iax.SetAudioCallback(func(pcm []int16) {
		svxTalkMu.Lock()
		if svxTalking {
			svxTalkMu.Unlock()
			return
		}
		svxTalkMu.Unlock()

		// Detect new transmission (first audio after silence)
		asTalkMu.Lock()
		if !asStreamActive {
			asStreamActive = true
			asTalking = true
			asTalkMu.Unlock()

			log.Printf("[AS→SVX] Voice from AllStar node %s", asNode)
			svx.SendTalkerStart(svxTG, callsign)
			agcAsToSvx.Reset()

			pcmBufMu.Lock()
			pcmBuffer = pcmBuffer[:0]
			pcmBufMu.Unlock()
		} else {
			asTalkMu.Unlock()
		}

		agcAsToSvx.Process(pcm)

		pcmBufMu.Lock()
		pcmBuffer = append(pcmBuffer, pcm...)

		// Encode to OPUS in 60ms chunks (480 samples)
		if len(pcmBuffer) >= 480 {
			samples := make([]int16, 480)
			copy(samples, pcmBuffer[:480])
			pcmBuffer = pcmBuffer[480:]
			pcmBufMu.Unlock()

			opusBuf := make([]byte, 256)
			n, err := opusEnc.Encode(samples, opusBuf)
			if err != nil {
				log.Printf("[AS→SVX] OPUS encode error: %v", err)
				return
			}

			if err := svx.SendAudio(opusBuf[:n]); err != nil {
				log.Printf("[AS→SVX] SendAudio error: %v", err)
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

	if err := iax.Connect(); err != nil {
		svx.Close()
		return fmt.Errorf("AllStar connect: %w", err)
	}

	// --- Start background goroutines ---
	go svx.RunTCPReader()
	go svx.RunTCPHeartbeat()
	go svx.RunUDPReader()
	go svx.RunUDPHeartbeat()
	go iax.RunReader()

	// Silence detection for AllStar → SVX direction
	// AllStar doesn't send explicit end-of-transmission markers via audio,
	// but KEY/UNKEY control frames are handled in the reader.
	// For audio-based detection: if no audio for 1 second, consider TX ended.
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		lastAudioTime := time.Now()
		for {
			select {
			case <-ticker.C:
			case <-sigCh:
				return
			}

			asTalkMu.Lock()
			active := asStreamActive
			asTalkMu.Unlock()

			if !active {
				lastAudioTime = time.Now()
				continue
			}

			pcmBufMu.Lock()
			bufLen := len(pcmBuffer)
			pcmBufMu.Unlock()

			if bufLen > 0 {
				lastAudioTime = time.Now()
			} else if time.Since(lastAudioTime) > 1*time.Second {
				asTalkMu.Lock()
				asStreamActive = false
				asTalking = false
				asTalkMu.Unlock()

				log.Println("[AS→SVX] Voice end (silence timeout)")

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
			}
		}
	}()

	log.Printf("Bridge active: SVX TG %d ↔ AllStar node %s", svxTG, asNode)

	// --- Wait for disconnect or shutdown ---
	var result error
	select {
	case <-svx.Done():
		log.Println("SVXReflector connection lost")
		result = fmt.Errorf("SVX connection lost")
	case <-iax.Done():
		log.Println("AllStar connection lost")
		result = fmt.Errorf("AllStar connection lost")
	case <-sigCh:
		log.Println("Shutting down...")
		result = errShutdown
	}

	iax.Close()
	svx.Close()

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
