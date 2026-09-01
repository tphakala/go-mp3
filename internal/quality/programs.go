package quality

import (
	"math"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// Program is one deterministic test signal of the synthetic corpus. Names
// are stable identifiers used in reports and -programs filters.
type Program struct {
	Name     string
	Channels int
	// Gen returns Channels planar float64 channels of nSamples samples each,
	// every sample in [-1, 1], identical on every call.
	Gen func(sampleRate, nSamples int) [][]float64
}

// clickPeriodFrames and clickBurstFrames are the click-program cadence, the
// same 4-frame period and 1-frame burst the compat gate drives block
// switching with.
const (
	clickPeriodFrames = 4
	clickBurstFrames  = 1
)

// Programs returns the synthetic corpus in report order: eight mono
// programs spanning tonal, noisy, transient, swept, bird-like, and
// speech-like content, then three stereo programs exercising decorrelated,
// near-identical (M/S), and fully independent channels.
func Programs() []Program {
	mono := func(name string, gen func(sr, n int) []float64) Program {
		return Program{Name: name, Channels: 1, Gen: func(sr, n int) [][]float64 { return [][]float64{gen(sr, n)} }}
	}
	return []Program{
		mono("multitone", func(sr, n int) []float64 { return testsignal.MultiTone(sr, n, 0, 0.7) }),
		mono("harmonic-vibrato", genHarmonicVibrato),
		mono("pink-noise", genPinkNoise),
		mono("click-train", func(_, n int) []float64 {
			return toF64(testsignal.ClickTrain(n, clickPeriodFrames, clickBurstFrames))
		}),
		mono("tone-click", func(sr, n int) []float64 {
			return toF64(testsignal.ToneClick(sr, n, clickPeriodFrames, clickBurstFrames))
		}),
		mono("sweep", genSweep),
		mono("bird-chirps", genBirdChirps),
		mono("speech-like", genSpeechLike),
		{Name: "stereo-decorrelated", Channels: 2, Gen: func(sr, n int) [][]float64 {
			return [][]float64{testsignal.MultiTone(sr, n, 0, 0.7), testsignal.MultiTone(sr, n, 0.37, 0.7)}
		}},
		{Name: "stereo-near-identical", Channels: 2, Gen: func(sr, n int) [][]float64 {
			l := genHarmonicVibrato(sr, n)
			r := make([]float64, n)
			seed := uint64(0x51DE)
			for i := range r {
				r[i] = clamp1(l[i] + 0.01*testsignal.LCGSigned(&seed))
			}
			return [][]float64{l, r}
		}},
		{Name: "stereo-wide", Channels: 2, Gen: func(sr, n int) [][]float64 {
			return [][]float64{genBirdChirps(sr, n), genSweep(sr, n)}
		}},
	}
}

// ProgramByName looks a program up by its report name.
func ProgramByName(name string) (Program, bool) {
	for _, p := range Programs() {
		if p.Name == name {
			return p, true
		}
	}
	return Program{}, false
}

func toF64(x []float32) []float64 {
	out := make([]float64, len(x))
	for i, v := range x {
		out[i] = float64(v)
	}
	return out
}

func clamp1(v float64) float64 { return max(-1, min(1, v)) }

// genHarmonicVibrato: 220 Hz with 12 harmonics at 1/k, 5.5 Hz vibrato of
// +/-1 %, 0.5 Hz amplitude modulation between 0.3 and 1.0, peak 0.6.
func genHarmonicVibrato(sr, n int) []float64 {
	x := make([]float64, n)
	var norm float64
	for k := 1; k <= 12; k++ {
		norm += 1 / float64(k)
	}
	phase := 0.0
	for i := range x {
		t := float64(i) / float64(sr)
		f0 := 220 * (1 + 0.01*math.Sin(2*math.Pi*5.5*t))
		phase += 2 * math.Pi * f0 / float64(sr)
		var v float64
		for k := 1; k <= 12; k++ {
			v += math.Sin(float64(k)*phase) / float64(k)
		}
		am := 0.65 + 0.35*math.Sin(2*math.Pi*0.5*t)
		x[i] = 0.6 * am * v / norm
	}
	return x
}

// genPinkNoise: LCG white noise through the Paul Kellet 3-pole pinking
// filter (a widely published -3 dB/octave approximation), peak-normalized
// to 0.5.
func genPinkNoise(_, n int) []float64 {
	x := make([]float64, n)
	seed := uint64(0x9A11C0DE)
	var b0, b1, b2 float64
	peak := 0.0
	for i := range x {
		w := testsignal.LCGSigned(&seed)
		b0 = 0.99765*b0 + w*0.0990460
		b1 = 0.96300*b1 + w*0.2965164
		b2 = 0.57000*b2 + w*1.0526913
		x[i] = b0 + b1 + b2 + w*0.1848
		peak = max(peak, math.Abs(x[i]))
	}
	if peak == 0 {
		return x
	}
	for i := range x {
		x[i] = 0.5 * x[i] / peak
	}
	return x
}

// genSweep: logarithmic sine sweep 50 Hz to 18 kHz over the duration,
// amplitude 0.5.
func genSweep(sr, n int) []float64 {
	x := make([]float64, n)
	const f0, f1 = 50.0, 18000.0
	dur := float64(n) / float64(sr)
	k := math.Log(f1 / f0)
	for i := range x {
		t := float64(i) / float64(sr)
		phase := 2 * math.Pi * f0 * dur / k * (math.Exp(k*t/dur) - 1)
		x[i] = 0.5 * math.Sin(phase)
	}
	return x
}

// genBirdChirps: an FM chirp rising 2 kHz to 6 kHz over 60 ms with a 3 ms
// attack and a 40 ms exponential decay, one every 250 ms, peak 0.6, silence
// between chirps. The short attack is what makes it a pre-echo probe.
func genBirdChirps(sr, n int) []float64 {
	x := make([]float64, n)
	period := sr / 4
	length := sr * 60 / 1000
	attack := sr * 3 / 1000
	for start := 0; start+length <= n; start += period {
		phase := 0.0
		for i := range length {
			u := float64(i) / float64(length)
			f := 2000 + 4000*u
			phase += 2 * math.Pi * f / float64(sr)
			env := math.Exp(-float64(i-attack) / (float64(sr) * 0.040))
			if i < attack {
				env = float64(i) / float64(attack)
			}
			x[start+i] = 0.6 * env * math.Sin(phase)
		}
	}
	return x
}

// genSpeechLike: a 120 Hz pulse train (harmonics 1..40 at 1/k) shaped by
// two moving formant resonances, a 4 Hz syllabic amplitude envelope, and a
// 30 ms noise burst every 500 ms, peak-normalized to 0.6.
func genSpeechLike(sr, n int) []float64 {
	x := make([]float64, n)
	seed := uint64(0x5EEC4)
	peak := 0.0
	for i := range x {
		t := float64(i) / float64(sr)
		f1 := 700 + 200*math.Sin(2*math.Pi*3*t)
		f2 := 1800 + 600*math.Sin(2*math.Pi*3*t+1)
		var v float64
		for k := 1; k <= 40; k++ {
			fk := 120 * float64(k)
			g := math.Exp(-((fk-f1)*(fk-f1))/(2*150*150)) + 0.5*math.Exp(-((fk-f2)*(fk-f2))/(2*250*250))
			v += g * math.Sin(2*math.Pi*fk*t) / float64(k)
		}
		v *= 0.55 + 0.45*math.Sin(2*math.Pi*4*t)
		if (i*1000/sr)%500 < 30 {
			v += 0.25 * testsignal.LCGSigned(&seed)
		}
		x[i] = v
		peak = max(peak, math.Abs(v))
	}
	if peak == 0 {
		return x
	}
	for i := range x {
		x[i] = 0.6 * x[i] / peak
	}
	return x
}
