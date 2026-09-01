package testsignal

import "testing"

// TestClickTrainShape pins the click-train geometry: burst frames are
// nonzero, gap frames are exactly zero, the first burst starts at sample 0,
// and every sample stays within the 0.8 burst amplitude.
func TestClickTrainShape(t *testing.T) {
	x := ClickTrain(8*FrameSize, 4, 1)
	if len(x) != 8*FrameSize {
		t.Fatalf("len = %d, want %d", len(x), 8*FrameSize)
	}
	nonzero := func(lo, hi int) bool {
		for _, v := range x[lo:hi] {
			if v != 0 {
				return true
			}
		}
		return false
	}
	if !nonzero(0, FrameSize) || !nonzero(4*FrameSize, 5*FrameSize) {
		t.Fatal("burst frames 0 and 4 must carry noise")
	}
	if nonzero(FrameSize, 4*FrameSize) || nonzero(5*FrameSize, 8*FrameSize) {
		t.Fatal("gap frames must be exactly silent")
	}
	for _, v := range x {
		if v > 0.8 || v < -0.8 {
			t.Fatalf("sample %v exceeds the 0.8 burst amplitude", v)
		}
	}
}

// TestToneClickShape pins the tone-click geometry: the 576-sample gap before
// each burst is exactly silent, the burst frame is nonzero, the tone region
// is not silent, and everything is clamped to [-1, 1].
func TestToneClickShape(t *testing.T) {
	x := ToneClick(44100, 8*FrameSize, 4, 1)
	if len(x) != 8*FrameSize {
		t.Fatalf("len = %d, want %d", len(x), 8*FrameSize)
	}
	for i := 4*FrameSize - 576; i < 4*FrameSize; i++ {
		if x[i] != 0 {
			t.Fatalf("x[%d] = %v, want silence in the pre-burst gap", i, x[i])
		}
	}
	burstHasNoise := false
	for _, v := range x[4*FrameSize : 5*FrameSize] {
		if v != 0 {
			burstHasNoise = true
		}
	}
	if !burstHasNoise {
		t.Fatal("burst frame 4 must carry noise")
	}
	for i, v := range x {
		if v > 1 || v < -1 {
			t.Fatalf("x[%d] = %v not clamped to [-1, 1]", i, v)
		}
	}
	if x[10] == 0 {
		t.Fatal("tone region must not be silent")
	}
}

// TestClickProgramsDeterministic guards the golden-pinned seeds: two calls
// must be identical sample for sample.
func TestClickProgramsDeterministic(t *testing.T) {
	a, b := ClickTrain(3*FrameSize, 2, 1), ClickTrain(3*FrameSize, 2, 1)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("ClickTrain not deterministic at %d", i)
		}
	}
	c, d := ToneClick(48000, 3*FrameSize, 2, 1), ToneClick(48000, 3*FrameSize, 2, 1)
	for i := range c {
		if c[i] != d[i] {
			t.Fatalf("ToneClick not deterministic at %d", i)
		}
	}
}
