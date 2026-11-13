package enc

// This file is the deterministic FFT for the psychoacoustic model:
// an iterative in-place radix-2 decimation-in-time transform, driven
// entirely by the literal tables in ffttables.go. Determinism: butterflies
// use only +, -, * with every product feeding a +/- rounded to float64
// before it, never fused into an FMA, in a fixed iteration order, so the
// output is bit-identical on every architecture (TestFFTGolden is the
// cross-arch gate). No recursion, no allocation, no scaling: the forward
// transform is X[k] = sum_n x[n] * W_N^(k*n) with W_N = exp(-2*pi*i/N), and
// callers own any 1/N normalization.
//
// The butterfly stages are build specific (fft_simd.go, fft_noasm.go); both
// forms compute every butterfly with the same operands in the same order.
// TestFFTParity pins that against a reference in the test file.

// fftBitReverse applies the radix-2 DIT input permutation in place: element
// i trades places with element brev[i]. re and im have length len(brev).
func fftBitReverse(re, im []float64, brev []uint16) {
	for i := range re {
		if j := int(brev[i]); i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
}

// fft1024 computes the in-place forward complex DFT of length 1024:
// X[k] = sum_n x[n] * exp(-2*pi*i*k*n/1024). Input and output are in
// natural order (the bit-reversal permutation is internal).
func fft1024(re, im *[1024]float64) {
	fftBitReverse(re[:], im[:], fftBitrev1024[:])
	fftStages1024(re, im)
}

// fft256 is the 256-point sibling of fft1024.
func fft256(re, im *[256]float64) {
	fftBitReverse(re[:], im[:], fftBitrev256[:])
	fftStages256(re, im)
}
