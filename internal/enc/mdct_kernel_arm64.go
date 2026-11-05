//go:build arm64 && !noasm

package enc

// mdctGranuleWindowed computes the MDCT with two adjacent subbands as two NEON
// lanes. The result is bit-identical to mdctGranuleWindowedGo
// (TestMDCTKernelParity).
func mdctGranuleWindowed(prev, cur *[18][32]float64, xr *[576]float64, window *[36]float64) {
	mdctNEON(prev, cur, xr, window, &mdctCos, mdctScale)
}

// mdctNEON accumulates all coefficients of each pair of subbands in n order
// with eighteen accumulators.
//
//go:noescape
func mdctNEON(prev, cur *[18][32]float64, xr *[576]float64, window *[36]float64, cos *[18][36]float64, scale float64)
