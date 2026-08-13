package enc

import (
	"math"
	"math/big"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
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

// ulpSteps returns the number of math.Nextafter steps from a to b, capped
// at limit+1. A wider-cap sibling of ulpDistance (filterbank_test.go, cap
// 4) for the transcendental KATs, whose bounds must absorb the reference
// function's own per-architecture variation.
func ulpSteps(a, b float64, limit int) int {
	if a == b {
		return 0
	}
	for steps := 1; steps <= limit; steps++ {
		a = math.Nextafter(a, b)
		if a == b {
			return steps
		}
	}
	return limit + 1
}

func TestPlogSpecialCases(t *testing.T) {
	if v := plog(math.NaN()); !math.IsNaN(v) {
		t.Errorf("plog(NaN) = %v, want NaN", v)
	}
	if v := plog(-1); !math.IsNaN(v) {
		t.Errorf("plog(-1) = %v, want NaN", v)
	}
	if v := plog(0); !math.IsInf(v, -1) {
		t.Errorf("plog(0) = %v, want -Inf", v)
	}
	if v := plog(math.Copysign(0, -1)); !math.IsInf(v, -1) {
		t.Errorf("plog(-0) = %v, want -Inf", v)
	}
	if v := plog(math.Inf(1)); !math.IsInf(v, 1) {
		t.Errorf("plog(+Inf) = %v, want +Inf", v)
	}
	if v := plog(1); v != 0 {
		// s = 0 makes the series exactly 0 and e is 0: exact zero.
		t.Errorf("plog(1) = %v, want exactly 0", v)
	}
}

// plogKATMaxULP bounds the plog-vs-math.Log distance over normal inputs.
// Measured 3 ulp at plan time on linux/amd64 go1.26.5 (worst near x = 1
// where math.Log carries its own ulp of error); the bound is 8, NOT
// tightened to the local measurement, because math.Log's error varies per
// architecture and this test must pass on both CI legs against the same
// bit-identical plog.
const plogKATMaxULP = 8

func TestPlogMatchesMathLog(t *testing.T) {
	check := func(x float64) {
		t.Helper()
		got, want := plog(x), math.Log(x)
		if u := ulpSteps(got, want, plogKATMaxULP); u > plogKATMaxULP {
			t.Fatalf("plog(%v) = %v, math.Log = %v: > %d ulp apart",
				x, got, want, plogKATMaxULP)
		}
		if a := math.Abs(want); a >= 1 {
			if r := math.Abs((got - want) / want); r > 1e-14 {
				t.Fatalf("plog(%v) rel error %.3g vs math.Log, want <= 1e-14", x, r)
			}
		}
	}

	// Full normal exponent range, LCG-composed (deterministic).
	seed := uint64(1)
	for range 300000 {
		m := 0.5 + testsignal.LCG(&seed)/2                // [0.5, 1)
		e := int(testsignal.LCG(&seed)*2100) - 1050       // [-1050, 1050)
		x := math.Ldexp(m, e)
		if x < 2.2250738585072014e-308 || math.IsInf(x, 0) {
			continue // subnormal/overflow: covered by TestPlogSubnormalIdentity
		}
		check(x)
	}
	// Dense near 1, the cancellation-sensitive region.
	for i := -100000; i <= 100000; i++ {
		check(1 + float64(i)*1e-9)
	}
	// Exact powers of two and range endpoints.
	for _, x := range []float64{0.25, 0.5, 2, 4, 1024, 0x1p-1000, 0x1p1000,
		2.2250738585072014e-308, math.MaxFloat64} {
		check(x)
	}
}

// TestPlogSubnormalIdentity checks plog on subnormal inputs against the
// exact-shift identity ln(x) = ln(x * 2^1074) - 1074*ln2: for subnormal x,
// Ldexp(x, 1074) is an exact integer-valued float64, where math.Log is
// trustworthy. plog is NOT compared against math.Log directly here because
// go1.26.5 math.Log is WRONG on subnormal arguments (math.Log(5e-324)
// returns -709.0895... but the true value is -744.4400719213812; verified
// against math.Log2 and exact integer decomposition at plan time). plog
// handles subnormals correctly via Frexp; measured max relative error of
// this identity at plan time: 1.6e-16.
func TestPlogSubnormalIdentity(t *testing.T) {
	check := func(x float64) {
		t.Helper()
		got := plog(x)
		want := math.Log(math.Ldexp(x, 1074)) - 1074*math.Ln2
		if r := math.Abs((got - want) / want); r > 1e-13 {
			t.Fatalf("plog(%v) = %v, identity reference %v: rel error %.3g > 1e-13",
				x, got, want, r)
		}
	}
	check(math.SmallestNonzeroFloat64)                  // 5e-324 = 2^-1074
	check(math.Float64frombits(0x000FFFFFFFFFFFFF))     // largest subnormal
	check(math.Float64frombits(0x2adecac52eb))          // the plan-time probe value
	seed := uint64(7)
	for range 100000 {
		m := 0.5 + testsignal.LCG(&seed)/2
		e := int(testsignal.LCG(&seed)*52) - 1074 // [-1074, -1022): subnormal results
		x := math.Ldexp(m, e)
		if x == 0 || x >= 2.2250738585072014e-308 {
			continue
		}
		check(x)
	}
}

func TestPlogMonotoneSpot(t *testing.T) {
	// Coarse spacing on purpose: a correctly rounded-ish implementation is
	// not guaranteed monotone at adjacent floats, but must be monotone at
	// 1e-9 relative spacing.
	seed := uint64(11)
	for range 50000 {
		x := math.Ldexp(0.5+testsignal.LCG(&seed)/2, int(testsignal.LCG(&seed)*200)-100)
		if plog(x*(1+1e-9)) < plog(x) {
			t.Fatalf("plog not monotone at x = %v", x)
		}
	}
}

// plogGoldenSHA freezes plog's output bits over a fixed input vector; the
// amd64+arm64 CI matrix turns this into the cross-arch determinism gate.
// A mismatch between architectures is ALWAYS a code bug (FMA leak or
// nondeterminism), never something to fix by re-freezing.
const plogGoldenSHA = "7dee2c9ad2fcb47f6db65e49a2cc36ad1e95295b16cc3d430db7449a5d7ebd75" // FROZEN in Task 2 Step 4

func TestPlogGolden(t *testing.T) {
	seed := uint64(42)
	out := make([]float64, 0, 4096)
	for range 4096 {
		m := 0.5 + testsignal.LCG(&seed)/2
		e := int(testsignal.LCG(&seed)*2154) - 1130 // spans subnormal..overflowing
		x := math.Ldexp(m, e)
		if x == 0 {
			x = math.SmallestNonzeroFloat64
		}
		if math.IsInf(x, 0) {
			x = math.MaxFloat64
		}
		out = append(out, plog(x))
	}
	got := sha256Float64s(out...)
	if plogGoldenSHA == "" {
		t.Fatalf("FREEZE ME: const plogGoldenSHA = %q", got)
	}
	if got != plogGoldenSHA {
		t.Fatalf("plog output changed: sha256 = %s, frozen %s", got, plogGoldenSHA)
	}
}

func BenchmarkPlog(b *testing.B) {
	x := 0.123456789
	for b.Loop() {
		x = 1 + math.Abs(plog(1+x))*0.001
	}
}
