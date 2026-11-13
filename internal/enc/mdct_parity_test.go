package enc

import (
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// refMDCTWindowed is the 36-point forward MDCT written one subband and one
// spectral line at a time: window the 36 samples, then sum each line from
// +0.0 in ascending n with every product rounded before its add, then scale.
// It is the reference mdctGranuleWindowed must match bit for bit in every
// build (simd kernels, simd's portable path, noasm).
func refMDCTWindowed(prev, cur *[18][32]float64, xr *[576]float64, window *[36]float64) {
	for b := range 32 {
		var z [36]float64
		for i := range 18 {
			z[i] = float64(prev[i][b] * window[i])
		}
		for i := range 18 {
			z[18+i] = float64(cur[i][b] * window[18+i])
		}
		for k := range 18 {
			sum := 0.0
			for n := range 36 {
				sum += float64(mdctCos[k][n] * z[n])
			}
			xr[b*18+k] = sum * mdctScale
		}
	}
}

// mdctParityBlocks pairs each block type that runs the 36-point kernel with
// the window MDCTGranuleBlock must apply for it.
var mdctParityBlocks = [...]struct {
	name      string
	blockType int
	window    *[36]float64
}{
	{"long", blockLong, &MDCTWindow},
	{"start", blockStart, &MDCTWindowStart},
	{"stop", blockStop, &MDCTWindowStop},
}

// checkMDCTParity draws a prev and a cur granule and requires
// MDCTGranuleBlock to match the reference for block bi.
func checkMDCTParity(t *testing.T, seed *uint64, scale float64, bi int, nonFinite bool) {
	t.Helper()
	var prev, cur [18][32]float64
	for i := range 18 {
		for b := range 32 {
			prev[i][b] = frontParitySample(seed, i*32+b, scale, nonFinite)
			cur[i][b] = frontParitySample(seed, i*32+b+50, scale, nonFinite)
		}
	}
	blk := &mdctParityBlocks[bi]
	var got, want [576]float64
	MDCTGranuleBlock(&prev, &cur, &got, blk.blockType)
	refMDCTWindowed(&prev, &cur, &want, blk.window)
	for i := range got {
		if !sameFloatBits(got[i], want[i]) {
			t.Fatalf("%s xr[%d] (subband %d, line %d) = %v (bits %x), want %v (bits %x)",
				blk.name, i, i/18, i%18, got[i], math.Float64bits(got[i]), want[i], math.Float64bits(want[i]))
		}
	}
}

// TestMDCTParity requires the long, start and stop block MDCT to match the
// per-subband reference bit for bit across twelve decades of magnitude, with
// signed zeros, subnormals and large values in the input, and again with NaN
// and infinities.
func TestMDCTParity(t *testing.T) {
	var seed uint64 = 11
	for trial := range 96 {
		scale := math.Pow(10, float64(trial%12)-6)
		for bi := range mdctParityBlocks {
			checkMDCTParity(t, &seed, scale, bi, trial/12%2 == 1)
		}
	}
}

// FuzzMDCTParity drives the seed, magnitude, block type and the presence of
// non-finite input beyond the fixed sweep.
func FuzzMDCTParity(f *testing.F) {
	f.Add(uint64(1), uint8(6), uint8(0), false)
	f.Add(uint64(2), uint8(0), uint8(1), false)
	f.Add(uint64(3), uint8(11), uint8(2), true)
	f.Fuzz(func(t *testing.T, seed uint64, scaleR, blockR uint8, nonFinite bool) {
		scale := math.Pow(10, float64(scaleR%12)-6)
		checkMDCTParity(t, &seed, scale, int(blockR)%len(mdctParityBlocks), nonFinite)
	})
}

// BenchmarkMDCTGranule measures the long-block forward MDCT of one granule
// (32 subbands) in isolation. Must report 0 allocs/op.
func BenchmarkMDCTGranule(b *testing.B) {
	var prev, cur [18][32]float64
	seed := uint64(1)
	for i := range 18 {
		for s := range 32 {
			prev[i][s] = testsignal.LCGSigned(&seed)
			cur[i][s] = testsignal.LCGSigned(&seed)
		}
	}
	var xr [576]float64
	b.ReportAllocs()
	for b.Loop() {
		MDCTGranuleBlock(&prev, &cur, &xr, blockLong)
	}
	benchMDCTSink = xr // keep the result live
}

// benchMDCTSink keeps BenchmarkMDCTGranule's output live so the call cannot
// be eliminated.
var benchMDCTSink [576]float64
