//go:build (amd64 || arm64) && !noasm

package enc

// fftStageRe1024 and fftStageIm1024 hold the 1024-point twiddles in the order
// fftForward's stages use them: the stage with half-size h occupies indices
// [h-1, 2h-1), so each stage reads them contiguously.
var fftStageRe1024, fftStageIm1024 = fftStageTwiddles(fftTwRe1024[:], fftTwIm1024[:])

// fftStageRe256 and fftStageIm256 are the 256-point stage twiddles.
var fftStageRe256, fftStageIm256 = fftStageTwiddles(fftTwRe256[:], fftTwIm256[:])

// fftStageTwiddles gathers from the W_N^k tables the twiddle each butterfly of
// each fftForward stage uses, stage by stage.
func fftStageTwiddles(twRe, twIm []float64) (re, im []float64) {
	n := 2 * len(twRe)
	for size := 2; size <= n; size <<= 1 {
		step := n / size
		for m := range size / 2 {
			re = append(re, twRe[m*step])
			im = append(im, twIm[m*step])
		}
	}
	return re, im
}
