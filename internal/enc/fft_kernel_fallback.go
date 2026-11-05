//go:build noasm || (!amd64 && !arm64)

package enc

// fftKernel1024 returns false on the noasm build and on any architecture
// without a kernel, so the caller runs fftForward.
func fftKernel1024(re, im *[1024]float64) bool {
	return false
}

// fftKernel256 is the 256-point sibling of fftKernel1024.
func fftKernel256(re, im *[256]float64) bool {
	return false
}
