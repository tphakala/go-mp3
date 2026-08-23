package enc

import "testing"

// blockTypeForLegal is the ISO 2.4.1.7 window-compatibility grammar
// blockTypeFor must never violate: the legal successor set for each block
// type (Global Constraints / decision 10): 0->{0,1}, 1->{2}, 2->{2,3},
// 3->{0,1}.
var blockTypeForLegal = map[int]map[int]bool{
	blockLong:  {blockLong: true, blockStart: true},
	blockStart: {blockShort: true},
	blockShort: {blockShort: true, blockStop: true},
	blockStop:  {blockLong: true, blockStart: true},
}

// TestBlockTypeFor exhaustively covers all 16 (prev, want, wantNext) cells
// of blockTypeFor's total function against decision 10's formula
// (including the prev==blockShort bridge case: 2->1 is illegal, so a
// granule with no attack of its own but an attacking successor extends
// the short run instead of jumping straight to start), and separately
// asserts start -> stop (1 -> 3) is never emitted for ANY want/wantNext
// pair.
func TestBlockTypeFor(t *testing.T) {
	blockTypes := []int{blockLong, blockStart, blockShort, blockStop}

	for _, prev := range blockTypes {
		for _, want := range []bool{false, true} {
			for _, wantNext := range []bool{false, true} {
				got := blockTypeFor(prev, want, wantNext)

				var wantType int
				switch {
				case want:
					wantType = blockShort
				case prev == blockShort:
					if wantNext {
						wantType = blockShort
					} else {
						wantType = blockStop
					}
				case wantNext:
					wantType = blockStart
				default:
					wantType = blockLong
				}

				if got != wantType {
					t.Errorf("blockTypeFor(prev=%d, want=%v, wantNext=%v) = %d, want %d",
						prev, want, wantNext, got, wantType)
				}
			}
		}
	}

	// Decision 10: start -> stop (1 -> 3) is never emitted, for any
	// want/wantNext combination.
	for _, want := range []bool{false, true} {
		for _, wantNext := range []bool{false, true} {
			if got := blockTypeFor(blockStart, want, wantNext); got == blockStop {
				t.Errorf("blockTypeFor(blockStart, want=%v, wantNext=%v) = blockStop, want != blockStop (1->3 must be unreachable)",
					want, wantNext)
			}
		}
	}

	// Spot-check the legal-successor-set grammar on the reachable subset,
	// including the bridge case (short with no attack of its own, but an
	// attacking successor: must stay short, not jump to start).
	cases := []struct {
		prev           int
		want, wantNext bool
	}{
		{blockLong, false, false},  // -> long
		{blockLong, false, true},   // -> start
		{blockStart, true, false},  // -> short (the only reachable successor of start)
		{blockStart, true, true},   // -> short
		{blockShort, true, false},  // -> short
		{blockShort, false, false}, // -> stop
		{blockShort, false, true},  // -> short (bridge: 2->1 would be illegal)
		{blockStop, false, false},  // -> long
		{blockStop, false, true},   // -> start
	}
	for _, c := range cases {
		got := blockTypeFor(c.prev, c.want, c.wantNext)
		if !blockTypeForLegal[c.prev][got] {
			t.Errorf("blockTypeFor(prev=%d, want=%v, wantNext=%v) = %d, not in legal successor set %v",
				c.prev, c.want, c.wantNext, got, blockTypeForLegal[c.prev])
		}
	}
}

// TestBlockTypeForGrammarSimulation is the inductive proof from
// blockTypeFor's doc comment, checked by exhaustive simulation instead of
// trusted by hand: chain blockTypeFor over long synthetic want-sequences
// (every combination of attack density a simple LCG-driven bit stream can
// produce, run many times with different seeds) and require every
// EMITTED transition to land in blockTypeForLegal, for the self-consistent
// chain a real Encoder actually builds (each granule's prev is the
// previous granule's own decided type, and want(g+1) is fed back in as
// THIS granule's wantNext exactly once).
func TestBlockTypeForGrammarSimulation(t *testing.T) {
	next := func(seed *uint64) bool {
		// A small xorshift, not testsignal.LCG: this test needs a raw bit
		// stream (attack or not), not a float amplitude.
		*seed ^= *seed << 13
		*seed ^= *seed >> 7
		*seed ^= *seed << 17
		return *seed&1 == 1
	}

	for _, seed0 := range []uint64{1, 2, 3, 12345, 0xC0FFEE, 0xA5A5A5A5} {
		seed := seed0
		const n = 2000
		wants := make([]bool, n)
		for i := range wants {
			wants[i] = next(&seed)
		}

		// prev starts at blockLong as a BOOTSTRAP sentinel, the same
		// arbitrary value Encoder.blockPrev's zero value carries: there is
		// no real previous granule at stream start (silence precedes it),
		// so the grammar has nothing genuine to violate on the very FIRST
		// transition (a hard onset at granule 0 can legitimately jump
		// straight to short there, exactly as it does in the real
		// encoder). Enforcement starts at the SECOND transition, once
		// prev is a real, blockTypeFor-produced value.
		prev := blockTypeFor(blockLong, wants[0], wants[1])
		for i := 1; i < n-1; i++ {
			got := blockTypeFor(prev, wants[i], wants[i+1])
			if !blockTypeForLegal[prev][got] {
				t.Fatalf("seed=%d step=%d: blockTypeFor(prev=%d, want=%v, wantNext=%v) = %d, illegal successor of %d",
					seed0, i, prev, wants[i], wants[i+1], got, prev)
			}
			prev = got
		}
	}
}

// stepGranule returns a 576-sample granule that is silent for the first n
// samples and at amplitude amp for the rest: the basic "step" attack shape.
func stepGranule(n int, amp float64) []float64 {
	g := make([]float64, 576)
	for i := n; i < 576; i++ {
		g[i] = amp
	}
	return g
}

// constGranule returns a 576-sample granule at a constant amplitude
// (alternating sign so its energy is nonzero without needing libm): the
// "steady tone" shape attackDetect must never fire on once its own energy
// has stabilized.
func constGranule(amp float64) []float64 {
	g := make([]float64, 576)
	for i := range g {
		v := amp
		if i%2 == 1 {
			v = -amp
		}
		g[i] = v
	}
	return g
}

// TestAttackDetect exercises attackDetect's step/click/tone/silence shapes
// and its carry semantics across a granule boundary (design decision 9).
func TestAttackDetect(t *testing.T) {
	t.Run("silence never attacks", func(t *testing.T) {
		g := make([]float64, 576)
		attack, last := attackDetect(g, 0)
		if attack {
			t.Errorf("silence: attack = true, want false")
		}
		if last != 0 {
			t.Errorf("silence: lastE = %v, want 0", last)
		}
	})

	t.Run("steady tone never attacks after warm-up", func(t *testing.T) {
		g := constGranule(0.5)
		// Warm up the carry with an identical granule's own last sub-block
		// energy, mirroring a steady stream where every granule looks like
		// its predecessor.
		_, prev := attackDetect(g, 0)
		attack, _ := attackDetect(g, prev)
		if attack {
			t.Errorf("steady tone: attack = true after warm-up, want false")
		}
	})

	t.Run("step at granule start fires on the first sub-block", func(t *testing.T) {
		// The whole granule jumps from silence to amp=0.5: every sub-block
		// has the same nonzero energy, but the carry-in (prevE) is silence
		// (0), so the first sub-block's ratio test fires unconditionally.
		g := constGranule(0.5)
		attack, _ := attackDetect(g, 0)
		if !attack {
			t.Errorf("step from silence: attack = false, want true")
		}
	})

	t.Run("step mid-granule fires on the later sub-block", func(t *testing.T) {
		// Silent for the first 192 samples (sub-block 0), then loud for the
		// rest: sub-block 0 stays at the carried-in energy (also silence,
		// so no attack there), sub-block 1 jumps far above sub-block 0's
		// near-zero energy.
		g := stepGranule(192, 0.7)
		attack, _ := attackDetect(g, 0)
		if !attack {
			t.Errorf("step mid-granule: attack = false, want true")
		}
	})

	t.Run("click confined to one sub-block fires", func(t *testing.T) {
		// A short, loud click inside sub-block 2 only; sub-blocks 0 and 1
		// stay silent, carried in from silence.
		g := make([]float64, 576)
		for i := 400; i < 420; i++ {
			g[i] = 0.9
		}
		attack, _ := attackDetect(g, 0)
		if !attack {
			t.Errorf("click: attack = false, want true")
		}
	})

	t.Run("below the floor never attacks regardless of ratio", func(t *testing.T) {
		// A tiny nonzero signal whose energy sits below attackFloorE: even
		// though it is "infinitely" above a zero carry in ratio terms, the
		// absolute floor must suppress it.
		g := make([]float64, 576)
		for i := range g {
			g[i] = 1e-4 // 192 * (1e-4)^2 = 1.92e-6, far below attackFloorE=1e-3
		}
		attack, _ := attackDetect(g, 0)
		if attack {
			t.Errorf("sub-floor signal: attack = true, want false (below attackFloorE)")
		}
	})

	t.Run("carry propagates across the granule boundary", func(t *testing.T) {
		// Granule 1 is loud throughout (steady, so its own internal ratios
		// never fire once past sub-block 0). Granule 2 is silent. Granule
		// 2's own sub-block 0 energy (0) compared against granule 1's
		// carried-in last sub-block energy (loud) is a DROP, not a rise,
		// so it must not fire either: attackDetect only tests rises.
		loud := constGranule(0.6)
		_, carry1 := attackDetect(loud, 0)
		silent := make([]float64, 576)
		attack, carry2 := attackDetect(silent, carry1)
		if attack {
			t.Errorf("loud-to-silent transition: attack = true, want false (a drop is not an attack)")
		}
		if carry2 != 0 {
			t.Errorf("carry after silent granule = %v, want 0", carry2)
		}

		// Now the reverse: silence then loud, using the real carried value.
		attack2, _ := attackDetect(loud, carry2)
		if !attack2 {
			t.Errorf("silent-to-loud transition (using real carry): attack = false, want true")
		}
	})
}
