package enc

import (
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// TestMdctWindowKnownAnswer recomputes every MDCTWindow[i] with math.Sin and
// requires agreement with the committed literal within a cross-arch-robust
// tolerance (closeToFormula, filterbank_test.go), same pattern as
// TestFBMatrixKnownAnswer.
func TestMdctWindowKnownAnswer(t *testing.T) {
	for i := range 36 {
		want := math.Sin(math.Pi / 36 * (float64(i) + 0.5))
		got := MDCTWindow[i]
		if !closeToFormula(got, want) {
			t.Fatalf("MDCTWindow[%d] = %v (bits %x), want %v (bits %x), diff >= %d ULP",
				i, got, math.Float64bits(got), want, math.Float64bits(want), ulpDistance(got, want))
		}
	}
}

// TestMdctCosKnownAnswer recomputes every mdctCos[k][n] with math.Cos and
// requires agreement with the committed literal within a cross-arch-robust
// tolerance (closeToFormula, filterbank_test.go).
func TestMdctCosKnownAnswer(t *testing.T) {
	for k := range 18 {
		for n := range 36 {
			want := math.Cos(math.Pi / 72 * float64(2*n+19) * float64(2*k+1))
			got := mdctCos[k][n]
			if !closeToFormula(got, want) {
				t.Fatalf("mdctCos[%d][%d] = %v (bits %x), want %v (bits %x), diff >= %d ULP",
					k, n, got, math.Float64bits(got), want, math.Float64bits(want), ulpDistance(got, want))
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

// TestMdctShortTablesRecompute recomputes MDCTWindowStart, MDCTWindowStop,
// MDCTWindowShort and mdctCos12 with math.Sin/math.Cos (test-side libm is
// legal; see TestMdctWindowKnownAnswer) and requires agreement with the
// committed literals within closeToFormula's tolerance, then checks the two
// structural properties the brief calls out: MDCTWindowStart[:18] equals
// MDCTWindow[:18] bit-exactly (both are the same sin(pi/36*(i+0.5)) long
// rise), and MDCTWindowStop is the exact time reverse of MDCTWindowStart
// (decision 3). Finally it freezes a sha256 checksum over all four tables,
// same pattern as TestMdctTablesChecksum.
func TestMdctShortTablesRecompute(t *testing.T) {
	wantStart := func(i int) float64 {
		switch {
		case i < 18:
			return math.Sin(math.Pi / 36 * (float64(i) + 0.5))
		case i < 24:
			return 1.0
		case i < 30:
			return math.Sin(math.Pi / 12 * (float64(i) - 18 + 0.5))
		default:
			return 0.0
		}
	}

	for i := range 36 {
		want := wantStart(i)
		got := MDCTWindowStart[i]
		if !closeToFormula(got, want) {
			t.Fatalf("MDCTWindowStart[%d] = %v (bits %x), want %v (bits %x), diff >= %d ULP",
				i, got, math.Float64bits(got), want, math.Float64bits(want), ulpDistance(got, want))
		}

		wantStop := wantStart(35 - i)
		gotStop := MDCTWindowStop[i]
		if !closeToFormula(gotStop, wantStop) {
			t.Fatalf("MDCTWindowStop[%d] = %v (bits %x), want %v (bits %x), diff >= %d ULP",
				i, gotStop, math.Float64bits(gotStop), wantStop, math.Float64bits(wantStop), ulpDistance(gotStop, wantStop))
		}
	}

	for j := range 12 {
		want := math.Sin(math.Pi / 12 * (float64(j) + 0.5))
		got := MDCTWindowShort[j]
		if !closeToFormula(got, want) {
			t.Fatalf("MDCTWindowShort[%d] = %v (bits %x), want %v (bits %x), diff >= %d ULP",
				j, got, math.Float64bits(got), want, math.Float64bits(want), ulpDistance(got, want))
		}
	}

	for k := range 6 {
		for j := range 12 {
			want := math.Cos(math.Pi / 24 * float64(2*j+1+6) * float64(2*k+1))
			got := mdctCos12[k][j]
			if !closeToFormula(got, want) {
				t.Fatalf("mdctCos12[%d][%d] = %v (bits %x), want %v (bits %x), diff >= %d ULP",
					k, j, got, math.Float64bits(got), want, math.Float64bits(want), ulpDistance(got, want))
			}
		}
	}

	for i := range 18 {
		if math.Float64bits(MDCTWindowStart[i]) != math.Float64bits(MDCTWindow[i]) {
			t.Fatalf("MDCTWindowStart[%d] = %v (bits %x), want MDCTWindow[%d] = %v (bits %x) bit-exact",
				i, MDCTWindowStart[i], math.Float64bits(MDCTWindowStart[i]),
				i, MDCTWindow[i], math.Float64bits(MDCTWindow[i]))
		}
	}

	for i := range 36 {
		if math.Float64bits(MDCTWindowStop[i]) != math.Float64bits(MDCTWindowStart[35-i]) {
			t.Fatalf("MDCTWindowStop[%d] = %v (bits %x), want MDCTWindowStart[%d] = %v (bits %x) bit-exact (time reverse)",
				i, MDCTWindowStop[i], math.Float64bits(MDCTWindowStop[i]),
				35-i, MDCTWindowStart[35-i], math.Float64bits(MDCTWindowStart[35-i]))
		}
	}

	const wantHex = "104d32eb3a55934d00cfd266cac513fd02a56c09abc1e0916c721cde8380080a"
	var vals []float64
	vals = append(vals, MDCTWindowStart[:]...)
	vals = append(vals, MDCTWindowStop[:]...)
	vals = append(vals, MDCTWindowShort[:]...)
	for _, row := range mdctCos12 {
		vals = append(vals, row[:]...)
	}
	got := sha256Float64s(vals...)
	if got != wantHex {
		t.Fatalf("short mdct tables checksum = %s, want %s", got, wantHex)
	}
}

// TestMdctTablesChecksum guards MDCTWindow, mdctCos, AliasCS and AliasCA's
// committed literals against accidental edits with a golden sha256 over
// their float64 bit patterns, same pattern as TestFBWindowChecksum
// (filterbank_test.go). Frozen on first run.
func TestMdctTablesChecksum(t *testing.T) {
	const wantHex = "ac9065a7e278e69f5a6c4215a7e672aa16bfeb976eacfe3e740c6d011b40bb58"

	var vals []float64
	vals = append(vals, MDCTWindow[:]...)
	for _, row := range mdctCos {
		vals = append(vals, row[:]...)
	}
	vals = append(vals, AliasCS[:]...)
	vals = append(vals, AliasCA[:]...)

	got := sha256Float64s(vals...)
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
		return testsignal.LCGSigned(&seed)
	}

	vals := make([]float64, 0, 576*granules)

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

		vals = append(vals, xr[:]...)

		prev = cur
	}

	const wantHex = "4281aa40cdf26eb1a7ea0489d53fcca561bd5c26de62115993599609f92956e0"
	got := sha256Float64s(vals...)
	if got != wantHex {
		t.Fatalf("TestMdctGolden checksum = %s, want %s", got, wantHex)
	}
}

// TestMdctShortGolden runs FlipOddSubbands and MDCTGranuleBlock(blockShort)
// over 6 granules of LCG-generated pseudo-noise (no AliasReduce, mirroring
// the decoder's antialias gating for short blocks) and checks a golden
// sha256 over the resulting xr bit patterns, the short-block twin of
// TestMdctGolden. CI's arm64 leg failing this test (while amd64 stays
// green) means an FMA leak in mdctGranuleShort; fix the fusion site (an
// unwrapped product feeding a following +/-), never the golden.
func TestMdctShortGolden(t *testing.T) {
	const granules = 6

	var seed uint64 = 1
	next := func() float64 {
		return testsignal.LCGSigned(&seed)
	}

	vals := make([]float64, 0, 576*granules)

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
		MDCTGranuleBlock(&prev, &cur, &xr, blockShort)

		vals = append(vals, xr[:]...)

		prev = cur
	}

	const wantHex = "94abe305ba86c9e7373249ac23dea5e24894acc5bf93fdb04ebfdd3786008aed"
	got := sha256Float64s(vals...)
	if got != wantHex {
		t.Fatalf("TestMdctShortGolden checksum = %s, want %s", got, wantHex)
	}
}
