//go:build noasm || (!amd64 && !arm64)

package enc

// subMinReduce falls back to the pure-Go reference on the noasm build and on any
// architecture without a fused kernel. Same result as the AVX2/NEON kernels by
// construction (signed min has no accumulation order), so the encoder output is
// identical across all three.
func subMinReduce(a, b []int32) int32 {
	return subMinReduceGo(a, b)
}
