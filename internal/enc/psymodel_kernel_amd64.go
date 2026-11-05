//go:build amd64 && !noasm

package enc

// unpredictability fills r2 and cw with the AVX2 kernel when the CPU has AVX2,
// else with unpredictabilityGo. Each lane is one bin and repeats the
// reference's operations in its order (every product, quotient and square
// root separately rounded), so the result is bit-identical.
func (p *PsyModel) unpredictability() {
	if hasAVX2 {
		unpredAVX2(&p.fftRe, &p.fftIm, &p.prevRe, &p.prevIm, &p.r2, &p.cw)
		return
	}
	p.unpredictabilityGo()
}

// maskingThresholds fills ecb, cbs and nb with the AVX2 kernel when the CPU has
// AVX2, else with maskingThresholdsGo. Each lane is one partition: the
// spreading sums run in the reference's bb order and the tonality, required
// SNR, pre-echo cap and floor repeat its operations, with plog and pexp2
// evaluated lane-wise by the same reductions and Horner orders as detmath.go.
func (p *PsyModel) maskingThresholds() {
	if hasAVX2 {
		tab := p.tab
		thresholdsAVX2(&p.e, &p.ct, &p.ecb, &p.cbs, &p.nb, &p.nbPrev, &psySprdT[p.srIndex], &tab.norm, &tab.qthr, tab.nParts, psyTmnDB, psyNmtDB, psyRpelev2)
		return
	}
	p.maskingThresholdsGo()
}

// shortThresholds fills nbS with the AVX2 kernel when the CPU has AVX2, else
// with shortThresholdsGo. tab must be psyShortTables[p.srIndex], as it is for
// every caller.
func (p *PsyModel) shortThresholds(tab *psyShortTable) {
	if hasAVX2 {
		shortThresholdsAVX2(&p.eS, &p.nbS, &psySprdTS[p.srIndex], &tab.norm, &tab.qthr, tab.nParts, psyShortBC)
		return
	}
	p.shortThresholdsGo(tab)
}

// plogs sets dst[i] = plog(src[i]) for positive finite src, four lanes at a
// time with the AVX2 kernel when the CPU has AVX2 and len(src) >= 4, else with
// plogsGo. A non-positive or NaN input leaves an unspecified value.
func plogs(dst, src []float64) {
	if hasAVX2 && len(src) >= 4 {
		plogsAVX2(&dst[0], &src[0], len(src))
		return
	}
	plogsGo(dst, src)
}

// unpredAVX2 computes four bins per iteration, the last iteration overlapping
// the previous one so all 513 bins are covered.
//
//go:noescape
func unpredAVX2(re, im *[1024]float64, prevRe, prevIm *[2][513]float64, r2, cw *[513]float64)

// thresholdsAVX2 spreads eight partitions per pass and derives the thresholds
// four partitions at a time, each with a last block overlapping the previous
// one; n must be at least 8. tmn, nmt and rpelev2 are psyTmnDB, psyNmtDB and
// psyRpelev2, passed in like shortThresholdsAVX2's bc so the kernel keeps no
// copy of the model parameters.
//
//go:noescape
func thresholdsAVX2(e, ct, ecb, cbs, nb *[psyMaxParts]float64, nbPrev *[2][psyMaxParts]float64, sprdT *[psyMaxParts][psyMaxParts]float64, norm, qthr *[psyMaxParts]float64, n int, tmn, nmt, rpelev2 float64)

// shortThresholdsAVX2 spreads and floors eight partitions per pass with a last
// block overlapping the previous one; n must be at least 8.
//
//go:noescape
func shortThresholdsAVX2(eS, nbS *[psyMaxPartsS]float64, sprdT *[psyMaxPartsS][psyMaxPartsS]float64, norm, qthr *[psyMaxPartsS]float64, n int, bc float64)

// plogsAVX2 computes four logarithms per iteration with a last iteration
// overlapping the previous one; n must be at least 4 and dst must not overlap
// src.
//
//go:noescape
func plogsAVX2(dst, src *float64, n int)
