package enc

import (
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// refFilterbank is the analysis filterbank written as the ISO C.1.3 flow
// chart, one step and one output at a time: a 512-sample register shifted by
// 32 per step, the window, the partial sums and the matrixing, each sum from
// +0.0 in ascending index order with every product rounded before its add.
// It is the reference Filterbank.AnalyzeGranule must match bit for bit in
// every build (simd kernels, simd's portable path, noasm).
type refFilterbank struct {
	x [512]float64 // x[0] newest
}

func (r *refFilterbank) analyzeGranule(in []float64, out *[18][32]float64) {
	var z [512]float64
	var y [64]float64
	for t := range 18 {
		copy(r.x[32:], r.x[:480])
		for k := range 32 {
			r.x[31-k] = in[t*32+k]
		}
		for i := range 512 {
			z[i] = float64(r.x[i] * fbWindow[i])
		}
		for j := range 64 {
			sum := 0.0
			for k := range 8 {
				sum += z[j+k*64]
			}
			y[j] = sum
		}
		for b := range 32 {
			sum := 0.0
			for j := range 64 {
				sum += float64(fbMatrix[b][j] * y[j])
			}
			out[t][b] = sum
		}
	}
}

// frontParitySample draws one input value in (-scale, scale) and plants
// special values at fixed spacings of i: signed zeros, subnormals and a
// large magnitude, plus NaN and infinities when nonFinite is set.
func frontParitySample(seed *uint64, i int, scale float64, nonFinite bool) float64 {
	v := testsignal.LCGSigned(seed) * scale
	switch i % 97 {
	case 3:
		v = math.Copysign(0, -1)
	case 5:
		v = 0
	case 23:
		v = math.Copysign(5e-324, v)
	case 29:
		v = math.Copysign(1e-310, v)
	case 31:
		v = math.Copysign(1e150, v)
	}
	if nonFinite {
		switch i % 389 {
		case 11:
			v = math.NaN()
		case 17:
			v = math.Inf(1)
		case 19:
			v = math.Inf(-1)
		}
	}
	return v
}

// sameFloatBits reports whether got and want are the same float64 bit for
// bit, or both NaN. A NaN's sign and payload are not compared: simd leaves
// them unspecified, and no encoder code reads them.
func sameFloatBits(got, want float64) bool {
	return math.Float64bits(got) == math.Float64bits(want) || (math.IsNaN(got) && math.IsNaN(want))
}

// checkFilterbankParity runs granules granules through a fresh Filterbank
// and a fresh refFilterbank and requires every subband sample to match. More
// than one granule is what exercises the history the register carries from
// one AnalyzeGranule call to the next.
func checkFilterbankParity(t *testing.T, seed *uint64, scale float64, granules int, nonFinite bool) {
	t.Helper()
	var fb Filterbank
	var ref refFilterbank
	in := make([]float64, 576)
	var got, want [18][32]float64
	for g := range granules {
		for i := range in {
			in[i] = frontParitySample(seed, i+g, scale, nonFinite)
		}
		fb.AnalyzeGranule(in, &got)
		ref.analyzeGranule(in, &want)
		for tt := range 18 {
			for b := range 32 {
				if !sameFloatBits(got[tt][b], want[tt][b]) {
					t.Fatalf("granule %d out[%d][%d] = %v (bits %x), want %v (bits %x)",
						g, tt, b, got[tt][b], math.Float64bits(got[tt][b]), want[tt][b], math.Float64bits(want[tt][b]))
				}
			}
		}
	}
}

// TestFilterbankParity requires Filterbank.AnalyzeGranule to match the
// step-by-step reference bit for bit over runs of granules, across twelve
// decades of magnitude, with signed zeros, subnormals and large values in
// the input, and again with NaN and infinities.
func TestFilterbankParity(t *testing.T) {
	var seed uint64 = 7
	for trial := range 48 {
		scale := math.Pow(10, float64(trial%12)-6)
		checkFilterbankParity(t, &seed, scale, 6, trial%4 == 3)
	}
}

// FuzzFilterbankParity drives the seed, magnitude, run length and the
// presence of non-finite input beyond the fixed sweep.
func FuzzFilterbankParity(f *testing.F) {
	f.Add(uint64(1), uint8(6), uint8(1), false)
	f.Add(uint64(2), uint8(0), uint8(3), false)
	f.Add(uint64(3), uint8(11), uint8(2), true)
	f.Fuzz(func(t *testing.T, seed uint64, scaleR, granulesR uint8, nonFinite bool) {
		scale := math.Pow(10, float64(scaleR%12)-6)
		checkFilterbankParity(t, &seed, scale, 1+int(granulesR%4), nonFinite)
	})
}
