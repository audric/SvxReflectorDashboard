package main

// 2:1 resamplers between the reflector side (OPUS decoded/encoded at 16 kHz,
// like every other bridge) and the USRP side (raw 8 kHz PCM). Voice audio is
// already band-limited by the voice bandpass filter (LPF ~3 kHz, below the
// 4 kHz Nyquist of 8 kHz), so simple pair-averaging on the way down and linear
// interpolation on the way up are sufficient and cheap.

// downsample16to8 halves the sample rate by averaging adjacent pairs. An odd
// trailing sample is dropped.
func downsample16to8(in []int16) []int16 {
	out := make([]int16, len(in)/2)
	for i := range out {
		out[i] = int16((int32(in[2*i]) + int32(in[2*i+1])) / 2)
	}
	return out
}

// upsampler doubles the sample rate with linear interpolation, carrying the
// last sample across calls so consecutive frames interpolate cleanly.
type upsampler struct {
	prev int16
}

func (u *upsampler) process(in []int16) []int16 {
	out := make([]int16, len(in)*2)
	for i, s := range in {
		out[2*i] = int16((int32(u.prev) + int32(s)) / 2)
		out[2*i+1] = s
		u.prev = s
	}
	return out
}

func (u *upsampler) reset() { u.prev = 0 }
