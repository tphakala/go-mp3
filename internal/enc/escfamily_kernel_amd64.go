//go:build amd64 && !noasm

package enc

// escFamilyPrefix computes the whole escape-family phase of one
// bigValuesPrefixCost call in a single fused kernel (issue #68). On amd64 it
// uses the fused AVX2 kernel when the CPU has AVX2 (default GOAMD64=v1 does not
// guarantee it, so the check is at runtime, sharing hasAVX2 with the region-cost
// kernel); otherwise it falls back to the pure-Go fused reference. The kernel
// walks the coding bands internally, keeps the two 8-lane accumulators resident,
// and writes each band's cumulative prefix cost straight into prefixCost, so the
// loop-invariant constant vectors load once and the former per-band call
// overhead and Go snapshot loop are gone. The base16/base24 codebook gather
// stays a Go pre-pass in the caller (kept out of the vector path).
func escFamilyPrefix(prefixCost *[40][32]int32, pb *[40]int, nBands int, ax, ay, base16, base24 *[288]int32) {
	if hasAVX2 {
		escFamilyPrefixAVX2(prefixCost, pb, nBands, ax, ay, base16, base24)
	} else {
		escFamilyPrefixGo(prefixCost, pb, nBands, ax, ay, base16, base24)
	}
}

//go:noescape
func escFamilyPrefixAVX2(prefixCost *[40][32]int32, pb *[40]int, nBands int, ax, ay, base16, base24 *[288]int32)
