//go:build ruleguard

package gorules

import "github.com/quasilyte/go-ruleguard/dsl"

// EncNoLibmTranscendentals bans libm transcendental calls in internal/enc
// PRODUCTION code. The encoder's byte output must be identical on amd64
// and arm64 (a CI gate), and Go's math transcendentals are not cross-arch
// bit-portable; the encode path must use the deterministic substitutes
// instead: plog/pexp2 (internal/enc/detmath.go), literal hex-float tables
// (fbtables.go, mdcttables.go, ffttables.go, detmathtables.go),
// sqrt(x*sqrt(x)) for x^0.75, and Ldexp+pow2Quarter for 2^(q/4).
//
// Allowed and NOT matched here (exact per IEEE-754 or constants):
// math.Sqrt, Abs, Ldexp, Frexp, Floor, Trunc, Ceil, Mod, Copysign, NaN,
// Inf, IsNaN, IsInf, Signbit, Float64bits, Float64frombits, Min/Max
// constants.
//
// Scope: internal/enc non-test files only. Test files legitimately call
// math.* as accuracy references (with stated tolerances), and other
// packages (internal/testsignal, internal/dec tests) are outside the
// encoder's determinism boundary; see internal/testsignal's package doc.
//
// math.FMA is banned too. It is not a transcendental, but it emits a
// hardware fused multiply-add that bypasses the float64(a*b) fusion-blocking
// discipline and so breaks cross-arch bit-identity, exactly what this rule
// protects.
//
// Import aliasing is handled: ruleguard resolves the selector to the math
// package, so an aliased import (import m "math"; m.Sin(x)) is matched the
// same as math.Sin(x). Verified empirically against ruleguard dsl v0.3.23.
//
// Known limitation: this matches selector call expressions only. Two forms
// are not matched: a dot import (import . "math"; Sin(x)) and a
// function-pointer alias (f := math.Sin; f(x)). Both were left out on
// purpose. Catching them needs a bare-identifier pattern (Sin($*_)), which
// would flag any local function named Sin/Cos/... and so is high-noise for a
// near-zero real risk here (dot-importing math is itself a red flag other
// linters reject). The cross-arch golden tests (plog/pexp2/FFT digests on
// the amd64+arm64 matrix) are the ultimate backstop: any determinism leak
// such a call introduced would change a digest and fail CI.
func EncNoLibmTranscendentals(m dsl.Matcher) {
	m.Match(
		`math.Sin($*_)`, `math.Cos($*_)`, `math.Tan($*_)`, `math.Sincos($*_)`,
		`math.Asin($*_)`, `math.Acos($*_)`, `math.Atan($*_)`, `math.Atan2($*_)`,
		`math.Sinh($*_)`, `math.Cosh($*_)`, `math.Tanh($*_)`,
		`math.Asinh($*_)`, `math.Acosh($*_)`, `math.Atanh($*_)`,
		`math.Exp($*_)`, `math.Exp2($*_)`, `math.Expm1($*_)`,
		`math.Log($*_)`, `math.Log2($*_)`, `math.Log10($*_)`, `math.Log1p($*_)`,
		`math.Pow($*_)`, `math.Pow10($*_)`, `math.Cbrt($*_)`, `math.Hypot($*_)`,
		`math.Erf($*_)`, `math.Erfc($*_)`, `math.Erfinv($*_)`, `math.Erfcinv($*_)`,
		`math.Gamma($*_)`, `math.Lgamma($*_)`,
		`math.J0($*_)`, `math.J1($*_)`, `math.Jn($*_)`,
		`math.Y0($*_)`, `math.Y1($*_)`, `math.Yn($*_)`,
		`math.FMA($*_)`,
	).
		Where(m.File().PkgPath.Matches(`internal/enc$`) &&
			!m.File().Name.Matches(`_test\.go$`)).
		Report("internal/enc encode path must not call libm transcendentals (cross-arch bit-exact output rule): use plog/pexp2 (detmath.go) or literal hex-float tables; the exact ops (Sqrt/Abs/Ldexp/Frexp/Floor/Trunc/Ceil/Mod/Copysign/... - see the rule doc) are allowed")
}
