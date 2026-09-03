package enc

import (
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// linearFirstFit models RateControlExact's selection over a precomputed bits
// table: the smallest gg in [gg0,255] with bits(gg) <= budget scanning upward,
// else 255. bits is indexed by gg-gg0.
func linearFirstFit(bits []int, gg0, budget int) int {
	for gg := gg0; gg < 255; gg++ {
		if bits[gg-gg0] <= budget {
			return gg
		}
	}
	return 255
}

// bisectFirstFit models RateControlFast's selection: the binary-search
// lower_bound over the same table. gg0 already fitting, or gg0 already at 255,
// returns gg0, which mirrors codeGranule's `gc.ri.bits <= effBudget || gg >= 255`
// short-circuit in searchGlobalGainFast, so the model agrees with production at
// that boundary rather than running off the end of the table. When the
// lower_bound lands on a non-fitting gg (a top-of-range bump, or nothing fits),
// production falls back to the exact scan, so the model returns linearFirstFit
// there too.
func bisectFirstFit(bits []int, gg0, budget int) int {
	if bits[0] <= budget || gg0 >= 255 {
		return gg0
	}
	lo, hi := gg0+1, 255
	for lo < hi {
		mid := (lo + hi) / 2
		if bits[mid-gg0] <= budget {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	if bits[lo-gg0] > budget {
		return linearFirstFit(bits, gg0, budget)
	}
	return lo
}

// rawLowerBound is the binary-search lower_bound WITHOUT searchGlobalGainFast's
// non-fitting fallback: the pre-fix behavior. It exists only so the probe can
// spot the top-of-range-bump cases where the raw search lands on a non-fitting
// gg (rawLowerBound != bisectFirstFit), which are exactly the cases the fix
// recovers.
func rawLowerBound(bits []int, gg0, budget int) int {
	if bits[0] <= budget || gg0 >= 255 {
		return gg0
	}
	lo, hi := gg0+1, 255
	for lo < hi {
		mid := (lo + hi) / 2
		if bits[mid-gg0] <= budget {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

// probeSpectrum builds one randomized signal of the given shape at scale amp.
func probeSpectrum(seed *uint64, shape int, amp float64) [576]float64 {
	switch shape {
	case 0: // full-scale low-frequency taper
		return fullScaleSpectrum(seed, amp)
	case 1: // flat broadband
		return flatSpectrum(seed, amp)
	case 2: // tonal: energy in a few bands (the count1-boundary culprit)
		return tonalSpectrum(seed, amp)
	default: // random sparse: each line kept with prob ~0.3
		var xr [576]float64
		for i := range xr {
			if testsignal.LCG(seed) < 0.3 {
				xr[i] = testsignal.LCGSigned(seed) * amp
			}
		}
		return xr
	}
}

// probeSF draws a randomized scalefactor state: scalefac_scale and a random
// per-band scf profile, independently.
func probeSF(seed *uint64) scfState {
	var sf scfState
	if testsignal.LCG(seed) < 0.5 {
		sf.scalefacScale = 1
	}
	if testsignal.LCG(seed) < 0.5 {
		for b := range 21 {
			sf.scf[b] = int(testsignal.LCG(seed) * 4)
		}
	}
	return sf
}

// sweepBits returns bits(gg) for gg in [gg0, 255], gg0 = minGlobalGain, coding
// each gg through recode under the given block type (so blockShort/start/stop
// exercise chooseRegionsWS, blockLong chooseRegions).
func sweepBits(xr *[576]float64, sf *scfState, lay *bandLayout, blockType int) (gg0 int, bits []int) {
	gg0 = minGlobalGain(xr, sf, lay)
	bits = make([]int, 0, 256-gg0)
	var gc granuleCoding
	gc.sf = *sf
	gc.lay = lay
	gc.blockType = blockType
	for gg := gg0; gg <= 255; gg++ {
		recode(xr, gg, lay, &gc)
		bits = append(bits, gc.ri.bits)
	}
	return gg0, bits
}

// probeStats accumulates monotonicity and fast-vs-exact selection statistics.
type probeStats struct {
	inputs        int
	bumpInputs    int
	bumpCount     int
	worstBump     int
	budgetCheck   int
	diverged      int // model: fast selected a coarser gg than exact
	worstGGDelta  int // largest fast_gg - exact_gg
	finerViol     int // model: fast selected a FINER gg than exact (must stay 0)
	prodDiverged  int // production codeGranule(fast) confirmed coarser than exact
	recovered     int // model: a top-of-range bump the non-fitting fallback fixed
	prodRecovered int // production confirmed the fallback restored the exact gg
}

// countBumps records the non-monotonicity of one bits(gg) sweep.
func (s *probeStats) countBumps(bits []int) {
	s.inputs++
	hadBump := false
	for k := 1; k < len(bits); k++ {
		if d := bits[k] - bits[k-1]; d > 0 {
			s.bumpCount++
			hadBump = true
			if d > s.worstBump {
				s.worstBump = d
			}
		}
	}
	if hadBump {
		s.bumpInputs++
	}
}

// checkSelection compares the fast and exact global_gain selection for one
// (input, budget). Only two shapes need a real codeGranule re-run: a divergence
// (fast settles coarser than exact, rawGG == fastGG) and a top-bump recovery (a
// bump near 255 made the raw lower_bound land on a non-fitting gg and the
// fallback restored the exact choice, rawGG != fastGG == exactGG). For those it
// runs both modes and asserts the contract (model matches production, never
// finer, still fits, and a recovery reproduces the exact coding), recording the
// outcome in s.
func (s *probeStats) checkSelection(t *testing.T, blkName string, bt, srIndex int, lay *bandLayout, xr *[576]float64, sf *scfState, gg0 int, bits []int, budget int) {
	t.Helper()
	eff := min(budget, maxPart23Length)
	s.budgetCheck++
	exactGG := linearFirstFit(bits, gg0, eff)
	fastGG := bisectFirstFit(bits, gg0, eff)
	rawGG := rawLowerBound(bits, gg0, eff)
	if fastGG < exactGG {
		s.finerViol++
		return
	}
	isDiverge := fastGG > exactGG
	isRecovery := rawGG != fastGG
	if !isDiverge && !isRecovery {
		return
	}

	var ex, fa granuleCoding
	ex.blockType, fa.blockType = bt, bt
	ex.sf, fa.sf = *sf, *sf
	x1, x2 := *xr, *xr
	codeGranule(&x1, budget, lay, &ex, false)
	codeGranule(&x2, budget, lay, &fa, true)
	if ex.globalGain != exactGG || fa.globalGain != fastGG {
		t.Fatalf("model/production disagree at blk=%s sr=%d budget=%d: model exact=%d fast=%d, production exact=%d fast=%d",
			blkName, srIndex, budget, exactGG, fastGG, ex.globalGain, fa.globalGain)
	}
	if fa.globalGain < ex.globalGain {
		t.Fatalf("production fast selected a FINER gg than exact at blk=%s sr=%d budget=%d: fast=%d exact=%d",
			blkName, srIndex, budget, fa.globalGain, ex.globalGain)
	}
	if fa.ri.bits > eff {
		t.Fatalf("production fast overflowed budget at blk=%s sr=%d budget=%d: ri.bits=%d > %d",
			blkName, srIndex, budget, fa.ri.bits, eff)
	}

	if isDiverge {
		s.diverged++
		if d := fastGG - exactGG; d > s.worstGGDelta {
			s.worstGGDelta = d
		}
		s.prodDiverged++
	}
	if isRecovery {
		if fa.globalGain != ex.globalGain {
			t.Fatalf("production fast did not recover the exact gg on a top bump at blk=%s sr=%d budget=%d rawGG=%d: fast=%d exact=%d",
				blkName, srIndex, budget, rawGG, fa.globalGain, ex.globalGain)
		}
		s.recovered++
		s.prodRecovered++
	}
}

// TestCodeGranuleFastDivergence characterizes RateControlFast's divergence from
// RateControlExact: it sweeps bits(gg) over the full gg range for a randomized
// corpus (full-scale, flat, tonal, and sparse signals, both scalefac_scale
// settings, random scalefactors) across all four block types, measures how
// non-monotone bits(gg) is (the bumps that make the fast binary search
// approximate), and compares the fast lower_bound against the exact first-fit
// over a spread of budgets. bits(gg) is genuinely not monotone, so the fast
// search does diverge, and on every budget where the model says it diverges
// this re-runs the REAL codeGranule (both modes) on that exact input and
// asserts production diverges the same way and honors its contract there
// (matches the model, never finer, still fits), so the production fast search
// is guarded on inputs it actually diverges on, not only where it equals exact.
// The guarantees checked: fast never selects a FINER gain than exact
// (finerViol == 0), the corpus stays non-trivial (bumpInputs > 0), and both the
// model and production divergence paths are actually exercised (diverged > 0
// and prodDiverged > 0, so the test cannot pass vacuously). The divergence rate
// and worst gg delta are logged as the standing record of the fast mode's cost.
func TestCodeGranuleFastDivergence(t *testing.T) {
	if testing.Short() {
		t.Skip("full gg sweep; skipped under -short")
	}
	// Dense budgets so a non-monotone bump that straddles some budget is
	// actually hit, which is what makes the fast search diverge and what the
	// production checks below need to fire.
	budgets := make([]int, 0, 210)
	for b := 60; b <= 4080; b += 20 {
		budgets = append(budgets, b)
	}

	var stats probeStats
	var seed uint64 = 0x9E3779B97F4A7C15
	for _, blk := range bisectBlockTypes {
		for srIndex := range layoutLong {
			lay := layoutFor(blk.bt, srIndex)
			for iter := range 90 {
				amp := (testsignal.LCG(&seed)*3 + 0.01) * 30000
				xr := probeSpectrum(&seed, iter%4, amp)
				sf := probeSF(&seed)
				gg0, bits := sweepBits(&xr, &sf, lay, blk.bt)
				stats.countBumps(bits)

				for _, budget := range budgets {
					stats.checkSelection(t, blk.name, blk.bt, srIndex, lay, &xr, &sf, gg0, bits, budget)
				}
			}
		}
	}

	t.Logf("inputs=%d  bumpInputs=%d (%.1f%%)  bumpCount=%d  worstBump=%d bits",
		stats.inputs, stats.bumpInputs, 100*float64(stats.bumpInputs)/float64(stats.inputs),
		stats.bumpCount, stats.worstBump)
	t.Logf("selection checks=%d  diverged=%d (%.3f%%)  worst gg delta=%d  prod divergences verified=%d  top-bump recoveries verified=%d",
		stats.budgetCheck, stats.diverged, 100*float64(stats.diverged)/float64(stats.budgetCheck), stats.worstGGDelta, stats.prodDiverged, stats.prodRecovered)

	if stats.finerViol > 0 {
		t.Errorf("fast selected a FINER gg than exact in %d cases: the never-finer contract is broken", stats.finerViol)
	}
	if stats.bumpInputs == 0 {
		t.Fatal("corpus produced no bits(gg) bumps: the divergence probe is vacuous, regenerate the corpus")
	}
	if stats.diverged == 0 {
		t.Fatal("no fast/exact divergence found: the probe never hit a budget-straddling bump, widen the corpus")
	}
	if stats.prodDiverged == 0 {
		t.Fatal("no production divergence verified: the fast search contract was never checked on a real divergence")
	}
	// Top-of-range-bump recoveries are corpus-fragile (they need minGlobalGain
	// high with a fit only just below 255), so this probe only reports them and
	// verifies any it hits; the hard guard that the fallback recovers rather
	// than truncates is TestCodeGranuleFastRecoversTopBump.
	t.Logf("top-of-range-bump recoveries hit and verified by the probe: %d", stats.prodRecovered)
}

// TestProbeModelsMatchProduction ties the cheap table-based models
// (linearFirstFit / bisectFirstFit) to the real codeGranule search, so the
// dense characterization in TestCodeGranuleFastDivergence cannot silently drift
// from production if searchGlobalGainExact/Fast change. For a small randomized
// set it confirms the model's selected gg equals codeGranule's globalGain in
// both modes, on both a long and a short block layout.
func TestProbeModelsMatchProduction(t *testing.T) {
	budgets := []int{120, 400, 1200, 3000}
	var seed uint64 = 0xD1B54A32D192ED03
	for _, blk := range []int{blockLong, blockShort} {
		lay := layoutFor(blk, 0)
		for iter := range 40 {
			amp := (testsignal.LCG(&seed)*3 + 0.01) * 30000
			xr := probeSpectrum(&seed, iter%4, amp)
			sf := probeSF(&seed)
			gg0, bits := sweepBits(&xr, &sf, lay, blk)
			for _, budget := range budgets {
				eff := min(budget, maxPart23Length)

				var exactGC granuleCoding
				exactGC.blockType, exactGC.sf = blk, sf
				x1 := xr
				codeGranule(&x1, budget, lay, &exactGC, false)

				var fastGC granuleCoding
				fastGC.blockType, fastGC.sf = blk, sf
				x2 := xr
				codeGranule(&x2, budget, lay, &fastGC, true)

				// The models select a gg from the bits table; production applies
				// the same choice then may enter the truncation fallback (gg=255,
				// budget unmeetable), which does not change globalGain. So the
				// models must match globalGain whenever the exact choice fits, and
				// both agree on 255 when nothing fits.
				wantExact := linearFirstFit(bits, gg0, eff)
				wantFast := bisectFirstFit(bits, gg0, eff)
				if exactGC.globalGain != wantExact {
					t.Fatalf("exact model drift block=%d budget=%d: model=%d production=%d", blk, budget, wantExact, exactGC.globalGain)
				}
				if fastGC.globalGain != wantFast {
					t.Fatalf("fast model drift block=%d budget=%d: model=%d production=%d", blk, budget, wantFast, fastGC.globalGain)
				}
			}
		}
	}
}

// TestCodeGranuleFastRecoversTopBump is the regression for the top-of-range
// bump reported on PR #60: a granule whose bits(gg) fits at gg 253 but rises
// again at 254 and 255, so the plain binary-search lower_bound overshoots to a
// non-fitting 255 and, left alone, the truncation fallback would zero a
// scalefactor band even though gg 253 codes within budget. searchGlobalGainFast
// must instead recover the exact gg. This pins a concrete such granule (a single
// very loud short-block line, minGlobalGain 206) and asserts the fast coding
// equals the exact coding and fits, plus a positive control that the case
// really is a top bump the raw lower_bound mishandles, so it cannot pass
// vacuously if the corpus or region costs shift.
func TestCodeGranuleFastRecoversTopBump(t *testing.T) {
	lay := layoutFor(blockShort, 0)
	const budget = 19

	// Reconstruct the deterministic granule: one loud line, scalefac_scale=1.
	seed := uint64(188)*2654435761 + 12345
	var xr [576]float64
	idx := int(testsignal.LCG(&seed) * 60)
	xr[idx] = testsignal.LCGSigned(&seed) * 3e6
	var sf scfState
	if testsignal.LCG(&seed) < 0.5 {
		sf.scalefacScale = 1
	}

	// Positive control: confirm this is genuinely a top-of-range bump, so the
	// raw lower_bound lands on a non-fitting gg while a fit exists below 255.
	gg0 := minGlobalGain(&xr, &sf, lay)
	bits := make([]int, 0, 256-gg0)
	var probe granuleCoding
	probe.sf, probe.lay, probe.blockType = sf, lay, blockShort
	for gg := gg0; gg <= 255; gg++ {
		recode(&xr, gg, lay, &probe)
		bits = append(bits, probe.ri.bits)
	}
	raw := rawLowerBound(bits, gg0, budget)
	exactSel := linearFirstFit(bits, gg0, budget)
	if bits[raw-gg0] <= budget || exactSel >= 255 || bits[exactSel-gg0] > budget {
		t.Fatalf("input is no longer a top-of-range bump (gg0=%d rawGG=%d bits=%d exactGG=%d bits=%d); pick a new regression case",
			gg0, raw, bits[raw-gg0], exactSel, bits[exactSel-gg0])
	}

	// The fix: fast recovers the exact within-budget coding instead of
	// overshooting to 255 and truncating.
	var ex, fa granuleCoding
	ex.blockType, fa.blockType = blockShort, blockShort
	ex.sf, fa.sf = sf, sf
	x1, x2 := xr, xr
	codeGranule(&x1, budget, lay, &ex, false)
	codeGranule(&x2, budget, lay, &fa, true)

	if fa.globalGain != ex.globalGain {
		t.Fatalf("fast did not recover the exact gg on a top bump: fast globalGain=%d, exact=%d (the pre-fix search would truncate at 255)", fa.globalGain, ex.globalGain)
	}
	if fa != ex {
		t.Fatalf("recovered fast coding differs from exact at globalGain %d: the fallback must reproduce the exact coding exactly", fa.globalGain)
	}
	if fa.ri.bits > budget {
		t.Fatalf("recovered fast coding does not fit: ri.bits=%d > budget=%d", fa.ri.bits, budget)
	}
}
