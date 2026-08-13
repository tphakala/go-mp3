package enc

import (
	"math"
	"slices"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// safeLog is math.Log with correct subnormal handling. go1.26.5
// linux/amd64 math.Log is WRONG for subnormal arguments: math.Log(5e-324)
// returns -709.0895657128241 but the true value is -744.4400719213812
// (verified against math.Log2, which is correct there, and against the
// exact integer decomposition ln(m) - 1074*ln2 during increment 2
// planning). Do NOT "fix" the runtime plog to match the broken stdlib;
// this reference routes subnormals through the exact-shift identity
// instead: for subnormal x, Ldexp(x, 1074) is an exact integer-valued
// float64 where math.Log is trustworthy.
func safeLog(x float64) float64 {
	if x > 0 && x < 2.2250738585072014e-308 {
		return math.Log(math.Ldexp(x, 1074)) - 1074*math.Ln2
	}
	return math.Log(x)
}

// refThresholds recomputes computeThresholds from the same annex
// equations with unrestricted libm (math.Pow for the power ratio,
// safeLog for the tonality log), sharing the committed tables. The
// runtime path (plog/pexp2, fixed-order sums) must agree within refTol
// relative on every partition.
const refTol = 1e-9

func refThresholds(tab *psyRateTable, r2, cw, nb1, nb2 []float64) (e, nb []float64) {
	n := tab.nParts
	e = make([]float64, n)
	ct := make([]float64, n)
	for i := range 513 {
		b := tab.partOfLine[i]
		e[b] += r2[i]
		ct[b] += cw[i] * r2[i]
	}
	nb = make([]float64, n)
	for b := range n {
		var ecb, cbs float64
		for bb := range n {
			ecb += e[bb] * tab.sprd[b][bb]
			cbs += ct[bb] * tab.sprd[b][bb]
		}
		tb := 0.0
		if ecb > 0 {
			cbb := cbs / ecb
			if cbb > 1 {
				cbb = 1
			}
			if cbb > 0 {
				tb = -0.299 - 0.43*safeLog(cbb)
			} else {
				tb = 1
			}
			tb = math.Min(math.Max(tb, 0), 1)
		}
		snr := tb*psyTmnDB + (1-tb)*psyNmtDB
		bc := math.Pow(10, -snr/10)
		raw := ecb * tab.norm[b] * bc
		capped := math.Min(raw, math.Min(psyRpelev*nb1[b], psyRpelev2*nb2[b]))
		nb[b] = math.Max(tab.qthr[b], capped)
	}
	return e, nb
}

func TestPsyThresholdsMatchReference(t *testing.T) {
	var p PsyModel
	p.Reset(0)
	pcm := make([]float64, 1024)
	seed := uint64(101)
	for range 5 {
		for i := range pcm {
			pcm[i] = testsignal.LCGSigned(&seed) * 0.7
		}
		nb1 := slices.Clone(p.nbPrev[0][:p.tab.nParts])
		nb2 := slices.Clone(p.nbPrev[1][:p.tab.nParts])
		p.analyzeSpectrum(pcm)
		wantE, wantNb := refThresholds(p.tab, p.r2[:], p.cw[:], nb1, nb2)
		p.computeThresholds()
		for b := range p.tab.nParts {
			if wantE[b] != 0 {
				if r := math.Abs((p.e[b] - wantE[b]) / wantE[b]); r > refTol {
					t.Fatalf("part %d: e rel diff %.3g", b, r)
				}
			}
			if r := math.Abs((p.nb[b] - wantNb[b]) / wantNb[b]); r > refTol {
				t.Fatalf("part %d: nb = %g, reference %g (rel %.3g)", b, p.nb[b], wantNb[b], r)
			}
		}
	}
}

// TestPsyModelMatchesReference: full-pipeline agreement of the runtime
// path (plog/pexp2/fixed order) with the libm reference: sfb outputs and
// PE within refTol relative.
func TestPsyModelMatchesReference(t *testing.T) {
	var p PsyModel
	p.Reset(1)
	pcm := make([]float64, 1024)
	seed := uint64(141)
	var out PsyOut
	for range 5 {
		for i := range pcm {
			pcm[i] = testsignal.LCGSigned(&seed) * 0.8
		}
		nb1 := slices.Clone(p.nbPrev[0][:p.tab.nParts])
		nb2 := slices.Clone(p.nbPrev[1][:p.tab.nParts])
		p.analyzeSpectrum(pcm)
		r2 := slices.Clone(p.r2[:])
		cw := slices.Clone(p.cw[:])
		e, nb := refThresholds(p.tab, r2, cw, nb1, nb2)
		// Reference sfb mapping + PE (same equations; PE in bits via
		// math.Log2, matching the runtime's plog*psyLog2E scaling; the
		// ratio argument is > 1, never subnormal, so plain libm is safe
		// here and safeLog is not needed for PE).
		var refXmin, refEn [22]float64
		var refPE float64
		line := 0
		for s := range 22 {
			for range sfbWidthsLong[1][s] {
				b := p.tab.partOfMdctLine[line]
				refXmin[s] += nb[b] / p.tab.mlines[b]
				refEn[s] += e[b] / p.tab.mlines[b]
				line++
			}
		}
		for b := range p.tab.nParts {
			if ratio := e[b] / nb[b]; ratio > 1 {
				refPE += p.tab.lines[b] * math.Log2(ratio)
			}
		}
		p.computeThresholds()
		p.mapToSfb(&out)
		for s := range 22 {
			if r := math.Abs((out.Xmin[s] - refXmin[s]) / refXmin[s]); r > refTol {
				t.Fatalf("Xmin[%d] rel diff %.3g", s, r)
			}
			if refEn[s] != 0 {
				if r := math.Abs((out.En[s] - refEn[s]) / refEn[s]); r > refTol {
					t.Fatalf("En[%d] rel diff %.3g", s, r)
				}
			}
		}
		if refPE != 0 {
			if r := math.Abs((out.PE - refPE) / refPE); r > refTol {
				t.Fatalf("PE = %v, reference %v (rel %.3g)", out.PE, refPE, r)
			}
		}
	}
}
