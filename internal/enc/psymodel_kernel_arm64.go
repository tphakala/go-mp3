//go:build arm64 && !noasm

package enc

// unpredictability fills r2 and cw with the NEON kernel (NEON is baseline on
// arm64, so no runtime feature check). Each lane is one bin and repeats
// unpredictabilityGo's operations in its order (every product, quotient and
// square root separately rounded), so the result is bit-identical.
func (p *PsyModel) unpredictability() {
	unpredNEON(&p.fftRe, &p.fftIm, &p.prevRe, &p.prevIm, &p.r2, &p.cw)
}

// maskingThresholds fills ecb, cbs and nb with the NEON kernel. Each lane is
// one partition: the spreading sums run in maskingThresholdsGo's bb order and
// the tonality, required SNR, pre-echo cap and floor repeat its operations,
// with plog and pexp2 evaluated lane-wise by the same reductions and Horner
// orders as detmath.go.
func (p *PsyModel) maskingThresholds() {
	tab := p.tab
	thresholdsNEON(&p.e, &p.ct, &p.ecb, &p.cbs, &p.nb, &p.nbPrev, &psySprdT[p.srIndex], &tab.norm, &tab.qthr, tab.nParts, psyTmnDB, psyNmtDB, psyRpelev2)
}

// shortThresholds fills nbS with the NEON kernel. tab must be
// psyShortTables[p.srIndex], as it is for every caller.
func (p *PsyModel) shortThresholds(tab *psyShortTable) {
	shortThresholdsNEON(&p.eS, &p.nbS, &psySprdTS[p.srIndex], &tab.norm, &tab.qthr, tab.nParts, psyShortBC)
}

// plogs sets dst[i] = plog(src[i]) for positive finite src, two lanes at a
// time with the NEON kernel when len(src) >= 2, else with plogsGo. A
// non-positive or NaN input leaves an unspecified value.
func plogs(dst, src []float64) {
	if len(src) >= 2 {
		plogsNEON(&dst[0], &src[0], len(src))
		return
	}
	plogsGo(dst, src)
}

// unpredNEON computes two bins per iteration, the last iteration overlapping
// the previous one so all 513 bins are covered.
//
//go:noescape
func unpredNEON(re, im *[1024]float64, prevRe, prevIm *[2][513]float64, r2, cw *[513]float64)

// thresholdsNEON spreads eight partitions per pass and derives the thresholds
// two partitions at a time, each with a last block overlapping the previous
// one; n must be at least 8. tmn, nmt and rpelev2 are psyTmnDB, psyNmtDB and
// psyRpelev2, passed in like shortThresholdsNEON's bc so the kernel keeps no
// copy of the model parameters.
//
//go:noescape
func thresholdsNEON(e, ct, ecb, cbs, nb *[psyMaxParts]float64, nbPrev *[2][psyMaxParts]float64, sprdT *[psyMaxParts][psyMaxParts]float64, norm, qthr *[psyMaxParts]float64, n int, tmn, nmt, rpelev2 float64)

// shortThresholdsNEON spreads and floors eight partitions per pass with a last
// block overlapping the previous one; n must be at least 8.
//
//go:noescape
func shortThresholdsNEON(eS, nbS *[psyMaxPartsS]float64, sprdT *[psyMaxPartsS][psyMaxPartsS]float64, norm, qthr *[psyMaxPartsS]float64, n int, bc float64)

// plogsNEON computes two logarithms per iteration with a last iteration
// overlapping the previous one; n must be at least 2 and dst must not overlap
// src.
//
//go:noescape
func plogsNEON(dst, src *float64, n int)
