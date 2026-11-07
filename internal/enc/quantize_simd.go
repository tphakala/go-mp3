//go:build !noasm

package enc

import "github.com/tphakala/simd/f64"

// quantizeGranule is the default build's quantizer. It computes exactly
// quantizeGranuleGo's result through one f64.QuantizePow34Bands call, because
// simd performs the same operations in the same order:
//   - the same five separately rounded float64 operations, never fused;
//   - the maxQuant clamp compared in float64 before the int32 conversion;
//   - NaN -> +maxQuant, +-Inf -> +-maxQuant, +-0 -> 0, as in the reference;
//   - the zero skip uses simd's own proven bound in place of quantZeroMax and
//     never changes a result.
//
// One call per granule amortizes the wrapper's validation. scales stays on the
// stack because simd dispatches with a direct call; the encoder alloc tests
// pin that. TestQuantizeGranuleParity and FuzzQuantizeGranuleParity gate the
// result against quantizeGranuleGo.
func quantizeGranule(xr *[576]float64, gg int, sf *scfState, lay *bandLayout, ix *[576]int32) {
	var scales [39]float64
	for sfb := range lay.nBands {
		scales[sfb] = stepQ((quantGainBase - gg) + sf.bandExtraQuarters(sfb, lay))
	}
	f64.QuantizePow34Bands(ix[:], xr[:], lay.width[:lay.nBands], scales[:lay.nBands], quantRounding, maxQuant)
}
