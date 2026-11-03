package enc

import (
	"fmt"
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// refPairMin is the oracle for the per-pair minimum: the cheapest valid table
// for the pair, costed with pairCost (the general entry point every other
// pair-cost helper is checked against).
func refPairMin(ax, ay int32) int {
	best := impossibleCost
	for _, t := range validBigTables {
		best = min(best, pairCost(bigTables[t], ax, ay))
	}
	return best
}

// gotPairMin is the per-pair minimum as part3LowerBound consumes it.
func gotPairMin(ax, ay int32) int {
	if ax < escTableDim && ay < escTableDim {
		return int(pairMinDirect[ax][ay])
	}
	return pairMinCostEsc(ax, ay)
}

func TestPairMinCostMatchesReference(t *testing.T) {
	check := func(ax, ay int32) {
		t.Helper()
		if got, want := gotPairMin(ax, ay), refPairMin(ax, ay); got != want {
			t.Fatalf("pair (%d,%d): bound uses %d, minimum over tables is %d", ax, ay, got, want)
		}
	}
	for x := range int32(200) {
		for y := range int32(200) {
			check(x, y)
		}
	}

	// Escape capacity boundaries of both families, crossed with small, marker
	// (15, 16) and large partners.
	partners := []int32{0, 1, 14, 15, 16, 199}
	for _, linb := range [][8]int{escFam16Linbits, escFam24Linbits} {
		for _, l := range linb {
			edge := int32(escMaxDirect) + int32(1)<<uint(l)
			for _, v := range []int32{edge - 1, edge} {
				for _, p := range partners {
					check(v, p)
					check(p, v)
					check(v, v)
				}
			}
		}
	}

	// maxQuant is the last codable magnitude; one beyond is impossible and
	// must not index past the linbits table.
	check(maxQuant, 0)
	check(0, maxQuant)
	check(maxQuant, maxQuant)
	for _, p := range partners {
		if got := pairMinCostEsc(maxQuant+1, p); got != impossibleCost {
			t.Fatalf("pairMinCostEsc(%d,%d) = %d, want impossibleCost", maxQuant+1, p, got)
		}
		if got := pairMinCostEsc(p, maxQuant+1); got != impossibleCost {
			t.Fatalf("pairMinCostEsc(%d,%d) = %d, want impossibleCost", p, maxQuant+1, got)
		}
	}
}

// boundStats tracks how tight part3LowerBound is over a corpus.
type boundStats struct {
	codings  int
	ratioSum float64
}

// checkCoding verifies the bound against regionsFor for the quantized granule
// in gc: lb <= ri.bits, and the early-exit result decides "over limit" like
// the full bound does.
func (s *boundStats) checkCoding(t *testing.T, where string, gc *granuleCoding) {
	t.Helper()
	ri := regionsFor(&gc.ix, gc.part, gc.lay, gc.blockType)
	lb := part3LowerBound(&gc.ix, gc.part, math.MaxInt)
	if lb > ri.bits {
		t.Fatalf("%s: bound %d exceeds region search bits %d", where, lb, ri.bits)
	}
	for _, limit := range []int{ri.bits - 1, ri.bits, lb - 1, lb} {
		if (part3LowerBound(&gc.ix, gc.part, limit) > limit) != (lb > limit) {
			t.Fatalf("%s: early exit at limit %d disagrees with the full bound %d", where, limit, lb)
		}
	}
	if ri.bits > 0 {
		s.codings++
		s.ratioSum += float64(lb) / float64(ri.bits)
	}
}

func (s *boundStats) requireUseful(t *testing.T) {
	t.Helper()
	if s.codings == 0 {
		t.Fatal("no nonzero codings were checked")
	}
	mean := s.ratioSum / float64(s.codings)
	t.Logf("checked %d codings, mean bound/bits = %.3f", s.codings, mean)
	// A bound that decays toward zero keeps every identity test green while
	// silently losing the speedup it exists for.
	if mean < 0.5 {
		t.Fatalf("mean bound/bits = %.3f, below 0.5: the bound is no longer tight enough to skip steps", mean)
	}
}

func TestPart3LowerBoundSound(t *testing.T) {
	var stats boundStats
	var seed uint64 = 0x2545F4914F6CDD1D
	for _, blk := range bisectBlockTypes {
		for srIndex := range layoutLong {
			lay := layoutFor(blk.bt, srIndex)
			for iter := range 12 {
				amp := (testsignal.LCG(&seed)*3 + 0.01) * 30000
				xr := probeSpectrum(&seed, iter%4, amp)
				var gc granuleCoding
				gc.lay = lay
				gc.blockType = blk.bt
				gc.sf = probeSF(&seed)
				for gg := minGlobalGain(&xr, &gc.sf, lay); gg <= 255; gg++ {
					quantizeGranule(&xr, gg, &gc.sf, lay, &gc.ix)
					gc.part = partitionSpectrum(&gc.ix)
					stats.checkCoding(t, blk.name, &gc)
				}
			}
		}
	}
	stats.requireUseful(t)
}

// replayGranule is one captured granule-channel of a live encoder run: its
// final spectrum and coding state, enough to rerun the rate loop on.
type replayGranule struct {
	xr [576]float64
	gc granuleCoding
}

// captureReplayGranules encodes planar samples (one slice per channel, a whole
// number of frames) and returns every granule-channel coded by a non-nil
// EncodeFrame call. The first call only fills the lookahead and codes nothing,
// so it is skipped; no capture follows the nil drain, which would leave only
// the trailing silence frame in the encoder's state.
func captureReplayGranules(t *testing.T, cfg Config, planar [][]float32, nFrames int) []replayGranule {
	t.Helper()
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var out []replayGranule
	var dst []byte
	frame := make([][]float32, cfg.Channels)
	for f := range nFrames {
		for ch := range frame {
			frame[ch] = planar[ch][f*testsignal.FrameSize : (f+1)*testsignal.FrameSize]
		}
		if dst, err = e.EncodeFrame(dst[:0], frame); err != nil {
			t.Fatalf("EncodeFrame %d: %v", f, err)
		}
		if f == 0 {
			continue
		}
		for g := range 2 {
			for ch := range cfg.Channels {
				out = append(out, replayGranule{xr: e.xr[g][ch], gc: e.gr[g][ch]})
			}
		}
	}
	return out
}

// replaySignal builds nFrames of planar input for a named program.
func replaySignal(name string, sampleRate, channels, nFrames int) [][]float32 {
	n := nFrames * testsignal.FrameSize
	planar := make([][]float32, channels)
	seed := uint64(sampleRate) ^ uint64(len(name))<<40
	for ch := range planar {
		switch name {
		case "noise":
			planar[ch] = make([]float32, n)
			for i := range planar[ch] {
				planar[ch][i] = float32(testsignal.LCGSigned(&seed)) * 0.7
			}
		case "loud":
			planar[ch] = make([]float32, n)
			for i := range planar[ch] {
				planar[ch][i] = float32(testsignal.LCGSigned(&seed))
			}
		case "tones":
			tone := testsignal.MultiTone(sampleRate, n, float64(ch), 0.6)
			planar[ch] = make([]float32, n)
			for i := range planar[ch] {
				planar[ch][i] = float32(tone[i]) + 0.02*float32(testsignal.LCGSigned(&seed))
			}
		default: // "clicks"
			planar[ch] = testsignal.ToneClick(sampleRate, n, 2, 1)
		}
	}
	return planar
}

// TestExactScanMatchesOracleOnEncoderGranules replays real encoder granules
// through production codeGranule (exact mode) and the frozen linear oracle at
// budgets around the coding the encoder chose, including slack above it: a
// skip that wrongly rejects a fitting gain shows up as a different coding.
// The synthetic sweeps in TestCodeGranuleExactMatchesOracle cannot reach the
// spectra and scalefactor states a live encoder produces.
func TestExactScanMatchesOracleOnEncoderGranules(t *testing.T) {
	if testing.Short() {
		t.Skip("replays encoder runs against the linear oracle; skipped under -short")
	}
	configs := []Config{
		{SampleRate: 44100, Channels: 2, BitrateKbps: 32},
		{SampleRate: 48000, Channels: 1, BitrateKbps: 32},
		{SampleRate: 32000, Channels: 2, BitrateKbps: 64},
		{SampleRate: 44100, Channels: 2, BitrateKbps: 128},
		{SampleRate: 48000, Channels: 1, BitrateKbps: 128},
		{SampleRate: 32000, Channels: 1, BitrateKbps: 320},
	}
	const nFrames = 4
	for _, cfg := range configs {
		for _, sig := range []string{"noise", "loud", "tones", "clicks"} {
			t.Run(sigName(cfg, sig), func(t *testing.T) {
				t.Parallel()
				planar := replaySignal(sig, cfg.SampleRate, cfg.Channels, nFrames)
				grs := captureReplayGranules(t, cfg, planar, nFrames)
				var stats boundStats
				for i := range grs {
					g := &grs[i]
					base := g.gc.ri.bits
					for _, budget := range []int{base - 1, base, base + 8, base + 16, base * 3 / 4, base / 2, 60} {
						xr := g.xr
						want := granuleCoding{sf: g.gc.sf, blockType: g.gc.blockType}
						got := want
						codeGranuleLinear(&xr, budget, g.gc.lay, &want)
						codeGranule(&xr, budget, g.gc.lay, &got, false)
						if got != want {
							t.Fatalf("granule %d budget %d: exact gg=%d bits=%d, oracle gg=%d bits=%d",
								i, budget, got.globalGain, got.ri.bits, want.globalGain, want.ri.bits)
						}
					}
					stats.checkCoding(t, "captured", &g.gc)
				}
			})
		}
	}
}

func sigName(cfg Config, sig string) string {
	return fmt.Sprintf("%s/%d/%dch/%dk", sig, cfg.SampleRate, cfg.Channels, cfg.BitrateKbps)
}
