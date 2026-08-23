package dec

// This is a white-box test (package dec, not dec_test): it drives the
// unexported l3Antialias, l3Imdct36 and l3ChangeSign directly and imports
// internal/enc, the sanctioned cross-package exception documented in
// internal/enc/doc.go and used first by TestReconstructionGate
// (encx_filterbank_test.go). The Task 3 forward MDCT / alias-reduction pair
// has no bit-exact oracle stream of its own; the decoder's inverse stages
// are the only oracle available, so these gates prove correctness by round
// trip rather than by byte comparison against a reference dump.

import (
	"fmt"
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/enc"
	"github.com/tphakala/go-mp3/internal/testsignal"
)

// ulp32Distance walks the float32 sequence from a towards b with
// math.Nextafter32 and returns the number of representable steps between
// them, capped at maxULP+1 so a badly wrong value fails fast instead of
// looping. Mirrors internal/enc/filterbank_test.go's ulpDistance (float64),
// no shared helper exists across packages so this is a package-local twin.
func ulp32Distance(a, b float32, maxULP int) int {
	if a == b {
		return 0
	}
	for steps := 1; steps <= maxULP; steps++ {
		a = math.Nextafter32(a, b)
		if a == b {
			return steps
		}
	}
	return maxULP + 1
}

// TestEncAliasCoefficientsMatchDec checks enc.AliasCS/enc.AliasCA (derived
// from the ISO Table B.9 c coefficients) against the decoder's gAA table.
// cs matches gAA[0] bit for bit in float32 (both positive, no tolerance
// needed). ca matches gAA[1] in magnitude within a small ULP tolerance,
// not bit-exact: every Table B.9 c is published to only 2-4 significant
// decimal digits (e.g. c[7] = -0.0037), while minimp3's g_aa[1] literals
// carry 8 significant digits directly (e.g. 0.00369997f) rather than being
// computed from c at compile time. Re-deriving ca from the low-precision c
// cannot recover those extra digits, so the smallest-magnitude coefficients
// drift a measured 1/9/20 ULP in float32 (i=5/6/7); the larger coefficients
// (i=0..4) are bit-exact. maxCAULP is set to the measured worst case; see
// PROVENANCE.md's float parity fallback list.
func TestEncAliasCoefficientsMatchDec(t *testing.T) {
	const maxCAULP = 20
	for i := range 8 {
		gotCS := float32(enc.AliasCS[i])
		wantCS := gAA[0][i]
		if gotCS != wantCS {
			t.Errorf("AliasCS[%d] = %v (bits %08x), want %v (bits %08x)",
				i, gotCS, math.Float32bits(gotCS), wantCS, math.Float32bits(wantCS))
		}

		gotCA := float32(enc.AliasCA[i])
		if gotCA < 0 {
			gotCA = -gotCA
		}
		wantCA := gAA[1][i]
		if diff := ulp32Distance(gotCA, wantCA, maxCAULP); diff > maxCAULP {
			t.Errorf("|AliasCA[%d]| = %v (bits %08x), want %v (bits %08x), diff %d ULP exceeds %d",
				i, gotCA, math.Float32bits(gotCA), wantCA, math.Float32bits(wantCA), diff, maxCAULP)
		}
	}
}

// TestEncMdctWindowMatchesDec checks that enc.MDCTWindow is the same sine
// window the decoder's gMdctWindow[0] carries, half-length and reordered:
// gMdctWindow[0][j] holds the window's high-index half in ascending j
// (j = 0..8 <-> enc window index 17..9) and its low-index half shifted by 9
// (j = 9..17 <-> enc window index 0..8). Like the alias coefficients,
// gMdctWindow's literals are minimp3's own 8-digit decimal transcription
// rather than a math.Sin call, so a handful of entries land within a small
// measured ULP tolerance rather than bit-exact; see PROVENANCE.md.
func TestEncMdctWindowMatchesDec(t *testing.T) {
	const maxULP = 2
	for j := range 9 {
		gotHi := float32(enc.MDCTWindow[17-j])
		wantHi := gMdctWindow[0][j]
		if diff := ulp32Distance(gotHi, wantHi, maxULP); diff > maxULP {
			t.Errorf("MDCTWindow[%d] = %v (bits %08x), want gMdctWindow[0][%d] = %v (bits %08x), diff %d ULP exceeds %d",
				17-j, gotHi, math.Float32bits(gotHi), j, wantHi, math.Float32bits(wantHi), diff, maxULP)
		}

		gotLo := float32(enc.MDCTWindow[j])
		wantLo := gMdctWindow[0][9+j]
		if diff := ulp32Distance(gotLo, wantLo, maxULP); diff > maxULP {
			t.Errorf("MDCTWindow[%d] = %v (bits %08x), want gMdctWindow[0][%d] = %v (bits %08x), diff %d ULP exceeds %d",
				j, gotLo, math.Float32bits(gotLo), 9+j, wantLo, math.Float32bits(wantLo), diff, maxULP)
		}
	}
}

// TestEncAliasCancellation checks that the decoder's l3Antialias exactly
// undoes enc.AliasReduce: apply the encoder butterflies to LCG-random
// spectral lines, run the decoder's inverse butterflies on a float32 copy,
// and require every line back within 1e-4 relative of the float64
// original (the float32 path dominates the error, since AliasReduce itself
// runs in float64 and is its own algebraic inverse of l3Antialias).
func TestEncAliasCancellation(t *testing.T) {
	var seed uint64 = 1
	var xr, want [576]float64
	for i := range xr {
		xr[i] = testsignal.LCGSigned(&seed)
		want[i] = xr[i]
	}

	enc.AliasReduce(&xr)

	var grbuf [576]float32
	for i := range xr {
		grbuf[i] = float32(xr[i])
	}
	l3Antialias(grbuf[:], 31)

	for i := range grbuf {
		got := float64(grbuf[i])
		diff := math.Abs(got - want[i])
		tol := 1e-4 * math.Max(1, math.Abs(want[i]))
		if diff > tol {
			t.Fatalf("line %d: got %v, want %v, diff %v exceeds tolerance %v", i, got, want[i], diff, tol)
		}
	}
}

// TestEncMdctTDACRoundTrip is the load-bearing gate for Task 3: it streams
// 8 granules of LCG-random subband data through the full encode/decode
// pair and requires time-domain alias cancellation (TDAC), the property
// that makes a windowed, 50%-overlapped MDCT/IMDCT chain reconstruct its
// input exactly.
//
// Encoder side: enc.FlipOddSubbands (in place) then
// enc.MDCTGranule(prev, cur, &xr) then enc.AliasReduce(&xr), each granule's
// flipped subband samples becoming both "cur" for this granule's MDCT and
// "prev" for the next.
//
// Decoder side: l3Antialias(grbuf, 31), then l3Imdct36(grbuf, overlap,
// gMdctWindow[0][:], 32) with overlap state persisted across granules, then
// l3ChangeSign(grbuf) to undo the encoder's odd/odd flip.
//
// TDAC's one-granule delay: the dec output for granule g is the properly
// windowed overlap-add of the MDCT block spanning granules g-1 and g, which
// reconstructs granule g-1's raw (pre-flip) subband samples exactly. So
// this test compares the decode of granule g against the raw subband data
// of granule g-1, starting at g=1 (g=0's decode reconstructs the granule
// before the stream starts, which does not exist).
func TestEncMdctTDACRoundTrip(t *testing.T) {
	const granules = 8

	var seed uint64 = 1
	raws := make([][18][32]float64, granules)
	for g := range granules {
		for tt := range 18 {
			for b := range 32 {
				raws[g][tt][b] = testsignal.LCGSigned(&seed)
			}
		}
	}

	flipped := make([][18][32]float64, granules)
	for g := range granules {
		flipped[g] = raws[g]
		enc.FlipOddSubbands(&flipped[g])
	}

	var overlap [9 * 32]float32
	var zero [18][32]float64

	// Per-line gain ratio measured on granule 2's decode (comparing against
	// granule 1's raw subband data), before any tolerance assertions: every
	// nonzero (want, got) pair should share the same ratio if the chain is
	// linear and time-invariant, which MDCT/IMDCT with a fixed window is.
	var granule2Gains []float64

	for g := range granules {
		var prev *[18][32]float64
		if g == 0 {
			prev = &zero
		} else {
			prev = &flipped[g-1]
		}

		var xr [576]float64
		enc.MDCTGranule(prev, &flipped[g], &xr)
		enc.AliasReduce(&xr)

		var grbuf [576]float32
		for i := range xr {
			grbuf[i] = float32(xr[i])
		}

		l3Antialias(grbuf[:], 31)
		l3Imdct36(grbuf[:], overlap[:], gMdctWindow[0][:], 32)
		l3ChangeSign(grbuf[:])

		if g == 0 {
			continue
		}
		want := &raws[g-1]

		if g == 2 {
			for tt := range 18 {
				for b := range 32 {
					w := want[tt][b]
					if math.Abs(w) < 1e-6 {
						continue
					}
					got := float64(grbuf[b*18+tt])
					granule2Gains = append(granule2Gains, got/w)
				}
			}
		}

		// RMS of the target granule, for a relative error bound.
		var sumSq float64
		for tt := range 18 {
			for b := range 32 {
				sumSq += float64(want[tt][b] * want[tt][b])
			}
		}
		rms := math.Sqrt(sumSq / (18 * 32))

		const maxRelErr = 1e-3
		for tt := range 18 {
			for b := range 32 {
				got := float64(grbuf[b*18+tt])
				diff := math.Abs(got - want[tt][b])
				if diff > maxRelErr*rms {
					t.Fatalf("granule %d (vs raw granule %d) t=%d b=%d: got %v, want %v, diff %v exceeds %v (%.1e * rms %v)",
						g, g-1, tt, b, got, want[tt][b], diff, maxRelErr*rms, maxRelErr, rms)
				}
			}
		}
	}

	if len(granule2Gains) == 0 {
		t.Fatal("granule 2: no usable (want, got) pairs for gain measurement")
	}
	mean := 0.0
	for _, g := range granule2Gains {
		mean += g
	}
	mean /= float64(len(granule2Gains))
	for _, g := range granule2Gains {
		if math.Abs(g-mean) > math.Abs(mean)*0.01 {
			t.Fatalf("granule 2 chain gain not constant: sample = %v, mean = %v, deviates more than 1%%", g, mean)
		}
	}
	t.Logf("measured chain gain (mdctScale folded in) = %v over %d samples", mean, len(granule2Gains))
}

// encBlockShort mirrors enc.blockShort (internal/enc/blocktypes.go), which
// is unexported: this white-box test package can call enc.MDCTGranuleBlock
// but cannot reference its unexported blockType constants, so the four
// ISO 2.4.1.7 block_type values are re-declared here as plain literals with
// this comment as the cross-reference. encBlockShort's value (2) is also
// dec's own shortBlockType (types.go); encBlockLong/Start/Stop (0/1/3)
// likewise match dec's own blockType field values (sideinfo.go,
// imdct.go's stopBlockType).
const (
	encBlockLong  = 0
	encBlockStart = 1
	encBlockShort = 2
	encBlockStop  = 3
)

// shortInterleaveInto reinterleaves one granule's short-block spectral
// lines from enc.MDCTGranuleBlock's window-major layout
// (xr[b*18+w*6+k], w = sub-window, k = spectral line) into the layout
// l3ImdctShort (internal/dec/imdct.go) expects: grbuf[18b+3k+w] = xr[18b+6w+k].
// This is the interleave Task A2's reorder + l3Reorder produce end to end
// in the real decode pipeline; this test performs it directly since reorder
// itself is out of scope for Task A1.
func shortInterleaveInto(grbuf []float32, xr *[576]float64) {
	for b := range 32 {
		for w := range 3 {
			for k := range 6 {
				grbuf[b*18+3*k+w] = float32(xr[b*18+w*6+k])
			}
		}
	}
}

// TestEncMdctShortTDACRoundTrip is the short-block twin of
// TestEncMdctTDACRoundTrip: it streams granules of LCG-random subband data,
// all as short blocks, through enc.MDCTGranuleBlock (no AliasReduce, since
// the decoder's l3Decode skips l3Antialias for a pure short granule via
// aaBands = nLongBands - 1 = -1) and l3ImdctGr's short-block path
// (blockType = shortBlockType, nLongBands = 0), and requires TDAC.
//
// The chain-gain measurement mirrors TestEncMdctTDACRoundTrip's granule-2
// procedure exactly, but pins mdctScaleShort rather than mdctScale: with
// mdctScaleShort temporarily set to 1 in mdct.go, this test's t.Logf line
// reports the measured gain (used to freeze the constant, then this
// comment and mdct.go's doc comment record the measured value); with the
// frozen constant in place, this test's tolerance assertions are the
// ongoing regression gate.
func TestEncMdctShortTDACRoundTrip(t *testing.T) {
	const granules = 8

	var seed uint64 = 1
	raws := make([][18][32]float64, granules)
	for g := range granules {
		for tt := range 18 {
			for b := range 32 {
				raws[g][tt][b] = testsignal.LCGSigned(&seed)
			}
		}
	}

	flipped := make([][18][32]float64, granules)
	for g := range granules {
		flipped[g] = raws[g]
		enc.FlipOddSubbands(&flipped[g])
	}

	var overlap [9 * 32]float32
	var zero [18][32]float64

	var granule2Gains []float64

	for g := range granules {
		var prev *[18][32]float64
		if g == 0 {
			prev = &zero
		} else {
			prev = &flipped[g-1]
		}

		var xr [576]float64
		enc.MDCTGranuleBlock(prev, &flipped[g], &xr, encBlockShort)

		var grbuf [576]float32
		shortInterleaveInto(grbuf[:], &xr)

		l3ImdctGr(grbuf[:], overlap[:], shortBlockType, 0)
		l3ChangeSign(grbuf[:])

		if g == 0 {
			continue
		}
		want := &raws[g-1]

		if g == 2 {
			for tt := range 18 {
				for b := range 32 {
					w := want[tt][b]
					if math.Abs(w) < 1e-6 {
						continue
					}
					got := float64(grbuf[b*18+tt])
					granule2Gains = append(granule2Gains, got/w)
				}
			}
		}

		var sumSq float64
		for tt := range 18 {
			for b := range 32 {
				sumSq += float64(want[tt][b] * want[tt][b])
			}
		}
		rms := math.Sqrt(sumSq / (18 * 32))

		const maxRelErr = 1e-3
		for tt := range 18 {
			for b := range 32 {
				got := float64(grbuf[b*18+tt])
				diff := math.Abs(got - want[tt][b])
				if diff > maxRelErr*rms {
					t.Fatalf("granule %d (vs raw granule %d) t=%d b=%d: got %v, want %v, diff %v exceeds %v (%.1e * rms %v)",
						g, g-1, tt, b, got, want[tt][b], diff, maxRelErr*rms, maxRelErr, rms)
				}
			}
		}
	}

	if len(granule2Gains) == 0 {
		t.Fatal("granule 2: no usable (want, got) pairs for gain measurement")
	}
	mean := 0.0
	for _, g := range granule2Gains {
		mean += g
	}
	mean /= float64(len(granule2Gains))
	for _, g := range granule2Gains {
		if math.Abs(g-mean) > math.Abs(mean)*0.01 {
			t.Fatalf("granule 2 chain gain not constant: sample = %v, mean = %v, deviates more than 1%%", g, mean)
		}
	}
	t.Logf("measured chain gain (mdctScaleShort folded in) = %v over %d samples", mean, len(granule2Gains))
}

// transitionGranule runs the encoder/decoder chain for one granule of
// TestEncMdctTransitionTDAC: enc.MDCTGranuleBlock, enc.AliasReduce for
// non-short block types, the short-block window-interleave or plain copy
// into grbuf, l3Antialias (gated exactly as l3Decode gates it, aaBands = -1
// skipping the loop entirely for a short granule), l3ImdctGr and
// l3ChangeSign, with overlap persisted by the caller across granules.
func transitionGranule(prev, cur *[18][32]float64, blockType int, overlap []float32) [576]float32 {
	var xr [576]float64
	enc.MDCTGranuleBlock(prev, cur, &xr, blockType)
	if blockType != encBlockShort {
		enc.AliasReduce(&xr)
	}

	var grbuf [576]float32
	if blockType == encBlockShort {
		shortInterleaveInto(grbuf[:], &xr)
	} else {
		for i := range xr {
			grbuf[i] = float32(xr[i])
		}
	}

	aaBands := 31
	if blockType == encBlockShort {
		aaBands = -1
	}
	l3Antialias(grbuf[:], aaBands)
	l3ImdctGr(grbuf[:], overlap, uint8(blockType), 0)
	l3ChangeSign(grbuf[:])

	return grbuf
}

// checkGranuleReconstruction is TestEncMdctTransitionTDAC's per-granule TDAC
// assertion: grbuf (granule g's decode) must match want (granule g-1's raw
// subband data) within maxRelErr of want's RMS, the same tolerance rule as
// TestEncMdctTDACRoundTrip and TestEncMdctShortTDACRoundTrip.
func checkGranuleReconstruction(t *testing.T, grbuf *[576]float32, want *[18][32]float64, g, blockType, prevBlockType int) {
	t.Helper()

	var sumSq float64
	for tt := range 18 {
		for b := range 32 {
			sumSq += float64(want[tt][b] * want[tt][b])
		}
	}
	rms := math.Sqrt(sumSq / (18 * 32))

	const maxRelErr = 1e-3
	for tt := range 18 {
		for b := range 32 {
			got := float64(grbuf[b*18+tt])
			diff := math.Abs(got - want[tt][b])
			if diff > maxRelErr*rms {
				t.Fatalf("granule %d (blockType %d, prev blockType %d, vs raw granule %d) t=%d b=%d: got %v, want %v, diff %v exceeds %v (%.1e * rms %v)",
					g, blockType, prevBlockType, g-1, tt, b, got, want[tt][b], diff, maxRelErr*rms, maxRelErr, rms)
			}
		}
	}
}

// TestEncMdctTransitionTDAC is the increment's transition-matrix arbiter:
// it streams 10 granules per sequence through mixed block types (covering
// every legal long/start/short/stop edge) with ONE persisted overlap array
// across the whole sequence, and requires that granule g's decode matches
// granule g-1's raw subband data, exactly as the single-block-type gates
// do. This proves both the window shapes (MDCTWindowStart/Stop/Short) AND
// the cross-boundary window-application convention: l3Imdct36 windows the
// incoming (previous-granule) raw overlap tail using the CURRENT granule's
// window array (imdct.go's l3ImdctGr doc comment), so TDAC across a
// long/start or short/stop boundary only holds if the encoder's window
// choice for each granule agrees with what the ISO windows guarantee at
// that seam (decision 3's named convention risk).
func TestEncMdctTransitionTDAC(t *testing.T) {
	sequences := [][]int{
		{0, 0, 1, 2, 2, 3, 0, 0, 1, 2},
		{0, 1, 2, 3, 0, 1, 2, 2, 3, 0},
		{0, 1, 2, 2, 2, 2, 3, 0, 0, 0},
	}

	for si, seq := range sequences {
		t.Run(fmt.Sprintf("seq%d", si), func(t *testing.T) {
			granules := len(seq)

			seed := uint64(1 + si)
			raws := make([][18][32]float64, granules)
			for g := range granules {
				for tt := range 18 {
					for b := range 32 {
						raws[g][tt][b] = testsignal.LCGSigned(&seed)
					}
				}
			}

			flipped := make([][18][32]float64, granules)
			for g := range granules {
				flipped[g] = raws[g]
				enc.FlipOddSubbands(&flipped[g])
			}

			var overlap [9 * 32]float32
			var zero [18][32]float64

			for g := range granules {
				blockType := seq[g]

				prev := &zero
				if g > 0 {
					prev = &flipped[g-1]
				}

				grbuf := transitionGranule(prev, &flipped[g], blockType, overlap[:])

				if g == 0 {
					continue
				}
				checkGranuleReconstruction(t, &grbuf, &raws[g-1], g, blockType, seq[g-1])
			}
		})
	}
}
