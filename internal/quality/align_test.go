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
