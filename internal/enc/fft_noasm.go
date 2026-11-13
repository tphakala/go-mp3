//go:build noasm

package enc

// fftStages1024 runs the ten butterfly stages of the 1024-point transform
// over bit-reversed input. This build links no simd code.
func fftStages1024(re, im *[1024]float64) {
	fftStages(re[:], im[:], fftTwRe1024[:], fftTwIm1024[:])
}

// fftStages256 runs the eight butterfly stages of the 256-point transform
// over bit-reversed input.
func fftStages256(re, im *[256]float64) {
	fftStages(re[:], im[:], fftTwRe256[:], fftTwIm256[:])
}

// fftStages runs the radix-2 DIT stages over one twiddle table. re and im
// have length n; twRe/twIm hold W_N^k for k in [0, n/2), and the stage with
// blocks of size reads every (n/size)-th entry. The float64() conversion on
// each product is the barrier that keeps arm64 from fusing it into the
// subtraction or addition it feeds.
func fftStages(re, im, twRe, twIm []float64) {
	n := len(re)
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
