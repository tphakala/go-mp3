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
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/enc"
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

// lcgFloat is the shared PCG-style LCG used across this project's fuzz-ish
// determinism tests (see internal/enc/filterbank_test.go's TestFBGolden and
// internal/bits/writer_test.go): it returns a pseudo-random float64 in
// [-1, 1) and advances seed.
func lcgFloat(seed *uint64) float64 {
	*seed = *seed*6364136223846793005 + 1442695040888963407
	return (float64(*seed>>11)/float64(1<<53))*2 - 1
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
		xr[i] = lcgFloat(&seed)
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
				raws[g][tt][b] = lcgFloat(&seed)
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
