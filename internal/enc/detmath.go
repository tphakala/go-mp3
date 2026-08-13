package enc

import "math"

// This file is the deterministic math kit: the only transcendental
// functions the encode path may use. plog and pexp2 are built from IEEE-754
// +, -, *, / (every product feeding a + or - wrapped in float64() to block
// FMA fusion), the exact operations Frexp/Ldexp/Floor, and the fixed-order
// polynomial tables in detmathtables.go, so their results are bit-identical
// on every architecture. math.Log/Exp/Exp2/Pow and friends are NOT
// cross-arch bit-portable and are banned from internal/enc production code
// by the EncNoLibmTranscendentals ruleguard rule (rules/detmath.go); tests
// use them as references with stated tolerances.
//
// Accuracy, measured against the stdlib at plan time (linux/amd64,
// go1.26.5): plog within 3 ulp of math.Log over normal inputs (and correct
// on subnormals, where go1.26.5 math.Log itself is wrong; see
// TestPlogSubnormalIdentity); pexp2 within 1 ulp of math.Exp2, exact on
// every integer input. The KAT bounds in detmath_test.go carry headroom
// over these measurements because the stdlib references vary per
// architecture; plog and pexp2 themselves do not.

// plog returns the natural logarithm of x, bit-identically across
// architectures.
//
// Reduction: Frexp splits x = m * 2^e exactly (subnormals included); m is
// recentered into [sqrt(1/2), sqrt(2)) by an exact power-of-two scale so
// that s = (m-1)/(m+1) stays within |s| <= (sqrt(2)-1)/(sqrt(2)+1) ~ 0.1716
// (and m-1 is exact by Sterbenz). Then ln(m) = 2*atanh(s) = s*P(s^2) with
// the truncated atanh series P in plogPoly, evaluated in fixed Horner
// order, and ln x = e*ln2 + ln(m) reconstructed from the Cody-Waite split
// (e*ln2Hi is exact: 37 significant bits times |e| <= 1074 fits in 53).
//
// Special cases mirror math.Log:
//
//	plog(NaN) = NaN; plog(x < 0) = NaN; plog(+-0) = -Inf; plog(+Inf) = +Inf.
func plog(x float64) float64 {
	switch {
	case math.IsNaN(x) || x < 0:
		return math.NaN()
	case x == 0:
		return math.Inf(-1)
	case math.IsInf(x, 1):
		return math.Inf(1)
	}

	m, e := math.Frexp(x) // x = m * 2^e, m in [0.5, 1); exact
	if m < sqrtHalf {
		m *= 2 // exact power-of-two scale
		e--
	}

	s := (m - 1) / (m + 1)
	z := float64(s * s)
	p := plogPoly[len(plogPoly)-1]
	for i := len(plogPoly) - 2; i >= 0; i-- {
		p = float64(p*z) + plogPoly[i]
	}
	return float64(float64(e)*ln2Hi) + (float64(float64(e)*ln2Lo) + float64(s*p))
}
