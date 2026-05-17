package main

// YSF channel coding primitives.
//
// Ported from G4KLX's MMDVMHost (GPLv2):
//   - YSFConvolution.cpp  → ysfConvolution
//   - YSFPayload.cpp      → INTERLEAVE_TABLE_26_4, WHITENING_DATA, voice de/scramble
//   - YSFFICH.cpp         → FICH interleave table
//   - Golay24128.cpp      → golay24Encode / golay24Decode (algorithmic, no LUTs)
//   - CRC.cpp             → CCITT-16/0xFFFF check + add (algorithmic, no LUT)

// --- Bit helpers ---

var bitMask = [8]byte{0x80, 0x40, 0x20, 0x10, 0x08, 0x04, 0x02, 0x01}

func readBit(p []byte, i int) bool { return p[i>>3]&bitMask[i&7] != 0 }

func writeBit(p []byte, i int, b bool) {
	if b {
		p[i>>3] |= bitMask[i&7]
	} else {
		p[i>>3] &^= bitMask[i&7]
	}
}

// --- INTERLEAVE_TABLE_26_4: VD2 voice channel (104 bits per VCH) ---
// Source: MMDVMHost YSFPayload.cpp.
var interleaveTable26x4 = [104]uint{
	0, 4, 8, 12, 16, 20, 24, 28, 32, 36, 40, 44, 48, 52, 56, 60, 64, 68, 72, 76, 80, 84, 88, 92, 96, 100,
	1, 5, 9, 13, 17, 21, 25, 29, 33, 37, 41, 45, 49, 53, 57, 61, 65, 69, 73, 77, 81, 85, 89, 93, 97, 101,
	2, 6, 10, 14, 18, 22, 26, 30, 34, 38, 42, 46, 50, 54, 58, 62, 66, 70, 74, 78, 82, 86, 90, 94, 98, 102,
	3, 7, 11, 15, 19, 23, 27, 31, 35, 39, 43, 47, 51, 55, 59, 63, 67, 71, 75, 79, 83, 87, 91, 95, 99, 103,
}

// --- INTERLEAVE_TABLE for FICH: 100 dibit positions over 200 bits ---
var interleaveTableFICH = [100]uint{
	0, 40, 80, 120, 160,
	2, 42, 82, 122, 162,
	4, 44, 84, 124, 164,
	6, 46, 86, 126, 166,
	8, 48, 88, 128, 168,
	10, 50, 90, 130, 170,
	12, 52, 92, 132, 172,
	14, 54, 94, 134, 174,
	16, 56, 96, 136, 176,
	18, 58, 98, 138, 178,
	20, 60, 100, 140, 180,
	22, 62, 102, 142, 182,
	24, 64, 104, 144, 184,
	26, 66, 106, 146, 186,
	28, 68, 108, 148, 188,
	30, 70, 110, 150, 190,
	32, 72, 112, 152, 192,
	34, 74, 114, 154, 194,
	36, 76, 116, 156, 196,
	38, 78, 118, 158, 198,
}

// WHITENING_DATA: descrambles the VCH after deinterleave.
var whiteningData = [20]byte{
	0x93, 0xD7, 0x51, 0x21, 0x9C, 0x2F, 0x6C, 0xD0, 0xEF, 0x0F,
	0xF8, 0x3D, 0xF1, 0x73, 0x20, 0x94, 0xED, 0x1E, 0x7C, 0xD8,
}

// --- Golay (24,12,8) ---
// Algorithmic encoder/decoder (no 4096-entry LUT).
// Generator polynomial (23,12): x^11 + x^10 + x^6 + x^5 + x^4 + x^2 + 1 = 0xC75.

const golayGenPoly = 0xC75

// golay23Encode: data is 12 bits → 23-bit codeword (12 data bits MSBs, 11 parity LSBs).
func golay23Encode(data uint32) uint32 {
	data &= 0xFFF
	cw := data << 11
	tmp := cw
	for i := 22; i >= 11; i-- {
		if tmp&(1<<uint(i)) != 0 {
			tmp ^= uint32(golayGenPoly) << uint(i-11)
		}
	}
	return cw | (tmp & 0x7FF)
}

// golay24Encode: 12 bits → 24-bit codeword (Golay23 + overall parity in LSB).
func golay24Encode(data uint32) uint32 {
	cw := golay23Encode(data) << 1
	var parity uint32
	for i := uint(0); i < 24; i++ {
		parity ^= (cw >> i) & 1
	}
	return cw | parity
}

// golay24Decode: 24-bit codeword → 12 data bits + validity.
//
// Specialized for UDP transit: packets are bit-perfect end-to-end, so the
// over-the-air FEC has no errors to correct. We extract the 12 data bits
// directly from the systematic codeword and validate by re-encoding.
// If the round-trip matches the received codeword the frame is valid.
func golay24Decode(code uint32) (data uint32, ok bool) {
	data = (code >> 12) & 0xFFF
	return data, golay24Encode(data) == (code & 0xFFFFFF)
}

// --- YSF convolutional code (rate 1/2, K=5) ---
// Generators per YSFConvolution.cpp:
//   g1 = D + D^3 + D^4 (1 + x^3 + x^4)
//   g2 = D + D^1 + D^2 + D^4

type ysfConvolution struct {
	oldMetrics [16]uint16
	newMetrics [16]uint16
	decisions  [180]uint16
	dp         int
}

func newYSFConvolution() *ysfConvolution { return &ysfConvolution{} }

func (c *ysfConvolution) start() {
	for i := range c.oldMetrics {
		c.oldMetrics[i] = 0
		c.newMetrics[i] = 0
	}
	c.dp = 0
}

// Branch tables (8-state).
var branchTable1 = [8]uint8{0, 0, 0, 0, 1, 1, 1, 1}
var branchTable2 = [8]uint8{0, 1, 1, 0, 0, 1, 1, 0}

// decode consumes one received symbol pair (s0, s1) ∈ {0,1}.
func (c *ysfConvolution) decode(s0, s1 uint8) {
	var dec uint16
	for i := uint8(0); i < 8; i++ {
		j := i * 2
		metric := uint16(branchTable1[i]^s0) + uint16(branchTable2[i]^s1)

		m0 := c.oldMetrics[i] + metric
		m1 := c.oldMetrics[i+8] + (2 - metric)
		var decision0 uint8
		if m0 >= m1 {
			decision0 = 1
			c.newMetrics[j+0] = m1
		} else {
			c.newMetrics[j+0] = m0
		}

		m0 = c.oldMetrics[i] + (2 - metric)
		m1 = c.oldMetrics[i+8] + metric
		var decision1 uint8
		if m0 >= m1 {
			decision1 = 1
			c.newMetrics[j+1] = m1
		} else {
			c.newMetrics[j+1] = m0
		}

		dec |= (uint16(decision1) << (j + 1)) | (uint16(decision0) << j)
	}
	c.decisions[c.dp] = dec
	c.dp++

	c.oldMetrics, c.newMetrics = c.newMetrics, c.oldMetrics
}

// chainback recovers the decoded bit stream.
func (c *ysfConvolution) chainback(out []byte, nBits uint) {
	state := uint32(0)
	for nBits > 0 {
		nBits--
		c.dp--
		idx := state >> (9 - 5) // K=5
		bit := uint8(c.decisions[c.dp]>>idx) & 1
		state = (uint32(bit) << 7) | (state >> 1)
		writeBit(out, int(nBits), bit != 0)
	}
}

// encodeYSFConv encodes nBits from in[] to out[] (rate 1/2).
func encodeYSFConv(in, out []byte, nBits uint) {
	var d1, d2, d3, d4 uint8
	k := 0
	for i := uint(0); i < nBits; i++ {
		var d uint8
		if readBit(in, int(i)) {
			d = 1
		}
		g1 := (d + d3 + d4) & 1
		g2 := (d + d1 + d2 + d4) & 1
		d4, d3, d2, d1 = d3, d2, d1, d
		writeBit(out, k, g1 != 0)
		k++
		writeBit(out, k, g2 != 0)
		k++
	}
}

// --- CRC-CCITT16: poly 0x1021, init 0x0000, xorout 0xFFFF (MMDVMHost variant) ---

func crcCCITT16(data []byte) uint16 {
	var crc uint16
	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return ^crc
}

// addCRCCCITT162 appends a CCITT-16 CRC over data[0:len-2] into data[len-2:len].
// Layout matches MMDVMHost: high byte at len-2, low byte at len-1.
func addCRCCCITT162(data []byte) {
	if len(data) < 3 {
		return
	}
	crc := crcCCITT16(data[:len(data)-2])
	data[len(data)-2] = byte(crc >> 8)
	data[len(data)-1] = byte(crc & 0xFF)
}

// checkCRCCCITT162 verifies the CRC trailing data.
func checkCRCCCITT162(data []byte) bool {
	if len(data) < 3 {
		return false
	}
	crc := crcCCITT16(data[:len(data)-2])
	return data[len(data)-2] == byte(crc>>8) && data[len(data)-1] == byte(crc&0xFF)
}
