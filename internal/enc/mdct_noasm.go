//go:build noasm

package enc

// mdctGranuleWindowed is the shared 36-point forward-MDCT body behind
// MDCTGranule (blockLong, window = &MDCTWindow) and MDCTGranuleBlock's
// blockStart and blockStop cases, parameterized by the window. This build
// links no simd code.
//
// Six spectral lines share each loaded z[n]; every line still has its own
// accumulator, starts at +0.0 and adds its products in ascending n, so the
// result is the one-line-at-a-time loop's. The float64() conversion on each
// product is the barrier that keeps arm64 from fusing it into the add. The
// window products are stored, not added, so they cannot fuse; their wrap is
// kept for uniformity.
func mdctGranuleWindowed(prev, cur *[18][32]float64, xr *[576]float64, window *[36]float64) {
	for b := range 32 {
		var z [36]float64
		for i := range 18 {
			z[i] = float64(prev[i][b] * window[i])
		}
		for i := range 18 {
			z[18+i] = float64(cur[i][b] * window[18+i])
		}
		o := (*[18]float64)(xr[b*18:])
		for k := 0; k < 18; k += 6 {
			c0, c1, c2 := &mdctCos[k], &mdctCos[k+1], &mdctCos[k+2]
			c3, c4, c5 := &mdctCos[k+3], &mdctCos[k+4], &mdctCos[k+5]
			var s0, s1, s2, s3, s4, s5 float64
			for n := range 36 {
				zn := z[n]
				s0 += float64(c0[n] * zn)
				s1 += float64(c1[n] * zn)
				s2 += float64(c2[n] * zn)
				s3 += float64(c3[n] * zn)
				s4 += float64(c4[n] * zn)
				s5 += float64(c5[n] * zn)
			}
			o[k], o[k+1], o[k+2] = s0*mdctScale, s1*mdctScale, s2*mdctScale
			o[k+3], o[k+4], o[k+5] = s3*mdctScale, s4*mdctScale, s5*mdctScale
		}
	}
}
