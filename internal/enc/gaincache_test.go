package enc

import (
	"math/bits"
	"slices"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// band0Fills counts the band-0 entries qc has filled since its last reset.
func band0Fills(qc *quantCache) int {
	n := 0
	for _, w := range qc.valid[0] {
		n += bits.OnesCount64(w)
	}
	return n
}

// TestExactScanFillsOnlyFromBoundStart pins that the production exact scan
// starts at the bound, not at minGlobalGain: the output is identical either
// way, so the oracle tests cannot tell. Without pruning the scan fills one
// band-0 cache entry per gain from minGlobalGain to the fitting gain; with it,
// only the bisection probes and the few steps after the start do. The scan
// start is computed on a cache of its own: a firstAdmissibleGain search that
// runs past its lower end leaves its answer as the hint for the next search,
// and the cold-start fill count only means something on a cache that has none.
func TestExactScanFillsOnlyFromBoundStart(t *testing.T) {
	const minGap = 40 // gains skipped; well above the ~8 probes plus the steps after the start
	lay := &layoutLong[0]
	qualified := 0
	for _, s := range bisectSweepSpectra() {
		for _, budget := range bisectBudgets {
			xr := s.xr
			var sf scfState
			probe := new(quantCache)
			probe.reset(&xr, lay)
			gg0 := minGlobalGain(&xr, &sf, lay)
			start := exactScanStart(probe, &xr, &sf, lay, budget)
			if start-gg0 < minGap || start >= 255 {
				continue
			}
			qc := new(quantCache)
			qc.reset(&xr, lay)

			var got, want granuleCoding
			codeGranule(&xr, budget, lay, &got, qc)
			codeGranuleLinear(&xr, budget, lay, &want)
			if got != want {
				t.Fatalf("%s budget %d: gain %d, oracle %d", s.name, budget, got.globalGain, want.globalGain)
			}

			if filled := band0Fills(qc); filled >= start-gg0 {
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

// TestOuterLoopResetsCacheOnEntry runs outerLoop on one cache and one xr
// address with different contents, as the Encoder does every frame, and
// requires every result to equal the linear oracle's coding of that spectrum:
// a cache that outerLoop did not invalidate would serve an earlier spectrum's
// bands or its band maxima, and the oracle (unlike a run on a fresh cache)
// does not share a cache's bug. The spectra are quiet apart from one loud line
// in band 2 whose magnitude changes by orders of magnitude from one to the
// next, so that band alone sets minGlobalGain for most of them and a stale band
// maximum (too high or too low) moves the coded gain. The noise thresholds are
// so loose that outerLoop stops after coding the zero scalefactor state once,
// which is the state the oracle codes.
func TestOuterLoopResetsCacheOnEntry(t *testing.T) {
	const budget = 4000
	lay := &layoutLong[0]
	var xmin [39]float64
	for s := range 22 {
		xmin[s] = 1e30
	}
	var xr [576]float64
	qc := new(quantCache)
	for i, loud := range []float64{3e5, 3e7, 3e3, 3e7, 0} {
		s := uint64(71 + i)
		for j := range xr {
			xr[j] = (testsignal.LCG(&s) - 0.5) * 40
		}
		xr[lay.width[0]+lay.width[1]] = loud
		var gc, best, want granuleCoding
		if iters := outerLoop(&xr, &xmin, budget, lay, &gc, &best, qc, nil); iters != 1 {
			t.Fatalf("spectrum %d: outerLoop ran %d iterations, the test needs the zero scalefactor state alone", i, iters)
		}
		codeGranuleLinear(&xr, budget, lay, &want)
		if gc.globalGain != want.globalGain || gc.ix != want.ix {
			t.Fatalf("spectrum %d (loud line %g): outerLoop on a reused cache gave gain %d, the linear oracle %d (minGlobalGain %d)",
				i, loud, gc.globalGain, want.globalGain, minGlobalGain(&xr, &want.sf, lay))
		}
	}
}

// TestQuantCacheCheckPanics requires codeGranule to refuse a cache that was
// reset for another spectrum or layout instead of reading its entries, and
// codingMemo.codeOrRecall to refuse a memo begun for another spectrum or
// layout.
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

	memo := new(codingMemo)
	memo.begin(&a, lay)
	var noise [39]float64
	if panics(func() { memo.codeOrRecall(&a, 3000, lay, &gc, qc, &noise) }) {
		t.Fatal("codeOrRecall panicked on the spectrum and layout the memo was begun for")
	}
	if !panics(func() { memo.codeOrRecall(&b, 3000, lay, &gc, qc, &noise) }) {
		t.Error("codeOrRecall accepted a memo begun for a different spectrum")
	}
	if !panics(func() { memo.codeOrRecall(&a, 3000, &layoutLong[1], &gc, qc, &noise) }) {
		t.Error("codeOrRecall accepted a memo begun for a different layout")
	}
}

// warmStartCall is one firstAdmissibleGain search: a scalefactor state and an
// effective budget.
type warmStartCall struct {
	sf     scfState
	budget int
}

// warmStartCalls returns searches over xr and lay whose answers span the
// gains: for each state, budgets equal to the bound at minGlobalGain, one
// above it, a third and two thirds of the way up, and 254 (each admits its
// target gain, clamped to 254, and every later one), one less than the bound at
// 254 (which admits nothing and leaves the 255 sentinel), and zero. ref is a
// cache reset for xr and lay that is never searched.
func warmStartCalls(ref *quantCache, xr *[576]float64, lay *bandLayout, sfs []scfState) []warmStartCall {
	const unbounded = 1 << 30
	var calls []warmStartCall
	for i := range sfs {
		sf := sfs[i]
		q0 := bandExponentBase(&sf, lay)
		gg0 := minGlobalGain(xr, &sf, lay)
		for _, target := range []int{gg0, gg0 + 1, gg0 + (254-gg0)/3, gg0 + 2*(254-gg0)/3, 254} {
			target = min(max(target, gg0), 254)
			calls = append(calls, warmStartCall{sf, ref.boundAt(target, &q0, lay, unbounded)})
		}
		if b := ref.boundAt(254, &q0, lay, unbounded); b > 0 {
			calls = append(calls, warmStartCall{sf, b - 1})
		}
		calls = append(calls, warmStartCall{sf, 0})
	}
	return calls
}

// TestFirstAdmissibleGainWarmStart runs one cache through sequences of
// (scalefactor state, budget) searches whose answers go up, down, stay equal,
// land on the search's own lower end, on 254 and on the 255 sentinel, and
// requires each answer to equal the first gain at or above the lower end whose
// bound fits, found by a linear scan on a cache that is never searched (so no
// hint can steer it). The carried-over hint decides only which gains a search
// probes first; a gallop that stops a step early or late, or a hint outside
// the search interval, changes the answer here.
func TestFirstAdmissibleGainWarmStart(t *testing.T) {
	// scan is the oracle: the first gg in [gg0, 254] whose bound fits, else 255.
	scan := func(ref *quantCache, gg0, budget int, q0 *[39]int, lay *bandLayout) int {
		for gg := gg0; gg < 255; gg++ {
			if ref.boundAt(gg, q0, lay, budget) <= budget {
				return gg
			}
		}
		return 255
	}

	// The zero state, a gradient with a doubled step, and subblock gain.
	all := bisectSweepSFStates()
	sfs := []scfState{all[0].sf, all[3].sf, all[5].sf}

	var sawUp, sawDown, sawSame, sawGG0, saw254, saw255 bool
	for _, lay := range []*bandLayout{&layoutLong[0], &layoutShort[1]} {
		for _, s := range bisectSweepSpectra() {
			xr := s.xr
			ref := new(quantCache)
			ref.reset(&xr, lay)
			calls := warmStartCalls(ref, &xr, lay, sfs)

			// Three passes over the same calls on one cache: as built,
			// reversed, and shuffled.
			order := make([]int, len(calls))
			for i := range order {
				order[i] = i
			}
			reversed := slices.Clone(order)
			slices.Reverse(reversed)
			shuffled := slices.Clone(order)
			seed := uint64(len(calls)) + 5
			for i := len(shuffled) - 1; i > 0; i-- {
				j := int(testsignal.LCG(&seed) * float64(i+1))
				shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
			}

			qc := new(quantCache)
			prev := -1
			for pass, idx := range [][]int{order, reversed, shuffled} {
				for n, ci := range idx {
					c := calls[ci]
					if n%7 == 0 {
						// outerLoop resets the cache on entry; the hint stays.
						qc.reset(&xr, lay)
					}
					q0 := bandExponentBase(&c.sf, lay)
					gg0 := minGlobalGain(&xr, &c.sf, lay)
					got := qc.firstAdmissibleGain(gg0, c.budget, &q0, lay)
					if want := scan(ref, gg0, c.budget, &q0, lay); got != want {
						t.Fatalf("%s short=%v pass %d call %d (budget %d, gg0 %d): firstAdmissibleGain = %d, linear scan = %d",
							s.name, lay.short, pass, n, c.budget, gg0, got, want)
					}
					sawUp = sawUp || prev >= 0 && got > prev
					sawDown = sawDown || prev >= 0 && got < prev
					sawSame = sawSame || got == prev
					sawGG0 = sawGG0 || got == gg0 && gg0 < 254
					saw254 = saw254 || got == 254
					saw255 = saw255 || got == 255
					prev = got
				}
			}
		}
	}
	for name, saw := range map[string]bool{
		"an answer above the previous one": sawUp, "an answer below the previous one": sawDown,
		"an answer equal to the previous one": sawSame, "an answer at the lower end gg0": sawGG0,
		"the answer 254": saw254, "the 255 sentinel": saw255,
	} {
		if !saw {
			t.Errorf("no call produced %s", name)
		}
	}
}

// TestFirstAdmissibleGainProbeCounts counts the band-0 cache entries a search
// fills, which is the number of gains it probes. A search on a cache with no
// earlier answer must probe no more than a plain bisection of [gg0+1, 255]
// after the gg0 probe: the initial hint must not send it galloping from a gain
// outside that interval. The same search repeated after reset (which keeps the
// hint) must probe only gg0, the previous answer and one gain below it,
// fewer than the cold search. The answer is the same either way, so only the
// fill counts show whether the hint is stored, kept across reset and used.
func TestFirstAdmissibleGainProbeCounts(t *testing.T) {
	lay := &layoutLong[0]
	var sf scfState
	checked := 0
	for _, s := range bisectSweepSpectra() {
		for _, budget := range bisectBudgets {
			xr := s.xr
			qc := new(quantCache)
			qc.reset(&xr, lay)
			q0 := bandExponentBase(&sf, lay)
			gg0 := minGlobalGain(&xr, &sf, lay)
			eff := min(budget, maxPart23Length)
			got := qc.firstAdmissibleGain(gg0, eff, &q0, lay)
			if got == gg0 || got == 255 {
				continue // answered by the gg0 probe alone, or the sentinel, which a hint never steers to
			}
			cold := band0Fills(qc)
			if limit := 1 + bits.Len(uint(255-gg0)); cold > limit {
				t.Errorf("%s budget %d: %d band-0 entries filled by a cold search over [%d, 255], a plain bisection needs at most %d",
					s.name, budget, cold, gg0+1, limit)
			}

			qc.reset(&xr, lay)
			if again := qc.firstAdmissibleGain(gg0, eff, &q0, lay); again != got {
				t.Fatalf("%s budget %d: the repeated search answered %d, the first %d", s.name, budget, again, got)
			}
			if warm := band0Fills(qc); warm > 3 || warm >= cold {
				t.Errorf("%s budget %d: %d band-0 entries filled by a search that repeats the previous answer after reset, want at most 3 and fewer than the cold search's %d",
					s.name, budget, warm, cold)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no sweep case needed a search past gg0")
	}
}
