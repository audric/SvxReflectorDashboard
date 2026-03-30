package main

import "math"

// AGC implements automatic gain control for PCM audio.
// It tracks the peak level over a sliding window and applies gain
// to bring the audio closer to a target level.
type AGC struct {
	targetLevel float64 // target peak level (0.0-1.0 of int16 range)
	attackRate  float64 // how fast gain increases (0.0-1.0, higher = faster)
	decayRate   float64 // how fast gain decreases (0.0-1.0, higher = faster)
	maxGain     float64 // maximum gain to apply
	minGain     float64 // minimum gain (can attenuate loud signals)
	limitLevel  float64 // hard limiter threshold (0.0-1.0 of int16 range, 0 = disabled)

	currentGain float64
}

// NewAGC creates an AGC with sensible defaults for voice audio.
func NewAGC() *AGC {
	return &AGC{
		targetLevel: 0.3,  // target 30% of full scale (AMBE-friendly headroom)
		attackRate:  0.01, // increase gain slowly
		decayRate:   0.3,  // decrease gain fast (prevent clipping on hot signals)
		maxGain:     4.0,  // max 4x amplification (~12 dB)
		minGain:     0.1,  // allow attenuating to 10% (-20 dB)
		limitLevel:  0.9,  // hard limit at 90% of full scale
		currentGain: 1.0,  // start at unity
	}
}

// NewAGCFromEnv creates an AGC with parameters overridable via environment
// variables. Each parameter falls back to the default if the env var is unset.
func NewAGCFromEnv(prefix string) *AGC {
	a := NewAGC()
	a.targetLevel = envFloat(prefix+"TARGET_LEVEL", a.targetLevel)
	a.attackRate = envFloat(prefix+"ATTACK_RATE", a.attackRate)
	a.decayRate = envFloat(prefix+"DECAY_RATE", a.decayRate)
	a.maxGain = envFloat(prefix+"MAX_GAIN", a.maxGain)
	a.minGain = envFloat(prefix+"MIN_GAIN", a.minGain)
	a.limitLevel = envFloat(prefix+"LIMIT_LEVEL", a.limitLevel)
	return a
}

// Process applies AGC and hard limiting to a buffer of PCM samples in-place.
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

	// Hard limiter — clamp peaks that still exceed the threshold
	if a.limitLevel > 0 && a.limitLevel < 1.0 {
		limit := a.limitLevel * 32767.0
		for i, s := range samples {
			if float64(s) > limit {
				samples[i] = int16(limit)
			} else if float64(s) < -limit {
				samples[i] = int16(-limit)
			}
		}
	}
}

// Reset resets the AGC state to unity gain.
func (a *AGC) Reset() {
	a.currentGain = 1.0
}
