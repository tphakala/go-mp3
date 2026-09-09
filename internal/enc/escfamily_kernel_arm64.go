//go:build arm64 && !noasm

package enc

// escFamilyPrefix computes the whole escape-family phase of one
// bigValuesPrefixCost call in a single fused kernel (issue #68). On arm64 it
// uses the fused NEON kernel (NEON is baseline on arm64, so no runtime feature
// check). The kernel walks the coding bands internally, keeps the two 8-lane
// accumulators resident, and writes each band's cumulative prefix cost straight
// into prefixCost, so the former per-band call overhead and Go snapshot loop are
// gone. The base16/base24 codebook gather stays a Go pre-pass in the caller.
func escFamilyPrefix(prefixCost *[40][32]int32, pb *[40]int, nBands int, ax, ay, base16, base24 *[288]int32) {
	escFamilyPrefixNEON(prefixCost, pb, nBands, ax, ay, base16, base24)
}

//go:noescape
func escFamilyPrefixNEON(prefixCost *[40][32]int32, pb *[40]int, nBands int, ax, ay, base16, base24 *[288]int32)
