package enc

import (
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// fillFFTInput fills re (and im when complexInput) with samples scaled by
// scale.
func fillFFTInput(seed *uint64, re, im *[1024]float64, scale float64, complexInput bool) {
	for i := range re {
		re[i] = (testsignal.LCG(seed) - 0.5) * scale
		im[i] = 0
		if complexInput {
			im[i] = (testsignal.LCG(seed) - 0.5) * scale
		}
	}
}

// checkFFT runs re and im in place through fft1024, and two quarters of the
// result through fft256, and requires both to match fftForward bit for bit.
func checkFFT(t *testing.T, re, im *[1024]float64) {
	t.Helper()
	wantRe, wantIm := *re, *im
	fft1024(re, im)
	fftForward(wantRe[:], wantIm[:], fftBitrev1024[:], fftTwRe1024[:], fftTwIm1024[:])
	for i := range re {
		if math.Float64bits(re[i]) != math.Float64bits(wantRe[i]) || math.Float64bits(im[i]) != math.Float64bits(wantIm[i]) {
			t.Fatalf("1024 bin %d: got %v%+vi want %v%+vi", i, re[i], im[i], wantRe[i], wantIm[i])
		}
	}
	var re2, im2 [256]float64
	copy(re2[:], re[:256])
	copy(im2[:], im[256:512])
	wantRe2, wantIm2 := re2, im2
	fft256(&re2, &im2)
	fftForward(wantRe2[:], wantIm2[:], fftBitrev256[:], fftTwRe256[:], fftTwIm256[:])
	for i := range re2 {
		if math.Float64bits(re2[i]) != math.Float64bits(wantRe2[i]) || math.Float64bits(im2[i]) != math.Float64bits(wantIm2[i]) {
			t.Fatalf("256 bin %d: got %v%+vi want %v%+vi", i, re2[i], im2[i], wantRe2[i], wantIm2[i])
		}
	}
}

// TestFFTKernelParity runs random complex input through fft1024 and fft256 and
// requires them to match fftForward bit for bit.
func TestFFTKernelParity(t *testing.T) {
	var seed uint64 = 1618
	for trial := range 20 {
		var re, im [1024]float64
		fillFFTInput(&seed, &re, &im, math.Pow(2, float64(trial)-6), trial%2 == 1)
		checkFFT(t, &re, &im)
	}
}

// FuzzFFTKernelParity drives the input scale and whether the imaginary part is
// populated beyond the fixed sweep, with the samples drawn from a seeded LCG.
func FuzzFFTKernelParity(f *testing.F) {
	f.Add(uint8(6), false, uint64(1))
	f.Add(uint8(0), true, uint64(2))
	f.Add(uint8(40), true, uint64(3))
	f.Fuzz(func(t *testing.T, scaleR uint8, complexInput bool, seed uint64) {
		var re, im [1024]float64
		fillFFTInput(&seed, &re, &im, math.Pow(2, float64(scaleR%48)-24), complexInput)
		checkFFT(t, &re, &im)
	})
}
