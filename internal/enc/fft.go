package enc

// This file is the deterministic FFT for the psychoacoustic model:
// an iterative in-place radix-2 decimation-in-time transform, driven
// entirely by the literal tables in ffttables.go. Determinism: butterflies
// use only +, -, * with every product feeding a +/- wrapped in float64()
// to block FMA fusion, in a fixed iteration order, so the output is
// bit-identical on every architecture (TestFFTGolden is the cross-arch
// gate). No recursion, no allocation, no scaling: the forward transform is
// X[k] = sum_n x[n] * W_N^(k*n) with W_N = exp(-2*pi*i/N), and callers own
// any 1/N normalization.

// fftForward runs the shared radix-2 DIT flow over one twiddle/bit-reversal
// table set. re and im are the caller's buffers of length n = len(brev);
// twRe/twIm hold W_N^k for k in [0, n/2).
func fftForward(re, im []float64, brev []uint16, twRe, twIm []float64) {
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
			k := 0
			for j := start; j < start+half; j++ {
				l := j + half
				wr, wi := twRe[k], twIm[k]
				tr := float64(re[l]*wr) - float64(im[l]*wi)
				ti := float64(re[l]*wi) + float64(im[l]*wr)
				re[l] = re[j] - tr
				im[l] = im[j] - ti
				re[j] += tr
				im[j] += ti
				k += step
			}
		}
	}
}

// fft1024 computes the in-place forward complex DFT of length 1024:
// X[k] = sum_n x[n] * exp(-2*pi*i*k*n/1024). Input and output are in
// natural order (the bit-reversal permutation is internal).
func fft1024(re, im *[1024]float64) {
	fftForward(re[:], im[:], fftBitrev1024[:], fftTwRe1024[:], fftTwIm1024[:])
}

// fft256 is the 256-point sibling of fft1024.
func fft256(re, im *[256]float64) {
	fftForward(re[:], im[:], fftBitrev256[:], fftTwRe256[:], fftTwIm256[:])
}
