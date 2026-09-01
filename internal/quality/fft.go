package quality

import "math"

// nextPow2 returns the smallest power of two >= n (and 1 for n <= 1).
func nextPow2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// hannWindow returns the periodic Hann window of length n:
// w[i] = 0.5 - 0.5*cos(2*pi*i/n).
func hannWindow(n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		w[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n))
	}
	return w
}

// fft computes the in-place forward radix-2 complex FFT of (re, im), whose
// length must be a power of two (callers pad with nextPow2). No scaling is
// applied: X[k] = sum_i x[i] * exp(-2*pi*j*k*i/N).
func fft(re, im []float64) {
	n := len(re)
	if n != len(im) || n == 0 || n&(n-1) != 0 {
		panic("quality: fft length must be a power of two and re/im equal length")
	}
	// Bit-reversal permutation.
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	// Iterative Cooley-Tukey butterflies.
	for size := 2; size <= n; size <<= 1 {
		half := size >> 1
		step := -2 * math.Pi / float64(size)
		for start := 0; start < n; start += size {
			for k := range half {
				wr, wi := math.Cos(step*float64(k)), math.Sin(step*float64(k))
				a, b := start+k, start+k+half
				tr := re[b]*wr - im[b]*wi
				ti := re[b]*wi + im[b]*wr
				re[b], im[b] = re[a]-tr, im[a]-ti
				re[a], im[a] = re[a]+tr, im[a]+ti
			}
		}
	}
}
