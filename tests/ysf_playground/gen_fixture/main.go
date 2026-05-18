// Generates a 1.5-second mono 8 kHz 16-bit PCM WAV containing:
//   - 0.5 s of a 1 kHz sine (warm-up tone)
//   - 1.0 s of a linear chirp from 300 Hz to 2700 Hz
//
// Used as the default input for the YSF loopback test. The chirp lets us
// eyeball spectral fidelity of the round-trip in a spectrogram.
package main

import (
	"encoding/binary"
	"flag"
	"log"
	"math"
	"os"
)

func main() {
	out := flag.String("out", "fixtures/test.wav", "output WAV path")
	flag.Parse()

	const (
		sampleRate = 8000
		amp        = 0.6 // ~-4 dBFS
		toneDur    = 0.5
		chirpDur   = 1.0
		toneFreq   = 1000.0
		chirpStart = 300.0
		chirpEnd   = 2700.0
	)
	toneSamples := int(toneDur * sampleRate)
	chirpSamples := int(chirpDur * sampleRate)
	total := toneSamples + chirpSamples
	samples := make([]int16, total)

	for i := 0; i < toneSamples; i++ {
		t := float64(i) / sampleRate
		samples[i] = int16(amp * math.MaxInt16 * math.Sin(2*math.Pi*toneFreq*t))
	}
	for i := 0; i < chirpSamples; i++ {
		t := float64(i) / sampleRate
		phase := 2 * math.Pi * (chirpStart*t + 0.5*(chirpEnd-chirpStart)*t*t/chirpDur)
		samples[toneSamples+i] = int16(amp * math.MaxInt16 * math.Sin(phase))
	}

	if err := writeWAV(*out, samples, sampleRate); err != nil {
		log.Fatalf("write wav: %v", err)
	}
	log.Printf("wrote %s (%d samples, %.2fs)", *out, total, float64(total)/sampleRate)
}

func writeWAV(path string, samples []int16, sampleRate int) error {
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
	binary.Write(f, binary.LittleEndian, uint32(sampleRate))
	binary.Write(f, binary.LittleEndian, uint32(sampleRate*2))
	binary.Write(f, binary.LittleEndian, uint16(2))
	binary.Write(f, binary.LittleEndian, uint16(16))
	f.Write([]byte("data"))
	binary.Write(f, binary.LittleEndian, dataBytes)
	return binary.Write(f, binary.LittleEndian, samples)
}
