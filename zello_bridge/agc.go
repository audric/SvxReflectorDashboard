package main

import "math"

// AGC implements a simple automatic gain control for PCM audio.
// It tracks the peak level over a sliding window and applies gain
// to bring the audio closer to a target level.
type AGC struct {
	targetLevel float64 // target peak level (0.0-1.0 of int16 range)
	attackRate  float64 // how fast gain increases (0.0-1.0, higher = faster)
	decayRate   float64 // how fast gain decreases (0.0-1.0, higher = faster)
	maxGain     float64 // maximum gain to apply
	minGain     float64 // minimum gain (can attenuate loud signals)

	currentGain float64
}

// NewAGC creates an AGC with sensible defaults for voice audio.
func NewAGC() *AGC {
	return &AGC{
		targetLevel: 0.5,  // target 50% of full scale
		attackRate:  0.01, // increase gain slowly
		decayRate:   0.05, // decrease gain faster (avoid clipping)
		maxGain:     6.0,  // max 6x amplification (~15 dB)
		minGain:     0.5,  // allow attenuating to 50%
		currentGain: 1.0,  // start at unity
	}
}

// Process applies AGC to a buffer of PCM samples in-place.
func (a *AGC) Process(samples []int16) {
	if len(samples) == 0 {
		return
	}

	// Find peak level in this frame
	var peak float64
	for _, s := range samples {
		v := math.Abs(float64(s)) / 32768.0
		if v > peak {
			peak = v
		}
	}

	// Skip silence (avoid boosting noise)
	if peak < 0.01 {
		return
	}

	// Calculate desired gain
	desiredGain := a.targetLevel / peak

	// Clamp to min/max
	if desiredGain > a.maxGain {
		desiredGain = a.maxGain
	}
	if desiredGain < a.minGain {
		desiredGain = a.minGain
	}

	// Smoothly adjust current gain (attack/decay)
	if desiredGain < a.currentGain {
		// Signal is louder than target — reduce gain quickly
		a.currentGain += (desiredGain - a.currentGain) * a.decayRate
	} else {
		// Signal is quieter than target — increase gain slowly
		a.currentGain += (desiredGain - a.currentGain) * a.attackRate
	}

	// Apply gain to samples
	for i, s := range samples {
		v := float64(s) * a.currentGain
		// Clip to int16 range
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		samples[i] = int16(v)
	}
}

// Reset resets the AGC state to unity gain.
func (a *AGC) Reset() {
	a.currentGain = 1.0
}
