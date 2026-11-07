package enc

import (
	"fmt"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// prefixCostLayouts are the long and short band layouts of all three sample
// rates.
var prefixCostLayouts = []*bandLayout{
	&layoutLong[0], &layoutLong[1], &layoutLong[2],
	&layoutShort[0], &layoutShort[1], &layoutShort[2],
}

// prefixCostLayoutName names prefixCostLayouts[i] for failure messages.
func prefixCostLayoutName(i int) string {
	if i >= 3 {
		return fmt.Sprintf("short[%d]", i-3)
	}
	return fmt.Sprintf("long[%d]", i)
}

// checkPrefixCost compares every prefix cost bigValuesPrefixCost and
// bigValuesPrefixCostGo write for one quantized spectrum, with the output
// matrices zeroed (as chooseRegions passes them) and again pre-filled with the
// same distinct marker in every cell, so a row or column a kernel skips or
// writes outside its range shows up as a difference.
func checkPrefixCost(t *testing.T, ix *[576]int32, lay *bandLayout, bigValues int, ctx string) {
	t.Helper()
	pb := pairBoundaries(lay, bigValues)
	var marked [40][32]int32
	for k := range marked {
		for c := range marked[k] {
			marked[k][c] = int32(-1000000 - 40*k - c)
		}
	}
	for _, start := range [2]*[40][32]int32{{}, &marked} {
		got, want := *start, *start
		bigValuesPrefixCost(ix, &pb, lay, &got)
		bigValuesPrefixCostGo(ix, &pb, lay, &want)
		if got != want {
			for k := range got {
				if got[k] != want[k] {
					t.Fatalf("%s bigValues=%d (marked start=%v) row %d: got %v want %v", ctx, bigValues, start == &marked, k, got[k], want[k])
				}
			}
		}
	}
}

// fillPrefixCostSpectrum fills ix with signed magnitudes up to scale that taper
// toward the top of the spectrum, the shape quantized granules have.
func fillPrefixCostSpectrum(seed *uint64, ix *[576]int32, scale int32) {
	for i := range ix {
		frac := float64(576-i) / 576
		v := int32(testsignal.LCG(seed) * float64(scale+1) * frac)
		if testsignal.LCG(seed) < 0.5 {
			v = -v
		}
		ix[i] = v
	}
}

// TestPrefixCostParity pins the SIMD kernel to bigValuesPrefixCostGo on random
// spectra whose magnitudes span every table's range, up to maxQuant. On the
// noasm build, on other architectures and on amd64 without AVX2 the
// dispatcher is the Go reference, so this compares it with itself and only
// guards the harness there; the arm64 and amd64 default builds are what it
// tests.
func TestPrefixCostParity(t *testing.T) {
	var seed uint64 = 4242
	scales := []int32{1, 2, 3, 8, 15, 16, 40, 255, 3000, maxQuant}
	for li, lay := range prefixCostLayouts {
		for _, scale := range scales {
			for range 40 {
				var ix [576]int32
				fillPrefixCostSpectrum(&seed, &ix, scale)
				part := partitionSpectrum(&ix)
				ctx := fmt.Sprintf("%s scale=%d", prefixCostLayoutName(li), scale)
				checkPrefixCost(t, &ix, lay, part.bigValues, ctx)
				checkPrefixCost(t, &ix, lay, int(testsignal.LCG(&seed)*289), ctx)
			}
		}
	}
}

// randomPartitionLayout returns a long-block layout with nBands in 1..39 and
// random band widths, a quarter of them zero (including the first band and
// bands in the middle), so pb has repeated boundaries and zero-width bands.
func randomPartitionLayout(seed *uint64) *bandLayout {
	lay := layoutLong[0]
	lay.nBands = 1 + int(testsignal.LCG(seed)*39)
	for b := range lay.width {
		lay.width[b] = 0
		if b < lay.nBands && testsignal.LCG(seed) >= 0.25 {
			lay.width[b] = 2 * (1 + int(testsignal.LCG(seed)*20))
		}
	}
	if lay.nBands > 1 {
		lay.width[0] = 0
	}
	return &lay
}

// TestPrefixCostRandomPartitions varies the partition itself: nBands 1..39,
// zero-width bands at the start and in the middle, bigValues 0..288 cutting
// bands short, against the Go reference. Like TestPrefixCostParity it compares
// the reference with itself wherever no kernel is linked.
func TestPrefixCostRandomPartitions(t *testing.T) {
	var seed uint64 = 777
	scales := []int32{1, 15, 16, 300, maxQuant}
	for trial := range 600 {
		lay := randomPartitionLayout(&seed)
		scale := scales[trial%len(scales)]
		var ix [576]int32
		fillPrefixCostSpectrum(&seed, &ix, scale)
		bigValues := int(testsignal.LCG(&seed) * 289)
		ctx := fmt.Sprintf("random layout nBands=%d scale=%d trial=%d", lay.nBands, scale, trial)
		checkPrefixCost(t, &ix, lay, bigValues, ctx)
	}
}

// FuzzPrefixCostParity drives the layout, the magnitude ceiling (up to
// maxQuant) and the big-values count beyond the fixed sweep, with the spectrum
// drawn from a seeded LCG. Where no kernel is linked (noasm, other
// architectures, amd64 without AVX2) it compares the reference with itself.
func FuzzPrefixCostParity(f *testing.F) {
	f.Add(uint8(0), uint16(15), uint16(288), uint64(1))
	f.Add(uint8(3), uint16(maxQuant), uint16(100), uint64(2))
	f.Add(uint8(5), uint16(1), uint16(0), uint64(3))
	f.Fuzz(func(t *testing.T, layR uint8, scaleR, bigValuesR uint16, seed uint64) {
		var ix [576]int32
		startSeed := seed
		scale := int32(scaleR) % (maxQuant + 1)
		fillPrefixCostSpectrum(&seed, &ix, scale)
		li := int(layR) % len(prefixCostLayouts)
		ctx := fmt.Sprintf("%s scale=%d seed=%d", prefixCostLayoutName(li), scale, startSeed)
		checkPrefixCost(t, &ix, prefixCostLayouts[li], int(bigValuesR)%289, ctx)
	})
}

// TestPrefixCostEscapeBounds places pair magnitudes on both sides of every
// escape table's largest representable value, taken from bigTables. Like
// TestPrefixCostParity it compares the reference with itself wherever no
// kernel is linked.
func TestPrefixCostEscapeBounds(t *testing.T) {
	edges := []int32{0, 1, 14, 15, 16, maxQuant}
	for _, bt := range &bigTables {
		if bt.linbits > 0 {
			top := int32(bt.dim-1) + int32(1)<<uint(bt.linbits) - 1
			edges = append(edges, top-1, top, top+1)
		}
	}
	for li, lay := range prefixCostLayouts {
		for _, x := range edges {
			var ix [576]int32
			for i := range 288 {
				ix[2*i] = x
				ix[2*i+1] = edges[i%len(edges)]
				if i%3 == 0 {
					ix[2*i], ix[2*i+1] = -ix[2*i+1], -ix[2*i]
				}
			}
			checkPrefixCost(t, &ix, lay, 288, fmt.Sprintf("%s x=%d", prefixCostLayoutName(li), x))
		}
	}
}

// TestPrefixCostAllocFree pins the dispatcher to zero allocations. The output
// matrix is declared inside the measured closure so that a kernel declaration
// letting it escape would be counted.
func TestPrefixCostAllocFree(t *testing.T) {
	var ix [576]int32
	for i := range ix {
		ix[i] = int32(i%23) - 11
	}
	lay := &layoutLong[0]
	pb := pairBoundaries(lay, 288)
	if n := testing.AllocsPerRun(200, func() {
		var pc [40][32]int32
		bigValuesPrefixCost(&ix, &pb, lay, &pc)
	}); n != 0 {
		t.Fatalf("bigValuesPrefixCost allocated %v times per run, want 0", n)
	}
}

// TestPrefixCostRejectsBadLayout requires a panic, on every build, for layouts
// the kernels cannot take: nBands above 39 (or negative), and a last pair
// boundary beyond the 288 pairs of ix. Where a kernel is linked the dispatcher
// must panic before it runs; elsewhere the Go reference does through its
// bounds checks.
func TestPrefixCostRejectsBadLayout(t *testing.T) {
	cases := []struct {
		name   string
		nBands int
		last   int // pb[nBands] when in range
	}{
		{"nBands 40", 40, 0},
		{"nBands negative", -1, 0},
		{"pb[nBands] beyond ix", 22, 289},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lay := layoutLong[0]
			lay.nBands = tc.nBands
			var ix [576]int32
			var pb [40]int
			if tc.nBands >= 0 && tc.nBands < len(pb) {
				pb[tc.nBands] = tc.last
			}
			var pc [40][32]int32
			defer func() {
				if recover() == nil {
					t.Fatal("bigValuesPrefixCost accepted an out-of-range layout")
				}
			}()
			bigValuesPrefixCost(&ix, &pb, &lay, &pc)
		})
	}
}
