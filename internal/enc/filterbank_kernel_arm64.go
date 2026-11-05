//go:build arm64 && !noasm

package enc

// subbandKernel runs one window, partialSums and matrixStep step with NEON and
// returns true. The result is bit-identical to the Go steps
// (TestSubbandKernelParity).
func subbandKernel(x *[512]float64, out *[32]float64) bool {
	subbandNEON(x, &fbWindow, &fbMatrixT, out)
	return true
}

// subbandNEON computes the 64 partial sums two lanes at a time, then the 32
// subbands in two halves of eight accumulators.
//
//go:noescape
func subbandNEON(x, window *[512]float64, matrixT *[64][32]float64, out *[32]float64)
