package quality

import (
	"math"
	"slices"
	"testing"
)

// genTone returns n samples of a sine at f Hz with the given amplitude.
func genTone(n, sampleRate int, f, amp float64) []float64 {
	x := make([]float64, n)
	for i := range x {
		x[i] = amp * math.Sin(2*math.Pi*f*float64(i)/float64(sampleRate))
	}
	return x
}

// genNoise returns deterministic uniform noise in [-amp, amp].
func genNoise(n int, amp float64, seed uint64) []float64 {
	x := make([]float64, n)
	for i := range x {
		seed = seed*6364136223846793005 + 1442695040888963407
		x[i] = (float64(seed>>11)/float64(1<<53)*2 - 1) * amp
	}
	return x
}

func addSignals(a, b []float64) []float64 {
	out := make([]float64, len(a))
	for i := range a {
		out[i] = a[i] + b[i]
	}
	return out
}

func TestSNRIdenticalIsCapped(t *testing.T) {
	x := genTone(44100, 44100, 1000, 0.5)
	if got := SNR(x, x); got != SNRCap {
		t.Fatalf("SNR(x, x) = %v, want cap %v", got, SNRCap)
	}
}

// TestSNRKnownNoise adds noise of a known RMS and checks SNR within 0.2 dB
// of the analytic value (sine RMS = amp/sqrt(2); uniform noise RMS =
// amp/sqrt(3)).
func TestSNRKnownNoise(t *testing.T) {
	const n = 1 << 16
	ref := genTone(n, 48000, 997, 0.5)
	noise := genNoise(n, 0.01, 7)
	got := SNR(ref, addSignals(ref, noise))
	want := 20 * math.Log10((0.5/math.Sqrt2)/(0.01/math.Sqrt(3)))
	if math.Abs(got-want) > 0.2 {
		t.Fatalf("SNR = %v, want %v +/- 0.2", got, want)
	}
}

// TestSegmentalSNRClampsAndSkipsSilence: segments 0..4 silent (skipped),
// 5, 6, 8 identical (clamped to SegSNRMax), 7 zeroed (error equals signal,
// exactly 0 dB, inside the clamp), 9 destroyed (clamped to SegSNRMin).
func TestSegmentalSNRClampsAndSkipsSilence(t *testing.T) {
	const n = 10 * SegSNRSegment
	ref := make([]float64, n)
	deg := make([]float64, n)
	tone := genTone(n, 44100, 1000, 0.5)
	copy(ref[5*SegSNRSegment:], tone[5*SegSNRSegment:])
	copy(deg[5*SegSNRSegment:], tone[5*SegSNRSegment:])
	for i := 7 * SegSNRSegment; i < 8*SegSNRSegment; i++ {
		deg[i] = 0 // error equals signal: exactly 0 dB
	}
	for i := 9 * SegSNRSegment; i < n; i++ {
		deg[i] = 5 // absurd error
	}
	got := SegmentalSNR(ref, deg, SegSNRSegment)
	want := (3*SegSNRMax + 0 + SegSNRMin) / 5
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("SegSNR = %v, want %v", got, want)
	}
	if v := SegmentalSNR(make([]float64, n), make([]float64, n), SegSNRSegment); !math.IsNaN(v) {
		t.Fatalf("all-silent SegSNR = %v, want NaN", v)
	}
}

// TestSpectralMetricsGain: a 6.02 dB gain error is exactly a 6.02 dB LSD on
// every active bin, and the band-limited SNR of a signal scaled by 2 is 0 dB
// (the error equals the reference). Identical signals hit the cap and 0.
func TestSpectralMetricsGain(t *testing.T) {
	const n = 1 << 16
	ref := genNoise(n, 0.3, 99)
	deg := make([]float64, n)
	for i := range ref {
		deg[i] = 2 * ref[i]
	}
	bandSNR, lsd := SpectralMetrics(ref, deg, 48000)
	if math.Abs(bandSNR) > 0.05 {
		t.Fatalf("BandSNR = %v, want 0", bandSNR)
	}
	if want := 20 * math.Log10(2); math.Abs(lsd-want) > 0.05 {
		t.Fatalf("LSD = %v, want %v", lsd, want)
	}
	if b, l := SpectralMetrics(ref, ref, 48000); b != SNRCap || l != 0 {
		t.Fatalf("identical: BandSNR=%v LSD=%v, want cap and 0", b, l)
	}
	if _, l := SpectralMetrics(make([]float64, n), make([]float64, n), 48000); !math.IsNaN(l) {
		t.Fatalf("silent LSD = %v, want NaN", l)
	}
}

// TestBandSNRIgnoresHighBand: an error confined above 16 kHz must not lower
// BandSNR while the full-band SNR drops.
func TestBandSNRIgnoresHighBand(t *testing.T) {
	const n = 1 << 16
	ref := genTone(n, 48000, 1000, 0.5)
	hf := genTone(n, 48000, 20000, 0.1)
	deg := addSignals(ref, hf)
	if full := SNR(ref, deg); full > 30 {
		t.Fatalf("full-band SNR = %v, expected the 20 kHz error to register", full)
	}
	bandSNR, _ := SpectralMetrics(ref, deg, 48000)
	if bandSNR < 60 {
		t.Fatalf("BandSNR = %v, want the 20 kHz error excluded (>= 60 dB)", bandSNR)
	}
}

// TestBandwidthTonesTo8k: tones up to 8 kHz only, so the bandwidth must land
// near 8 kHz (plus a few bins of Hann leakage), well below Nyquist.
func TestBandwidthTonesTo8k(t *testing.T) {
	const n = 1 << 16
	var x []float64
	for f := 500.0; f <= 8000; f += 500 {
		tone := genTone(n, 48000, f, 0.05)
		if x == nil {
			x = tone
		} else {
			x = addSignals(x, tone)
		}
	}
	bw := Bandwidth(x, 48000)
	if bw < 7800 || bw > 8300 {
		t.Fatalf("Bandwidth = %v Hz, want about 8000", bw)
	}
	if bw := Bandwidth(make([]float64, n), 48000); bw != 0 {
		t.Fatalf("silent Bandwidth = %v, want 0", bw)
	}
	if bw := Bandwidth(make([]float64, 100), 48000); bw != 0 {
		t.Fatalf("too-short Bandwidth = %v, want 0", bw)
	}
}

// TestPreEchoDetectsAndRanks: silence then a burst is one attack; noise only
// in the 20 ms before the burst raises PreEcho, noise only after it does not.
func TestPreEchoDetectsAndRanks(t *testing.T) {
	const sr = 48000
	const n = sr // 1 s
	ref := make([]float64, n)
	copy(ref[sr/2:], genNoise(2400, 0.8, 3)) // 50 ms burst at 0.5 s

	clean := slices.Clone(ref)
	pre := slices.Clone(ref)
	post := slices.Clone(ref)
	noise := genNoise(960, 0.05, 11) // 20 ms of noise
	copy(pre[sr/2-960:], noise)
	copy(post[sr/2+2400:], noise)

	cleanDB, events := PreEcho(ref, clean, sr)
	if events != 1 {
		t.Fatalf("events = %d, want 1", events)
	}
	if cleanDB > -100 {
		t.Fatalf("clean PreEcho = %v dB, want far below zero", cleanDB)
	}
	preDB, _ := PreEcho(ref, pre, sr)
	postDB, _ := PreEcho(ref, post, sr)
	if preDB <= postDB+20 {
		t.Fatalf("pre-noise %v dB must rank far worse than post-noise %v dB", preDB, postDB)
	}
	// Analytic check of the pre-noise case: error energy 960*0.05^2/3 over
	// attack energy 240*0.8^2/3, about -18 dB, within statistical slack.
	if want := 10 * math.Log10((960*0.05*0.05)/(240*0.8*0.8)); math.Abs(preDB-want) > 1 {
		t.Fatalf("pre-noise PreEcho = %v dB, want about %v", preDB, want)
	}
	if v, ev := PreEcho(make([]float64, n), make([]float64, n), sr); ev != 0 || !math.IsNaN(v) {
		t.Fatalf("silent: events=%d value=%v, want 0 and NaN", ev, v)
	}
}

// TestPreEchoHoldoff: two bursts 30 ms apart count once, two bursts 100 ms
// apart count twice.
func TestPreEchoHoldoff(t *testing.T) {
	const sr = 48000
	mk := func(gapMs int) []float64 {
		x := make([]float64, sr)
		burst := genNoise(240, 0.8, 5) // one 5 ms window
		copy(x[sr/2:], burst)
		copy(x[sr/2+sr*gapMs/1000:], burst)
		return x
	}
	if _, ev := PreEcho(mk(30), mk(30), sr); ev != 1 {
		t.Fatalf("30 ms spacing: events = %d, want 1 (holdoff)", ev)
	}
	if _, ev := PreEcho(mk(100), mk(100), sr); ev != 2 {
		t.Fatalf("100 ms spacing: events = %d, want 2", ev)
	}
}

func TestCompareAveragesChannels(t *testing.T) {
	const n = 1 << 15
	l := genTone(n, 44100, 440, 0.5)
	r := genTone(n, 44100, 660, 0.5)
	degL := addSignals(l, genNoise(n, 0.01, 1))
	m := Compare([][]float64{l, r}, [][]float64{degL, r}, 44100)
	if want := (SNR(l, degL) + SNRCap) / 2; math.Abs(m.SNR-want) > 1e-9 {
		t.Fatalf("Compare SNR = %v, want channel mean %v", m.SNR, want)
	}
	if m.Bandwidth < 600 || m.Bandwidth > 900 {
		t.Fatalf("Bandwidth = %v, want the max over channels near 660 Hz", m.Bandwidth)
	}
	if !math.IsNaN(m.PreEcho) || m.PreEchoN != 0 {
		t.Fatalf("steady tones: PreEcho=%v N=%d, want NaN and 0", m.PreEcho, m.PreEchoN)
	}
}
