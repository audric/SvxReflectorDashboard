package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"gopkg.in/hraban/opus.v2"
)

// Bridge routes audio between SVXReflector and a SIP endpoint via sip_helper.
//
// Audio flow:
//   SVXReflector (OPUS/UDP) → decode OPUS → PCM 8kHz → AGC → fd 3 → sip_helper → PJSIP (RTP) → PBX
//   PBX (SIP/RTP) → PJSIP → sip_helper → fd 4 → PCM 8kHz → AGC → encode OPUS → SVXReflector

const (
	pcmSampleRate = 8000
	pcmFrameSize  = 160 // 20ms at 8kHz
	pcmFrameBytes = 320 // 160 samples * 2 bytes (16-bit LE)
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.Println("SIP Bridge starting...")

	svxHost := envRequired("REFLECTOR_HOST")
	svxPort := envInt("REFLECTOR_PORT", 5300)
	svxAuthKey := envRequired("REFLECTOR_AUTH_KEY")
	svxTG := uint32(envInt("REFLECTOR_TG", 1))
	callsign := envRequired("CALLSIGN")

	sipServer := envRequired("SIP_SERVER")
	sipExtension := envDefault("SIP_EXTENSION", "")
	sipMode := envDefault("SIP_MODE", "persistent")
	sipIdleTimeout := envInt("SIP_IDLE_TIMEOUT", 30)
	sipDTMF := envDefault("SIP_DTMF", "")
	sipDTMFDelay := envInt("SIP_DTMF_DELAY", 2000)

	nodeLocation := envDefault("NODE_LOCATION", "")
	sysop := envDefault("SYSOP", "")

	log.Printf("Config: SVX=%s:%d TG=%d | SIP=%s mode=%s",
		svxHost, svxPort, svxTG, sipServer, sipMode)

	opusDec, err := opus.NewDecoder(pcmSampleRate, 1)
	if err != nil {
		log.Fatalf("OPUS decoder init: %v", err)
	}
	opusEnc, err := opus.NewEncoder(pcmSampleRate, 1, opus.AppVoIP)
	if err != nil {
		log.Fatalf("OPUS encoder init: %v", err)
	}
	opusEnc.SetBitrate(16000)
	opusEnc.SetComplexity(5)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	backoff := 2 * time.Second
	maxBackoff := time.Minute

	for {
		err := runBridge(svxHost, svxPort, svxAuthKey, svxTG, callsign, nodeLocation, sysop,
			sipServer, sipExtension, sipMode, sipIdleTimeout,
			sipDTMF, sipDTMFDelay, opusDec, opusEnc, sigCh)

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
			log.Println("Shutdown during reconnect")
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
	sipServer, sipExtension, sipMode string, sipIdleTimeout int,
	sipDTMF string, sipDTMFDelay int,
	opusDec *opus.Decoder, opusEnc *opus.Encoder,
	sigCh <-chan os.Signal,
) error {
	var (
		svxTalking      bool
		sipTalking      bool
		svxTalkMu       sync.Mutex
		sipTalkMu       sync.Mutex
		pcmOutBuf       []int16
		pcmOutMu        sync.Mutex
		pcmInBuf        []int16
		pcmInMu         sync.Mutex
		agcSvxToSIP     = NewAGCFromEnv("AGC_SVX_TO_EXT_")
		agcSIPToSvx     = NewAGCFromEnv("AGC_EXT_TO_SVX_")
		filterSvxToSIP  = NewVoiceFilterFromEnv("FILTER_SVX_TO_EXT_", pcmSampleRate)
		filterSIPToSvx  = NewVoiceFilterFromEnv("FILTER_EXT_TO_SVX_", pcmSampleRate)
		sipConnected    bool
		sipConnectedMu  sync.Mutex
		sipStreamActive bool
		lastActivity    time.Time
		lastActivityMu  sync.Mutex
	)

	touchActivity := func() {
		lastActivityMu.Lock()
		lastActivity = time.Now()
		lastActivityMu.Unlock()
	}

	// ── Launch sip_helper subprocess ──
	audioInR, audioInW, _ := os.Pipe()   // fd 3: Go writes, helper reads
	audioOutR, audioOutW, _ := os.Pipe() // fd 4: helper writes, Go reads

	cmd := exec.Command("sip_helper")
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{audioInR, audioOutW} // fd 3, fd 4 in child

	stdinPipe, _ := cmd.StdinPipe()
	stdoutPipe, _ := cmd.StdoutPipe()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start sip_helper: %w", err)
	}
	audioInR.Close()
	audioOutW.Close()

	helperDone := make(chan error, 1)
	go func() { helperDone <- cmd.Wait() }()

	sendCmd := func(c string) {
		fmt.Fprintf(stdinPipe, "%s\n", c)
	}

	// ── Event reader (stdout from helper) ──
	events := make(chan string, 32)
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			events <- scanner.Text()
		}
		close(events)
	}()

	// ── Audio reader: helper → Go (fd 4) ──
	audioFromSIP := make(chan []int16, 64)
	go func() {
		buf := make([]byte, pcmFrameBytes)
		for {
			n, err := io.ReadFull(audioOutR, buf)
			if err != nil {
				return
			}
			if n == pcmFrameBytes {
				pcm := make([]int16, pcmFrameSize)
				for i := 0; i < pcmFrameSize; i++ {
					pcm[i] = int16(buf[i*2]) | int16(buf[i*2+1])<<8
				}
				audioFromSIP <- pcm
			}
		}
	}()

	// ── Connect SVX ──
	svx := NewSVXLinkClient(svxHost, svxPort, svxAuthKey, callsign, nodeLocation, sysop)
	svx.SetExtraNodeInfo(map[string]interface{}{"remoteHost": sipServer})

	svx.SetAudioCallback(func(opusFrame []byte) {
		sipTalkMu.Lock()
		if sipTalking {
			sipTalkMu.Unlock()
			return
		}
		sipTalkMu.Unlock()

		sipConnectedMu.Lock()
		connected := sipConnected
		sipConnectedMu.Unlock()
		if !connected {
			return
		}

		pcm := make([]int16, 960)
		n, err := opusDec.Decode(opusFrame, pcm)
		if err != nil {
			return
		}
		pcm = pcm[:n]
		filterSvxToSIP.Process(pcm)
		agcSvxToSIP.Process(pcm)

		pcmOutMu.Lock()
		pcmOutBuf = append(pcmOutBuf, pcm...)
		for len(pcmOutBuf) >= pcmFrameSize {
			chunk := pcmOutBuf[:pcmFrameSize]
			pcmOutBuf = pcmOutBuf[pcmFrameSize:]
			pcmOutMu.Unlock()

			buf := make([]byte, pcmFrameBytes)
			for i, s := range chunk {
				buf[i*2] = byte(s)
				buf[i*2+1] = byte(s >> 8)
			}
			audioInW.Write(buf)

			pcmOutMu.Lock()
		}
		pcmOutMu.Unlock()
	})

	svx.SetTalkerStartCallback(func(tg uint32, cs string) {
		if tg != svxTG || strings.EqualFold(cs, callsign) {
			return
		}
		svxTalkMu.Lock()
		svxTalking = true
		svxTalkMu.Unlock()
		touchActivity()

		log.Printf("[SVX→SIP] Talker start: %s on TG %d", cs, tg)

		if sipMode == "on_demand" {
			sipConnectedMu.Lock()
			connected := sipConnected
			sipConnectedMu.Unlock()
			if !connected && sipExtension != "" {
				log.Println("[SIP] On-demand: placing call...")
				sendCmd(fmt.Sprintf("CALL sip:%s@%s", sipExtension, sipServer))
			}
		}

		filterSvxToSIP.Reset()
		agcSvxToSIP.Reset()
		pcmOutMu.Lock()
		pcmOutBuf = pcmOutBuf[:0]
		pcmOutMu.Unlock()
	})

	svx.SetTalkerStopCallback(func(tg uint32, cs string) {
		if tg != svxTG || strings.EqualFold(cs, callsign) {
			return
		}
		svxTalkMu.Lock()
		svxTalking = false
		svxTalkMu.Unlock()
		touchActivity()
	})

	if err := svx.Connect(); err != nil {
		stdinPipe.Close()
		return fmt.Errorf("SVX connect: %w", err)
	}
	if err := svx.SelectTG(svxTG); err != nil {
		svx.Close()
		stdinPipe.Close()
		return fmt.Errorf("SVX SelectTG: %w", err)
	}

	go svx.RunTCPReader()
	go svx.RunTCPHeartbeat()
	go svx.RunUDPReader()
	go svx.RunUDPHeartbeat()

	log.Printf("SVX connected on TG %d, waiting for SIP registration...", svxTG)

	// ── SIP → SVX audio processing ──
	stopAudio := make(chan struct{})
	go func() {
		for {
			select {
			case pcm, ok := <-audioFromSIP:
				if !ok {
					return
				}
				svxTalkMu.Lock()
				if svxTalking {
					svxTalkMu.Unlock()
					continue
				}
				svxTalkMu.Unlock()
				touchActivity()

				sipTalkMu.Lock()
				if !sipStreamActive {
					sipStreamActive = true
					sipTalking = true
					sipTalkMu.Unlock()
					log.Println("[SIP→SVX] Voice from SIP")
					svx.SendTalkerStart(svxTG, callsign)
					filterSIPToSvx.Reset()
					agcSIPToSvx.Reset()
					pcmInMu.Lock()
					pcmInBuf = pcmInBuf[:0]
					pcmInMu.Unlock()
				} else {
					sipTalkMu.Unlock()
				}

				filterSIPToSvx.Process(pcm)
				agcSIPToSvx.Process(pcm)

				pcmInMu.Lock()
				pcmInBuf = append(pcmInBuf, pcm...)
				if len(pcmInBuf) >= 480 {
					samples := make([]int16, 480)
					copy(samples, pcmInBuf[:480])
					pcmInBuf = pcmInBuf[480:]
					pcmInMu.Unlock()
					opusBuf := make([]byte, 256)
					n, err := opusEnc.Encode(samples, opusBuf)
					if err == nil {
						svx.SendAudio(opusBuf[:n])
					}
				} else {
					pcmInMu.Unlock()
				}

			case <-stopAudio:
				return
			}
		}
	}()

	// ── Silence detection ──
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		lastAudio := time.Now()
		for {
			select {
			case <-ticker.C:
			case <-stopAudio:
				return
			}

			sipTalkMu.Lock()
			active := sipStreamActive
			sipTalkMu.Unlock()
			if !active {
				lastAudio = time.Now()
				continue
			}

			pcmInMu.Lock()
			bufLen := len(pcmInBuf)
			pcmInMu.Unlock()

			if bufLen > 0 {
				lastAudio = time.Now()
			} else if time.Since(lastAudio) > 1*time.Second {
				sipTalkMu.Lock()
				sipStreamActive = false
				sipTalking = false
				sipTalkMu.Unlock()
				log.Println("[SIP→SVX] Voice end (silence)")

				pcmInMu.Lock()
				if len(pcmInBuf) > 0 {
					samples := pcmInBuf
					pcmInBuf = nil
					pcmInMu.Unlock()
					for len(samples) < 480 {
						samples = append(samples, 0)
					}
					opusBuf := make([]byte, 256)
					n, err := opusEnc.Encode(samples[:480], opusBuf)
					if err == nil {
						svx.SendAudio(opusBuf[:n])
					}
				} else {
					pcmInMu.Unlock()
				}
				svx.SendTalkerStop(svxTG, callsign)
			}
		}
	}()

	// ── Idle timeout for on-demand ──
	touchActivity()
	var idleTicker *time.Ticker
	var idleTickerC <-chan time.Time
	if sipMode == "on_demand" {
		idleTicker = time.NewTicker(5 * time.Second)
		idleTickerC = idleTicker.C
		defer idleTicker.Stop()
	}

	// ── Event loop ──
	registered := false
	for {
		select {
		case event, ok := <-events:
			if !ok {
				close(stopAudio)
				svx.Close()
				return fmt.Errorf("sip_helper exited")
			}

			switch {
			case event == "REGISTERED":
				registered = true
				log.Println("[SIP] Registered")

				if sipMode == "persistent" && sipExtension != "" {
					sendCmd(fmt.Sprintf("CALL sip:%s@%s", sipExtension, sipServer))
				}

			case strings.HasPrefix(event, "REG_FAILED"):
				log.Printf("[SIP] Registration failed: %s", event)

			case strings.HasPrefix(event, "INCOMING"):
				log.Printf("[SIP] %s", event)
				if sipMode == "listen_only" || sipMode == "on_demand" {
					sendCmd("ANSWER")
				}

			case event == "CONNECTED":
				sipConnectedMu.Lock()
				sipConnected = true
				sipConnectedMu.Unlock()
				touchActivity()
				log.Println("[SIP] Call connected")

				if sipDTMF != "" {
					go func() {
						time.Sleep(time.Duration(sipDTMFDelay) * time.Millisecond)
						log.Printf("[SIP] Sending DTMF: %s", sipDTMF)
						sendCmd("DTMF " + sipDTMF)
					}()
				}

			case event == "DISCONNECTED":
				sipConnectedMu.Lock()
				sipConnected = false
				sipConnectedMu.Unlock()
				log.Println("[SIP] Call disconnected")

				sipTalkMu.Lock()
				sipStreamActive = false
				sipTalking = false
				sipTalkMu.Unlock()

				if sipMode == "persistent" && registered && sipExtension != "" {
					log.Println("[SIP] Persistent: re-calling...")
					time.Sleep(2 * time.Second)
					sendCmd(fmt.Sprintf("CALL sip:%s@%s", sipExtension, sipServer))
				}

			case strings.HasPrefix(event, "DTMF_RECEIVED"):
				log.Printf("[SIP] %s", event)

			case event == "PIN_OK":
				log.Println("[SIP] PIN accepted, audio bridged")

			case event == "PIN_FAILED":
				log.Println("[SIP] PIN failed or timeout, call hung up")
			}

		case <-idleTickerC:
			sipConnectedMu.Lock()
			connected := sipConnected
			sipConnectedMu.Unlock()
			if !connected {
				continue
			}

			lastActivityMu.Lock()
			idle := time.Since(lastActivity)
			lastActivityMu.Unlock()

			if idle > time.Duration(sipIdleTimeout)*time.Second {
				log.Printf("[SIP] On-demand: idle %s, hanging up", idle.Round(time.Second))
				sendCmd("HANGUP")
			}

		case <-svx.Done():
			sendCmd("QUIT")
			close(stopAudio)
			return fmt.Errorf("SVX connection lost")

		case err := <-helperDone:
			close(stopAudio)
			svx.Close()
			if err != nil {
				return fmt.Errorf("sip_helper crashed: %w", err)
			}
			return fmt.Errorf("sip_helper exited")

		case <-sigCh:
			sendCmd("QUIT")
			close(stopAudio)
			svx.Close()
			cmd.Wait()
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
