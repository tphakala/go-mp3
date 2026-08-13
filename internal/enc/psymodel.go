package enc

import "math"

// This file implements the analysis side of psychoacoustic model 2 for
// long blocks (ISO/IEC 11172-3 informative annex; Brandenburg/Stoll;
// Painter/Spanias), on the deterministic substrate of detmath.go and
// fft.go: the only transcendentals on this path are plog and pexp2, all
// other arithmetic is +,-,*,/ and sqrt with FMA fusion blocked, and every
// nonlinear table (Bark geometry, ATH, spreading) is precomputed in
// psytables.go. Output is bit-identical across architectures; the frozen
// goldens in psymodel_test.go are the gate.
//
// The unpredictability measure uses the CARTESIAN reformulation (roadmap
// D1): the annex extrapolates magnitude and phase (rpred = 2 r1 - r2,
// fpred = 2 f1 - f2); cos/sin of fpred expand by double- and
// difference-angle identities into rational functions of the stored
// complex spectra, so no atan2/sin/cos runs at runtime.
// TestPsyCartesianMatchesPolar proves the identity against the polar form.

// psyNbInit initializes the pre-echo threshold history: large enough that
// min(nb, rpelev*nb1, rpelev2*nb2) never binds before two real frames of
// history exist.
const psyNbInit = 1e30

// Tone-masking-noise and noise-masking-tone constants, dB, and the
// two-frame pre-echo rule factors. TMN is FLAT 15.5 dB across all
// partitions: the ISO model 2 simplification, confirmed by the agy plan
// review; the Johnston-style 14.5 + bval form is rejected because it
// starves high frequencies (about 38.5 dB demanded at 24 Bark). NMT is
// 5.5 dB (Brandenburg/Stoll).
const (
	psyTmnDB   = 15.5
	psyNmtDB   = 5.5
	psyRpelev  = 2.0
	psyRpelev2 = 16.0
)

// psyLog2E is log2(e) = 1/ln2 as an exact hex literal: it converts the
// plog-based (natural log) perceptual-entropy accumulation into BITS,
// Johnston's PE convention, with a single FMA-blocked multiply and no new
// transcendental.
const psyLog2E = 0x1.71547652b82fep+00

// PsyModel is one channel's psychoacoustic model 2 state. See the plan
// for field semantics. Not safe for concurrent use; all scratch is
// preallocated here so AnalyzeGranule is allocation-free.
type PsyModel struct {
	tab     *psyRateTable
	srIndex int

	prevRe, prevIm [2][513]float64
	nbPrev         [2][psyMaxParts]float64

	fftRe, fftIm [1024]float64
	r2, cw       [513]float64
	e, ct        [psyMaxParts]float64
	ecb, cbs, nb [psyMaxParts]float64
}

// Reset prepares the model for a fresh stream at srIndex (0 = 44100,
// 1 = 48000, 2 = 32000, the project's srIndex order).
func (p *PsyModel) Reset(srIndex int) {
	*p = PsyModel{tab: &psyRateTables[srIndex], srIndex: srIndex}
	for i := range p.nbPrev {
		for b := range p.nbPrev[i] {
			p.nbPrev[i][b] = psyNbInit
		}
	}
}

// analyzeSpectrum windows pcm (the most recent 1024 samples, oldest
// first), transforms, and fills r2 (squared magnitudes) and cw
// (unpredictability) for bins 0..512, rotating the spectral history.
func (p *PsyModel) analyzeSpectrum(pcm []float64) {
	_ = pcm[1023]
	for i := range 1024 {
		p.fftRe[i] = pcm[i] * hannWindow1024[i] // stored product: no fusion hazard
		p.fftIm[i] = 0
	}
	fft1024(&p.fftRe, &p.fftIm)

	for i := range 513 {
		re, im := p.fftRe[i], p.fftIm[i]
		re1, im1 := p.prevRe[0][i], p.prevIm[0][i]
		re2, im2 := p.prevRe[1][i], p.prevIm[1][i]

		rSq := float64(re*re) + float64(im*im)
		p.r2[i] = rSq

		r1Sq := float64(re1*re1) + float64(im1*im1)
		r2Sq := float64(re2*re2) + float64(im2*im2)
		r := math.Sqrt(rSq)
		r1 := math.Sqrt(r1Sq)
		r2m := math.Sqrt(r2Sq)
		rp := float64(2*r1) - r2m

		if r1 == 0 || r2m == 0 || (r == 0 && rp == 0) {
			p.cw[i] = 1 // R3: no history basis, maximal unpredictability
		} else {
			// cos(2 f1) and sin(2 f1) over the EXACT r1Sq (R2), cos/sin f2
			// over the magnitude, combined by the angle-difference
			// identities: fpred = 2 f1 - f2.
			cos2f1 := (float64(re1*re1) - float64(im1*im1)) / r1Sq
			sin2f1 := float64(2*re1) * im1 / r1Sq
			cosf2 := re2 / r2m
			sinf2 := im2 / r2m
			cosfp := float64(cos2f1*cosf2) + float64(sin2f1*sinf2)
			sinfp := float64(sin2f1*cosf2) - float64(cos2f1*sinf2)

			dRe := re - float64(rp*cosfp)
			dIm := im - float64(rp*sinfp)
			num := math.Sqrt(float64(dRe*dRe) + float64(dIm*dIm))
			c := num / (r + math.Abs(rp))
			if c > 1 {
				c = 1 // rounding can push a hair past the analytic bound
			}
			p.cw[i] = c
		}
	}

	// Rotate spectral history: current becomes [0], old [0] becomes [1].
	for i := range 513 {
		p.prevRe[1][i], p.prevIm[1][i] = p.prevRe[0][i], p.prevIm[0][i]
		p.prevRe[0][i], p.prevIm[0][i] = p.fftRe[i], p.fftIm[i]
	}
}

// computeThresholds derives the per-partition masking threshold nb from
// the current spectrum (r2, cw): partition energies and energy-weighted
// unpredictability, spreading convolution in fixed index order, tonality
// tb = clamp(-0.299 - 0.43*ln(cb/ecb), 0, 1) via plog, required SNR
// tb*15.5 + (1-tb)*5.5 dB (flat TMN) converted through pexp2, spreading
// normalization, the two-frame pre-echo cap min(raw, 2*nb1, 16*nb2), and
// the absolute-threshold floor qthr. Rotates the threshold history.
func (p *PsyModel) computeThresholds() {
	tab := p.tab
	n := tab.nParts

	for b := range n {
		p.e[b] = 0
		p.ct[b] = 0
	}
	for i := range 513 {
		b := tab.partOfLine[i]
		p.e[b] += p.r2[i]
		p.ct[b] += float64(p.cw[i] * p.r2[i])
	}

	for b := range n {
		var ecb, cbs float64
		sp := &tab.sprd[b]
		for bb := range n {
			ecb += float64(p.e[bb] * sp[bb])
			cbs += float64(p.ct[bb] * sp[bb])
		}
		p.ecb[b] = ecb
		p.cbs[b] = cbs

		tb := 0.0
		if ecb > 0 {
			cbb := cbs / ecb
			switch {
			case cbb <= 0:
				tb = 1 // fully predictable in the limit: tonal
			default:
				if cbb > 1 {
					cbb = 1
				}
				tb = -0.299 - float64(0.43*plog(cbb))
				if tb < 0 {
					tb = 0
				}
				if tb > 1 {
					tb = 1
				}
			}
		}

		snr := float64(tb*psyTmnDB) + float64((1-tb)*psyNmtDB)
		bc := pexp2(-snr * psyLog2TenOver10)
		raw := float64(float64(ecb*tab.norm[b]) * bc)

		cap1 := float64(psyRpelev * p.nbPrev[0][b])
		cap2 := float64(psyRpelev2 * p.nbPrev[1][b])
		nb := min(raw, cap1, cap2)
		if nb < tab.qthr[b] {
			nb = tab.qthr[b]
		}
		p.nb[b] = nb
	}

	// Rotate threshold history with the UNFLOORED spread threshold? No:
	// the annex compares against the previous FINAL thresholds; keep nb.
	for b := range n {
		p.nbPrev[1][b] = p.nbPrev[0][b]
		p.nbPrev[0][b] = p.nb[b]
	}
}

// PsyOut is one granule-channel's psychoacoustic model output over the 22
// long scalefactor bands of the current sample rate. Xmin is the allowed
// noise energy per band (the outer loop's distortion target), En the
// model's signal energy per band, SMR their ratio (0 where En is 0), and
// PE the perceptual entropy in BITS (Johnston's convention: line counts
// times log2 of the energy-to-threshold ratio), so increment 7's
// block-switch decision can compare against the literature's absolute
// thresholds (about 1800 bits) directly.
type PsyOut struct {
	Xmin [22]float64
	En   [22]float64
	SMR  [22]float64
	PE   float64
}

// mapToSfb distributes partition thresholds and energies over the 22 long
// scalefactor bands by per-MDCT-line density (nb[b]/mlines[b] per line),
// which conserves totals across the 576-line domain, and computes PE in
// bits (plog accumulation scaled once by the psyLog2E literal).
func (p *PsyModel) mapToSfb(out *PsyOut) {
	tab := p.tab
	sfb := &sfbWidthsLong[p.srIndex]
	line := 0
	for s := range 22 {
		var x, en float64
		for range sfb[s] {
			b := tab.partOfMdctLine[line]
			x += tab.invMlinesTimes(p.nb[b], b)
			en += tab.invMlinesTimes(p.e[b], b)
			line++
		}
		out.Xmin[s] = x
		out.En[s] = en
		if x > 0 && en > 0 {
			out.SMR[s] = en / x
		} else {
			out.SMR[s] = 0
		}
	}

	pe := 0.0
	for b := range tab.nParts {
		if ratio := p.e[b] / p.nb[b]; ratio > 1 {
			pe += float64(tab.lines[b] * plog(ratio))
		}
	}
	out.PE = float64(pe * psyLog2E) // nats -> bits: one blocked multiply
}

// invMlinesTimes returns v/mlines[b]: a helper so the division appears
// once (mlines is never zero: every partition receives at least one MDCT
// line by construction, validated by TestPsyMdctLineMap).
func (t *psyRateTable) invMlinesTimes(v float64, b uint8) float64 {
	return v / t.mlines[b]
}

// AnalyzeGranule runs the full model 2 long-block analysis on the most
// recent 1024 samples of one channel's PCM (float64 in [-1, 1], oldest
// first: the CAUSAL window ending at the granule's last sample; the
// one-frame lookahead recentering is increment 7) and fills out.
// Allocation-free after Reset.
func (p *PsyModel) AnalyzeGranule(pcm []float64, out *PsyOut) {
	p.analyzeSpectrum(pcm)
	p.computeThresholds()
	p.mapToSfb(out)
}
