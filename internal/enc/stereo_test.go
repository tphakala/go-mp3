package enc

import (
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

func TestButterflyExactness(t *testing.T) {
	var l, r, m, s [1024]float64
	seed := uint64(91)
	for i := range l {
		l[i] = testsignal.LCGSigned(&seed)
		r[i] = testsignal.LCGSigned(&seed)
	}
	// Identical channels: the difference is EXACTLY zero (l-l == 0 in
	// IEEE-754), so the S window is exactly zero.
	r2 := l
	butterflyWindows(&l, &r2, &m, &s)
	for i := range s {
		if s[i] != 0 {
			t.Fatalf("identical channels: s[%d] = %v, want exactly 0", i, s[i])
		}
	}
	// Round trip: the butterfly is an involution up to rounding. Applying
	// it twice recovers the INPUT ITSELF: m2 = (m+s)*sqrtHalf =
	// ((l+r)+(l-r))*sqrtHalf^2 = 2l*sqrtHalf^2 ~= l. (An earlier draft of
	// this test compared against l/2, a factor-of-two error agy's review
	// caught; the correct expectation is l.) The double butterfly
	// accumulates 6 IEEE-754 roundings (2 adds and 2 multiplies per call,
	// twice), and the m/s intermediates exceed |l| in magnitude, so the
	// per-line error is larger than a single-rounding estimate suggests:
	// measured (deterministic, cross-arch, since the (a+b)*c shape has no
	// FMA fusion opportunity on any architecture) at up to 5 ulp for this
	// seed. 8 ulp leaves headroom while staying about 2^49x tighter than
	// any factor/sign/constant error (~2^52 ulp), so it still catches
	// every real bug.
	var m2, s2 [1024]float64
	butterflyWindows(&l, &r, &m, &s)
	butterflyWindows(&m, &s, &m2, &s2) // m2 ~= l, s2 ~= r
	for i := range l {
		if d := ulpDistance(m2[i], l[i]); d > 8 {
			t.Fatalf("round trip: m2[%d] %d ulp from l", i, d)
		}
		if d := ulpDistance(s2[i], r[i]); d > 8 {
			t.Fatalf("round trip: s2[%d] %d ulp from r", i, d)
		}
	}
	// Energy preservation (unitary up to rounding): sum(m^2+s^2) ~=
	// sum(l^2+r^2) within 1e-12 relative.
	var eIn, eOut float64
	for i := range l {
		eIn += float64(l[i]*l[i]) + float64(r[i]*r[i])
		eOut += float64(m[i]*m[i]) + float64(s[i]*s[i])
	}
	if rel := math.Abs(eOut-eIn) / eIn; rel > 1e-12 {
		t.Fatalf("butterfly not energy-preserving: rel %.3g", rel)
	}
}

func TestButterflyXrMatchesWindows(t *testing.T) {
	// The xr butterfly must be the same arithmetic as the window butterfly
	// (bit-identical per line for identical inputs): one definition, two
	// array shapes.
	var xl, xr576, xm, xs [576]float64
	var wl, wr, wm, ws [1024]float64
	seed := uint64(93)
	for i := range xl {
		v := testsignal.LCGSigned(&seed) * 100
		w := testsignal.LCGSigned(&seed) * 100
		xl[i], wl[i] = v, v
		xr576[i], wr[i] = w, w
	}
	butterflyXr(&xl, &xr576, &xm, &xs)
	butterflyWindows(&wl, &wr, &wm, &ws)
	for i := range xm {
		if xm[i] != wm[i] || xs[i] != ws[i] {
			t.Fatalf("line %d: xr butterfly diverges from window butterfly", i)
		}
	}
}

func TestMsDecide(t *testing.T) {
	// Correlated: M/S PE well under L/R: chosen.
	if !msDecide(1000, 1000, 1000, 50) {
		t.Error("correlated frame: want M/S")
	}
	// Hard pan: M/S costs more: L/R.
	if msDecide(1000, 0, 720, 720) {
		t.Error("hard pan: want L/R")
	}
	// Static bias: an M/S win smaller than the margin stays L/R; at
	// exactly the margin, M/S.
	if msDecide(1000, 1000, 1000, 999) {
		t.Error("sub-margin win: want L/R")
	}
	if !msDecide(1000, 1000, 1000, 1000-msPeMarginBits) {
		t.Error("exact-margin win: want M/S")
	}
}
