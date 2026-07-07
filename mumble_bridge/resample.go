package main

// Decimator3 downsamples 48kHz mono PCM to 16kHz (a clean 3:1 ratio) by
// anti-alias low-pass filtering and then keeping every 3rd sample. Filter
// state and the decimation phase persist across calls, so streaming buffers
// of arbitrary length resample seamlessly.
//
// This exists because Mumble/gumble is 48kHz-native but SVX-bound Opus must
// be encoded at 16kHz to match the SvxLink ecosystem (svxlink/SVXConnect
// decode at 16kHz; 48kHz frames arrive as silence on those clients).
type Decimator3 struct {
	// Two cascaded Butterworth low-pass stages for a steeper anti-alias
	// roll-off below the 8kHz post-decimation Nyquist.
	lpf1, lpf2 *Biquad
	phase      int
}

func NewDecimator3() *Decimator3 {
	return &Decimator3{
		lpf1: NewLowPass(7000, 48000),
		lpf2: NewLowPass(7000, 48000),
	}
}

func (d *Decimator3) Reset() {
	d.lpf1.Reset()
	d.lpf2.Reset()
	d.phase = 0
}

// Process anti-alias filters in (48kHz, modified in place) and returns a
// freshly allocated 16kHz buffer.
func (d *Decimator3) Process(in []int16) []int16 {
	d.lpf1.Process(in)
	d.lpf2.Process(in)
	out := make([]int16, 0, len(in)/3+1)
	for i := 0; i < len(in); i++ {
		if d.phase == 0 {
			out = append(out, in[i])
		}
		d.phase++
		if d.phase == 3 {
			d.phase = 0
		}
	}
	return out
}
