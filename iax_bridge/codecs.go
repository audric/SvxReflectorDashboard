package main

/*
#cgo LDFLAGS: -lgsm
#include <gsm/gsm.h>
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Codec represents an audio codec that converts between PCM and encoded frames.
type Codec interface {
	Encode(pcm []int16) ([]byte, error)
	Decode(data []byte) ([]int16, error)
	FormatBit() uint32
	FrameSize() int // PCM samples per frame
	Name() string
}

// --- A-law codec ---

type AlawCodec struct{}

func (c *AlawCodec) Name() string      { return "alaw" }
func (c *AlawCodec) FormatBit() uint32 { return AST_FORMAT_ALAW }
func (c *AlawCodec) FrameSize() int    { return PCMFrameSize }

func (c *AlawCodec) Encode(pcm []int16) ([]byte, error) {
	out := make([]byte, len(pcm))
	for i, s := range pcm {
		out[i] = Linear16ToAlaw(s)
	}
	return out, nil
}

func (c *AlawCodec) Decode(data []byte) ([]int16, error) {
	out := make([]int16, len(data))
	for i, a := range data {
		out[i] = AlawToLinear16(a)
	}
	return out, nil
}

func Linear16ToAlaw(sample int16) byte {
	sign := 0
	s := int(sample)
	if s < 0 {
		sign = 0x55
		s = -s - 1
		if s < 0 {
			s = 0
		}
	} else {
		sign = 0xD5
	}

	if s > 32767 {
		s = 32767
	}

	exp := 7
	for i := 7; i > 0; i-- {
		if s >= (256 << uint(i)) {
			exp = i
			break
		}
		exp = i - 1
	}

	var mantissa int
	if exp > 0 {
		mantissa = (s >> (uint(exp) + 3)) & 0x0F
	} else {
		mantissa = (s >> 4) & 0x0F
	}

	aval := byte((exp << 4) | mantissa)
	return aval ^ byte(sign)
}

func AlawToLinear16(aval byte) int16 {
	a := aval ^ 0x55
	sign := a & 0x80
	exp := int((a >> 4) & 0x07)
	mantissa := int(a & 0x0F)

	var sample int
	if exp == 0 {
		sample = (mantissa<<4 + 8)
	} else {
		sample = ((mantissa<<4 + 8 + 256) << uint(exp-1))
	}

	if sign == 0 {
		return int16(-sample)
	}
	return int16(sample)
}

// --- G.726 ADPCM codec (32kbps) ---

type G726Codec struct {
	encState g726State
	decState g726State
}

type g726State struct {
	sr    [2]int32
	dq    [6]int32
	a     [2]int32
	b     [6]int32
	pk    [2]int32
	ap    int32
	yu    int32
	yl    int32
	td    int32
}

func NewG726Codec() *G726Codec {
	c := &G726Codec{}
	c.encState.yl = 34816
	c.encState.yu = 544
	c.decState.yl = 34816
	c.decState.yu = 544
	return c
}

func (c *G726Codec) Name() string      { return "g726" }
func (c *G726Codec) FormatBit() uint32 { return AST_FORMAT_G726 }
func (c *G726Codec) FrameSize() int    { return PCMFrameSize }

func (c *G726Codec) Encode(pcm []int16) ([]byte, error) {
	out := make([]byte, len(pcm)/2)
	for i := 0; i < len(pcm); i += 2 {
		code1 := g726Encode(&c.encState, pcm[i])
		code2 := byte(0)
		if i+1 < len(pcm) {
			code2 = g726Encode(&c.encState, pcm[i+1])
		}
		out[i/2] = (code1 << 4) | code2
	}
	return out, nil
}

func (c *G726Codec) Decode(data []byte) ([]int16, error) {
	out := make([]int16, len(data)*2)
	for i, b := range data {
		out[i*2] = g726Decode(&c.decState, (b>>4)&0x0F)
		out[i*2+1] = g726Decode(&c.decState, b&0x0F)
	}
	return out, nil
}

func g726Encode(st *g726State, sample int16) byte {
	sez := int32(0)
	for i := 0; i < 6; i++ {
		sez += st.b[i] * st.dq[i]
	}
	sez >>= 14

	se := sez
	for i := 0; i < 2; i++ {
		se += st.a[i] * st.sr[i]
	}
	se >>= 14

	d := int32(sample) - se
	y := int32(st.yu)

	dln := int32(0)
	if d < 0 {
		dln = -d
	} else {
		dln = d
	}

	var code byte
	if dln >= y {
		code = 7
	} else {
		ratio := (dln << 15) / (y + 1)
		switch {
		case ratio < 80:
			code = 0
		case ratio < 178:
			code = 1
		case ratio < 346:
			code = 2
		case ratio < 686:
			code = 3
		case ratio < 1355:
			code = 4
		case ratio < 2708:
			code = 5
		case ratio < 5765:
			code = 6
		default:
			code = 7
		}
	}
	if d < 0 {
		code |= 0x08
	}

	dq := g726InverseQuantize(code, y)
	sr := se + dq
	g726UpdateState(st, dq, sr, sez, d < 0)

	return code & 0x0F
}

func g726Decode(st *g726State, code byte) int16 {
	sez := int32(0)
	for i := 0; i < 6; i++ {
		sez += st.b[i] * st.dq[i]
	}
	sez >>= 14

	se := sez
	for i := 0; i < 2; i++ {
		se += st.a[i] * st.sr[i]
	}
	se >>= 14

	y := int32(st.yu)
	dq := g726InverseQuantize(code, y)
	sr := se + dq

	g726UpdateState(st, dq, sr, sez, code&0x08 != 0)

	if sr > 32767 {
		sr = 32767
	} else if sr < -32768 {
		sr = -32768
	}
	return int16(sr)
}

func g726InverseQuantize(code byte, y int32) int32 {
	mag := code & 0x07
	levels := [8]int32{0, 33, 95, 200, 400, 800, 1600, 3200}
	dq := (levels[mag] * y) >> 15
	if code&0x08 != 0 {
		return -dq
	}
	return dq
}

func g726UpdateState(st *g726State, dq, sr, sez int32, sign bool) {
	for i := 5; i > 0; i-- {
		st.dq[i] = st.dq[i-1]
	}
	st.dq[0] = dq

	st.sr[1] = st.sr[0]
	st.sr[0] = sr

	pk0 := int32(0)
	if sr < 0 {
		pk0 = 1
	}

	for i := 1; i >= 0; i-- {
		if st.pk[i] == pk0 {
			st.a[i] += 192
		} else {
			st.a[i] -= 192
		}
		if st.a[i] > 12288 {
			st.a[i] = 12288
		} else if st.a[i] < -12288 {
			st.a[i] = -12288
		}
	}
	st.pk[1] = st.pk[0]
	st.pk[0] = pk0

	for i := 5; i >= 0; i-- {
		if dq != 0 {
			if (dq > 0 && st.dq[i] > 0) || (dq < 0 && st.dq[i] < 0) {
				st.b[i] += 128
			} else {
				st.b[i] -= 128
			}
		}
		if st.b[i] > 16383 {
			st.b[i] = 16383
		} else if st.b[i] < -16383 {
			st.b[i] = -16383
		}
	}

	absDq := dq
	if absDq < 0 {
		absDq = -absDq
	}
	st.yu = y32(absDq, st.yu)
}

func y32(dq, yu int32) int32 {
	if dq > (3*yu)>>2 {
		return yu + 64
	}
	if yu > 544 {
		return yu - 1
	}
	return 544
}

// --- GSM full-rate codec (via libgsm CGo) ---

type GSMCodec struct {
	enc C.gsm
	dec C.gsm
}

func NewGSMCodec() (*GSMCodec, error) {
	enc := C.gsm_create()
	if enc == nil {
		return nil, fmt.Errorf("gsm_create failed for encoder")
	}
	dec := C.gsm_create()
	if dec == nil {
		C.gsm_destroy(enc)
		return nil, fmt.Errorf("gsm_create failed for decoder")
	}
	return &GSMCodec{enc: enc, dec: dec}, nil
}

func (c *GSMCodec) Name() string      { return "gsm" }
func (c *GSMCodec) FormatBit() uint32 { return AST_FORMAT_GSM }
func (c *GSMCodec) FrameSize() int    { return 160 }

func (c *GSMCodec) Encode(pcm []int16) ([]byte, error) {
	if len(pcm) < 160 {
		return nil, fmt.Errorf("gsm encode requires 160 samples, got %d", len(pcm))
	}
	var frame [33]C.gsm_byte
	C.gsm_encode(c.enc, (*C.gsm_signal)(unsafe.Pointer(&pcm[0])), &frame[0])
	out := make([]byte, 33)
	for i := range out {
		out[i] = byte(frame[i])
	}
	return out, nil
}

func (c *GSMCodec) Decode(data []byte) ([]int16, error) {
	if len(data) < 33 {
		return nil, fmt.Errorf("gsm decode requires 33 bytes, got %d", len(data))
	}
	var frame [33]C.gsm_byte
	for i := range frame {
		frame[i] = C.gsm_byte(data[i])
	}
	pcm := make([]int16, 160)
	ret := C.gsm_decode(c.dec, &frame[0], (*C.gsm_signal)(unsafe.Pointer(&pcm[0])))
	if ret != 0 {
		return nil, fmt.Errorf("gsm_decode failed: %d", ret)
	}
	return pcm, nil
}

func (c *GSMCodec) Close() {
	if c.enc != nil {
		C.gsm_destroy(c.enc)
	}
	if c.dec != nil {
		C.gsm_destroy(c.dec)
	}
}

// --- Ulaw codec (wraps existing ulaw.go functions) ---

type UlawCodec struct{}

func (c *UlawCodec) Name() string      { return "ulaw" }
func (c *UlawCodec) FormatBit() uint32 { return AST_FORMAT_ULAW }
func (c *UlawCodec) FrameSize() int    { return PCMFrameSize }

func (c *UlawCodec) Encode(pcm []int16) ([]byte, error) {
	return PCMToUlaw(pcm), nil
}

func (c *UlawCodec) Decode(data []byte) ([]int16, error) {
	return UlawToPCM(data), nil
}

// --- Codec registry ---

func ParseCodecList(codecStr string) uint32 {
	var capability uint32
	for _, name := range splitTrim(codecStr) {
		switch name {
		case "gsm":
			capability |= AST_FORMAT_GSM
		case "ulaw":
			capability |= AST_FORMAT_ULAW
		case "alaw":
			capability |= AST_FORMAT_ALAW
		case "g726":
			capability |= AST_FORMAT_G726
		}
	}
	if capability == 0 {
		capability = AST_FORMAT_ULAW
	}
	return capability
}

func CodecForFormat(format uint32) (Codec, error) {
	switch format {
	case AST_FORMAT_ULAW:
		return &UlawCodec{}, nil
	case AST_FORMAT_ALAW:
		return &AlawCodec{}, nil
	case AST_FORMAT_GSM:
		return NewGSMCodec()
	case AST_FORMAT_G726:
		return NewG726Codec(), nil
	default:
		return nil, fmt.Errorf("unsupported codec format: %d", format)
	}
}

func splitTrim(s string) []string {
	var out []string
	for _, part := range splitComma(s) {
		t := trimSpace(part)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

func splitComma(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func trimSpace(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	j := len(s)
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t') {
		j--
	}
	return s[i:j]
}
