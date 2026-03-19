// C wrapper for DroidStar's MBEVocoder C++ class.
// Provides C-linkage functions for CGo.
// Supports both D-STAR (AMBE 2400x1200) and DMR (AMBE+2 2450x1150).

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

// Decode one AMBE+2 2450x1150 frame (DMR) to PCM.
// ambe: 9 bytes input, pcm: 160 int16 samples output.
void mbe_vocoder_decode_dmr(void* voc, uint8_t* ambe, int16_t* pcm) {
    static_cast<MBEVocoder*>(voc)->decode_2450x1150(pcm, ambe);
}

// Encode one PCM frame to AMBE+2 2450x1150 (DMR).
// pcm: 160 int16 samples input, ambe: 9 bytes output (must be zeroed by caller).
void mbe_vocoder_encode_dmr(void* voc, int16_t* pcm, uint8_t* ambe) {
    memset(ambe, 0, 9); // zero output buffer (required by MBEVocoder)
    static_cast<MBEVocoder*>(voc)->encode_2450x1150(pcm, ambe);
}

} // extern "C"
