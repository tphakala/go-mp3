//go:build noasm || (!amd64 && !arm64)

package enc

// quantizeGranule runs the pure-Go quantizer on the noasm build and on any
// architecture without a kernel.
func quantizeGranule(xr *[576]float64, gg int, sf *scfState, lay *bandLayout, ix *[576]int32) {
	quantizeGranuleGo(xr, gg, sf, lay, ix)
}
