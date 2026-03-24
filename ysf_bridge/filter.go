package main

import "math"

// Biquad implements a second-order IIR filter (Butterworth design).
// Used for HPF and LPF in the voice audio chain.
type Biquad struct {
	b0, b1, b2 float64 // feedforward coefficients
	a1, a2     float64 // feedback coefficients (a0 is normalized to 1)
	x1, x2     float64 // input delay line
	y1, y2     float64 // output delay line
}

// NewHighPass creates a 2nd-order Butterworth high-pass filter.
// cutoffHz is the -3dB cutoff frequency, sampleRate is in Hz.
func NewHighPass(cutoffHz, sampleRate float64) *Biquad {
	w0 := 2.0 * math.Pi * cutoffHz / sampleRate
	alpha := math.Sin(w0) / (2.0 * math.Sqrt2) // Q = 1/sqrt(2) for Butterworth
	cosW0 := math.Cos(w0)

	a0 := 1.0 + alpha
	return &Biquad{
		b0: ((1.0 + cosW0) / 2.0) / a0,
		b1: (-(1.0 + cosW0)) / a0,
		b2: ((1.0 + cosW0) / 2.0) / a0,
		a1: (-2.0 * cosW0) / a0,
		a2: (1.0 - alpha) / a0,
	}
}

// NewLowPass creates a 2nd-order Butterworth low-pass filter.
// cutoffHz is the -3dB cutoff frequency, sampleRate is in Hz.
func NewLowPass(cutoffHz, sampleRate float64) *Biquad {
	w0 := 2.0 * math.Pi * cutoffHz / sampleRate
	alpha := math.Sin(w0) / (2.0 * math.Sqrt2) // Q = 1/sqrt(2) for Butterworth
	cosW0 := math.Cos(w0)

	a0 := 1.0 + alpha
	return &Biquad{
		b0: ((1.0 - cosW0) / 2.0) / a0,
		b1: (1.0 - cosW0) / a0,
		b2: ((1.0 - cosW0) / 2.0) / a0,
		a1: (-2.0 * cosW0) / a0,
		a2: (1.0 - alpha) / a0,
	}
}

// Process applies the filter to a buffer of PCM samples in-place.
func (b *Biquad) Process(samples []int16) {
	for i, s := range samples {
		x0 := float64(s)
		y0 := b.b0*x0 + b.b1*b.x1 + b.b2*b.x2 - b.a1*b.y1 - b.a2*b.y2

		b.x2 = b.x1
		b.x1 = x0
		b.y2 = b.y1
		b.y1 = y0

		// Clip to int16 range
		if y0 > 32767 {
			y0 = 32767
		} else if y0 < -32768 {
			y0 = -32768
		}
		samples[i] = int16(y0)
	}
}

// Reset clears the filter delay lines (call on talk start).
func (b *Biquad) Reset() {
	b.x1, b.x2, b.y1, b.y2 = 0, 0, 0, 0
}

// VoiceFilter chains an HPF and LPF for voice bandpass filtering.
type VoiceFilter struct {
	hpf        *Biquad
	lpf        *Biquad
	hpfCutoff  float64
	lpfCutoff  float64
	enabled    bool
}

// DefaultHPFCutoff is the standard voice band high-pass cutoff (300 Hz).
const DefaultHPFCutoff = 300.0

// DefaultLPFCutoff is the standard voice band low-pass cutoff (3000 Hz).
const DefaultLPFCutoff = 3000.0

// NewVoiceFilter creates a bandpass voice filter.
// Set hpfCutoff or lpfCutoff to 0 to disable that stage.
func NewVoiceFilter(hpfCutoff, lpfCutoff, sampleRate float64) *VoiceFilter {
	vf := &VoiceFilter{hpfCutoff: hpfCutoff, lpfCutoff: lpfCutoff}
	if hpfCutoff > 0 {
		vf.hpf = NewHighPass(hpfCutoff, sampleRate)
	}
	if lpfCutoff > 0 {
		vf.lpf = NewLowPass(lpfCutoff, sampleRate)
	}
	vf.enabled = vf.hpf != nil || vf.lpf != nil
	return vf
}

// NewVoiceFilterFromEnv creates a voice filter with env-configurable cutoffs.
// prefix+"HPF_CUTOFF" and prefix+"LPF_CUTOFF" override defaults. Set to 0 to disable.
func NewVoiceFilterFromEnv(prefix string, sampleRate float64) *VoiceFilter {
	hpf := envFloat(prefix+"HPF_CUTOFF", DefaultHPFCutoff)
	lpf := envFloat(prefix+"LPF_CUTOFF", DefaultLPFCutoff)
	return NewVoiceFilter(hpf, lpf, sampleRate)
}

// Process applies HPF then LPF to the samples in-place.
func (vf *VoiceFilter) Process(samples []int16) {
	if !vf.enabled {
		return
	}
	if vf.hpf != nil {
		vf.hpf.Process(samples)
	}
	if vf.lpf != nil {
		vf.lpf.Process(samples)
	}
}

// Reset clears the filter state (call on talk start).
func (vf *VoiceFilter) Reset() {
	if vf.hpf != nil {
		vf.hpf.Reset()
	}
	if vf.lpf != nil {
		vf.lpf.Reset()
	}
}

// HPFCutoff returns the configured HPF cutoff or 0 if disabled.
func (vf *VoiceFilter) HPFCutoff() float64 { return vf.hpfCutoff }

// LPFCutoff returns the configured LPF cutoff or 0 if disabled.
func (vf *VoiceFilter) LPFCutoff() float64 { return vf.lpfCutoff }
