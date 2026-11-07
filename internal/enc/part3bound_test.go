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

// TestMonoPairFloorTable pins the premises of the monotone floor exhaustively
// over the table: it is below the cheapest cost of every pair up to maxQuant
// (magnitude classes are exact), non-decreasing in both indices, and its
// count1 charge is below what a count1 quad costs.
func TestMonoPairFloorTable(t *testing.T) {
	checkMonoFloorBelowPairMin(t)
	checkMonoFloorMonotone(t)
	checkCount1Premise(t)
}

// checkMonoFloorBelowPairMin covers every pair up to maxQuant: a magnitude
// class is as good as its members, and the floor never exceeds the pair's
// cheapest cost.
func checkMonoFloorBelowPairMin(t *testing.T) {
	t.Helper()
	for x := range int32(maxQuant + 1) {
		cx := int(magClassOf(x))
		if rep := magClassRep(cx); magClassOf(rep) != uint8(cx) || rep > x {
			t.Fatalf("magClassOf(%d) = %d but class %d has representative %d", x, cx, cx, rep)
		}
		if x > 0 && magClassOf(x) < magClassOf(x-1) {
			t.Fatalf("magClassOf not non-decreasing at %d", x)
		}
		for y := range int32(maxQuant + 1) {
			cy := int(magClassOf(y))
			want := gotPairMin(x, y)
			if rep := gotPairMin(magClassRep(cx), magClassRep(cy)); rep != want {
				t.Fatalf("pair (%d,%d) costs %d but its class representatives (%d,%d) cost %d",
					x, y, want, magClassRep(cx), magClassRep(cy), rep)
			}
			floor := int(monoPairFloor[cx][cy])
			if floor > want {
				t.Fatalf("floor[%d][%d] = %d exceeds the pair minimum %d at (%d,%d)", cx, cy, floor, want, x, y)
			}
			// A pair of at most 1 can sit in a count1 quad: it is charged its
			// nonzero count plus 2, and 0 when both lines are zero.
			if x <= 1 && y <= 1 {
				signs := int(x + y)
				if signs == 0 && floor != 0 {
					t.Fatalf("floor[0][0] = %d, want 0 (rzero pairs cost nothing)", floor)
				}
				if signs > 0 && floor > signs+2 {
					t.Fatalf("floor[%d][%d] = %d exceeds the count1 charge %d", cx, cy, floor, signs+2)
				}
			}
		}
	}
	if got := magClassOf(maxQuant); got != nMagClasses-1 {
		t.Fatalf("magClassOf(maxQuant) = %d, want %d", got, nMagClasses-1)
	}
}

// checkMonoFloorMonotone requires the floor to be non-decreasing in both
// indices, which makes the bound non-increasing in the gain once each line's
// magnitude is.
func checkMonoFloorMonotone(t *testing.T) {
	t.Helper()
	for cx := range nMagClasses {
		for cy := range nMagClasses {
			f := monoPairFloor[cx][cy]
			if cx+1 < nMagClasses && monoPairFloor[cx+1][cy] < f {
				t.Fatalf("floor decreases from [%d][%d]=%d to [%d][%d]=%d", cx, cy, f, cx+1, cy, monoPairFloor[cx+1][cy])
			}
			if cy+1 < nMagClasses && monoPairFloor[cx][cy+1] < f {
				t.Fatalf("floor decreases from [%d][%d]=%d to [%d][%d]=%d", cx, cy, f, cx, cy+1, monoPairFloor[cx][cy+1])
			}
		}
	}
}

// checkCount1Premise pins what the count1 charge assumes: a quad with a
// nonzero element costs at least 4 bits plus one per nonzero element under
// table A (table B is exactly 4 plus signs), and end to end the floors of a
// quad's two pairs never exceed what count1Cost charges for the quad.
func checkCount1Premise(t *testing.T) {
	t.Helper()
	for idx := 1; idx < 16; idx++ {
		if count1A[idx].len < 4 {
			t.Fatalf("count1A[%d] has length %d, below the 4 the floor assumes", idx, count1A[idx].len)
		}
	}
	for idx := range 16 {
		var ix [576]int32
		for i := range 4 {
			if idx>>(3-i)&1 != 0 {
				ix[i] = 1
			}
		}
		cost, _ := count1Cost(&ix, spectrumPartition{bigValues: 0, count1: 1})
		if floors := monoBoundOf(&ix); floors > cost {
			t.Fatalf("quad pattern %04b: pair floors %d exceed the count1 cost %d", idx, floors, cost)
		}
	}
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

	// monoRatioSum accumulates monoBound/ri.bits over the same codings, for
	// the sweeps that also check the monotone bound.
	monoRatioSum float64
}

// monoBoundOf is the monotone bound the exact scan bisects on, computed
// straight from a quantized granule: monoPairFloor summed over all 288 pairs.
func monoBoundOf(ix *[576]int32) int {
	sum := 0
	for p := 0; p < 576; p += 2 {
		sum += int(monoPairFloor[magClassOf(abs32(ix[p]))][magClassOf(abs32(ix[p+1]))])
	}
	return sum
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
	if s.monoRatioSum > 0 {
		monoMean := s.monoRatioSum / float64(s.codings)
		t.Logf("mean monotone bound/bits = %.3f", monoMean)
		// The monotone bound drives the scan's start; one that decays keeps
		// every identity test green while the bisection stops pruning.
		if monoMean < monoBoundMinTightness {
			t.Fatalf("mean monotone bound/bits = %.3f, below %.2f: the bisection no longer prunes", monoMean, monoBoundMinTightness)
		}
	}
}

// monoBoundMinTightness is the floor on the monotone bound's mean ratio to the
// real bits over TestPart3LowerBoundSound's corpus, which measures 0.83 (the
// pair floor without the count1 term measures about 0.7, a per-line floor about
// 0.55).
const monoBoundMinTightness = 0.73

func TestPart3LowerBoundSound(t *testing.T) {
	var stats boundStats
	var seed uint64 = 0x2545F4914F6CDD1D
	for _, blk := range bisectBlockTypes {
		for srIndex := range layoutLong {
			lay := layoutFor(blk.bt, srIndex)
			for iter := range 12 {
				amp := (float64(testsignal.LCG(&seed)*3) + 0.01) * 30000
				xr := probeSpectrum(&seed, iter%4, amp)
				var gc granuleCoding
				gc.lay = lay
				gc.blockType = blk.bt
				gc.sf = probeSF(&seed)
				prevMono := math.MaxInt
				for gg := minGlobalGain(&xr, &gc.sf, lay); gg <= 255; gg++ {
					quantizeGranule(&xr, gg, &gc.sf, lay, &gc.ix)
					gc.part = partitionSpectrum(&gc.ix)
					stats.checkCoding(t, blk.name, &gc)

					mono := monoBoundOf(&gc.ix)
					if full := part3LowerBound(&gc.ix, gc.part, math.MaxInt); mono > full {
						t.Fatalf("%s gg=%d: monotone bound %d exceeds part3LowerBound %d", blk.name, gg, mono, full)
					}
					if mono > prevMono {
						t.Fatalf("%s gg=%d: monotone bound rose from %d to %d", blk.name, gg, prevMono, mono)
					}
					prevMono = mono
					if ri := regionsFor(&gc.ix, gc.part, lay, gc.blockType); ri.bits > 0 {
						stats.monoRatioSum += float64(mono) / float64(ri.bits)
					}
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

// Programs of replaySignal; sigLoud is the full-scale noise one.
const (
	sigLoud   = "loud"
	sigNoise  = "noise"
	sigTones  = "tones"
	sigClicks = "clicks"
)

// replaySignal builds nFrames of planar input for a named program.
func replaySignal(name string, sampleRate, channels, nFrames int) [][]float32 {
	n := nFrames * testsignal.FrameSize
	planar := make([][]float32, channels)
	seed := uint64(sampleRate) ^ uint64(len(name))<<40
	for ch := range planar {
		switch name {
		case sigNoise, sigLoud:
			scale := float32(0.7)
			if name == sigLoud {
				scale = 1
			}
			planar[ch] = make([]float32, n)
			for i := range planar[ch] {
				planar[ch][i] = float32(testsignal.LCGSigned(&seed)) * scale
			}
		case sigTones:
			tone := testsignal.MultiTone(sampleRate, n, float64(ch), 0.6)
			planar[ch] = make([]float32, n)
			for i := range planar[ch] {
				planar[ch][i] = float32(tone[i]) + float32(0.02*float32(testsignal.LCGSigned(&seed))) // explicit conversion blocks FMA fusion, keeping the signal identical across architectures
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
		for _, sig := range []string{sigNoise, sigLoud, sigTones, sigClicks} {
			t.Run(fmt.Sprintf("%s/%d/%dch/%dk", sig, cfg.SampleRate, cfg.Channels, cfg.BitrateKbps), func(t *testing.T) {
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
						codeGranuleFresh(&xr, budget, g.gc.lay, &got, false)
						if got != want {
							t.Fatalf("granule %d budget %d: exact gg=%d bits=%d, oracle gg=%d bits=%d",
								i, budget, got.globalGain, got.ri.bits, want.globalGain, want.ri.bits)
						}
					}
					stats.checkCoding(t, "captured", &g.gc)
				}
				stats.requireUseful(t)
			})
		}
	}
}
