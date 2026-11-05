package enc

import (
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// analyzeGranuleGo computes AnalyzeGranule's output with the Go window,
// partialSums and matrixStep steps, the reference for subbandKernel.
func analyzeGranuleGo(fb *Filterbank, in []float64, out *[18][32]float64) {
	var z [512]float64
	var y [64]float64
	for t := range 18 {
		fb.shiftIn(in[t*32 : t*32+32])
		fb.window(&z)
		partialSums(&z, &y)
		matrixStep(&y, &out[t])
	}
}

// fillSubbandInput fills in with samples scaled by scale and, when zeroEvery
// is positive, a negative zero at every zeroEvery-th sample.
func fillSubbandInput(seed *uint64, in []float64, scale float64, zeroEvery int) {
	for i := range in {
		in[i] = (testsignal.LCG(seed) - 0.5) * scale
		if zeroEvery > 0 && i%zeroEvery == 0 {
			in[i] = math.Copysign(0, -1)
		}
	}
}

// checkSubbandGranule feeds the same granule to both filterbanks and requires
// AnalyzeGranule to match the step-by-step Go reference bit for bit.
func checkSubbandGranule(t *testing.T, got, want *Filterbank, in []float64, granule int) {
	t.Helper()
	var gotOut, wantOut [18][32]float64
	got.AnalyzeGranule(in, &gotOut)
	analyzeGranuleGo(want, in, &wantOut)
	for ti := range gotOut {
		for b := range gotOut[ti] {
			if math.Float64bits(gotOut[ti][b]) != math.Float64bits(wantOut[ti][b]) {
				t.Fatalf("granule %d t=%d b=%d: got %v want %v", granule, ti, b, gotOut[ti][b], wantOut[ti][b])
			}
		}
	}
}

// TestSubbandKernelParity feeds consecutive granules of random and full-scale
// input and requires AnalyzeGranule to match the step-by-step Go reference bit
// for bit.
func TestSubbandKernelParity(t *testing.T) {
	var seed uint64 = 31337
	var got, want Filterbank
	in := make([]float64, 576)
	for granule := range 40 {
		zeroEvery := 0
		if granule%9 == 4 {
			zeroEvery = 5
		}
		fillSubbandInput(&seed, in, math.Pow(2, float64(granule%20)-5), zeroEvery)
		checkSubbandGranule(t, &got, &want, in, granule)
	}
}

// FuzzSubbandKernelParity drives the input scale, the negative-zero spacing and
// the number of consecutive granules beyond the fixed sweep, with the samples
// drawn from a seeded LCG.
func FuzzSubbandKernelParity(f *testing.F) {
	f.Add(uint8(5), uint8(5), uint8(3), uint64(1))
	f.Add(uint8(0), uint8(0), uint8(1), uint64(2))
	f.Add(uint8(19), uint8(2), uint8(7), uint64(3))
	f.Fuzz(func(t *testing.T, scaleR, zeroEvery, granules uint8, seed uint64) {
		var got, want Filterbank
		in := make([]float64, 576)
		for granule := range 1 + int(granules%8) {
			fillSubbandInput(&seed, in, math.Pow(2, float64(scaleR%20)-5), int(zeroEvery))
			checkSubbandGranule(t, &got, &want, in, granule)
		}
	})
}
