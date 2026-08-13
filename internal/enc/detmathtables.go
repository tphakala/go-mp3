package enc

// Coefficient tables for the deterministic math kit (detmath.go),
// generated as exact hex float64 literals by a throwaway scratchpad
// program (never committed) that uses only math/big exact rational and
// 200-bit float arithmetic: ln 2 comes from the exact rational partial
// sum of the Mercator series ln 2 = sum 1/(k*2^k) truncated at k = 250,
// each pexp2Poly entry from 200-bit ln2^k/k!, each plogPoly entry from
// the exact rational 2/(2k+1), sqrtHalf from a 200-bit square root. No
// float transcendental runs at generation, init, or run time, so these
// literals are platform-independent by construction; the detmath_test
// known-answer tests recompute every value with math/big and require
// bit equality. The committed literal is the runtime truth.

// plogPoly[k] is the nearest float64 of 2/(2k+1): the atanh series
// ln(m) = s*(2 + (2/3)z + (2/5)z^2 + ...), s = (m-1)/(m+1), z = s^2,
// truncated at z^12 (truncation < 1e-21 relative over plog's reduced
// domain |s| <= sqrt(2)-1 / sqrt(2)+1, far below float64 resolution).
var plogPoly = [13]float64{
	0x1p+01,               // 2/1
	0x1.5555555555555p-01, // 2/3
	0x1.999999999999ap-02, // 2/5
	0x1.2492492492492p-02, // 2/7
	0x1.c71c71c71c71cp-03, // 2/9
	0x1.745d1745d1746p-03, // 2/11
	0x1.3b13b13b13b14p-03, // 2/13
	0x1.1111111111111p-03, // 2/15
	0x1.e1e1e1e1e1e1ep-04, // 2/17
	0x1.af286bca1af28p-04, // 2/19
	0x1.8618618618618p-04, // 2/21
	0x1.642c8590b2164p-04, // 2/23
	0x1.47ae147ae147bp-04, // 2/25
}

// pexp2Poly[k] is the nearest float64 of (ln 2)^k / k!: the Taylor
// series of 2^f, truncated at f^14 (truncation < 2e-19 relative over
// pexp2's reduced domain |f| <= 1/2).
var pexp2Poly = [15]float64{
	0x1p+00,               // ln2^0/0!
	0x1.62e42fefa39efp-01, // ln2^1/1!
	0x1.ebfbdff82c58fp-03, // ln2^2/2!
	0x1.c6b08d704a0cp-05,  // ln2^3/3!
	0x1.3b2ab6fba4e77p-07, // ln2^4/4!
	0x1.5d87fe78a6731p-10, // ln2^5/5!
	0x1.430912f86c787p-13, // ln2^6/6!
	0x1.ffcbfc588b0c7p-17, // ln2^7/7!
	0x1.62c0223a5c824p-20, // ln2^8/8!
	0x1.b5253d395e7c4p-24, // ln2^9/9!
	0x1.e4cf5158b8ecap-28, // ln2^10/10!
	0x1.e8cac7351bb25p-32, // ln2^11/11!
	0x1.c3bd650fc2986p-36, // ln2^12/12!
	0x1.816193166d0f9p-40, // ln2^13/13!
	0x1.314964d5878a9p-44, // ln2^14/14!
}

// ln2Hi and ln2Lo are the Cody-Waite split of ln 2: ln2Hi is float64
// (ln 2) with its low 16 mantissa bits zeroed, so e*ln2Hi is an exact
// product for every |e| <= 2^16 (plog uses |e| <= 1074), and ln2Lo is
// the 200-bit remainder ln 2 - ln2Hi rounded to float64. Both are typed
// float64 (not untyped constants): an untyped ln2Hi+ln2Lo would fold at
// compile time under Go's high-precision (256-bit+) constant arithmetic,
// which does not equal the IEEE-754 float64-rounded runtime sum; typing
// them float64 forces every constant expression that touches them to
// round to float64 at each step, matching runtime semantics exactly.
const (
	ln2Hi float64 = 0x1.62e42fefap-01
	ln2Lo float64 = 0x1.cf79abc9e3b3ap-40
)

// sqrtHalf is the nearest float64 of sqrt(1/2), the boundary plog uses
// to recenter the Frexp mantissa into [sqrt(1/2), sqrt(2)).
const sqrtHalf = 0x1.6a09e667f3bcdp-01
