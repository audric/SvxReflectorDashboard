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

const (
	SVXSampleRate = 48000
	SVXFrameSize  = 960 // 20ms @ 48kHz (SVX Opus frame)
	MumbleFrame   = 480 // 10ms @ 48kHz (gumble outgoing frame)
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.Println("Mumble Bridge starting...")

	svxHost := envRequired("REFLECTOR_HOST")
	svxPort := envInt("REFLECTOR_PORT", 5300)
	svxAuthKey := envRequired("REFLECTOR_AUTH_KEY")
	svxTG := uint32(envInt("REFLECTOR_TG", 1))
	callsign := envRequired("CALLSIGN")
	nodeLocation := envDefault("NODE_LOCATION", "")
	sysop := envDefault("SYSOP", "")

	mumbleHost := envRequired("MUMBLE_HOST")
	mumblePort := envInt("MUMBLE_PORT", 64738)
	mumbleUser := envRequired("MUMBLE_USERNAME")
	mumblePass := envDefault("MUMBLE_PASSWORD", "")
	mumbleChannel := envRequired("MUMBLE_CHANNEL")

	log.Printf("Config: SVX=%s:%d TG=%d | Mumble=%s:%d user=%s channel=%q",
		svxHost, svxPort, svxTG, mumbleHost, mumblePort, mumbleUser, mumbleChannel)

	// Opus codec for the SVX side (both directions 48kHz mono).
	svxDec, err := opus.NewDecoder(SVXSampleRate, 1)
	if err != nil {
		log.Fatalf("OPUS decoder init: %v", err)
	}
	svxEnc, err := opus.NewEncoder(SVXSampleRate, 1, opus.AppVoIP)
	if err != nil {
		log.Fatalf("OPUS encoder init: %v", err)
	}
	svxEnc.SetBitrate(24000)
	svxEnc.SetComplexity(5)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	backoff := 2 * time.Second
	maxBackoff := time.Minute
	for {
		err := runBridge(svxHost, svxPort, svxAuthKey, svxTG, callsign, nodeLocation, sysop,
			mumbleHost, mumblePort, mumbleUser, mumblePass, mumbleChannel,
			svxDec, svxEnc, sigCh)
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
	mumbleHost string, mumblePort int, mumbleUser, mumblePass, mumbleChannel string,
	svxDec *opus.Decoder, svxEnc *opus.Encoder, sigCh <-chan os.Signal,
) error {
	var (
		// Half-duplex state: who currently owns the TG.
		talkMu     sync.Mutex
		svxTalking bool // TG audio is flowing SVX -> Mumble
		mumTalking bool // a Mumble user is flowing Mumble -> TG
		mumTalker  string

		agcSvxToMum  = NewAGCFromEnv("AGC_SVX_TO_EXT_")
		agcMumToSvx  = NewAGCFromEnv("AGC_EXT_TO_SVX_")
		filtSvxToMum = NewVoiceFilterFromEnv("FILTER_SVX_TO_EXT_", float64(SVXSampleRate))
		filtMumToSvx = NewVoiceFilterFromEnv("FILTER_EXT_TO_SVX_", float64(SVXSampleRate))

		// Reframe buffer for Mumble -> SVX (accumulate to 960 samples).
		mumBuf   []int16
		mumBufMu sync.Mutex
	)

	svx := NewSVXLinkClient(svxHost, svxPort, svxAuthKey, callsign, nodeLocation, sysop)
	svx.SetExtraNodeInfo(map[string]interface{}{
		"links": []map[string]interface{}{
			{"localTg": svxTG, "remoteTg": mumbleChannel},
		},
	})

	mum := NewMumbleClient(mumbleHost, mumblePort, mumbleUser, mumblePass, mumbleChannel)

	// --- SVX -> Mumble: TG Opus(48k) -> PCM -> filter/AGC -> 480-sample frames -> Mumble ---
	svx.SetAudioCallback(func(opusFrame []byte) {
		talkMu.Lock()
		if mumTalking { // half-duplex: ignore TG audio while a Mumble user holds the channel
			talkMu.Unlock()
			return
		}
		talkMu.Unlock()

		pcm := make([]int16, SVXFrameSize)
		n, err := svxDec.Decode(opusFrame, pcm)
		if err != nil || n == 0 {
			return
		}
		pcm = pcm[:n]
		filtSvxToMum.Process(pcm)
		agcSvxToMum.Process(pcm)
		for len(pcm) >= MumbleFrame {
			frame := make([]int16, MumbleFrame)
			copy(frame, pcm[:MumbleFrame])
			pcm = pcm[MumbleFrame:]
			mum.SendPCM(frame)
		}
		if len(pcm) > 0 { // pad the tail to a full frame
			frame := make([]int16, MumbleFrame)
			copy(frame, pcm)
			mum.SendPCM(frame)
		}
	})

	svx.SetTalkerStartCallback(func(tg uint32, cs string) {
		if tg != svxTG || strings.EqualFold(cs, callsign) {
			return
		}
		talkMu.Lock()
		svxTalking = true
		talkMu.Unlock()
		filtSvxToMum.Reset()
		agcSvxToMum.Reset()
		log.Printf("[SVX->Mumble] Talker start: %s on TG %d", cs, tg)
	})
	svx.SetTalkerStopCallback(func(tg uint32, cs string) {
		if tg != svxTG || strings.EqualFold(cs, callsign) {
			return
		}
		talkMu.Lock()
		svxTalking = false
		talkMu.Unlock()
		log.Printf("[SVX->Mumble] Talker stop: %s", cs)
	})

	// --- Mumble -> SVX: PCM(48k) -> filter/AGC -> reframe 960 -> Opus -> TG ---
	mum.SetStreamStartCallback(func(sender string) {
		talkMu.Lock()
		if svxTalking || mumTalking { // half-duplex / first-talker-wins
			talkMu.Unlock()
			return
		}
		mumTalking = true
		mumTalker = sender
		talkMu.Unlock()

		filtMumToSvx.Reset()
		agcMumToSvx.Reset()
		mumBufMu.Lock()
		mumBuf = mumBuf[:0]
		mumBufMu.Unlock()
		talker := sender
		if talker == "" {
			talker = callsign
		}
		svx.SendTalkerStart(svxTG, talker)
		log.Printf("[Mumble->SVX] Stream start from %q", sender)
	})

	mum.SetAudioCallback(func(sender string, pcm []int16) {
		talkMu.Lock()
		active := mumTalking && sender == mumTalker
		talkMu.Unlock()
		if !active {
			return // a different (concurrent) talker — dropped under first-wins
		}
		work := make([]int16, len(pcm))
		copy(work, pcm)
		filtMumToSvx.Process(work)
		agcMumToSvx.Process(work)

		mumBufMu.Lock()
		mumBuf = append(mumBuf, work...)
		for len(mumBuf) >= SVXFrameSize {
			chunk := make([]int16, SVXFrameSize)
			copy(chunk, mumBuf[:SVXFrameSize])
			mumBuf = mumBuf[SVXFrameSize:]
			mumBufMu.Unlock()

			opusBuf := make([]byte, 512)
			nn, err := svxEnc.Encode(chunk, opusBuf)
			if err == nil {
				svx.SendAudio(opusBuf[:nn])
			}
			mumBufMu.Lock()
		}
		mumBufMu.Unlock()
	})

	mum.SetStreamStopCallback(func(sender string) {
		talkMu.Lock()
		if !mumTalking || sender != mumTalker {
			talkMu.Unlock()
			return
		}
		mumTalking = false
		mumTalker = ""
		talkMu.Unlock()
		talker := sender
		if talker == "" {
			talker = callsign
		}
		svx.SendTalkerStop(svxTG, talker)
		log.Printf("[Mumble->SVX] Stream stop from %q", sender)
	})

	// --- Connect both sides ---
	if err := svx.Connect(); err != nil {
		return fmt.Errorf("SVX connect: %w", err)
	}
	if err := svx.SelectTG(svxTG); err != nil {
		svx.Close()
		return fmt.Errorf("SVX SelectTG: %w", err)
	}
	if err := mum.Connect(); err != nil {
		svx.Close()
		return fmt.Errorf("Mumble connect: %w", err)
	}

	go svx.RunTCPReader()
	go svx.RunTCPHeartbeat()
	go svx.RunUDPReader()
	go svx.RunUDPHeartbeat()

	log.Printf("Bridge active: SVX TG %d <-> Mumble channel %q", svxTG, mumbleChannel)

	var result error
	select {
	case <-svx.Done():
		result = fmt.Errorf("SVX connection lost")
	case <-mum.Done():
		result = fmt.Errorf("Mumble connection lost")
	case <-sigCh:
		result = errShutdown
	}
	mum.Close()
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
