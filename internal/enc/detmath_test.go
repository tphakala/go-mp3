package enc

import (
	"math"
	"math/big"
	"testing"
)

// bigLn2 recomputes ln 2 exactly as the offline table generator does: the
// exact rational partial sum of ln 2 = sum_{k>=1} 1/(k*2^k) (the Mercator
// series -ln(1-x) at x = 1/2) truncated at k = 250 (tail < 2^-250, far
// below the 200-bit working precision), rounded to a 200-bit big.Float.
// math/big arithmetic is platform-independent, so this recomputation is
// bit-exact against the generated literals on every architecture; the
// table tests below are exact equality checks, not tolerance checks.
func bigLn2() *big.Float {
	acc := new(big.Rat)
	pow := new(big.Rat).SetInt64(1)
	two := new(big.Rat).SetInt64(2)
	term := new(big.Rat)
	for k := int64(1); k <= 250; k++ {
		pow.Quo(pow, two)
		term.SetInt64(k)
		term.Inv(term)
		term.Mul(term, pow)
		acc.Add(acc, term)
	}
	return new(big.Float).SetPrec(200).SetRat(acc)
}

func TestPlogPolyKnownAnswer(t *testing.T) {
	if len(plogPoly) != 13 {
		t.Fatalf("plogPoly has %d entries, want 13", len(plogPoly))
	}
	for k, got := range plogPoly {
		want, _ := big.NewRat(2, int64(2*k+1)).Float64()
		if math.Float64bits(got) != math.Float64bits(want) {
			t.Errorf("plogPoly[%d] = %x, want %x (nearest float64 of 2/%d)",
				k, got, want, 2*k+1)
		}
	}
}

func TestPexp2PolyKnownAnswer(t *testing.T) {
	if len(pexp2Poly) != 15 {
		t.Fatalf("pexp2Poly has %d entries, want 15", len(pexp2Poly))
	}
	ln2 := bigLn2()
	c := new(big.Float).SetPrec(200).SetInt64(1)
	for k, got := range pexp2Poly {
		if k > 0 {
			c.Mul(c, ln2)
			c.Quo(c, new(big.Float).SetPrec(200).SetInt64(int64(k)))
		}
		want, _ := c.Float64()
		if math.Float64bits(got) != math.Float64bits(want) {
			t.Errorf("pexp2Poly[%d] = %x, want %x (nearest float64 of ln2^%d/%d!)",
				k, got, want, k, k)
		}
	}
}

func TestLn2SplitKnownAnswer(t *testing.T) {
	// hi is float64(ln 2) with its low 16 mantissa bits zeroed, so that
	// e*ln2Hi is an exact product for every |e| <= 2^16 (plog uses
	// |e| <= 1074): 37 significant bits + 16 more fit in 53.
	if math.Float64bits(ln2Hi)&0xFFFF != 0 {
		t.Errorf("ln2Hi low 16 mantissa bits not zero: %#x", math.Float64bits(ln2Hi))
	}
	if ln2Hi+ln2Lo != math.Ln2 {
		t.Errorf("ln2Hi+ln2Lo = %x, want math.Ln2 = %x", ln2Hi+ln2Lo, math.Ln2)
	}
	ln2 := bigLn2()
	f64, _ := ln2.Float64()
	wantHi := math.Float64frombits(math.Float64bits(f64) &^ 0xFFFF)
	loBig := new(big.Float).SetPrec(200).Sub(ln2, new(big.Float).SetPrec(200).SetFloat64(wantHi))
	wantLo, _ := loBig.Float64()
	if math.Float64bits(ln2Hi) != math.Float64bits(wantHi) {
		t.Errorf("ln2Hi = %x, want %x", ln2Hi, wantHi)
	}
	if math.Float64bits(ln2Lo) != math.Float64bits(wantLo) {
		t.Errorf("ln2Lo = %x, want %x", ln2Lo, wantLo)
	}
}

func TestSqrtHalfKnownAnswer(t *testing.T) {
	// math.Sqrt is IEEE-754 correctly rounded, hence exact cross-arch:
	// bit equality is a valid assertion.
	if math.Float64bits(sqrtHalf) != math.Float64bits(math.Sqrt(0.5)) {
		t.Errorf("sqrtHalf = %x, want math.Sqrt(0.5) = %x", sqrtHalf, math.Sqrt(0.5))
	}
}

// detmathTablesSHA freezes the detmath table constants. Freeze procedure:
// with the constant empty, the test fails and prints the measured digest;
// paste it here in the same commit. Any later change to any table value
// fails this test on both CI architectures.
const detmathTablesSHA = "382aa0ab5c52664607621c1340f80c67b5ef55a2eed7512b259c9170a2f1299c" // FROZEN in Task 1 Step 4

func TestDetmathTablesChecksum(t *testing.T) {
	vs := make([]float64, 0, len(plogPoly)+len(pexp2Poly)+3)
	vs = append(vs, plogPoly[:]...)
	vs = append(vs, pexp2Poly[:]...)
	vs = append(vs, ln2Hi, ln2Lo, sqrtHalf)
	got := sha256Float64s(vs...)
	if detmathTablesSHA == "" {
		t.Fatalf("FREEZE ME: const detmathTablesSHA = %q", got)
	}
	if got != detmathTablesSHA {
		t.Fatalf("detmath tables changed: sha256 = %s, frozen %s", got, detmathTablesSHA)
	}
}
