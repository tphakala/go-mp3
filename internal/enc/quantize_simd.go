//go:build !noasm

package enc

import "github.com/tphakala/simd/f64"

// quantizeGranule is the default build's whole-granule quantizer. It has no
// caller in the encoder, which quantizes one band at a time through
// quantizeBand below (quantCache.ensure, gaincache.go); the parity tests and
// the linear rate-loop oracle in the tests use it. It computes exactly
// quantizeGranuleGo's result through one f64.QuantizePow34Bands call, because
// simd performs the same operations in the same order:
//   - the same five separately rounded float64 operations, never fused;
//   - the maxQuant clamp compared in float64 before the int32 conversion;
//   - NaN -> +maxQuant, +-Inf -> +-maxQuant, +-0 -> 0, as in the reference;
//   - the zero skip uses simd's own proven bound in place of quantZeroMax and
//     never changes a result.
//
// One call per granule amortizes the wrapper's validation.
// TestQuantizeGranuleParity and FuzzQuantizeGranuleParity gate the result
// against quantizeGranuleGo.
func quantizeGranule(xr *[576]float64, gg int, sf *scfState, lay *bandLayout, ix *[576]int32) {
	var scales [39]float64
	for sfb := range lay.nBands {
		scales[sfb] = stepQ((quantGainBase - gg) + sf.bandExtraQuarters(sfb, lay))
	}
	f64.QuantizePow34Bands(ix[:], xr[:], lay.width[:lay.nBands], scales[:lay.nBands], quantRounding, maxQuant)
}

// quantizeBand quantizes one band with scale is, the per-band unit the quantCache
// fills on a miss. It is a one-element QuantizePow34Bands call, which computes
// the same lines as the whole-granule call above because simd keeps no state
// across bands (its zero skip depends only on the rounding and on every scale
// being non-negative). TestQuantizeGranuleParity assembles granules from this
// and compares them with quantizeGranuleGo.
func quantizeBand(dst []int32, src []float64, is float64) {
	widths := [1]int{len(src)}
	scales := [1]float64{is}
	f64.QuantizePow34Bands(dst, src, widths[:], scales[:], quantRounding, maxQuant)
}
