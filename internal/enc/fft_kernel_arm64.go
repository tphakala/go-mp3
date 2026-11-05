//go:build arm64 && !noasm

package enc

// fftKernel1024 runs the 1024-point transform with NEON and returns true. The
// result is bit-identical to fftForward (TestFFTKernelParity).
func fftKernel1024(re, im *[1024]float64) bool {
	fftBitReverse(re[:], im[:], fftBitrev1024[:])
	fftStagesNEON(re[:], im[:], fftStageRe1024, fftStageIm1024)
	return true
}

// fftKernel256 is the 256-point sibling of fftKernel1024.
func fftKernel256(re, im *[256]float64) bool {
	fftBitReverse(re[:], im[:], fftBitrev256[:])
	fftStagesNEON(re[:], im[:], fftStageRe256, fftStageIm256)
	return true
}

// fftStagesNEON runs every butterfly stage, one butterfly per lane. re and im
// have the same length, a power of two of at least 4, and stageRe and stageIm
// are the fftStageTwiddles tables for that length.
//
//go:noescape
func fftStagesNEON(re, im, stageRe, stageIm []float64)
