//go:build amd64 && !noasm

package enc

// quantizeGranule quantizes four lines per AVX2 iteration when the CPU has
// AVX2 (default GOAMD64=v1 does not guarantee it, so the check is at runtime),
// else runs quantizeGranuleGo. The kernel stores zeros for a group of lines
// whose scaled magnitudes are all below quantZeroMax, which the full sqrt
// chain rounds to 0 as well, and otherwise computes sqrt(t*sqrt(t)) for every
// line of the group, so the result is bit-identical (TestQuantizeGranuleParity).
func quantizeGranule(xr *[576]float64, gg int, sf *scfState, lay *bandLayout, ix *[576]int32) {
	if !hasAVX2 {
		quantizeGranuleGo(xr, gg, sf, lay, ix)
		return
	}
	var steps [39]float64
	quantizeSteps(gg, sf, lay, &steps)
	quantizeAVX2(xr, ix, &steps, &lay.width, lay.nBands, 0.4054, maxQuant, quantZeroMax)
}

// quantizeAVX2 quantizes band by band in quantizeGranuleGo's operation order.
// Every band width is even.
//
//go:noescape
func quantizeAVX2(xr *[576]float64, ix *[576]int32, steps *[39]float64, widths *[39]int, nBands int, rounding, limit, zeroMax float64)
