package main

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestFICHRoundTrip(t *testing.T) {
	cases := []struct {
		name              string
		fi, fn, ft, dt, dg byte
	}{
		{"header", 0, 0, 6, 2, 0},
		{"voice", 1, 3, 6, 2, 0},
		{"term", 2, 0, 6, 2, 0},
		{"dgid42", 1, 5, 6, 2, 42},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fich := [6]byte{}
			fich[0] = (c.fi & 0x03) << 6
			fich[1] = (c.fn&0x07)<<3 | (c.ft & 0x07)
			fich[2] = c.dt & 0x03
			fich[3] = c.dg & 0x7F

			encoded := make([]byte, YSFFICHLen)
			encodeFICH(fich[:], encoded)

			var decoded [6]byte
			if !decodeFICH(encoded, decoded[:]) {
				t.Fatalf("decodeFICH returned false (CRC/Golay failure)")
			}

			fi := (decoded[0] >> 6) & 0x03
			fn := (decoded[1] >> 3) & 0x07
			ft := decoded[1] & 0x07
			dt := decoded[2] & 0x03
			dg := decoded[3] & 0x7F
			if fi != c.fi || fn != c.fn || ft != c.ft || dt != c.dt || dg != c.dg {
				t.Errorf("FICH fields mismatch: got fi=%d fn=%d ft=%d dt=%d dg=%d, want fi=%d fn=%d ft=%d dt=%d dg=%d",
					fi, fn, ft, dt, dg, c.fi, c.fn, c.ft, c.dt, c.dg)
			}
		})
	}
}

func TestVD2VoiceRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	var frames [5][YSFAMBEFrameSize]byte
	for j := 0; j < 5; j++ {
		var ambe [YSFAMBEFrameSize]byte
		rng.Read(ambe[:])
		// Mask out the unused 7 bits of the 7th byte (only bit 7 is meaningful).
		ambe[6] &= 0x80
		frames[j] = ambe
	}

	payload := PackVD2AMBE(frames)
	extracted := ExtractVD2AMBE(payload)

	for j := 0; j < 5; j++ {
		if !bytes.Equal(extracted[j][:], frames[j][:]) {
			t.Errorf("frame %d mismatch: got %x, want %x", j, extracted[j], frames[j])
		}
	}
}

func TestFullPacketRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var frames [5][YSFAMBEFrameSize]byte
	for j := 0; j < 5; j++ {
		var ambe [YSFAMBEFrameSize]byte
		rng.Read(ambe[:])
		ambe[6] &= 0x80
		frames[j] = ambe
	}
	payload := PackVD2AMBE(frames)

	pkt := BuildYSFD("MYGW", "MYRADIO", "ALL", 5, false,
		YSF_FI_COMM, 3, YSFFramesPerSuper-1, YSF_DT_VD2, 0, payload)
	if len(pkt) != YSFFrameSize {
		t.Fatalf("packet length %d != %d", len(pkt), YSFFrameSize)
	}

	parsed := ParseYSFD(pkt)
	if parsed == nil {
		t.Fatalf("ParseYSFD returned nil")
	}
	if parsed.FI != YSF_FI_COMM || parsed.DT != YSF_DT_VD2 || parsed.FN != 3 {
		t.Errorf("FICH mismatch: FI=%d DT=%d FN=%d", parsed.FI, parsed.DT, parsed.FN)
	}

	extracted := ExtractVD2AMBE(parsed.Payload)
	for j := 0; j < 5; j++ {
		if !bytes.Equal(extracted[j][:], frames[j][:]) {
			t.Errorf("frame %d mismatch through full packet round-trip", j)
		}
	}
}

func TestCRCCCITT16Vector(t *testing.T) {
	// Sanity: CRC of empty payload (init=0, xorout=0xFFFF) should be 0xFFFF.
	if got := crcCCITT16(nil); got != 0xFFFF {
		t.Errorf("crc(empty) = 0x%04X, want 0xFFFF", got)
	}
	// Sanity: add then check should round-trip.
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x00, 0x00}
	addCRCCCITT162(data)
	if !checkCRCCCITT162(data) {
		t.Errorf("CRC round-trip failed: %x", data)
	}
}

func TestGolay24RoundTrip(t *testing.T) {
	for d := uint32(0); d < 4096; d += 17 {
		cw := golay24Encode(d)
		decoded, ok := golay24Decode(cw)
		if !ok {
			t.Errorf("golay24Decode(encode(%d)) failed validation", d)
		}
		if decoded != d {
			t.Errorf("golay24 round-trip: %d → %06X → %d", d, cw, decoded)
		}
	}
}
