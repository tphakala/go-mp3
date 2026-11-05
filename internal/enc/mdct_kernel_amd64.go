//go:build amd64 && !noasm

package enc

// mdctGranuleWindowed computes the MDCT with four adjacent subbands as four
// AVX2 lanes when the CPU has AVX2, else runs mdctGranuleWindowedGo. The result
// is bit-identical (TestMDCTKernelParity).
func mdctGranuleWindowed(prev, cur *[18][32]float64, xr *[576]float64, window *[36]float64) {
	if !hasAVX2 {
		mdctGranuleWindowedGo(prev, cur, xr, window)
		return
	}
	mdctAVX2(prev, cur, xr, window, &mdctCos, mdctScale)
}

// mdctAVX2 windows each group of four subbands, then accumulates the 18
// coefficients in n order with six accumulators.
//
//go:noescape
func mdctAVX2(prev, cur *[18][32]float64, xr *[576]float64, window *[36]float64, cos *[18][36]float64, scale float64)
