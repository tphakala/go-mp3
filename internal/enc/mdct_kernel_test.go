package enc

import (
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// mdctParityWindows are the three 36-point windows mdctGranuleWindowed takes.
var mdctParityWindows = []*[36]float64{&MDCTWindow, &MDCTWindowStart, &MDCTWindowStop}

// fillMDCTInput fills prev and cur with subband samples scaled by scale and a
// negative zero in cur wherever (i+b+zeroPhase)%23 == 0.
func fillMDCTInput(seed *uint64, prev, cur *[18][32]float64, scale float64, zeroPhase int) {
	for i := range 18 {
		for b := range 32 {
			prev[i][b] = (testsignal.LCG(seed) - 0.5) * scale
			cur[i][b] = (testsignal.LCG(seed) - 0.5) * scale
			if (i+b+zeroPhase)%23 == 0 {
				cur[i][b] = math.Copysign(0, -1)
			}
		}
	}
}

// checkMDCT requires mdctGranuleWindowed to match mdctGranuleWindowedGo bit for
// bit under window w.
func checkMDCT(t *testing.T, prev, cur *[18][32]float64, w int) {
	t.Helper()
	var got, want [576]float64
	mdctGranuleWindowed(prev, cur, &got, mdctParityWindows[w])
	mdctGranuleWindowedGo(prev, cur, &want, mdctParityWindows[w])
	for i := range got {
		if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
			t.Fatalf("window %d line %d: got %v want %v", w, i, got[i], want[i])
		}
	}
}

// TestMDCTKernelParity runs the three 36-point windows over random subband
// samples and requires mdctGranuleWindowed to match mdctGranuleWindowedGo bit
// for bit.
func TestMDCTKernelParity(t *testing.T) {
	var seed uint64 = 2718
	for trial := range 30 {
		var prev, cur [18][32]float64
		fillMDCTInput(&seed, &prev, &cur, math.Pow(2, float64(trial%16)-4), trial)
		for w := range mdctParityWindows {
			checkMDCT(t, &prev, &cur, w)
		}
	}
}

// FuzzMDCTKernelParity drives the window, the input scale and the
// negative-zero phase beyond the fixed sweep, with the samples drawn from a
// seeded LCG.
func FuzzMDCTKernelParity(f *testing.F) {
	f.Add(uint8(0), uint8(4), uint8(0), uint64(1))
	f.Add(uint8(1), uint8(15), uint8(7), uint64(2))
	f.Add(uint8(2), uint8(0), uint8(22), uint64(3))
	f.Fuzz(func(t *testing.T, windowR, scaleR, zeroPhase uint8, seed uint64) {
		var prev, cur [18][32]float64
		fillMDCTInput(&seed, &prev, &cur, math.Pow(2, float64(scaleR%16)-4), int(zeroPhase))
		checkMDCT(t, &prev, &cur, int(windowR)%len(mdctParityWindows))
	})
}
