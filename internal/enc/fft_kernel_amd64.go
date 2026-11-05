//go:build amd64 && !noasm

package enc

// fftKernel1024 runs the 1024-point transform with AVX2 and returns true when
// the CPU has AVX2, else returns false and the caller runs fftForward. The
// result is bit-identical (TestFFTKernelParity).
func fftKernel1024(re, im *[1024]float64) bool {
	if !hasAVX2 {
		return false
	}
	fftBitReverse(re[:], im[:], fftBitrev1024[:])
	fftStagesAVX2(re[:], im[:], fftStageRe1024, fftStageIm1024)
	return true
}

// fftKernel256 is the 256-point sibling of fftKernel1024.
func fftKernel256(re, im *[256]float64) bool {
	if !hasAVX2 {
		return false
	}
	fftBitReverse(re[:], im[:], fftBitrev256[:])
	fftStagesAVX2(re[:], im[:], fftStageRe256, fftStageIm256)
	return true
}

// fftStagesAVX2 runs every butterfly stage, one butterfly per lane. re and im
// have the same length, a power of two of at least 8, and stageRe and stageIm
// are the fftStageTwiddles tables for that length.
//
//go:noescape
func fftStagesAVX2(re, im, stageRe, stageIm []float64)
