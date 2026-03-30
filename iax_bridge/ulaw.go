package main

// G.711 mu-law codec — converts between 16-bit linear PCM and 8-bit ulaw.
// No external dependencies needed.

const (
	ulawBias = 33
	ulawMax  = 0x1FFF
)

// Linear16ToUlaw converts a 16-bit signed PCM sample to 8-bit mu-law.
func Linear16ToUlaw(sample int16) byte {
	sign := 0
	s := int(sample)
	if s < 0 {
		sign = 0x80
		s = -s
	}
	s += ulawBias
	if s > ulawMax {
		s = ulawMax
	}

	// Find the segment (exponent)
	exp := 7
	for i := 7; i > 0; i-- {
		if s >= (1 << (uint(i) + 3)) {
			exp = i
			break
		}
		exp = i - 1
	}

	mantissa := (s >> (uint(exp) + 3)) & 0x0F
	uval := byte(sign | (exp << 4) | mantissa)
	return ^uval // complement
}

// UlawToLinear16 converts an 8-bit mu-law sample to 16-bit signed PCM.
func UlawToLinear16(uval byte) int16 {
	uval = ^uval // complement
	sign := uval & 0x80
	exp := int((uval >> 4) & 0x07)
	mantissa := int(uval & 0x0F)

	sample := (mantissa<<3 + 0x84) << uint(exp)
	sample -= ulawBias << 2 // remove bias

	if sign != 0 {
		return int16(-sample >> 2)
	}
	return int16(sample >> 2)
}

// PCMToUlaw converts a slice of PCM samples to ulaw bytes.
func PCMToUlaw(pcm []int16) []byte {
	out := make([]byte, len(pcm))
	for i, s := range pcm {
		out[i] = Linear16ToUlaw(s)
	}
	return out
}

// UlawToPCM converts ulaw bytes to PCM samples.
func UlawToPCM(ulaw []byte) []int16 {
	out := make([]int16, len(ulaw))
	for i, u := range ulaw {
		out[i] = UlawToLinear16(u)
	}
	return out
}
