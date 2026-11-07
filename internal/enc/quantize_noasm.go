//go:build noasm

package enc

// quantizeGranule is the noasm build's quantizer: the pure-Go reference
// quantizeGranuleGo. This build links no simd code.
func quantizeGranule(xr *[576]float64, gg int, sf *scfState, lay *bandLayout, ix *[576]int32) {
	quantizeGranuleGo(xr, gg, sf, lay, ix)
}
