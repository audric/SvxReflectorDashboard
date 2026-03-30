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

// Bridge routes audio between SVXReflector and an Asterisk PBX via IAX2.
//
// Audio flow:
//   SVXReflector (OPUS/UDP) → decode OPUS → PCM 8kHz → AGC → encode codec → IAX2 → Asterisk
//   Asterisk (IAX2/codec) → decode codec → PCM 8kHz → AGC → encode OPUS → SVXReflector (UDP)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.Println("IAX Bridge starting...")

	svxHost := envRequired("REFLECTOR_HOST")
	svxPort := envInt("REFLECTOR_PORT", 5300)
	svxAuthKey := envRequired("REFLECTOR_AUTH_KEY")
	svxTG := uint32(envInt("REFLECTOR_TG", 1))
	callsign := envRequired("CALLSIGN")

	iaxUsername := envRequired("IAX_USERNAME")
	iaxPassword := envRequired("IAX_PASSWORD")
	iaxServer := envRequired("IAX_SERVER")
	iaxPort := envInt("IAX_PORT", IAX2Port)
	iaxExtension := envDefault("IAX_EXTENSION", "s")
	iaxContext := envDefault("IAX_CONTEXT", "friend")
	iaxMode := envDefault("IAX_MODE", "persistent")
	iaxIdleTimeout := envInt("IAX_IDLE_TIMEOUT", 30)
	iaxCodecs := envDefault("IAX_CODECS", "gsm,ulaw,alaw,g726")

	nodeLocation := envDefault("NODE_LOCATION", "")
	sysop := envDefault("SYSOP", "")

	codecCap := ParseCodecList(iaxCodecs)
	log.Printf("Config: SVX=%s:%d TG=%d | IAX=%s@%s:%d ext=%s@%s mode=%s codecs=%s",
		svxHost, svxPort, svxTG, iaxUsername, iaxServer, iaxPort, iaxExtension, iaxContext, iaxMode, iaxCodecs)

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

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	backoff := time.Second * 2
	maxBackoff := time.Minute

	for {
		var runErr error
		if iaxMode == "on_demand" {
			runErr = runOnDemand(svxHost, svxPort, svxAuthKey, svxTG, callsign, nodeLocation, sysop,
				iaxServer, iaxPort, iaxUsername, iaxPassword, iaxExtension, iaxContext,
				codecCap, iaxIdleTimeout, opusDec, opusEnc, sigCh)
		} else {
			runErr = runPersistent(svxHost, svxPort, svxAuthKey, svxTG, callsign, nodeLocation, sysop,
				iaxServer, iaxPort, iaxUsername, iaxPassword, iaxExtension, iaxContext,
				codecCap, opusDec, opusEnc, sigCh)
		}

		if runErr == errShutdown {
			log.Println("Goodbye")
			return
		}

		if runErr != nil {
			log.Printf("Bridge error: %v", runErr)
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

func runPersistent(
	svxHost string, svxPort int, svxAuthKey string, svxTG uint32, callsign string, nodeLocation string, sysop string,
	iaxServer string, iaxPort int, iaxUsername, iaxPassword, iaxExtension, iaxContext string,
	codecCap uint32,
	opusDec *opus.Decoder, opusEnc *opus.Encoder,
	sigCh <-chan os.Signal,
) error {

	var (
		svxTalking bool
		iaxTalking bool
		svxTalkMu  sync.Mutex
		iaxTalkMu  sync.Mutex
		encBuf     []int16
		encBufMu   sync.Mutex
		pcmBuffer  []int16
		pcmBufMu   sync.Mutex
		agcSvxToIAX = NewAGCFromEnv("AGC_SVX_TO_EXT_")
		agcIAXToSvx = NewAGCFromEnv("AGC_EXT_TO_SVX_")
		filterSvxToIAX = NewVoiceFilterFromEnv("FILTER_SVX_TO_EXT_", PCMSampleRate)
		filterIAXToSvx = NewVoiceFilterFromEnv("FILTER_EXT_TO_SVX_", PCMSampleRate)
		iaxMu      sync.Mutex
		currentIAX *IAX2Client
		iaxStreamActive bool
	)

	getIAX := func() *IAX2Client {
		iaxMu.Lock()
		defer iaxMu.Unlock()
		return currentIAX
	}

	svx := NewSVXLinkClient(svxHost, svxPort, svxAuthKey, callsign, nodeLocation, sysop)
	svx.SetExtraNodeInfo(map[string]interface{}{
		"remoteHost": iaxServer,
	})

	svx.SetAudioCallback(func(opusFrame []byte) {
		iaxTalkMu.Lock()
		if iaxTalking {
			iaxTalkMu.Unlock()
			return
		}
		iaxTalkMu.Unlock()

		iax := getIAX()
		if iax == nil {
			return
		}
		codec := iax.ActiveCodec()
		if codec == nil {
			return
		}

		pcm := make([]int16, 960)
		n, err := opusDec.Decode(opusFrame, pcm)
		if err != nil {
			return
		}
		pcm = pcm[:n]
		filterSvxToIAX.Process(pcm)
		agcSvxToIAX.Process(pcm)

		encBufMu.Lock()
		encBuf = append(encBuf, pcm...)
		frameSize := codec.FrameSize()
		for len(encBuf) >= frameSize {
			chunk := encBuf[:frameSize]
			encBuf = encBuf[frameSize:]
			encBufMu.Unlock()

			encoded, err := codec.Encode(chunk)
			if err == nil {
				iax.SendAudio(encoded)
			}

			encBufMu.Lock()
		}
		encBufMu.Unlock()
	})

	svx.SetTalkerStartCallback(func(tg uint32, cs string) {
		if tg != svxTG || strings.EqualFold(cs, callsign) {
			return
		}
		svxTalkMu.Lock()
		svxTalking = true
		svxTalkMu.Unlock()

		log.Printf("[SVX→IAX] Talker start: %s on TG %d", cs, tg)
		filterSvxToIAX.Reset()
		agcSvxToIAX.Reset()
		encBufMu.Lock()
		encBuf = encBuf[:0]
		encBufMu.Unlock()

		if iax := getIAX(); iax != nil {
			iax.SendKey()
		}
	})

	svx.SetTalkerStopCallback(func(tg uint32, cs string) {
		if tg != svxTG || strings.EqualFold(cs, callsign) {
			return
		}
		svxTalkMu.Lock()
		svxTalking = false
		svxTalkMu.Unlock()

		log.Printf("[SVX→IAX] Talker stop: %s", cs)
		if iax := getIAX(); iax != nil {
			iax.SendUnkey()
		}
	})

	if err := svx.Connect(); err != nil {
		return fmt.Errorf("SVX connect: %w", err)
	}
	if err := svx.SelectTG(svxTG); err != nil {
		svx.Close()
		return fmt.Errorf("SVX SelectTG: %w", err)
	}

	go svx.RunTCPReader()
	go svx.RunTCPHeartbeat()
	go svx.RunUDPReader()
	go svx.RunUDPHeartbeat()

	log.Printf("SVX connected on TG %d, starting IAX2 connection...", svxTG)

	iaxBackoff := 2 * time.Second
	maxIAXBackoff := time.Minute

	for {
		iax := NewIAX2Client(iaxServer, iaxPort, iaxUsername, iaxPassword, iaxExtension, iaxContext, callsign, codecCap)

		iax.SetAudioCallback(func(pcm []int16) {
			svxTalkMu.Lock()
			if svxTalking {
				svxTalkMu.Unlock()
				return
			}
			svxTalkMu.Unlock()

			iaxTalkMu.Lock()
			if !iaxStreamActive {
				iaxStreamActive = true
				iaxTalking = true
				iaxTalkMu.Unlock()

				log.Printf("[IAX→SVX] Voice from IAX2")
				svx.SendTalkerStart(svxTG, callsign)
				filterIAXToSvx.Reset()
				agcIAXToSvx.Reset()
				pcmBufMu.Lock()
				pcmBuffer = pcmBuffer[:0]
				pcmBufMu.Unlock()
			} else {
				iaxTalkMu.Unlock()
			}

			filterIAXToSvx.Process(pcm)
			agcIAXToSvx.Process(pcm)

			pcmBufMu.Lock()
			pcmBuffer = append(pcmBuffer, pcm...)
			if len(pcmBuffer) >= 480 {
				samples := make([]int16, 480)
				copy(samples, pcmBuffer[:480])
				pcmBuffer = pcmBuffer[480:]
				pcmBufMu.Unlock()

				opusBuf := make([]byte, 256)
				n, err := opusEnc.Encode(samples, opusBuf)
				if err == nil {
					svx.SendAudio(opusBuf[:n])
				}
			} else {
				pcmBufMu.Unlock()
			}
		})

		if err := iax.Register(); err != nil {
			log.Printf("[IAX2] Registration failed: %v", err)
			goto reconnect
		}

		go iax.RunRegRefresh()

		if err := iax.PlaceCall(); err != nil {
			log.Printf("[IAX2] Call failed: %v", err)
			iax.Close()
			goto reconnect
		}

		iaxMu.Lock()
		currentIAX = iax
		iaxMu.Unlock()

		go iax.RunReader()

		{
			stopSilence := make(chan struct{})
			go func() {
				ticker := time.NewTicker(500 * time.Millisecond)
				defer ticker.Stop()
				lastAudioTime := time.Now()
				for {
					select {
					case <-ticker.C:
					case <-stopSilence:
						return
					}

					iaxTalkMu.Lock()
					active := iaxStreamActive
					iaxTalkMu.Unlock()

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
						iaxTalkMu.Lock()
						iaxStreamActive = false
						iaxTalking = false
						iaxTalkMu.Unlock()
						log.Println("[IAX→SVX] Voice end (silence timeout)")

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

			iaxBackoff = 2 * time.Second
			log.Printf("Bridge active: SVX TG %d ↔ IAX %s@%s:%d %s@%s", svxTG, iaxUsername, iaxServer, iaxPort, iaxExtension, iaxContext)

			select {
			case <-svx.Done():
				close(stopSilence)
				iaxMu.Lock()
				currentIAX = nil
				iaxMu.Unlock()
				iax.Close()
				return fmt.Errorf("SVX connection lost")

			case <-iax.Done():
				close(stopSilence)
				log.Println("[IAX2] Connection lost, reconnecting (SVX stays connected)...")
				iaxMu.Lock()
				currentIAX = nil
				iaxMu.Unlock()
				iax.Close()

				iaxTalkMu.Lock()
				iaxStreamActive = false
				iaxTalking = false
				iaxTalkMu.Unlock()
				encBufMu.Lock()
				encBuf = encBuf[:0]
				encBufMu.Unlock()
				pcmBufMu.Lock()
				pcmBuffer = pcmBuffer[:0]
				pcmBufMu.Unlock()

			case <-sigCh:
				close(stopSilence)
				iaxMu.Lock()
				currentIAX = nil
				iaxMu.Unlock()
				iax.Close()
				svx.Close()
				return errShutdown
			}
		}

	reconnect:
		log.Printf("[IAX2] Reconnecting in %s...", iaxBackoff)
		select {
		case <-svx.Done():
			svx.Close()
			return fmt.Errorf("SVX connection lost during IAX reconnect")
		case <-sigCh:
			svx.Close()
			return errShutdown
		case <-time.After(iaxBackoff):
		}
		iaxBackoff *= 2
		if iaxBackoff > maxIAXBackoff {
			iaxBackoff = maxIAXBackoff
		}
	}
}

func runOnDemand(
	svxHost string, svxPort int, svxAuthKey string, svxTG uint32, callsign string, nodeLocation string, sysop string,
	iaxServer string, iaxPort int, iaxUsername, iaxPassword, iaxExtension, iaxContext string,
	codecCap uint32, idleTimeout int,
	opusDec *opus.Decoder, opusEnc *opus.Encoder,
	sigCh <-chan os.Signal,
) error {

	var (
		svxTalking bool
		iaxTalking bool
		svxTalkMu  sync.Mutex
		iaxTalkMu  sync.Mutex
		encBuf     []int16
		encBufMu   sync.Mutex
		pcmBuffer  []int16
		pcmBufMu   sync.Mutex
		agcSvxToIAX = NewAGCFromEnv("AGC_SVX_TO_EXT_")
		agcIAXToSvx = NewAGCFromEnv("AGC_EXT_TO_SVX_")
		filterSvxToIAX = NewVoiceFilterFromEnv("FILTER_SVX_TO_EXT_", PCMSampleRate)
		filterIAXToSvx = NewVoiceFilterFromEnv("FILTER_EXT_TO_SVX_", PCMSampleRate)
		iaxMu      sync.Mutex
		currentIAX *IAX2Client
		iaxStreamActive bool
		lastActivity    time.Time
		lastActivityMu  sync.Mutex
		callActive      bool
		callActiveMu    sync.Mutex
	)

	getIAX := func() *IAX2Client {
		iaxMu.Lock()
		defer iaxMu.Unlock()
		return currentIAX
	}

	touchActivity := func() {
		lastActivityMu.Lock()
		lastActivity = time.Now()
		lastActivityMu.Unlock()
	}

	svx := NewSVXLinkClient(svxHost, svxPort, svxAuthKey, callsign, nodeLocation, sysop)
	svx.SetExtraNodeInfo(map[string]interface{}{
		"remoteHost": iaxServer,
	})

	iax := NewIAX2Client(iaxServer, iaxPort, iaxUsername, iaxPassword, iaxExtension, iaxContext, callsign, codecCap)

	svx.SetAudioCallback(func(opusFrame []byte) {
		iaxTalkMu.Lock()
		if iaxTalking {
			iaxTalkMu.Unlock()
			return
		}
		iaxTalkMu.Unlock()

		touchActivity()
		iaxRef := getIAX()
		if iaxRef == nil {
			return
		}
		codec := iaxRef.ActiveCodec()
		if codec == nil {
			return
		}

		pcm := make([]int16, 960)
		n, err := opusDec.Decode(opusFrame, pcm)
		if err != nil {
			return
		}
		pcm = pcm[:n]
		filterSvxToIAX.Process(pcm)
		agcSvxToIAX.Process(pcm)

		encBufMu.Lock()
		encBuf = append(encBuf, pcm...)
		frameSize := codec.FrameSize()
		for len(encBuf) >= frameSize {
			chunk := encBuf[:frameSize]
			encBuf = encBuf[frameSize:]
			encBufMu.Unlock()
			encoded, err := codec.Encode(chunk)
			if err == nil {
				iaxRef.SendAudio(encoded)
			}
			encBufMu.Lock()
		}
		encBufMu.Unlock()
	})

	svx.SetTalkerStartCallback(func(tg uint32, cs string) {
		if tg != svxTG || strings.EqualFold(cs, callsign) {
			return
		}
		svxTalkMu.Lock()
		svxTalking = true
		svxTalkMu.Unlock()
		touchActivity()

		log.Printf("[SVX→IAX] Talker start: %s on TG %d", cs, tg)

		callActiveMu.Lock()
		if !callActive {
			callActive = true
			callActiveMu.Unlock()

			log.Println("[IAX2] On-demand: placing call...")
			if err := iax.PlaceCall(); err != nil {
				log.Printf("[IAX2] Call failed: %v", err)
				callActiveMu.Lock()
				callActive = false
				callActiveMu.Unlock()
				return
			}
			iaxMu.Lock()
			currentIAX = iax
			iaxMu.Unlock()
			go iax.RunReader()
		} else {
			callActiveMu.Unlock()
		}

		filterSvxToIAX.Reset()
		agcSvxToIAX.Reset()
		encBufMu.Lock()
		encBuf = encBuf[:0]
		encBufMu.Unlock()

		if ref := getIAX(); ref != nil {
			ref.SendKey()
		}
	})

	svx.SetTalkerStopCallback(func(tg uint32, cs string) {
		if tg != svxTG || strings.EqualFold(cs, callsign) {
			return
		}
		svxTalkMu.Lock()
		svxTalking = false
		svxTalkMu.Unlock()
		touchActivity()

		if ref := getIAX(); ref != nil {
			ref.SendUnkey()
		}
	})

	iax.SetAudioCallback(func(pcm []int16) {
		svxTalkMu.Lock()
		if svxTalking {
			svxTalkMu.Unlock()
			return
		}
		svxTalkMu.Unlock()
		touchActivity()

		iaxTalkMu.Lock()
		if !iaxStreamActive {
			iaxStreamActive = true
			iaxTalking = true
			iaxTalkMu.Unlock()
			svx.SendTalkerStart(svxTG, callsign)
			filterIAXToSvx.Reset()
			agcIAXToSvx.Reset()
			pcmBufMu.Lock()
			pcmBuffer = pcmBuffer[:0]
			pcmBufMu.Unlock()
		} else {
			iaxTalkMu.Unlock()
		}

		filterIAXToSvx.Process(pcm)
		agcIAXToSvx.Process(pcm)

		pcmBufMu.Lock()
		pcmBuffer = append(pcmBuffer, pcm...)
		if len(pcmBuffer) >= 480 {
			samples := make([]int16, 480)
			copy(samples, pcmBuffer[:480])
			pcmBuffer = pcmBuffer[480:]
			pcmBufMu.Unlock()
			opusBuf := make([]byte, 256)
			n, err := opusEnc.Encode(samples, opusBuf)
			if err == nil {
				svx.SendAudio(opusBuf[:n])
			}
		} else {
			pcmBufMu.Unlock()
		}
	})

	if err := svx.Connect(); err != nil {
		return fmt.Errorf("SVX connect: %w", err)
	}
	if err := svx.SelectTG(svxTG); err != nil {
		svx.Close()
		return fmt.Errorf("SVX SelectTG: %w", err)
	}

	go svx.RunTCPReader()
	go svx.RunTCPHeartbeat()
	go svx.RunUDPReader()
	go svx.RunUDPHeartbeat()

	if err := iax.Register(); err != nil {
		svx.Close()
		return fmt.Errorf("IAX2 registration: %w", err)
	}
	go iax.RunRegRefresh()
	go iax.RunReader()

	touchActivity()
	log.Printf("IAX bridge on-demand: SVX TG %d ↔ IAX %s@%s:%d (idle timeout %ds)", svxTG, iaxUsername, iaxServer, iaxPort, idleTimeout)

	idleDur := time.Duration(idleTimeout) * time.Second
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			callActiveMu.Lock()
			active := callActive
			callActiveMu.Unlock()
			if !active {
				continue
			}

			lastActivityMu.Lock()
			idle := time.Since(lastActivity)
			lastActivityMu.Unlock()

			if idle > idleDur {
				log.Printf("[IAX2] On-demand: idle for %s, hanging up", idle.Round(time.Second))
				iax.Hangup()
				iaxMu.Lock()
				currentIAX = nil
				iaxMu.Unlock()
				callActiveMu.Lock()
				callActive = false
				callActiveMu.Unlock()

				iaxTalkMu.Lock()
				iaxStreamActive = false
				iaxTalking = false
				iaxTalkMu.Unlock()
			}

		case <-svx.Done():
			iax.Close()
			return fmt.Errorf("SVX connection lost")

		case <-iax.Done():
			log.Println("[IAX2] IAX connection lost")
			iaxMu.Lock()
			currentIAX = nil
			iaxMu.Unlock()
			svx.Close()
			return fmt.Errorf("IAX2 connection lost")

		case <-sigCh:
			iax.Close()
			svx.Close()
			return errShutdown
		}
	}
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
