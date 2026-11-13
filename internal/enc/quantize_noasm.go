//go:build noasm

package enc

// quantizeGranule is the noasm build's whole-granule quantizer: the pure-Go
// reference quantizeGranuleGo. Like the default build's, it has no caller in
// the encoder. This build links no simd code.
func quantizeGranule(xr *[576]float64, gg int, sf *scfState, lay *bandLayout, ix *[576]int32) {
	quantizeGranuleGo(xr, gg, sf, lay, ix)
}

// quantizeBand is the noasm build's per-band quantizer: quantizeBandGo.
func quantizeBand(dst []int32, src []float64, is float64) {
	quantizeBandGo(dst, src, is)
}
