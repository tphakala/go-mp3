package enc

import (
	"fmt"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// recode quantizes xr at gg under gc's scalefactor state, partitions the
// result, and chooses region boundaries, writing straight into gc: one step
// of the linear oracle below, through the whole-granule quantizer rather than
// the quantization cache.
func recode(xr *[576]float64, gg int, lay *bandLayout, gc *granuleCoding) {
	quantizeGranule(xr, gg, &gc.sf, lay, &gc.ix)
	gc.part = partitionSpectrum(&gc.ix)
	gc.ri = regionsFor(&gc.ix, gc.part, lay, gc.blockType)
}

// codeGranuleLinear is a verbatim copy of the ORIGINAL codeGranule inner rate
// loop: the exact linear global_gain scan (gg++ from minGlobalGain until
// ri.bits fits, else 255) followed by the spectral-truncation fallback. It is
// the frozen oracle the production search (codeGranule, via
// searchGlobalGainExact) is proven byte-identical against by
// TestCodeGranuleExactMatchesOracle, so a future refactor of the exact search
// cannot silently drift. Keep this function frozen; it is the oracle, not a
// second implementation to co-evolve.
func codeGranuleLinear(xr *[576]float64, budgetBits int, lay *bandLayout, gc *granuleCoding) {
	gc.lay = lay
	effBudget := min(budgetBits, maxPart23Length)

	gg := minGlobalGain(xr, &gc.sf, lay)
	recode(xr, gg, lay, gc)

	for gc.ri.bits > effBudget && gg < 255 {
		gg++
		recode(xr, gg, lay, gc)
	}

	for gc.ri.bits > effBudget && zeroTopSfb(&gc.ix, lay) {
		gc.part = partitionSpectrum(&gc.ix)
		gc.ri = regionsFor(&gc.ix, gc.part, lay, gc.blockType)
	}

	gc.globalGain = gg
	gc.part23Length = gc.part2Bits + gc.ri.bits
}

// flatSpectrum builds a broadband spectrum with roughly uniform per-line
// energy at scale amp: a flat magnitude profile stresses the big-values
// region search differently from fullScaleSpectrum's low-frequency taper.
func flatSpectrum(seed *uint64, amp float64) [576]float64 {
	var xr [576]float64
	for i := range xr {
		xr[i] = testsignal.LCGSigned(seed) * amp
	}
	return xr
}

// tonalSpectrum concentrates energy in a handful of low bands and leaves the
// rest near zero: a spectrum whose partition boundary (big_values/count1/rzero)
// is sensitive to gg, where a Huffman-granularity non-monotonicity in bits(gg)
// is most likely to surface.
func tonalSpectrum(seed *uint64, amp float64) [576]float64 {
	var xr [576]float64
	for _, i := range []int{0, 1, 2, 3, 8, 9, 20, 21, 40, 41} {
		xr[i] = testsignal.LCGSigned(seed) * amp
	}
	return xr
}

type sweepSpectrum struct {
	name string
	xr   [576]float64
}

type sweepSF struct {
	name string
	sf   scfState
}

// bisectSweepSpectra yields a deterministic spread of signal shapes at several
// amplitudes plus the all-zero spectrum: amplitude and shape together move
// where bits(gg) crosses the budget, the point the searches are compared on.
func bisectSweepSpectra() []sweepSpectrum {
	out := make([]sweepSpectrum, 0, 14)
	for _, amp := range []float64{30000, 8000, 1000, 100, 3} {
		seed := uint64(amp) + 1
		out = append(out, sweepSpectrum{"fullscale", fullScaleSpectrum(&seed, amp)})
	}
	for _, amp := range []float64{20000, 500, 30} {
		seed := uint64(amp) + 7
		out = append(out, sweepSpectrum{"flat", flatSpectrum(&seed, amp)})
	}
	for _, amp := range []float64{25000, 400} {
		seed := uint64(amp) + 13
		out = append(out, sweepSpectrum{"tonal", tonalSpectrum(&seed, amp)})
	}
	out = append(out, sweepSpectrum{"zero", [576]float64{}})

	// Extremes: a dense spectrum so loud that minGlobalGain sits at the top of
	// the range and the clamp engages, one so quiet that everything quantizes
	// to zero at most gains, and one very loud line over a quiet floor (the
	// shape whose bit count rises again at the top of the gain range).
	seed := uint64(99)
	loud := flatSpectrum(&seed, 20)
	loud[100] = 3e6
	return append(out,
		sweepSpectrum{"extreme", flatSpectrum(&seed, 1e9)},
		sweepSpectrum{"nearzero", flatSpectrum(&seed, 1e-4)},
		sweepSpectrum{"oneloud", loud})
}

// bisectSweepSFStates covers the scalefactor states codeGranule reads: the
// zero value, a doubled step (scalefac_scale=1), a per-band scf gradient, and
// both combined. The gradient sets only the first 21 bands, which is valid for
// both the 21-sfb long layout and the 36-band short layout. The last two add
// preflag (long) and subblock_gain (short), the other inputs of
// bandExtraQuarters.
func bisectSweepSFStates() []sweepSF {
	var grad scfState
	for b := range 21 {
		grad.scf[b] = b % 4
	}
	gradScale := grad
	gradScale.scalefacScale = 1
	preflag := grad
	preflag.preflag = 1 // long layouts only; short layouts ignore it
	var subblock scfState
	subblock.scf[0], subblock.scf[5], subblock.scf[20] = 3, 7, 2
	subblock.subblockGain = [3]int{0, 3, 7} // short layouts only
	return []sweepSF{
		{"zero", scfState{}},
		{"scale1", scfState{scalefacScale: 1}},
		{"gradient", grad},
		{"gradient_scale1", gradScale},
		{"gradient_preflag", preflag},
		{"subblock_gain", subblock},
	}
}

// bisectBlockTypes lists the four block shapes codeGranule handles. blockLong
// routes recode/regionsFor through chooseRegions; blockStart, blockShort, and
// blockStop route through chooseRegionsWS, a different region-selection
// primitive with its own bits(gg) shape. layoutFor pairs each with its coding
// layout (layoutShort for blockShort, layoutLong otherwise), exactly as the
// live encoder does.
var bisectBlockTypes = []struct {
	name string
	bt   int
}{
	{"long", blockLong},
	{"start", blockStart},
	{"short", blockShort},
	{"stop", blockStop},
}

var bisectBudgets = []int{0, 100, 120, 300, 800, 2000, 3500, 4095, 5676}

// TestCodeGranuleExactMatchesOracle is the golden-neutrality guard for the
// rate loop's global_gain search: production codeGranule must produce a
// byte-identical granuleCoding to the frozen linear oracle across block type x
// sample-rate layout x signal shape x scalefactor state x budget (tight,
// forcing the truncation fallback, through generous). Unlike the earlier long-block-only sweep, this now also drives the
// short/start/stop chooseRegionsWS path, so the exact path is guarded on every
// block type the live encoder emits.
func TestCodeGranuleExactMatchesOracle(t *testing.T) {
	spectra := bisectSweepSpectra()
	sfStates := bisectSweepSFStates()

	total, pruned := 0, 0
	for _, blk := range bisectBlockTypes {
		for srIndex := range layoutLong {
			lay := layoutFor(blk.bt, srIndex)
			for _, s := range spectra {
				xr := s.xr
				// One cache per spectrum and layout, shared by every
				// scalefactor state and budget below, so cached bands are read
				// back under different states and budgets than filled them.
				qc := new(quantCache)
				qc.reset(&xr, lay)
				for _, st := range sfStates {
					for _, budget := range bisectBudgets {
						var want, got granuleCoding
						want.blockType, got.blockType = blk.bt, blk.bt
						want.sf, got.sf = st.sf, st.sf
						codeGranuleLinear(&xr, budget, lay, &want)
						codeGranule(&xr, budget, lay, &got, qc)

						total++
						if exactScanStart(qc, &xr, &st.sf, lay, budget) > minGlobalGain(&xr, &st.sf, lay) {
							pruned++
						}
						if got != want {
							t.Fatalf("exact != oracle at block=%s sr=%d signal=%s sf=%s budget=%d:\n"+
								"  globalGain got=%d want=%d  ri.bits got=%d want=%d  ix equal=%v",
								blk.name, srIndex, s.name, st.name, budget,
								got.globalGain, want.globalGain, got.ri.bits, want.ri.bits, got.ix == want.ix)
						}
					}
				}
			}
		}
	}
	t.Logf("verified exact == oracle over %d (block x layout x signal x sf x budget) cases, %d with a pruned scan start", total, pruned)
	// The identity above would hold trivially if the bound never admitted a
	// later start than minGlobalGain; require the pruning to actually fire.
	if pruned < total/2 {
		t.Fatalf("the scan start was pruned in only %d of %d cases; the sweep no longer exercises the bisection", pruned, total)
	}
}

// exactScanStart returns the gain searchGlobalGainExact starts its scan at for
// xr under sf and budget, using qc (which must be reset for xr and lay).
func exactScanStart(qc *quantCache, xr *[576]float64, sf *scfState, lay *bandLayout, budget int) int {
	q0 := bandExponentBase(sf, lay)
	return qc.firstAdmissibleGain(minGlobalGain(xr, sf, lay), min(budget, maxPart23Length), &q0, lay)
}

// startTables are the floor tables TestExactScanStartSoundAndSensitive models
// the scan start with: the production table and three deliberately unsound
// ones. The mutants must be caught by the same corpus, which proves the corpus
// can tell an unsound bound from a sound one.
type startTables [4]*[nMagClasses][nMagClasses]uint8

const (
	tabProd = iota
	tabCount1
	tabScaled
	tabEscape
)

var startTableNames = [4]string{"production", "count1+1", "x5/4", "escape+1 per coordinate"}

func mutantStartTables() startTables {
	count1 := monoPairFloor
	for _, c := range [][2]int{{0, 1}, {1, 0}, {1, 1}} {
		count1[c[0]][c[1]]++
	}
	scaled := monoPairFloor
	for x := range scaled {
		for y := range scaled[x] {
			scaled[x][y] = uint8(int(scaled[x][y]) * 5 / 4)
		}
	}
	esc := monoPairFloor
	for x := range esc {
		for y := range esc[x] {
			// One extra bit per escaped coordinate (a linbits miscount).
			if x > escMaxDirect {
				esc[x][y]++
			}
			if y > escMaxDirect {
				esc[x][y]++
			}
		}
	}
	return startTables{&monoPairFloor, &count1, &scaled, &esc}
}

// startStats counts what TestExactScanStartSoundAndSensitive saw.
type startStats struct {
	cases    int
	detected [4]int // cases where the table's modelled start exceeds the oracle's gain
	tight    int    // cases where the production start equals the oracle's gain below 255
}

// check codes one case through the frozen linear oracle and compares the
// scan-start model of every table against the oracle's gain. The model is a
// linear scan over test-side quantization, independent of the production
// cache and bisection; it needs no monotonicity.
func (s *startStats) check(t *testing.T, tabs *startTables, qc *quantCache, xr *[576]float64, sf *scfState, blockType int, lay *bandLayout, budget int, ctx string) {
	t.Helper()
	var oracle granuleCoding
	oracle.sf, oracle.blockType = *sf, blockType
	codeGranuleLinear(xr, budget, lay, &oracle)
	gain := oracle.globalGain
	eff := min(budget, maxPart23Length)
	gg0 := minGlobalGain(xr, &oracle.sf, lay)

	// start[i] is the first gain in [gg0, min(gain, 254)] whose floor sum fits
	// the budget, 255 if gain is 255 and none does, else beyond (256).
	var start [4]int
	for i := range start {
		start[i] = -1
	}
	var ix [576]int32
	for gg := gg0; gg <= min(gain, 254); gg++ {
		quantizeGranule(xr, gg, &oracle.sf, lay, &ix)
		done := true
		for i, tab := range tabs {
			if start[i] >= 0 {
				continue
			}
			sum := 0
			for p := 0; p < 576; p += 2 {
				sum += int(tab[magClassOf(abs32(ix[p]))][magClassOf(abs32(ix[p+1]))])
			}
			if sum <= eff {
				start[i] = gg
			} else {
				done = false
			}
		}
		if done {
			break
		}
	}
	for i := range start {
		if start[i] < 0 {
			start[i] = 256
			if gain == 255 {
				start[i] = 255
			}
		}
	}

	s.cases++
	if got := exactScanStart(qc, xr, &oracle.sf, lay, budget); got != start[tabProd] {
		t.Fatalf("%s: production scan start %d, model of the production table %d", ctx, got, start[tabProd])
	}
	if start[tabProd] > gain {
		t.Fatalf("%s: scan start %d is above the oracle's gain %d: the bound is unsound", ctx, start[tabProd], gain)
	}
	if start[tabProd] == gain && gain < 255 {
		s.tight++
	}
	for i := tabCount1; i <= tabEscape; i++ {
		if start[i] > gain {
			s.detected[i]++
		}
	}
}

// require fails unless the corpus exposed each mutant table in lastMutant or
// earlier (the live-encoder replay passes tabScaled, because 32 to 128 kbps
// frames hold almost no escape magnitudes) and the production start was tight
// somewhere.
func (s *startStats) require(t *testing.T, lastMutant int) {
	t.Helper()
	t.Logf("%d cases; start == gain below 255 in %d; mutants detected: count1+1 %d, x5/4 %d, escape+1 %d",
		s.cases, s.tight, s.detected[tabCount1], s.detected[tabScaled], s.detected[tabEscape])
	for i := tabCount1; i <= lastMutant; i++ {
		if s.detected[i] == 0 {
			t.Errorf("the corpus never exposes the unsound %q table: it could not catch a bound that over-estimates", startTableNames[i])
		}
	}
	if s.tight == 0 {
		t.Error("the scan start never equals the oracle's gain below 255: an off-by-one start would go unnoticed")
	}
}

// TestExactScanStartSoundAndSensitive pins the property the bisection rests on,
// independently of the production search: the first gain the monotone bound
// admits is never above the linear scan's gain (soundness), equals the
// production start (the model is the definition), and the corpus is able to
// notice an unsound bound (sensitivity: three mutated tables each push the
// modelled start above the oracle's gain somewhere).
func TestExactScanStartSoundAndSensitive(t *testing.T) {
	tabs := mutantStartTables()
	var stats startStats

	spectra := bisectSweepSpectra()
	sfStates := bisectSweepSFStates()
	for _, blk := range bisectBlockTypes {
		for srIndex := range layoutLong {
			lay := layoutFor(blk.bt, srIndex)
			for _, s := range spectra {
				xr := s.xr
				qc := new(quantCache)
				qc.reset(&xr, lay)
				for _, st := range sfStates {
					for _, budget := range bisectBudgets {
						ctx := fmt.Sprintf("block=%s sr=%d signal=%s sf=%s budget=%d", blk.name, srIndex, s.name, st.name, budget)
						stats.check(t, &tabs, qc, &xr, &st.sf, blk.bt, lay, budget, ctx)
					}
				}
			}
		}
	}
	stats.require(t, tabEscape)

	// Spectra and scalefactor states a live encoder produces.
	if testing.Short() {
		return
	}
	var replay startStats
	for _, cfg := range []Config{
		{SampleRate: 44100, Channels: 2, BitrateKbps: 32},
		{SampleRate: 32000, Channels: 2, BitrateKbps: 64},
		{SampleRate: 44100, Channels: 2, BitrateKbps: 128},
	} {
		for _, sig := range []string{sigNoise, sigTones, sigClicks} {
			grs := captureReplayGranules(t, cfg, replaySignal(sig, cfg.SampleRate, cfg.Channels, 3), 3)
			for i := range grs {
				g := &grs[i]
				xr := g.xr
				qc := new(quantCache)
				qc.reset(&xr, g.gc.lay)
				base := g.gc.ri.bits
				for _, budget := range []int{base - 1, base, base + 16, base * 3 / 4, base / 2} {
					ctx := fmt.Sprintf("replay %s %d/%dk granule %d budget %d", sig, cfg.SampleRate, cfg.BitrateKbps, i, budget)
					replay.check(t, &tabs, qc, &xr, &g.gc.sf, g.gc.blockType, g.gc.lay, budget, ctx)
				}
			}
		}
	}
	replay.require(t, tabScaled)
}
