package quality

import "testing"

func TestAlignLagFindsDelay(t *testing.T) {
	const n = 1 << 15
	ref := genNoise(n, 0.5, 42)
	for _, lag := range []int{0, 1, 1057, 4000} {
		deg := make([]float64, n+lag)
		copy(deg[lag:], ref)
		if got := AlignLag(ref, deg, -128, 4096); got != lag {
			t.Fatalf("lag %d: AlignLag = %d", lag, got)
		}
	}
}

func TestAlignLagNegative(t *testing.T) {
	const n = 1 << 15
	ref := genNoise(n, 0.5, 43)
	deg := ref[17:] // deg leads ref by 17 samples
	if got := AlignLag(ref, deg, -128, 4096); got != -17 {
		t.Fatalf("AlignLag = %d, want -17", got)
	}
}

// TestAlignLagShortDeg: a deg shorter than the search window must not panic
// and must still find the lag from the overlapping samples.
func TestAlignLagShortDeg(t *testing.T) {
	ref := genNoise(3000, 0.5, 44)
	deg := make([]float64, 2500)
	copy(deg[100:], ref)
	if got := AlignLag(ref, deg, -128, 4096); got != 100 {
		t.Fatalf("AlignLag = %d, want 100", got)
	}
}

// TestAlignLagPeriodicProgram is the regression gate for the periodic-peak
// ambiguity. multitone is a 440 Hz fundamental plus two harmonics, so its
// correlation peaks repeat every 100.2 samples at 44.1 kHz. An unnormalized
// argmax over a longer-than-alignSamples reference then prefers whichever
// alias lands its window on the loud part of the amplitude envelope, and
// picks 3262, the true lag plus exactly 22 periods. Dividing by both windows'
// energies is what resolves it, and on the real encode of this program the
// difference is the whole measurement: 22.21 dB SNR at 3262 against 74.34 dB
// at the true lag.
func TestAlignLagPeriodicProgram(t *testing.T) {
	const sr, lag = 44100, 1057
	p, ok := ProgramByName("multitone")
	if !ok {
		t.Fatal("multitone program missing")
	}
	// Longer than alignSamples, so the correlation window is a strict prefix
	// and the aliases are not resolved by the window running out.
	ref := p.Gen(sr, 6*sr)[0]
	deg := make([]float64, len(ref)+lag)
	copy(deg[lag:], ref)
	if got := AlignLag(ref, deg, -128, 4096); got != lag {
		t.Fatalf("AlignLag on a periodic program = %d, want %d", got, lag)
	}
}

// TestAlignLagUncorrelated: a silent or anti-correlated pair has no positive
// peak, so the lag must be 0 rather than an arbitrary end of the search range
// (which would silently trim real samples off the reference).
func TestAlignLagUncorrelated(t *testing.T) {
	const n = 1 << 14
	zeros := make([]float64, n)
	if got := AlignLag(zeros, zeros, -128, 4096); got != 0 {
		t.Fatalf("silent pair: AlignLag = %d, want 0", got)
	}
	sig := genNoise(n, 0.5, 45)
	if got := AlignLag(sig, zeros, -128, 4096); got != 0 {
		t.Fatalf("silent deg: AlignLag = %d, want 0", got)
	}
	if got := AlignLag(sig, sig, -128, 4096); got != 0 {
		t.Fatalf("identical pair: AlignLag = %d, want 0", got)
	}
	if got := AlignLag(nil, sig, -128, 4096); got != 0 {
		t.Fatalf("empty ref: AlignLag = %d, want 0", got)
	}
}
