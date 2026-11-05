//go:build amd64 && !noasm

package enc

// subbandKernel runs one window, partialSums and matrixStep step with AVX2 and
// returns true when the CPU has AVX2, else returns false and the caller runs
// the Go steps. The result is bit-identical to them (TestSubbandKernelParity).
func subbandKernel(x *[512]float64, out *[32]float64) bool {
	if !hasAVX2 {
		return false
	}
	subbandAVX2(x, &fbWindow, &fbMatrixT, out)
	return true
}

// subbandAVX2 computes the 64 partial sums four lanes at a time, then the 32
// subbands with eight accumulators.
//
//go:noescape
func subbandAVX2(x, window *[512]float64, matrixT *[64][32]float64, out *[32]float64)
