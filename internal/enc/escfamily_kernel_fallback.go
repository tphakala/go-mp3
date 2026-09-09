//go:build noasm || (!amd64 && !arm64)

package enc

// escFamilyPrefix falls back to the pure-Go fused reference on the noasm build
// and on any architecture without a kernel. Identical result to the AVX2/NEON
// kernels by construction (the differential tests pin this), so encoder output
// is bit-exact across all build paths.
func escFamilyPrefix(prefixCost *[40][32]int32, pb *[40]int, nBands int, ax, ay, base16, base24 *[288]int32) {
	escFamilyPrefixGo(prefixCost, pb, nBands, ax, ay, base16, base24)
}
