package enc

import (
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// refFFT is the radix-2 decimation-in-time transform written one butterfly
// at a time over the committed twiddle tables (W_N^k for k in [0, N/2), read
// with a stride of N/size by the stage whose blocks are size long), every
// product rounded before the add or subtract it feeds. It is the reference
// fft1024 and fft256 must match bit for bit in every build (simd kernels,
// simd's portable path, noasm).
func refFFT(re, im []float64, brev []uint16, twRe, twIm []float64) {
	n := len(re)
	for i := range re {
		if j := int(brev[i]); i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		half := size >> 1
		step := n / size
		for start := 0; start < n; start += size {
			for j := range half {
				u, l := start+j, start+j+half
				wr, wi := twRe[j*step], twIm[j*step]
				tr := float64(re[l]*wr) - float64(im[l]*wi)
				ti := float64(re[l]*wi) + float64(im[l]*wr)
				re[u], re[l] = re[u]+tr, re[u]-tr
				im[u], im[l] = im[u]+ti, im[u]-ti
			}
		}
	}
}

// fftParityKinds is the number of input kinds fftParitySample draws.
const fftParityKinds = 14

// fftParitySample draws input value i of the given kind. Kind 0 is a zero
// of random sign, so a whole input of it makes every output a zero whose
// sign is compared. Kind 1 is a subnormal, so the first stages' products
// round in the subnormal range. Kinds 2 to 13 are frontParitySample at the
// twelve decades from 1e-6 to 1e5, with its signed zeros, subnormals and
// 1e150 planted among ordinary values. Non-finite input is left out:
// frontParitySample's NaN would reach every output of both forms, and the
// comparison treats two NaNs as equal.
func fftParitySample(seed *uint64, i, kind int) float64 {
	switch kind {
	case 0:
		return math.Copysign(0, testsignal.LCGSigned(seed))
	case 1:
		return testsignal.LCGSigned(seed) * 1e-310
	}
	return frontParitySample(seed, i, math.Pow(10, float64(kind)-8), false)
}

// checkFFTParity draws one complex input of the given kind and requires
// fft1024 (or fft256 when short is set) to match refFFT.
func checkFFTParity(t *testing.T, seed *uint64, kind int, short bool) {
	t.Helper()
	n, brev, twRe, twIm := 1024, fftBitrev1024[:], fftTwRe1024[:], fftTwIm1024[:]
	if short {
		n, brev, twRe, twIm = 256, fftBitrev256[:], fftTwRe256[:], fftTwIm256[:]
	}
	gotRe, gotIm := make([]float64, n), make([]float64, n)
	wantRe, wantIm := make([]float64, n), make([]float64, n)
	for i := range n {
		gotRe[i] = fftParitySample(seed, i, kind)
		gotIm[i] = fftParitySample(seed, i+40, kind)
	}
	copy(wantRe, gotRe)
	copy(wantIm, gotIm)
	runFFT(t, gotRe, gotIm)
	refFFT(wantRe, wantIm, brev, twRe, twIm)
	for i := range n {
		if !sameFloatBits(gotRe[i], wantRe[i]) || !sameFloatBits(gotIm[i], wantIm[i]) {
			t.Fatalf("N=%d kind %d bin %d = (%v, %v) (bits %x, %x), want (%v, %v) (bits %x, %x)",
				n, kind, i, gotRe[i], gotIm[i], math.Float64bits(gotRe[i]), math.Float64bits(gotIm[i]),
				wantRe[i], wantIm[i], math.Float64bits(wantRe[i]), math.Float64bits(wantIm[i]))
		}
	}
}

// TestFFTParity requires the 1024- and 256-point transforms to match the
// butterfly-at-a-time reference bit for bit on every input kind of
// fftParitySample: all signed zeros, all subnormals, and twelve decades of
// magnitude with signed zeros, subnormals and large values planted.
func TestFFTParity(t *testing.T) {
	var seed uint64 = 13
	for range 8 {
		for kind := range fftParityKinds {
			checkFFTParity(t, &seed, kind, false)
			checkFFTParity(t, &seed, kind, true)
		}
	}
}

// FuzzFFTParity drives the seed, input kind and transform size beyond the
// fixed sweep.
func FuzzFFTParity(f *testing.F) {
	f.Add(uint64(1), uint8(8), false)
	f.Add(uint64(2), uint8(0), true)
	f.Add(uint64(3), uint8(1), false)
	f.Fuzz(func(t *testing.T, seed uint64, kindR uint8, short bool) {
		checkFFTParity(t, &seed, int(kindR)%fftParityKinds, short)
	})
}
