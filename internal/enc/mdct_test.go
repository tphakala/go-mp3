package enc

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"
)

// TestMdctWindowKnownAnswer recomputes every MDCTWindow[i] with math.Sin and
// requires agreement with the committed literal within 1 ULP, same pattern
// as TestFBMatrixKnownAnswer (filterbank_test.go).
func TestMdctWindowKnownAnswer(t *testing.T) {
	for i := range 36 {
		want := math.Sin(math.Pi / 36 * (float64(i) + 0.5))
		got := MDCTWindow[i]
		if diff := ulpDistance(got, want); diff > 1 {
			t.Fatalf("MDCTWindow[%d] = %v (bits %x), want %v (bits %x), diff %d ULP",
				i, got, math.Float64bits(got), want, math.Float64bits(want), diff)
		}
	}
}

// TestMdctCosKnownAnswer recomputes every mdctCos[k][n] with math.Cos and
// requires agreement with the committed literal within 1 ULP.
func TestMdctCosKnownAnswer(t *testing.T) {
	for k := range 18 {
		for n := range 36 {
			want := math.Cos(math.Pi / 72 * float64(2*n+19) * float64(2*k+1))
			got := mdctCos[k][n]
			if diff := ulpDistance(got, want); diff > 1 {
				t.Fatalf("mdctCos[%d][%d] = %v (bits %x), want %v (bits %x), diff %d ULP",
					k, n, got, math.Float64bits(got), want, math.Float64bits(want), diff)
			}
		}
	}
}

// TestAliasCoefficientsKnownAnswer recomputes AliasCS/AliasCA from the ISO
// Table B.9 c coefficients with math.Sqrt and requires agreement with the
// committed literals within 1 ULP.
func TestAliasCoefficientsKnownAnswer(t *testing.T) {
	c := [8]float64{-0.6, -0.535, -0.33, -0.185, -0.095, -0.041, -0.0142, -0.0037}
	for i, cv := range c {
		s := math.Sqrt(1 + cv*cv)
		wantCS := 1 / s
		wantCA := cv / s

		if diff := ulpDistance(AliasCS[i], wantCS); diff > 1 {
			t.Fatalf("AliasCS[%d] = %v (bits %x), want %v (bits %x), diff %d ULP",
				i, AliasCS[i], math.Float64bits(AliasCS[i]), wantCS, math.Float64bits(wantCS), diff)
		}
		if diff := ulpDistance(AliasCA[i], wantCA); diff > 1 {
			t.Fatalf("AliasCA[%d] = %v (bits %x), want %v (bits %x), diff %d ULP",
				i, AliasCA[i], math.Float64bits(AliasCA[i]), wantCA, math.Float64bits(wantCA), diff)
		}
	}
}

// TestMdctTablesChecksum guards MDCTWindow, mdctCos, AliasCS and AliasCA's
// committed literals against accidental edits with a golden sha256 over
// their float64 bit patterns, same pattern as TestFBWindowChecksum
// (filterbank_test.go). Frozen on first run.
func TestMdctTablesChecksum(t *testing.T) {
	const wantHex = "ac9065a7e278e69f5a6c4215a7e672aa16bfeb976eacfe3e740c6d011b40bb58"

	h := sha256.New()
	var buf8 [8]byte
	write := func(v float64) {
		binary.LittleEndian.PutUint64(buf8[:], math.Float64bits(v))
		h.Write(buf8[:])
	}

	for _, v := range MDCTWindow {
		write(v)
	}
	for _, row := range mdctCos {
		for _, v := range row {
			write(v)
		}
	}
	for _, v := range AliasCS {
		write(v)
	}
	for _, v := range AliasCA {
		write(v)
	}

	got := hex.EncodeToString(h.Sum(nil))
	if got != wantHex {
		t.Fatalf("mdct tables checksum = %s, want %s", got, wantHex)
	}
}

// TestMdctGolden runs FlipOddSubbands, MDCTGranule and AliasReduce over 6
// granules of LCG-generated pseudo-noise and checks a golden sha256 over
// the resulting xr bit patterns, same pattern as TestFBGolden
// (filterbank_test.go). CI's arm64 leg failing this test (while amd64 stays
// green) means an FMA leak in MDCTGranule or AliasReduce; fix the fusion
// site (an unwrapped product feeding a following +/-), never the golden.
func TestMdctGolden(t *testing.T) {
	const granules = 6

	var seed uint64 = 1
	next := func() float64 {
		seed = seed*6364136223846793005 + 1442695040888963407
		return (float64(seed>>11)/float64(1<<53))*2 - 1
	}

	h := sha256.New()
	var buf8 [8]byte

	var prev [18][32]float64
	for range granules {
		var cur [18][32]float64
		for tt := range 18 {
			for b := range 32 {
				cur[tt][b] = next()
			}
		}
		FlipOddSubbands(&cur)

		var xr [576]float64
		MDCTGranule(&prev, &cur, &xr)
		AliasReduce(&xr)

		for _, v := range xr {
			binary.LittleEndian.PutUint64(buf8[:], math.Float64bits(v))
			h.Write(buf8[:])
		}

		prev = cur
	}

	const wantHex = "4281aa40cdf26eb1a7ea0489d53fcca561bd5c26de62115993599609f92956e0"
	got := hex.EncodeToString(h.Sum(nil))
	if got != wantHex {
		t.Fatalf("TestMdctGolden checksum = %s, want %s", got, wantHex)
	}
}
