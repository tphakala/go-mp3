//go:build !noasm

package enc

import "github.com/tphakala/simd/f64"

// mdctGranuleWindowed is the shared 36-point forward-MDCT body behind
// MDCTGranule (blockLong, window = &MDCTWindow) and MDCTGranuleBlock's
// blockStart and blockStop cases, parameterized by the window.
//
// It computes one spectral line index k for all 32 subbands per f64.VecMat
// call. z holds the windowed samples time-major, z[n*32+b] = sample n of
// subband b, which is the layout prev and cur already have, so the 32 values
// that meet one cosine are adjacent. With x = mdctCos[k] (36 entries) and
// stride 32, VecMat gives
//
//	s[b] = sum over n of float64(mdctCos[k][n] * z[n*32+b])
//
// from +0.0 in ascending n, never fused (its documented contract,
// github.com/tphakala/simd v1.16.0, f64/vecmat.go): the same products in the
// same order as the per-subband loop the noasm build runs, hence the same
// bits. TestMDCTParity pins that. 18 calls of 32 columns measured faster
// than 32 calls of 18 columns (BenchmarkMDCTGranule).
//
// The window products are stored, not added, so they cannot fuse; the
// float64() wrap is kept for uniformity. The scale is a lone rounded product.
func mdctGranuleWindowed(prev, cur *[18][32]float64, xr *[576]float64, window *[36]float64) {
	var z [36 * 32]float64
	for i := range 18 {
		w0, w1 := window[i], window[18+i]
		z0 := (*[32]float64)(z[i*32:])
		z1 := (*[32]float64)(z[(18+i)*32:])
		for b := range 32 {
			z0[b] = float64(prev[i][b] * w0)
			z1[b] = float64(cur[i][b] * w1)
		}
	}
	var s [32]float64
	for k := range 18 {
		f64.VecMat(s[:], mdctCos[k][:], z[:], 32)
		for b := range 32 {
			xr[b*18+k] = s[b] * mdctScale
		}
	}
}
