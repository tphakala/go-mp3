package enc

import (
	"math"
	"testing"
)

func TestMainAreaBytes(t *testing.T) {
	// Hand-computed: 128kbps 44.1kHz stereo unpadded: frameLength 417,
	// header 4, side 32: area 381. Mono 32kbps 48kHz: frameLength 96,
	// side 17: area 75.
	if got := mainAreaBytes(9, 0, 0, 2); got != 381 {
		t.Errorf("area(128k,44.1,st) = %d, want 381", got)
	}
	if got := mainAreaBytes(1, 1, 0, 1); got != 75 {
		t.Errorf("area(32k,48,mono) = %d, want 75", got)
	}
	if got := mainAreaBytes(9, 0, 1, 2); got != 382 {
		t.Errorf("padded area = %d, want 382", got)
	}
}

func TestResCapBytes(t *testing.T) {
	// High rates: the 511 field limit binds. 128k/44.1 stereo area 381:
	// 7*381 > 511 -> 511. Lowest: 32k/48 stereo area 60: 7*60 = 420.
	if got := resCapBytes(9, 0, 2); got != 511 {
		t.Errorf("cap(128k) = %d, want 511", got)
	}
	if got := resCapBytes(1, 1, 2); got != 420 {
		t.Errorf("cap(32k,48,st) = %d, want 420", got)
	}
	// The cap is always expressible and positive across the whole grid.
	for bi := 1; bi <= 14; bi++ {
		for sri := range 3 {
			for _, nch := range []int{1, 2} {
				c := resCapBytes(bi, sri, nch)
				if c <= 0 || c > resHardCapBytes {
					t.Fatalf("cap(%d,%d,%d) = %d out of range", bi, sri, nch, c)
				}
			}
		}
	}
}

func TestSpendBoundsInvariants(t *testing.T) {
	// For every rate and every legal occupancy: lo <= hi, hi == occ+area
	// EXACTLY (the Huffman field capacity is decoupled from the physical
	// spend range; folding it into hi made lo > hi reachable at high
	// bitrates, the agy critical finding), and spending inside [lo, hi]
	// keeps the next occupancy in [0, cap].
	for bi := 1; bi <= 14; bi++ {
		for sri := range 3 {
			for _, nch := range []int{1, 2} {
				area := mainAreaBytes(bi, sri, 0, nch)
				cap := resCapBytes(bi, sri, nch) //nolint:gocritic,predeclared // cap names the reservoir cap under test, matching the interface's own terminology; the builtin isn't used in this scope
				for occ := 0; occ <= cap; occ++ {
					r := reservoir{occ: occ}
					lo, hi := r.spendBounds(area, cap)
					if lo > hi {
						t.Fatalf("bi=%d sri=%d nch=%d occ=%d: lo %d > hi %d", bi, sri, nch, occ, lo, hi)
					}
					if hi != occ+area {
						t.Fatalf("hi = %d, want exactly occ+area = %d", hi, occ+area)
					}
					for _, spend := range []int{lo, (lo + hi) / 2, hi} {
						rr := reservoir{occ: occ}
						rr.commitFrame(area, spend)
						if rr.occ < 0 || rr.occ > cap {
							t.Fatalf("occ %d after spend %d (from %d, area %d, cap %d)",
								rr.occ, spend, occ, area, cap)
						}
					}
				}
			}
		}
	}
}

func TestGranuleDemandBits(t *testing.T) {
	const meanGB = 1500
	// Floor: silence (PE 0) demands meanGB/2.
	if got := granuleDemandBits(0, meanGB); got != meanGB/2 {
		t.Errorf("silence demand = %d, want %d", got, meanGB/2)
	}
	// Linear region: PE passes through.
	if got := granuleDemandBits(1800, meanGB); got != 1800 {
		t.Errorf("demand(1800) = %d, want 1800", got)
	}
	// Ceiling: 2*meanGB (below maxPart23Length here).
	if got := granuleDemandBits(1e6, meanGB); got != 2*meanGB {
		t.Errorf("huge PE demand = %d, want %d", got, 2*meanGB)
	}
	// maxPart23Length caps when 2*meanGB exceeds it.
	if got := granuleDemandBits(1e6, 3000); got != maxPart23Length {
		t.Errorf("demand cap = %d, want %d", got, maxPart23Length)
	}
	// NaN/negative PE: the pre-conversion clamp makes them the floor, not
	// implementation-defined garbage.
	if got := granuleDemandBits(-5, meanGB); got != meanGB/2 {
		t.Errorf("negative PE demand = %d, want floor", got)
	}
	if got := granuleDemandBits(math.NaN(), meanGB); got != meanGB/2 {
		t.Errorf("NaN PE demand = %d, want floor", got)
	}
}

func TestPlanFrameSplit(t *testing.T) {
	area, cap := 381, 511 //nolint:gocritic,predeclared // 128k/44.1 stereo; cap matches the interface's own terminology, the builtin isn't used in this scope
	r := reservoir{occ: 100}
	demands := [4]int{800, 1600, 400, 400} // sum 3200 bits = 400 bytes
	spend, huff, budgets := r.planFrame(&demands, 4, area, cap)
	if huff != 400 || spend != 400 {
		t.Fatalf("(spend, huff) = (%d, %d), want (400, 400): lo is 0, demands absorbable", spend, huff)
	}
	total := 0
	for i := range 4 {
		total += budgets[i]
		if budgets[i] > maxPart23Length {
			t.Fatalf("budget[%d] = %d over the field cap", i, budgets[i])
		}
	}
	if total != huff*8 {
		t.Fatalf("budgets sum %d, want huffTarget*8 = %d", total, huff*8)
	}
	// Proportionality: the 1600-demand granule gets twice the 800 one
	// within integer rounding.
	if d := budgets[1] - 2*budgets[0]; d < -8 || d > 8 {
		t.Fatalf("split not proportional: %v", budgets)
	}
	// Area-bound: demands exceeding the physical bytes are cut to occ+area.
	rHigh := reservoir{occ: 0}
	demands = [4]int{4095, 4095, 4095, 4095}
	spend, huff, _ = rHigh.planFrame(&demands, 4, area, cap)
	if huff != area || spend != area {
		t.Fatalf("area-bound: (spend, huff) = (%d, %d), want (%d, %d)", spend, huff, area, area)
	}
	// Anti-overflow burn: full reservoir, zero demand: the coded target is
	// 0 but the physical spend is forced up to lo entirely as ancillary.
	rFull := reservoir{occ: cap}
	demands = [4]int{0, 0, 0, 0}
	spend, huff, budgets = rFull.planFrame(&demands, 4, area, cap)
	lo, _ := rFull.spendBounds(area, cap)
	if huff != 0 || spend != lo {
		t.Fatalf("burn: (spend, huff) = (%d, %d), want (%d, 0)", spend, huff, lo)
	}
	for i := range 4 {
		if budgets[i] != 0 {
			t.Fatalf("burn budgets = %v, want all zero", budgets)
		}
	}
}

func TestPlanFrameDecoupling320k(t *testing.T) {
	// The agy critical case: 320kbps 32kHz mono. area = 1419 (frameLength
	// 1440 - 4 - 17), huffCap = ceil(2*4095/8) = 1024 < area, cap = 511. At
	// full occupancy with maximal demands: lo = 511+1419-511 = 1419 EXCEEDS
	// the Huffman capacity; the pre-decoupling design (hi folded the field
	// cap) had lo > hi here, a panic. Now: huffTarget = min(ceil(8190/8)=
	// 1024, min(1930, 1024)) = 1024, spend = max(1024, 1419) = 1419, and
	// the 395-byte delta is ancillary burn.
	area := mainAreaBytes(14, 2, 0, 1)
	if area != 1419 {
		t.Fatalf("area = %d, want 1419", area)
	}
	cap := resCapBytes(14, 2, 1) //nolint:gocritic,predeclared // cap matches the interface's own terminology, the builtin isn't used in this scope
	r := reservoir{occ: cap}
	demands := [4]int{4095, 4095, 0, 0} // nGC = 2 (mono)
	spend, huff, budgets := r.planFrame(&demands, 2, area, cap)
	if huff != 1024 || spend != 1419 {
		t.Fatalf("(spend, huff) = (%d, %d), want (1419, 1024)", spend, huff)
	}
	// huffTarget*8 = 8192 bits requests 2 more than the two part_2_3_length
	// fields can hold (2*4095 = 8190), so the cap-redistribution loop pins
	// both granule-channels at maxPart23Length and drops the 2-bit overflow.
	if budgets[0]+budgets[1] != 2*maxPart23Length || budgets[0] > maxPart23Length || budgets[1] > maxPart23Length {
		t.Fatalf("budgets %v: sum %d, want %d (both at the field cap)",
			budgets, budgets[0]+budgets[1], 2*maxPart23Length)
	}
	r.commitFrame(area, spend)
	if r.occ != cap {
		t.Fatalf("occ = %d after burn, want unchanged %d", r.occ, cap)
	}
}

func TestPlanFrameSkewedRedistribution(t *testing.T) {
	// The agy redistribution finding: byte-ceil inflation can push the
	// remainder-holding granule past maxPart23Length, and a single
	// redistribution pass could overflow its neighbor. 320k/44.1 stereo:
	// area = 1008 (frameLength 1044 - 4 - 32), occ 0, demands
	// {100, 100, 100, 4095}: sum 4395 bits, huffTarget = ceil(4395/8) =
	// 550 bytes = 4400 bits. Largest-remainder split: {100, 100, 100,
	// 4100}; 4100 exceeds 4095, the pool of 5 redistributes equally over
	// the uncapped {0, 1, 2} (remainder to the last uncapped): final
	// {101, 101, 103, 4095}, conserved total 4400. The exact values pin
	// the deterministic redistribution order.
	area := mainAreaBytes(14, 0, 0, 2)
	if area != 1008 {
		t.Fatalf("area = %d, want 1008", area)
	}
	cap := resCapBytes(14, 0, 2) //nolint:gocritic,predeclared // cap matches the interface's own terminology, the builtin isn't used in this scope
	r := reservoir{}
	demands := [4]int{100, 100, 100, 4095}
	spend, huff, budgets := r.planFrame(&demands, 4, area, cap)
	if huff != 550 || spend != 550 {
		t.Fatalf("(spend, huff) = (%d, %d), want (550, 550)", spend, huff)
	}
	want := [4]int{101, 101, 103, 4095}
	if budgets != want {
		t.Fatalf("budgets = %v, want %v (deterministic redistribution)", budgets, want)
	}
	total := 0
	for _, b := range budgets {
		total += b
	}
	if total != huff*8 {
		t.Fatalf("redistribution lost bits: sum %d, want %d", total, huff*8)
	}
}

func TestReservoirSimulation(t *testing.T) {
	// A deterministic 400-frame demand pattern (silence / steady / burst)
	// keeps every invariant: occupancy in [0, cap], spend in [lo, hi],
	// and the reservoir actually SWINGS (a burst after silence draws
	// occupancy down; sustained silence pushes it to the cap).
	area, cap, nGC := 381, 511, 4 //nolint:gocritic,predeclared // cap matches the interface's own terminology, the builtin isn't used in this scope
	var r reservoir
	maxOcc, minOccAfterFill := 0, cap
	filled := false
	for f := range 400 {
		var pe float64
		switch {
		case f%40 < 20:
			pe = 0 // silence: deposits
		case f%40 < 36:
			pe = 1900 // steady
		default:
			pe = 6000 // burst: withdrawals
		}
		var demands [4]int
		meanGB := area * 8 / nGC
		for i := range nGC {
			demands[i] = granuleDemandBits(pe, meanGB)
		}
		spend, _, _ := r.planFrame(&demands, nGC, area, cap)
		lo, hi := r.spendBounds(area, cap)
		if spend < lo || spend > hi {
			t.Fatalf("frame %d: spend %d outside [%d, %d]", f, spend, lo, hi)
		}
		r.commitFrame(area, spend)
		if r.occ < 0 || r.occ > cap {
			t.Fatalf("frame %d: occ %d out of range", f, r.occ)
		}
		if r.occ > maxOcc {
			maxOcc = r.occ
		}
		if r.occ == cap {
			filled = true
		}
		if filled && r.occ < minOccAfterFill {
			minOccAfterFill = r.occ
		}
	}
	if maxOcc != cap {
		t.Errorf("sustained silence never filled the reservoir (max occ %d)", maxOcc)
	}
	if minOccAfterFill > cap/2 {
		t.Errorf("bursts never drew the reservoir down (min occ after fill %d)", minOccAfterFill)
	}
}
