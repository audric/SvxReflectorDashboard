// C wrapper for DroidStar's MBEVocoder C++ class.
// Provides C-linkage functions for CGo.
// Supports DMR (AMBE+2 2450x1150 with DMR FEC) and YSF (AMBE+2 2450 raw 49-bit).

#include "mbevocoder.h"
#include <cstring>

extern "C" {

// Create a new MBEVocoder instance. Returns opaque pointer.
void* mbe_vocoder_new() {
    return new MBEVocoder();
}

// Destroy a MBEVocoder instance.
void mbe_vocoder_free(void* voc) {
    delete static_cast<MBEVocoder*>(voc);
}

// Decode one AMBE+2 2450x1150 frame (DMR over-the-air format with Golay/Hamming FEC) to PCM.
// ambe: 9 bytes input, pcm: 160 int16 samples output.
void mbe_vocoder_decode_dmr(void* voc, uint8_t* ambe, int16_t* pcm) {
    static_cast<MBEVocoder*>(voc)->decode_2450x1150(pcm, ambe);
}

// Encode one PCM frame to AMBE+2 2450x1150 (DMR).
// pcm: 160 int16 samples input, ambe: 9 bytes output.
void mbe_vocoder_encode_dmr(void* voc, int16_t* pcm, uint8_t* ambe) {
    memset(ambe, 0, 9);
    static_cast<MBEVocoder*>(voc)->encode_2450x1150(pcm, ambe);
}

// Decode one raw AMBE+2 2450 frame (49 bits voice, no FEC) to PCM.
// Used for YSF DN/VD Mode 2 where the FEC layer is handled by the channel coding,
// leaving the vocoder to consume bare voice bits.
// ambe: 7 bytes input (49 bits packed MSB-first: 6 full bytes + bit 7 of byte 6).
// pcm: 160 int16 samples output.
void mbe_vocoder_decode_ysf(void* voc, uint8_t* ambe, int16_t* pcm) {
    static_cast<MBEVocoder*>(voc)->decode_2450(pcm, ambe);
}

// Encode one PCM frame to raw AMBE+2 2450 (49 bits voice, no FEC).
// pcm: 160 int16 samples input, ambe: 7 bytes output.
void mbe_vocoder_encode_ysf(void* voc, int16_t* pcm, uint8_t* ambe) {
    memset(ambe, 0, 7);
    static_cast<MBEVocoder*>(voc)->encode_2450(pcm, ambe);
}

} // extern "C"
