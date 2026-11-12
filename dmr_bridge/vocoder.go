package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

// PCM audio parameters
const (
	PCMSampleRate = 8000 // 8 kHz
	PCMFrameSize  = 160  // 160 samples = 20ms at 8kHz
	AMBEFrameSize = 9    // 9 bytes = 72 bits DMR AMBE+2
)

// MD380 firmware images the operator may supply. They are not distributed
// with the bridge. The helper calls into the firmware at fixed addresses,
// so only these exact images (identified by SHA-256) are accepted.
const (
	md380FirmwareFile = "md380fw.img"
	md380FirmwareSHA  = "c0351f250a834660641bca3b06be931c80cc1ed0ef808356c550b99cd0f4c632"
	md380RAMFile      = "md380ram.img"
	md380RAMSHA       = "d40398183e9aa8db6288a62d9a0e0c233cb143ef5633680abd829cc03c719f66"
)

type vocoderBackend interface {
	Decode(ambe [9]byte) [PCMFrameSize]int16
	Encode(pcm [PCMFrameSize]int16) [9]byte
	Close()
}

// Vocoder converts between DMR AMBE+2 2450x1150 frames and PCM using one
// of two backends: the MD380 firmware when the operator has supplied it,
// otherwise the built-in software vocoder.
type Vocoder struct {
	vocoderBackend
}

// NewVocoder picks the backend. MD380_FIRMWARE_DIR (default /firmware) is
// where the operator-supplied firmware images are looked up.
func NewVocoder() (*Vocoder, error) {
	dir := envDefault("MD380_FIRMWARE_DIR", "/firmware")
	fw := filepath.Join(dir, md380FirmwareFile)
	ram := filepath.Join(dir, md380RAMFile)

	err := checkSHA256(fw, md380FirmwareSHA)
	if err == nil {
		err = checkSHA256(ram, md380RAMSHA)
	}
	if err == nil {
		var md *md380Vocoder
		if md, err = newMD380Vocoder(fw, ram); err == nil {
			log.Printf("[Vocoder] Using MD380 firmware vocoder (%s)", dir)
			return &Vocoder{md}, nil
		}
	}
	if os.IsNotExist(err) {
		log.Printf("[Vocoder] No MD380 firmware in %s, using built-in software vocoder", dir)
	} else {
		log.Printf("[Vocoder] MD380 firmware not usable (%v), using built-in software vocoder", err)
	}

	soft, err := newSoftVocoder()
	if err != nil {
		return nil, err
	}
	return &Vocoder{soft}, nil
}

func checkSHA256(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("%s: unexpected SHA-256 %s", filepath.Base(path), got)
	}
	return nil
}
