//go:build arm64 && !noasm

package enc

// quantizeGranule quantizes two lines per NEON iteration (NEON is baseline on
// arm64, so no runtime feature check). The kernel stores zeros for a pair of
// lines whose scaled magnitudes are both below quantZeroMax, which the full
// sqrt chain rounds to 0 as well. When only one of the two is below, it stores
// that zero and computes the other line with scalar square roots; otherwise it
// computes sqrt(t*sqrt(t)) for both lines with vector square roots. The result
// is bit-identical either way (TestQuantizeGranuleParity). The skips keep
// sparse spectra cheap on cores whose two-lane vector square root costs as
// much as two scalar ones, such as Neoverse N2.
func quantizeGranule(xr *[576]float64, gg int, sf *scfState, lay *bandLayout, ix *[576]int32) {
	var steps [39]float64
	quantizeSteps(gg, sf, lay, &steps)
	quantizeNEON(xr, ix, &steps, &lay.width, lay.nBands, 0.4054, maxQuant, quantZeroMax)
}

// quantizeNEON quantizes band by band in quantizeGranuleGo's operation order.
// Every band width is even.
//
//go:noescape
func quantizeNEON(xr *[576]float64, ix *[576]int32, steps *[39]float64, widths *[39]int, nBands int, rounding, limit, zeroMax float64)
