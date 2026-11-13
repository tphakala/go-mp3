package enc

import (
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// TestExactScanFillsOnlyFromBoundStart pins that the production exact scan
// starts at the bound, not at minGlobalGain: the output is identical either
// way, so the oracle tests cannot tell. Without pruning the scan fills one
// band-0 cache entry per gain from minGlobalGain to the fitting gain; with it,
// only the bisection probes and the few steps after the start do.
func TestExactScanFillsOnlyFromBoundStart(t *testing.T) {
	const minGap = 40 // gains skipped; well above the ~8 probes plus the steps after the start
	lay := &layoutLong[0]
	qualified := 0
	for _, s := range bisectSweepSpectra() {
		for _, budget := range bisectBudgets {
			xr := s.xr
			var sf scfState
			qc := new(quantCache)
			qc.reset(&xr, lay)
			gg0 := minGlobalGain(&xr, &sf, lay)
			start := exactScanStart(qc, &xr, &sf, lay, budget)
			if start-gg0 < minGap || start >= 255 {
				continue
			}
			qc.reset(&xr, lay)

			var got, want granuleCoding
			codeGranule(&xr, budget, lay, &got, qc)
			codeGranuleLinear(&xr, budget, lay, &want)
			if got != want {
				t.Fatalf("%s budget %d: gain %d, oracle %d", s.name, budget, got.globalGain, want.globalGain)
			}

			filled := 0
			for k := range qSpan {
				if qc.valid[0][k>>6]>>(k&63)&1 != 0 {
					filled++
				}
			}
			if filled >= start-gg0 {
				t.Errorf("%s budget %d: %d band-0 entries filled for a scan start %d above minGlobalGain %d: the scan did not start at the bound",
					s.name, budget, filled, start, gg0)
			}
			qualified++
		}
	}
	if qualified == 0 {
		t.Fatal("no sweep case has a scan start at least 40 gains above minGlobalGain")
	}
}

// TestOuterLoopResetsCacheOnEntry runs outerLoop twice on one cache and one xr
// address with different contents, as the Encoder does every frame, and
// requires the second result to equal a run on a fresh cache: a cache that
// outerLoop did not invalidate would serve the first spectrum's bands.
func TestOuterLoopResetsCacheOnEntry(t *testing.T) {
	lay := &layoutLong[0]
	var xmin [39]float64
	for s := range 22 {
		xmin[s] = 1e6
	}
	var xr [576]float64
	qc := new(quantCache)
	for _, seed := range []uint64{71, 72} {
		s := seed
		for i := range xr {
			xr[i] = (testsignal.LCG(&s) - 0.5) * 2000
		}
		var gc, best, fresh, freshBest granuleCoding
		outerLoop(&xr, &xmin, 1500, lay, &gc, &best, qc)
		outerLoop(&xr, &xmin, 1500, lay, &fresh, &freshBest, new(quantCache))
		if gc != fresh {
			t.Fatalf("seed %d: outerLoop on a reused cache gave gain %d, a fresh cache gave %d", seed, gc.globalGain, fresh.globalGain)
		}
	}
}

// TestQuantCacheCheckPanics requires codeGranule to refuse a cache that was
// reset for another spectrum or layout instead of reading its entries.
func TestQuantCacheCheckPanics(t *testing.T) {
	var a, b [576]float64
	for i := range a {
		a[i] = float64(i%17) * 100
		b[i] = a[i]
	}
	lay := &layoutLong[0]
	panics := func(f func()) (p bool) {
		defer func() { p = recover() != nil }()
		f()
		return false
	}
	qc := new(quantCache)
	qc.reset(&a, lay)
	var gc granuleCoding
	if panics(func() { codeGranule(&a, 3000, lay, &gc, qc) }) {
		t.Fatal("codeGranule panicked on the spectrum and layout the cache was reset for")
	}
	if !panics(func() { codeGranule(&b, 3000, lay, &gc, qc) }) {
		t.Error("codeGranule accepted a cache reset for a different spectrum")
	}
	if !panics(func() { codeGranule(&a, 3000, &layoutLong[1], &gc, qc) }) {
		t.Error("codeGranule accepted a cache reset for a different layout")
	}
}
