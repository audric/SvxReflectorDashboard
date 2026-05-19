package main

import "strconv"

// YSF protocol — packet parsing/building plus FICH and VD Mode 2 voice codec.
//
// Channel coding follows the YSF spec as implemented in G4KLX's MMDVMHost
// (YSFFICH.cpp, YSFPayload.cpp) and the YSF↔DMR transcoder in MMDVM_CM/DMR2YSF.
// Voice extraction maps the YSF VCH 49 voice bits onto the raw AMBE+2 2450
// buffer accepted by DroidStar's encode_2450 / decode_2450:
//
//   VCH bit position (after deinterleave + descramble) → AMBE bit
//     vch[3*i + 1]    for i=0..11   → ambe bit i      (a-block, 12 bits)
//     vch[3*(i+12)+1] for i=0..11   → ambe bit 12+i   (b-block, 12 bits)
//     vch[3*(i+24)+1] for i=0..2    → ambe bit 24+i   (c-block top, 3 bits)
//     vch[i+81]       for i=0..21   → ambe bit 27+i   (c-block bottom, 22 bits)

const (
	YSFPort         = 42000
	YSFPollSize     = 14
	YSFOptionSize   = 40
	YSFOptionStrLen = 26 // bytes available for the options string in a YSFO
	YSFFrameSize    = 155
	YSFSyncLen      = 5
	YSFFICHLen      = 25
	YSFPayloadLen   = 90
	YSFCallsignLen  = 10
	YSFVCHByteLen   = 13 // bytes per VCH section
	YSFSectionBytes = 18 // bytes per payload section (DCH 5 + VCH 13)

	YSFKeepaliveInterval = 5 // seconds

	YSF_FI_HEADER = 0
	YSF_FI_COMM   = 1
	YSF_FI_TERM   = 2

	YSF_DT_VD1      = 0
	YSF_DT_DATA_FR  = 1
	YSF_DT_VD2      = 2
	YSF_DT_VOICE_FR = 3

	YSFFramesPerSuper = 7 // FN cycles 0-6
)

var ysfSync = [5]byte{0xD4, 0x71, 0xC9, 0x63, 0x4D}

// padCallsign pads a callsign to exactly n bytes with spaces.
func padCallsign(cs string, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	copy(b, []byte(cs))
	return b
}

// BuildYSFPoll builds a 14-byte YSFP poll/registration packet.
func BuildYSFPoll(callsign string) []byte {
	pkt := make([]byte, YSFPollSize)
	copy(pkt[0:4], []byte("YSFP"))
	copy(pkt[4:14], padCallsign(callsign, YSFCallsignLen))
	return pkt
}

// BuildYSFUnlink builds a 14-byte YSFU disconnect packet.
func BuildYSFUnlink(callsign string) []byte {
	pkt := make([]byte, YSFPollSize)
	copy(pkt[0:4], []byte("YSFU"))
	copy(pkt[4:14], padCallsign(callsign, YSFCallsignLen))
	return pkt
}

// BuildYSFOption builds a 40-byte YSFO option packet used to subscribe a
// gateway to a specific DG-ID on multi-stream reflectors (pYSF3, YCS).
// Stock G4KLX YSFReflector silently ignores YSFO, so it is safe to send
// always. Layout matches G4KLX YSFGateway:
//
//	"YSFO" (4) + callsign (10, space-padded) + options (26, space-padded)
//
// pYSF3 parses the options string as the DG-ID in ASCII decimal.
// dgid == 0 produces an empty options field, which leaves the gateway on
// the reflector's default stream (legacy behaviour).
func BuildYSFOption(callsign string, dgid byte) []byte {
	pkt := make([]byte, YSFOptionSize)
	copy(pkt[0:4], []byte("YSFO"))
	copy(pkt[4:14], padCallsign(callsign, YSFCallsignLen))
	for i := 14; i < YSFOptionSize; i++ {
		pkt[i] = ' '
	}
	if dgid > 0 {
		s := strconv.Itoa(int(dgid))
		if len(s) > YSFOptionStrLen {
			s = s[:YSFOptionStrLen]
		}
		copy(pkt[14:14+len(s)], s)
	}
	return pkt
}

// YSFVoiceFrame represents a parsed YSFD network packet.
type YSFVoiceFrame struct {
	SrcGateway string
	SrcRadio   string
	Dest       string
	Counter    byte
	EOT        bool
	FI         byte
	FN         byte
	FT         byte
	DT         byte
	DGId       byte
	Payload    [YSFPayloadLen]byte
}

// ParseYSFD parses a 155-byte YSFD network packet.
func ParseYSFD(data []byte) *YSFVoiceFrame {
	if len(data) < YSFFrameSize || string(data[0:4]) != "YSFD" {
		return nil
	}
	f := &YSFVoiceFrame{
		SrcGateway: string(data[4:14]),
		SrcRadio:   string(data[14:24]),
		Dest:       string(data[24:34]),
		Counter:    (data[34] >> 1) & 0x7F,
		EOT:        (data[34] & 0x01) != 0,
	}

	// Decode FICH (bytes 40..64). Carries FI/FN/FT/DT/DGId.
	var fich [6]byte
	if decodeFICH(data[40:65], fich[:]) {
		f.FI = (fich[0] >> 6) & 0x03
		f.FN = (fich[1] >> 3) & 0x07
		f.FT = fich[1] & 0x07
		f.DT = fich[2] & 0x03
		f.DGId = fich[3] & 0x7F
	}

	copy(f.Payload[:], data[65:155])
	return f
}

// BuildYSFD builds a 155-byte YSFD voice/data packet.
func BuildYSFD(srcGW, srcRadio, dest string, counter byte, eot bool,
	fi, fn, ft, dt, dgid byte, payload [YSFPayloadLen]byte) []byte {

	pkt := make([]byte, YSFFrameSize)
	copy(pkt[0:4], []byte("YSFD"))
	copy(pkt[4:14], padCallsign(srcGW, YSFCallsignLen))
	copy(pkt[14:24], padCallsign(srcRadio, YSFCallsignLen))
	copy(pkt[24:34], padCallsign(dest, YSFCallsignLen))

	pkt[34] = (counter & 0x7F) << 1
	if eot {
		pkt[34] |= 0x01
	}

	copy(pkt[35:40], ysfSync[:])

	// Build FICH (6 bytes: FI/CS/CM/BN/BT/FN/FT/Dev/MR/VoIP/DT/SQL/SQ + CRC) and encode.
	// Bridge always transmits as a VoIP gateway, so bit 2 of byte 2 is set —
	// Yaesu firmware uses this to enable network-audio decode. Without it the
	// receiver opens squelch on FICH but mutes the voice path.
	var fich [6]byte
	fich[0] = (fi & 0x03) << 6   // FI in bits 7:6, CS/CM/BN zero
	fich[1] = (fn & 0x07) << 3   // FN in bits 5:3
	fich[1] |= ft & 0x07         // FT in bits 2:0
	fich[2] = (dt & 0x03) | 0x04 // DT in bits 1:0, VoIP=1 in bit 2
	fich[3] = dgid & 0x7F
	encodeFICH(fich[:], pkt[40:65])

	copy(pkt[65:155], payload[:])
	return pkt
}

// --- FICH codec --------------------------------------------------------------
//
// 4 × Golay(24,12) over 48 data bits (= 6 FICH bytes), then rate-1/2
// convolutional code (K=5) producing 200 bits, then interleaving over
// 100 dibit positions → 25 over-the-air bytes.

func encodeFICH(fich []byte, out []byte) {
	if len(fich) < 6 || len(out) < YSFFICHLen {
		return
	}
	// CRC over the 4 informative bytes; bytes 4..5 of fich hold CRC after this.
	var work [6]byte
	copy(work[:], fich[:6])
	addCRCCCITT162(work[:])

	// Pack 6 bytes (48 bits) into 4 × 12-bit groups for Golay(24,12).
	b0 := (uint32(work[0])<<4)&0xFF0 | uint32(work[1]>>4)&0x00F
	b1 := (uint32(work[1])<<8)&0xF00 | uint32(work[2])
	b2 := (uint32(work[3])<<4)&0xFF0 | uint32(work[4]>>4)&0x00F
	b3 := (uint32(work[4])<<8)&0xF00 | uint32(work[5])

	c0 := golay24Encode(b0)
	c1 := golay24Encode(b1)
	c2 := golay24Encode(b2)
	c3 := golay24Encode(b3)

	// Pack 4 × 24-bit Golay codewords into 12 bytes + 1 byte zero pad = 13 bytes.
	conv := [13]byte{}
	conv[0] = byte(c0 >> 16)
	conv[1] = byte(c0 >> 8)
	conv[2] = byte(c0)
	conv[3] = byte(c1 >> 16)
	conv[4] = byte(c1 >> 8)
	conv[5] = byte(c1)
	conv[6] = byte(c2 >> 16)
	conv[7] = byte(c2 >> 8)
	conv[8] = byte(c2)
	conv[9] = byte(c3 >> 16)
	conv[10] = byte(c3 >> 8)
	conv[11] = byte(c3)
	// conv[12] = 0 (tail bits to flush the convolutional encoder)

	// Convolutional encode 100 bits → 200 output bits = 25 bytes.
	convolved := make([]byte, 25)
	encodeYSFConv(conv[:], convolved, 100)

	// Interleave: write the 100 dibits to interleaved bit positions in `out`.
	for i := range out[:YSFFICHLen] {
		out[i] = 0
	}
	j := 0
	for i := 0; i < 100; i++ {
		n := interleaveTableFICH[i]
		s0 := readBit(convolved, j)
		j++
		s1 := readBit(convolved, j)
		j++
		writeBit(out, int(n), s0)
		writeBit(out, int(n)+1, s1)
	}
}

func decodeFICH(in []byte, fich []byte) bool {
	if len(in) < YSFFICHLen || len(fich) < 6 {
		return false
	}
	vit := newYSFConvolution()
	vit.start()

	// Deinterleave 100 dibits and feed to Viterbi.
	for i := 0; i < 100; i++ {
		n := interleaveTableFICH[i]
		var s0, s1 uint8
		if readBit(in, int(n)) {
			s0 = 1
		}
		if readBit(in, int(n)+1) {
			s1 = 1
		}
		vit.decode(s0, s1)
	}

	output := make([]byte, 13)
	vit.chainback(output, 96)

	c0 := uint32(output[0])<<16 | uint32(output[1])<<8 | uint32(output[2])
	c1 := uint32(output[3])<<16 | uint32(output[4])<<8 | uint32(output[5])
	c2 := uint32(output[6])<<16 | uint32(output[7])<<8 | uint32(output[8])
	c3 := uint32(output[9])<<16 | uint32(output[10])<<8 | uint32(output[11])

	b0, ok0 := golay24Decode(c0)
	b1, ok1 := golay24Decode(c1)
	b2, ok2 := golay24Decode(c2)
	b3, ok3 := golay24Decode(c3)
	if !ok0 || !ok1 || !ok2 || !ok3 {
		return false
	}

	fich[0] = byte((b0 >> 4) & 0xFF)
	fich[1] = byte(((b0 << 4) & 0xF0) | ((b1 >> 8) & 0x0F))
	fich[2] = byte(b1 & 0xFF)
	fich[3] = byte((b2 >> 4) & 0xFF)
	fich[4] = byte(((b2 << 4) & 0xF0) | ((b3 >> 8) & 0x0F))
	fich[5] = byte(b3 & 0xFF)

	return checkCRCCCITT162(fich[:6])
}

// --- VD Mode 2 voice codec ---------------------------------------------------

// ExtractVD2AMBE decodes the 5 voice frames from a 90-byte VD2 payload into
// 5 raw 7-byte AMBE+2 2450 frames suitable for DroidStar decode_2450.
func ExtractVD2AMBE(payload [YSFPayloadLen]byte) [5][YSFAMBEFrameSize]byte {
	var frames [5][YSFAMBEFrameSize]byte

	for j := 0; j < 5; j++ {
		// Each section is 18 bytes: 5 bytes DCH + 13 bytes VCH.
		section := payload[j*YSFSectionBytes : (j+1)*YSFSectionBytes]
		over := section[5 : 5+YSFVCHByteLen]

		// Deinterleave (INTERLEAVE_TABLE_26_4) into a 13-byte VCH.
		var vch [YSFVCHByteLen]byte
		for i := 0; i < 104; i++ {
			if readBit(over, int(interleaveTable26x4[i])) {
				writeBit(vch[:], i, true)
			}
		}
		// Descramble.
		for i := 0; i < YSFVCHByteLen; i++ {
			vch[i] ^= whiteningData[i]
		}

		// Extract 49 voice bits (vote middle bit of each protected triplet
		// for the protected portion, take direct bits 81..102 for the rest).
		var ambeBits [49]byte
		for i := 0; i < 12; i++ {
			ambeBits[i] = boolToByte(readBit(vch[:], 3*i+1)) // a-block
		}
		for i := 0; i < 12; i++ {
			ambeBits[12+i] = boolToByte(readBit(vch[:], 3*(i+12)+1)) // b-block
		}
		for i := 0; i < 3; i++ {
			ambeBits[24+i] = boolToByte(readBit(vch[:], 3*(i+24)+1)) // c-block top
		}
		for i := 0; i < 22; i++ {
			ambeBits[27+i] = boolToByte(readBit(vch[:], i+81)) // c-block bottom
		}

		// Pack 49 bits MSB-first into 7 bytes (matches encode_2450 / decode_2450).
		for i := 0; i < 49; i++ {
			if ambeBits[i] != 0 {
				frames[j][i>>3] |= 1 << uint(7-(i&7))
			}
		}
	}
	return frames
}

// PackVD2AMBE encodes 5 raw 7-byte AMBE+2 2450 frames into a 90-byte VD2 payload.
// DCH (data channel) sections are left zeroed; the receiving radio will simply
// display no callsign info, but audio will play.
func PackVD2AMBE(frames [5][YSFAMBEFrameSize]byte) [YSFPayloadLen]byte {
	var payload [YSFPayloadLen]byte

	for j := 0; j < 5; j++ {
		// Read 49 bits from the 7-byte AMBE frame.
		var ambeBits [49]byte
		for i := 0; i < 49; i++ {
			if frames[j][i>>3]&(1<<uint(7-(i&7))) != 0 {
				ambeBits[i] = 1
			}
		}

		// Build a 13-byte VCH: triplet-expand the protected 27 bits,
		// place the remaining 22 in positions 81..102, zero position 103.
		var vch [YSFVCHByteLen]byte
		for i := 0; i < 12; i++ {
			b := ambeBits[i] != 0
			writeBit(vch[:], 3*i+0, b)
			writeBit(vch[:], 3*i+1, b)
			writeBit(vch[:], 3*i+2, b)
		}
		for i := 0; i < 12; i++ {
			b := ambeBits[12+i] != 0
			writeBit(vch[:], 3*(i+12)+0, b)
			writeBit(vch[:], 3*(i+12)+1, b)
			writeBit(vch[:], 3*(i+12)+2, b)
		}
		for i := 0; i < 3; i++ {
			b := ambeBits[24+i] != 0
			writeBit(vch[:], 3*(i+24)+0, b)
			writeBit(vch[:], 3*(i+24)+1, b)
			writeBit(vch[:], 3*(i+24)+2, b)
		}
		for i := 0; i < 22; i++ {
			b := ambeBits[27+i] != 0
			writeBit(vch[:], i+81, b)
		}

		// Scramble (XOR whitening).
		for i := 0; i < YSFVCHByteLen; i++ {
			vch[i] ^= whiteningData[i]
		}

		// Interleave (INTERLEAVE_TABLE_26_4) into the over-the-air VCH bytes.
		var over [YSFVCHByteLen]byte
		for i := 0; i < 104; i++ {
			if readBit(vch[:], i) {
				writeBit(over[:], int(interleaveTable26x4[i]), true)
			}
		}

		// Place into payload: bytes 5..17 of the 18-byte section.
		copy(payload[j*YSFSectionBytes+5:(j+1)*YSFSectionBytes], over[:])
	}
	return payload
}

func boolToByte(b bool) byte {
	if b {
		return 1
	}
	return 0
}
