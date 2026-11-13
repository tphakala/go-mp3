//go:build !noasm

package enc

import "github.com/tphakala/simd/f64"

// The stage tables below hold the twiddles of ffttables.go in the order the
// stages read them. The stage whose blocks are 2*half long reads W_N^(j*step)
// for j in [0, half) with step = N/(2*half); stored contiguously those half
// values start at index half-1, because the stages before it hold
// 1 + 2 + ... + half/2 = half-1 entries. The tables are copies of the
// committed literals built at package init, so no value is computed.
var (
	fftStageRe1024 = func() (t [1023]float64) { fftStageOrder(t[:], fftTwRe1024[:]); return t }()
	fftStageIm1024 = func() (t [1023]float64) { fftStageOrder(t[:], fftTwIm1024[:]); return t }()
	fftStageRe256  = func() (t [255]float64) { fftStageOrder(t[:], fftTwRe256[:]); return t }()
	fftStageIm256  = func() (t [255]float64) { fftStageOrder(t[:], fftTwIm256[:]); return t }()
)

// fftStageOrder copies the twiddle table tw (W_N^k for k in [0, N/2)) into
// dst (N-1 entries) in stage order: dst[half-1+j] = tw[j*(N/2/half)].
func fftStageOrder(dst, tw []float64) {
	for half := 1; half <= len(tw); half <<= 1 {
		step := len(tw) / half
		for j := range half {
			dst[half-1+j] = tw[j*step]
		}
	}
}

// fftStages1024 runs the ten butterfly stages of the 1024-point transform
// over bit-reversed input.
func fftStages1024(re, im *[1024]float64) {
	fftStages(re[:], im[:], fftStageRe1024[:], fftStageIm1024[:])
}

// fftStages256 runs the eight butterfly stages of the 256-point transform
// over bit-reversed input.
func fftStages256(re, im *[256]float64) {
	fftStages(re[:], im[:], fftStageRe256[:], fftStageIm256[:])
}

// fftStages runs the radix-2 DIT stages, one f64.ButterflyComplexStage call
// each, over the stage-ordered twiddles stRe/stIm. Per its documented contract
// (github.com/tphakala/simd v1.16.0, f64/f64.go), for every block start k (a
// multiple of 2*half) and every j in [0, half) the stage computes
//
//	tr := float64(re[l]*wr) - float64(im[l]*wi)
//	ti := float64(re[l]*wi) + float64(im[l]*wr)
//	re[u], re[l] = re[u]+tr, re[u]-tr
//	im[u], im[l] = im[u]+ti, im[u]-ti
//
// with u = k+j, l = u+half and (wr, wi) the j-th twiddle of the stage, every
// product rounded before the add or subtract it feeds, never fused, on every
// path including the one SIMD_DISABLE=all selects. Those are the operands and the
// order of the loop the noasm build runs, and the butterflies of one stage
// touch disjoint elements, so the result has its bits. TestFFTParity pins
// that.
func fftStages(re, im, stRe, stIm []float64) {
	for half := 1; half < len(re); half <<= 1 {
		f64.ButterflyComplexStage(re, im, half, stRe[half-1:2*half-1], stIm[half-1:2*half-1])
	}
}
