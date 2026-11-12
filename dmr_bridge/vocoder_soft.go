package main

/*
#cgo LDFLAGS: -L/usr/local/lib -lvocoder_wrapper -lmbevocoder -lstdc++ -lm
#cgo CFLAGS: -I/usr/local/include

#include <stdint.h>

// C wrapper for DroidStar MBEVocoder (DMR AMBE+2)
extern void* mbe_vocoder_new();
extern void  mbe_vocoder_free(void* voc);
extern void  mbe_vocoder_decode_dmr(void* voc, uint8_t* ambe, int16_t* pcm);
extern void  mbe_vocoder_encode_dmr(void* voc, int16_t* pcm, uint8_t* ambe);
*/
import "C"

import (
	"errors"
	"sync"
	"unsafe"
)

// softVocoder wraps DroidStar's MBEVocoder for DMR AMBE+2 2450x1150. It is
// the built-in backend, used when no MD380 firmware has been supplied.
// The library is NOT thread-safe, so all calls are serialized with a mutex.
type softVocoder struct {
	handle unsafe.Pointer
	mu     sync.Mutex
}

// newSoftVocoder creates and initializes a new MBEVocoder instance.
func newSoftVocoder() (*softVocoder, error) {
	h := C.mbe_vocoder_new()
	if h == nil {
		return nil, errors.New("MBEVocoder allocation failed")
	}
	return &softVocoder{handle: h}, nil
}

// Close destroys the vocoder instance.
func (v *softVocoder) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.handle != nil {
		C.mbe_vocoder_free(v.handle)
		v.handle = nil
	}
}

// Decode converts a 9-byte DMR AMBE+2 frame to 160 PCM samples (8kHz, 16-bit mono).
func (v *softVocoder) Decode(ambe [9]byte) [PCMFrameSize]int16 {
	v.mu.Lock()
	defer v.mu.Unlock()

	var pcm [PCMFrameSize]int16
	C.mbe_vocoder_decode_dmr(
		v.handle,
		(*C.uint8_t)(unsafe.Pointer(&ambe[0])),
		(*C.int16_t)(unsafe.Pointer(&pcm[0])),
	)
	return pcm
}

// Encode converts 160 PCM samples (8kHz, 16-bit mono) to a 9-byte DMR AMBE+2 frame.
func (v *softVocoder) Encode(pcm [PCMFrameSize]int16) [9]byte {
	v.mu.Lock()
	defer v.mu.Unlock()

	var ambe [9]byte
	C.mbe_vocoder_encode_dmr(
		v.handle,
		(*C.int16_t)(unsafe.Pointer(&pcm[0])),
		(*C.uint8_t)(unsafe.Pointer(&ambe[0])),
	)
	return ambe
}
