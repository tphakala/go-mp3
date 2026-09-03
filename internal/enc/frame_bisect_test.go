package enc

import (
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// codeGranuleLinear is a verbatim copy of the ORIGINAL codeGranule inner rate
// loop: the exact linear global_gain scan (gg++ from minGlobalGain until
// ri.bits fits, else 255) followed by the spectral-truncation fallback. It is
// the frozen oracle the production exact path (codeGranule with fast=false, via
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
	out := make([]sweepSpectrum, 0, 11)
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
	return out
}

// bisectSweepSFStates covers the scalefactor states codeGranule reads: the
// zero value, a doubled step (scalefac_scale=1), a per-band scf gradient, and
// both combined. The gradient sets only the first 21 bands, which is valid for
// both the 21-sfb long layout and the 36-band short layout.
func bisectSweepSFStates() []sweepSF {
	var grad scfState
	for b := range 21 {
		grad.scf[b] = b % 4
	}
	gradScale := grad
	gradScale.scalefacScale = 1
	return []sweepSF{
		{"zero", scfState{}},
		{"scale1", scfState{scalefacScale: 1}},
		{"gradient", grad},
		{"gradient_scale1", gradScale},
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

var bisectBudgets = []int{100, 120, 300, 800, 2000, 3500, 4095, 5676}

// TestCodeGranuleExactMatchesOracle is the golden-neutrality guard for the
// DEFAULT rate-control mode (RateControlExact): production codeGranule with
// fast=false must produce a byte-identical granuleCoding to the frozen linear
// oracle across block type x sample-rate layout x signal shape x scalefactor
// state x budget (tight, forcing the truncation fallback, through generous).
// Unlike the earlier long-block-only sweep, this now also drives the
// short/start/stop chooseRegionsWS path, so the exact path is guarded on every
// block type the live encoder emits.
func TestCodeGranuleExactMatchesOracle(t *testing.T) {
	spectra := bisectSweepSpectra()
	sfStates := bisectSweepSFStates()

	total := 0
	for _, blk := range bisectBlockTypes {
		for srIndex := range layoutLong {
			lay := layoutFor(blk.bt, srIndex)
			for _, s := range spectra {
				for _, st := range sfStates {
					for _, budget := range bisectBudgets {
						xr := s.xr

						var want, got granuleCoding
						want.blockType, got.blockType = blk.bt, blk.bt
						want.sf, got.sf = st.sf, st.sf
						codeGranuleLinear(&xr, budget, lay, &want)
						codeGranule(&xr, budget, lay, &got, false)

						total++
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
	t.Logf("verified exact == oracle over %d (block x layout x signal x sf x budget) cases", total)
}

// TestCodeGranuleFastContract checks the opt-in RateControlFast path against
// the exact path over the same sweep. The fast (binary) search is not
// bit-identical, so instead of equality it asserts the three properties that
// make it a safe speed/quality trade: every fast coding still fits the budget
// (codeGranule's own guarantee), the fast global_gain is never FINER than the
// exact one (it can only settle equal or coarser, so it never improves quality
// by accident and never overflows), and where the two land on the same gg the
// coding is byte-identical. Divergences are expected and counted;
// TestCodeGranuleFastDivergence characterizes their magnitude.
func TestCodeGranuleFastContract(t *testing.T) {
	spectra := bisectSweepSpectra()
	sfStates := bisectSweepSFStates()

	total, diverged := 0, 0
	for _, blk := range bisectBlockTypes {
		for srIndex := range layoutLong {
			lay := layoutFor(blk.bt, srIndex)
			for _, s := range spectra {
				for _, st := range sfStates {
					for _, budget := range bisectBudgets {
						eff := min(budget, maxPart23Length)
						xr := s.xr

						var exactGC, fastGC granuleCoding
						exactGC.blockType, fastGC.blockType = blk.bt, blk.bt
						exactGC.sf, fastGC.sf = st.sf, st.sf
						codeGranule(&xr, budget, lay, &exactGC, false)
						codeGranule(&xr, budget, lay, &fastGC, true)

						total++
						if fastGC.ri.bits > eff {
							t.Fatalf("fast overflowed budget at block=%s sr=%d signal=%s sf=%s budget=%d: ri.bits=%d > eff=%d",
								blk.name, srIndex, s.name, st.name, budget, fastGC.ri.bits, eff)
						}
						if fastGC.globalGain < exactGC.globalGain {
							t.Fatalf("fast selected FINER gain than exact at block=%s sr=%d signal=%s sf=%s budget=%d: fast=%d exact=%d",
								blk.name, srIndex, s.name, st.name, budget, fastGC.globalGain, exactGC.globalGain)
						}
						if fastGC.globalGain == exactGC.globalGain && fastGC != exactGC {
							t.Fatalf("fast and exact share gg=%d but differ at block=%s sr=%d signal=%s sf=%s budget=%d",
								fastGC.globalGain, blk.name, srIndex, s.name, st.name, budget)
						}
						if fastGC.globalGain != exactGC.globalGain {
							diverged++
						}
					}
				}
			}
		}
	}
	t.Logf("fast contract held over %d cases; fast diverged from exact (coarser gg) on %d", total, diverged)
}
