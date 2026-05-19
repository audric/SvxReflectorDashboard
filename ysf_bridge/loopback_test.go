// Loopback integration test for the YSF debug playground.
// Gated by YSF_PARROT_HOST so a regular `go test` invocation skips it.
//
// Drives the bridge's YSF code path against a YSFParrot echo server:
//   WAV → PCM → Encode → YSFD → UDP → ysfparrot → UDP → ParseYSFD → Decode → WAV
//
// Env contract:
//   YSF_PARROT_HOST   host or IP of the echo server (required to run)
//   YSF_PARROT_PORT   UDP port (default: 42000)
//   WAV_IN            input WAV path (8 kHz mono 16-bit PCM)
//   WAV_OUT           output WAV path
//   CALLSIGN          our callsign for YSFP registration (default: LOOPTEST)
package main

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestLoopback(t *testing.T) {
	host := os.Getenv("YSF_PARROT_HOST")
	if host == "" {
		t.Skip("YSF_PARROT_HOST not set; loopback test is for the docker playground only")
	}
	port := 42000
	if p := os.Getenv("YSF_PARROT_PORT"); p != "" {
		v, err := strconv.Atoi(p)
		if err != nil {
			t.Fatalf("YSF_PARROT_PORT not an int: %v", err)
		}
		port = v
	}
	wavIn := os.Getenv("WAV_IN")
	wavOut := os.Getenv("WAV_OUT")
	if wavIn == "" || wavOut == "" {
		t.Fatal("WAV_IN and WAV_OUT must both be set")
	}
	callsign := os.Getenv("CALLSIGN")
	if callsign == "" {
		callsign = "LOOPTEST"
	}

	inputPCM, err := readMonoPCM8k(wavIn)
	if err != nil {
		t.Fatalf("read WAV_IN %s: %v", wavIn, err)
	}
	t.Logf("loaded %d input samples (%.2fs)", len(inputPCM), float64(len(inputPCM))/8000.0)

	vocEnc, err := NewVocoder()
	if err != nil {
		t.Fatalf("vocoder encode init: %v", err)
	}
	defer vocEnc.Close()
	vocDec, err := NewVocoder()
	if err != nil {
		t.Fatalf("vocoder decode init: %v", err)
	}
	defer vocDec.Close()

	client := NewYSFClient(host, port, callsign, 0)
	defer client.Close()

	if err := client.Connect(); err != nil {
		t.Fatalf("YSF connect to %s:%d: %v", host, port, err)
	}
	t.Log("YSF registered with parrot")

	var (
		outMu     sync.Mutex
		outPCM    []int16
		firstEcho time.Time
		lastEcho  time.Time
		echoCount int
	)

	// The bridge's RunReader drops YSFD frames whose SrcGateway equals our
	// own callsign — production-correct (don't process your own echo as
	// remote traffic) but exactly what YSFParrot's echo looks like. Run our
	// own reader on client.conn that doesn't filter self.
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		buf := make([]byte, 512)
		for {
			client.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
			n, err := client.conn.Read(buf)
			if err != nil {
				if client.isClosed() {
					return
				}
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					outMu.Lock()
					le := lastEcho
					outMu.Unlock()
					// Stop reading once we've seen frames AND nothing new for 2 s.
					if !le.IsZero() && time.Since(le) > 2*time.Second {
						return
					}
					continue
				}
				t.Logf("reader: unexpected read error: %v", err)
				return
			}
			data := buf[:n]
			if n >= 4 && string(data[0:4]) == "YSFP" {
				continue
			}
			if n < YSFFrameSize || string(data[0:4]) != "YSFD" {
				continue
			}
			frame := ParseYSFD(data)
			if frame == nil {
				continue
			}
			outMu.Lock()
			now := time.Now()
			if firstEcho.IsZero() {
				firstEcho = now
			}
			lastEcho = now
			echoCount++
			outMu.Unlock()
			if frame.FI != YSF_FI_COMM {
				continue
			}
			ambeFrames := ExtractVD2AMBE(frame.Payload)
			for _, ambe := range ambeFrames {
				pcm := vocDec.Decode(ambe)
				outMu.Lock()
				outPCM = append(outPCM, pcm[:]...)
				outMu.Unlock()
			}
		}
	}()
	go client.RunKeepalive()

	totalFrames := (len(inputPCM) + PCMFrameSize - 1) / PCMFrameSize
	framesPerYSFD := 5
	totalYSFDs := (totalFrames + framesPerYSFD - 1) / framesPerYSFD
	t.Logf("sending %d YSFDs (%d AMBE frames)", totalYSFDs, totalFrames)

	client.StartTX()
	txStart := time.Now()
	for yIdx := 0; yIdx < totalYSFDs; yIdx++ {
		var ambeBatch [5][YSFAMBEFrameSize]byte
		for f := 0; f < framesPerYSFD; f++ {
			frameIdx := yIdx*framesPerYSFD + f
			start := frameIdx * PCMFrameSize
			if start >= len(inputPCM) {
				break // remaining slots stay zero-filled
			}
			end := start + PCMFrameSize
			if end > len(inputPCM) {
				end = len(inputPCM)
			}
			var chunk [PCMFrameSize]int16
			copy(chunk[:], inputPCM[start:end])
			ambeBatch[f] = vocEnc.Encode(chunk)
		}
		if err := client.SendVoice(ambeBatch); err != nil {
			t.Fatalf("send YSFD %d: %v", yIdx, err)
		}
		// Pace at real time so the parrot doesn't drop bursts.
		time.Sleep(100 * time.Millisecond)
	}
	if err := client.StopTX(); err != nil {
		t.Fatalf("stop TX: %v", err)
	}
	t.Logf("TX complete in %v", time.Since(txStart))

	// Wait for the reader to settle (it self-terminates after 2 s of silence
	// post-first-echo) or hard deadline 10 s, whichever first.
	select {
	case <-readerDone:
	case <-time.After(10 * time.Second):
		t.Log("reader did not settle within 10 s — closing client to force exit")
		client.Close()
		<-readerDone
	}

	outMu.Lock()
	finalPCM := append([]int16(nil), outPCM...)
	gotFirstEcho := !firstEcho.IsZero()
	firstEchoLatency := time.Duration(0)
	if gotFirstEcho {
		firstEchoLatency = firstEcho.Sub(txStart)
	}
	totalEchoes := echoCount
	outMu.Unlock()

	t.Logf("received %d echoed YSFDs, %d output samples (%.2fs)",
		totalEchoes, len(finalPCM), float64(len(finalPCM))/8000.0)

	if err := writeMonoPCM8k(wavOut, finalPCM); err != nil {
		t.Fatalf("write WAV_OUT %s: %v", wavOut, err)
	}
	t.Logf("wrote %s", wavOut)

	// Assertions.
	if !gotFirstEcho {
		t.Fatal("no echo received from YSFParrot — outgoing YSFD likely rejected. Check pcap/ysf.pcap")
	}
	// YSFParrot buffers internally before echoing (its constructor uses 180 units
	// of delay). 6 s is a comfortable upper bound for the first echo to arrive.
	if firstEchoLatency > 6*time.Second {
		t.Errorf("first echo took %v (expected < 6s) — parrot may be congested", firstEchoLatency)
	}
	if len(finalPCM) == 0 {
		t.Fatal("output PCM is empty — frames echoed but decoder yielded nothing")
	}
	durDiff := absInt(len(finalPCM) - len(inputPCM))
	if durDiff > 800 {
		t.Errorf("output length differs from input by %d samples (>100 ms); in=%d out=%d",
			durDiff, len(inputPCM), len(finalPCM))
	}
	inRMS := rms(inputPCM)
	outRMS := rms(finalPCM)
	ratio := 0.0
	if inRMS > 0 {
		ratio = outRMS / inRMS
	}
	t.Logf("input RMS=%.0f  output RMS=%.0f  ratio=%.2f", inRMS, outRMS, ratio)
	// AMBE+2 is a voice codec; synthetic signals (sine/chirp) round-trip with
	// significant attenuation because the codec doesn't model them well. Real
	// voice retains 30-60% RMS. Use 3 % as a sanity floor — anything below that
	// almost certainly means decode produced silence.
	if outRMS < 0.03*inRMS {
		t.Errorf("output RMS (%.0f) below 3%% of input RMS (%.0f) — likely silence", outRMS, inRMS)
	}
}

func readMonoPCM8k(path string) ([]int16, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var hdr [12]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return nil, err
	}
	if string(hdr[0:4]) != "RIFF" || string(hdr[8:12]) != "WAVE" {
		return nil, errors.New("not a RIFF/WAVE file")
	}
	for {
		var chdr [8]byte
		if _, err := io.ReadFull(f, chdr[:]); err != nil {
			return nil, err
		}
		size := binary.LittleEndian.Uint32(chdr[4:8])
		switch string(chdr[0:4]) {
		case "fmt ":
			fmtBuf := make([]byte, size)
			if _, err := io.ReadFull(f, fmtBuf); err != nil {
				return nil, err
			}
			audioFmt := binary.LittleEndian.Uint16(fmtBuf[0:2])
			channels := binary.LittleEndian.Uint16(fmtBuf[2:4])
			sampleRate := binary.LittleEndian.Uint32(fmtBuf[4:8])
			bitsPerSample := binary.LittleEndian.Uint16(fmtBuf[14:16])
			if audioFmt != 1 || channels != 1 || sampleRate != 8000 || bitsPerSample != 16 {
				return nil, errors.New("WAV must be mono 8 kHz 16-bit PCM")
			}
		case "data":
			buf := make([]byte, size)
			if _, err := io.ReadFull(f, buf); err != nil {
				return nil, err
			}
			samples := make([]int16, size/2)
			for i := range samples {
				samples[i] = int16(binary.LittleEndian.Uint16(buf[i*2 : i*2+2]))
			}
			return samples, nil
		default:
			if _, err := io.CopyN(io.Discard, f, int64(size)); err != nil {
				return nil, err
			}
		}
	}
}

func writeMonoPCM8k(path string, samples []int16) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dataBytes := uint32(len(samples) * 2)
	f.Write([]byte("RIFF"))
	binary.Write(f, binary.LittleEndian, uint32(36+dataBytes))
	f.Write([]byte("WAVE"))
	f.Write([]byte("fmt "))
	binary.Write(f, binary.LittleEndian, uint32(16))
	binary.Write(f, binary.LittleEndian, uint16(1))
	binary.Write(f, binary.LittleEndian, uint16(1))
	binary.Write(f, binary.LittleEndian, uint32(8000))
	binary.Write(f, binary.LittleEndian, uint32(8000*2))
	binary.Write(f, binary.LittleEndian, uint16(2))
	binary.Write(f, binary.LittleEndian, uint16(16))
	f.Write([]byte("data"))
	binary.Write(f, binary.LittleEndian, dataBytes)
	return binary.Write(f, binary.LittleEndian, samples)
}

func rms(samples []int16) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sumSq float64
	for _, s := range samples {
		v := float64(s)
		sumSq += v * v
	}
	return math.Sqrt(sumSq / float64(len(samples)))
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
