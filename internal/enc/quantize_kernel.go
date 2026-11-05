//go:build (amd64 || arm64) && !noasm

package enc

// quantizeSteps writes each coding band's quantizer step, the same stepQ
// argument quantizeGranuleGo hoists per band, so the kernels can walk the
// bands without calling back into Go.
func quantizeSteps(gg int, sf *scfState, lay *bandLayout, steps *[39]float64) {
	for sfb := range lay.nBands {
		steps[sfb] = stepQ((quantGainBase - gg) + sf.bandExtraQuarters(sfb, lay))
	}
}
