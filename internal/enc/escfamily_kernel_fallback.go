//go:build noasm || (!amd64 && !arm64)

package enc

// escFamilyAccum falls back to the pure-Go reference on the noasm build and on
// any architecture without a fused kernel. Identical result to the AVX2/NEON
// kernels by construction (the differential tests pin this), so encoder output
// is bit-exact across all build paths.
func escFamilyAccum(acc16, acc24 *[8]int32, ax, ay, base16, base24 []int32) {
	escFamilyAccumGo(acc16, acc24, ax, ay, base16, base24)
}
