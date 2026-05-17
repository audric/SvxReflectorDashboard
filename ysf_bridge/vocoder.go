package main

/*
#cgo LDFLAGS: -L/usr/local/lib -lvocoder_wrapper -lmbevocoder -lstdc++ -lm
#cgo CFLAGS: -I/usr/local/include

#include <stdint.h>

extern void* mbe_vocoder_new();
extern void  mbe_vocoder_free(void* voc);
extern void  mbe_vocoder_decode_dmr(void* voc, uint8_t* ambe, int16_t* pcm);
extern void  mbe_vocoder_encode_dmr(void* voc, int16_t* pcm, uint8_t* ambe);
extern void  mbe_vocoder_decode_ysf(void* voc, uint8_t* ambe, int16_t* pcm);
extern void  mbe_vocoder_encode_ysf(void* voc, int16_t* pcm, uint8_t* ambe);
*/
import "C"

import (
	"sync"
	"unsafe"
)

// PCM audio parameters
const (
	PCMSampleRate    = 8000 // 8 kHz
	PCMFrameSize     = 160  // 160 samples = 20ms at 8kHz
	YSFAMBEFrameSize = 7    // 7 bytes = 49 bits raw AMBE+2 2450 (YSF DN mode)
)

// Vocoder wraps DroidStar's MBEVocoder for AMBE+2 2450.
// For YSF DN/VD2 mode we use the raw 49-bit form (encode/decode_2450),
// because the YSF channel coding already handles FEC at the VCH layer.
// The library is NOT thread-safe, so all calls are serialized with a mutex.
type Vocoder struct {
	handle unsafe.Pointer
	mu     sync.Mutex
}

// NewVocoder creates and initializes a new MBEVocoder instance.
func NewVocoder() (*Vocoder, error) {
	h := C.mbe_vocoder_new()
	if h == nil {
		return nil, nil
	}
	return &Vocoder{handle: h}, nil
}

// Close destroys the vocoder instance.
func (v *Vocoder) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.handle != nil {
		C.mbe_vocoder_free(v.handle)
		v.handle = nil
	}
}

// Decode converts a 7-byte raw AMBE+2 2450 frame (49 bits voice, MSB-first)
// to 160 PCM samples (8kHz, 16-bit mono).
func (v *Vocoder) Decode(ambe [YSFAMBEFrameSize]byte) [PCMFrameSize]int16 {
	v.mu.Lock()
	defer v.mu.Unlock()

	var pcm [PCMFrameSize]int16
	C.mbe_vocoder_decode_ysf(
		v.handle,
		(*C.uint8_t)(unsafe.Pointer(&ambe[0])),
		(*C.int16_t)(unsafe.Pointer(&pcm[0])),
	)
	return pcm
}

// Encode converts 160 PCM samples to a 7-byte raw AMBE+2 2450 frame.
func (v *Vocoder) Encode(pcm [PCMFrameSize]int16) [YSFAMBEFrameSize]byte {
	v.mu.Lock()
	defer v.mu.Unlock()

	var ambe [YSFAMBEFrameSize]byte
	C.mbe_vocoder_encode_ysf(
		v.handle,
		(*C.int16_t)(unsafe.Pointer(&pcm[0])),
		(*C.uint8_t)(unsafe.Pointer(&ambe[0])),
	)
	return ambe
}
