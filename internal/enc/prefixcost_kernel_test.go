package enc

import (
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// prefixCostLayouts are the long and short band layouts of all three sample
// rates.
var prefixCostLayouts = []*bandLayout{
	&layoutLong[0], &layoutLong[1], &layoutLong[2],
	&layoutShort[0], &layoutShort[1], &layoutShort[2],
}

// checkPrefixCost compares every prefix cost bigValuesPrefixCost and
// bigValuesPrefixCostGo write for one quantized spectrum.
func checkPrefixCost(t *testing.T, ix *[576]int32, lay *bandLayout, bigValues int) {
	t.Helper()
	pb := pairBoundaries(lay, bigValues)
	var got, want [40][32]int32
	bigValuesPrefixCost(ix, &pb, lay, &got)
	bigValuesPrefixCostGo(ix, &pb, lay, &want)
	if got != want {
		for k := range got {
			if got[k] != want[k] {
				t.Fatalf("short=%v bigValues=%d row %d: got %v want %v", lay.short, bigValues, k, got[k], want[k])
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
// spectra whose magnitudes span every table's range, up to maxQuant.
func TestPrefixCostParity(t *testing.T) {
	var seed uint64 = 4242
	scales := []int32{1, 2, 3, 8, 15, 16, 40, 255, 3000, maxQuant}
	for _, lay := range prefixCostLayouts {
		for _, scale := range scales {
			for range 40 {
				var ix [576]int32
				fillPrefixCostSpectrum(&seed, &ix, scale)
				part := partitionSpectrum(&ix)
				checkPrefixCost(t, &ix, lay, part.bigValues)
				checkPrefixCost(t, &ix, lay, int(testsignal.LCG(&seed)*289))
			}
		}
	}
}

// FuzzPrefixCostParity drives the layout, the magnitude ceiling (up to
// maxQuant) and the big-values count beyond the fixed sweep, with the spectrum
// drawn from a seeded LCG.
func FuzzPrefixCostParity(f *testing.F) {
	f.Add(uint8(0), uint16(15), uint16(288), uint64(1))
	f.Add(uint8(3), uint16(8206), uint16(100), uint64(2))
	f.Add(uint8(5), uint16(1), uint16(0), uint64(3))
	f.Fuzz(func(t *testing.T, layR uint8, scaleR, bigValuesR uint16, seed uint64) {
		var ix [576]int32
		fillPrefixCostSpectrum(&seed, &ix, int32(scaleR)%(maxQuant+1))
		checkPrefixCost(t, &ix, prefixCostLayouts[int(layR)%len(prefixCostLayouts)], int(bigValuesR)%289)
	})
}

// TestPrefixCostEscapeBounds places pair magnitudes on both sides of every
// escape table's largest representable value.
func TestPrefixCostEscapeBounds(t *testing.T) {
	edges := make([]int32, 0, 42)
	for _, linbits := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 13} {
		top := int32(15) + int32(1)<<uint(linbits) - 1
		edges = append(edges, top-1, top, top+1)
	}
	edges = append(edges, 0, 1, 14, 15, 16, maxQuant)
	for _, lay := range prefixCostLayouts {
		for _, x := range edges {
			var ix [576]int32
			for i := range 288 {
				ix[2*i] = x
				ix[2*i+1] = edges[i%len(edges)]
				if i%3 == 0 {
					ix[2*i], ix[2*i+1] = -ix[2*i+1], -ix[2*i]
				}
			}
			checkPrefixCost(t, &ix, lay, 288)
		}
	}
}

// TestPrefixCostAllocFree pins the dispatcher to zero allocations.
func TestPrefixCostAllocFree(t *testing.T) {
	var ix [576]int32
	for i := range ix {
		ix[i] = int32(i%23) - 11
	}
	lay := &layoutLong[0]
	pb := pairBoundaries(lay, 288)
	var pc [40][32]int32
	if n := testing.AllocsPerRun(200, func() { bigValuesPrefixCost(&ix, &pb, lay, &pc) }); n != 0 {
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
