package enc

import (
	"math"
	"testing"
)

// Test-side copies of the generator's defining formulas (libm allowed in
// tests). Constants mirror the generator config; see the plan's
// verify-against-annex checklist.
const (
	tstAthClampDB = 70.0
	tstAnchorDB   = 96.0
	tstSpreadCut  = -100.0
)

func tstBark(f float64) float64 {
	return 13*math.Atan(0.00076*f) + 3.5*math.Atan((f/7500)*(f/7500))
}

func tstAthDB(f float64) float64 {
	k := f / 1000
	a := 3.64*math.Pow(k, -0.8) - 6.5*math.Exp(-0.6*(k-3.3)*(k-3.3)) + 1e-3*k*k*k*k
	return math.Min(a, tstAthClampDB)
}

func tstSprdDB(dz float64) float64 {
	tmpx := 0.0
	if dz >= 0.5 && dz <= 2.5 {
		t := dz - 0.5
		tmpx = 8 * math.Min(t*t-2*t, 0)
	}
	tmpy := 15.811389 + 7.5*(dz+0.474) - 17.5*math.Sqrt(1+(dz+0.474)*(dz+0.474))
	return tmpx + tmpy
}

func tstFLine(i, sr int) float64 {
	x := float64(i)
	if i == 0 {
		x = 0.5 // half-bin floor: keeps the ATH pole at DC finite
	}
	return x * float64(sr) / 1024
}

var tstSampleRates = [3]int{44100, 48000, 32000}

// TestPsyPartitionStructure validates the committed partition geometry
// STRUCTURALLY (contiguity, coverage, the 1/3-Bark rule) instead of
// re-deriving the boundary walk, so a 1-ulp libm difference on another
// architecture cannot flip a boundary comparison and fail the test; the
// frozen checksums are the bit authority.
func TestPsyPartitionStructure(t *testing.T) {
	for sri, sr := range tstSampleRates {
		tab := &psyRateTables[sri]
		if tab.nParts < 1 || tab.nParts > psyMaxParts {
			t.Fatalf("sr=%d: nParts = %d out of range", sr, tab.nParts)
		}
		// partOfLine: starts at 0, non-decreasing, steps of at most 1,
		// ends at nParts-1; lines[] matches the counts; every partition
		// has at least one line.
		if tab.partOfLine[0] != 0 || int(tab.partOfLine[512]) != tab.nParts-1 {
			t.Fatalf("sr=%d: partOfLine endpoints %d..%d", sr, tab.partOfLine[0], tab.partOfLine[512])
		}
		counts := make([]int, tab.nParts)
		for i := range 513 {
			if i > 0 {
				d := int(tab.partOfLine[i]) - int(tab.partOfLine[i-1])
				if d < 0 || d > 1 {
					t.Fatalf("sr=%d: partOfLine jump %d at line %d", sr, d, i)
				}
			}
			counts[tab.partOfLine[i]]++
		}
		for b := range tab.nParts {
			if counts[b] < 1 || float64(counts[b]) != tab.lines[b] {
				t.Fatalf("sr=%d part %d: %d lines, table says %v", sr, b, counts[b], tab.lines[b])
			}
			// Bark span of the partition's lines <= 1/3 plus slack for the
			// boundary rule (a partition closes when the NEXT line would
			// exceed the step).
			lo, hi := -1, -1
			for i := range 513 {
				if int(tab.partOfLine[i]) == b {
					if lo < 0 {
						lo = i
					}
					hi = i
				}
			}
			if span := tstBark(tstFLine(hi, sr)) - tstBark(tstFLine(lo, sr)); span > 1.0/3.0+1e-9 {
				t.Fatalf("sr=%d part %d: bark span %.4f > 1/3", sr, b, span)
			}
			// bval sits inside the partition's bark range.
			if bv := tab.bval[b]; bv < tstBark(tstFLine(lo, sr))-1e-9 || bv > tstBark(tstFLine(hi, sr))+1e-9 {
				t.Fatalf("sr=%d part %d: bval %.4f outside [%f, %f]", sr, b,
					bv, tstBark(tstFLine(lo, sr)), tstBark(tstFLine(hi, sr)))
			}
		}
		// bval strictly increasing.
		for b := 1; b < tab.nParts; b++ {
			if tab.bval[b] <= tab.bval[b-1] {
				t.Fatalf("sr=%d: bval not increasing at %d", sr, b)
			}
		}
	}
}

// TestPsyMdctLineMap recomputes partOfMdctLine exactly: MDCT line k maps
// to the FFT line nearest (k+0.5)*1024/1152; no rounding ties exist on
// that rational grid (proof in the plan), so the recomputation is
// platform-exact.
func TestPsyMdctLineMap(t *testing.T) {
	for sri := range tstSampleRates {
		tab := &psyRateTables[sri]
		counts := make([]float64, tab.nParts)
		for k := range 576 {
			idx := int((float64(k)+0.5)*1024/1152 + 0.5)
			if idx > 512 {
				idx = 512
			}
			if tab.partOfMdctLine[k] != tab.partOfLine[idx] {
				t.Fatalf("sri=%d: partOfMdctLine[%d] = %d, want %d",
					sri, k, tab.partOfMdctLine[k], tab.partOfLine[idx])
			}
			counts[tab.partOfMdctLine[k]]++
		}
		for b := range tab.nParts {
			if tab.mlines[b] != counts[b] {
				t.Fatalf("sri=%d part %d: mlines %v, want %v", sri, b, tab.mlines[b], counts[b])
			}
		}
	}
}

// TestPsyTableValues recomputes qthr, norm, and sprd from the defining
// formulas GIVEN the committed partition structure, within a relative
// tolerance that absorbs test-side libm variation (the committed hex
// literals are the runtime truth; checksums pin the bits).
func TestPsyTableValues(t *testing.T) {
	const relTol = 1e-9
	relOK := func(got, want float64) bool {
		if want == 0 {
			return got == 0
		}
		return math.Abs((got-want)/want) <= relTol
	}
	// Full-scale-sine anchor energy from the window's closed form.
	norm := math.Sqrt(8.0 / 3.0)
	var wsum float64
	for i := range 1024 {
		wsum += norm * 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/1024))
	}
	fullBin := (wsum / 2) * (wsum / 2)

	for sri, sr := range tstSampleRates {
		tab := &psyRateTables[sri]
		for b := range tab.nParts {
			var q float64
			for i := range 513 {
				if int(tab.partOfLine[i]) == b {
					q += fullBin * math.Pow(10, (tstAthDB(tstFLine(i, sr))-tstAnchorDB)/10)
				}
			}
			if !relOK(tab.qthr[b], q) {
				t.Errorf("sri=%d qthr[%d] = %g, recompute %g", sri, b, tab.qthr[b], q)
			}
			var rowSum float64
			for bb := range tab.nParts {
				want := 0.0
				if d := tstSprdDB(tab.bval[b] - tab.bval[bb]); d > tstSpreadCut {
					want = math.Pow(10, d/10)
				}
				if !relOK(tab.sprd[b][bb], want) {
					t.Errorf("sri=%d sprd[%d][%d] = %g, recompute %g", sri, b, bb, tab.sprd[b][bb], want)
				}
				rowSum += tab.sprd[b][bb]
			}
			if !relOK(tab.norm[b], 1/rowSum) {
				t.Errorf("sri=%d norm[%d] = %g, recompute %g", sri, b, tab.norm[b], 1/rowSum)
			}
			if tab.qthr[b] <= 0 {
				t.Errorf("sri=%d qthr[%d] = %g, want > 0", sri, b, tab.qthr[b])
			}
		}
		// Spreading shape: the diagonal is each row's maximum and rows
		// decay monotonically for the first few steps on both sides.
		for b := 2; b < tab.nParts-2; b++ {
			d := tab.sprd[b][b]
			for _, bb := range []int{b - 1, b + 1} {
				if tab.sprd[b][bb] >= d {
					t.Errorf("sri=%d: sprd[%d][%d] >= diagonal", sri, b, bb)
				}
			}
			if tab.sprd[b][b-2] > tab.sprd[b][b-1] || tab.sprd[b][b+2] > tab.sprd[b][b+1] {
				t.Errorf("sri=%d: spreading row %d not decaying near diagonal", sri, b)
			}
		}
	}
	if psyLog2TenOver10 <= 0.332 || psyLog2TenOver10 >= 0.3325 {
		t.Errorf("psyLog2TenOver10 = %v, want log2(10)/10 ~= 0.33219", psyLog2TenOver10)
	}
}

// psyTablesSHA / psyLineMapsSHA freeze the tables (same procedure as the
// increment 2 checksums: run once empty, paste the printed digests).
const (
	psyTablesSHA   = "2f0d28bf6e42201d63b246b4326cad0b1c56ebf50b5e7e719f2c3240907f34fa"
	psyLineMapsSHA = "5c5701887ff346c39c9a254d1c170d777a81b6646bd99e20e4ca04546c8b2b71"
)

func TestPsyTablesChecksum(t *testing.T) {
	var vs []float64
	var us []uint16
	for sri := range psyRateTables {
		tab := &psyRateTables[sri]
		vs = append(vs, float64(tab.nParts))
		vs = append(vs, tab.lines[:]...)
		vs = append(vs, tab.mlines[:]...)
		vs = append(vs, tab.bval[:]...)
		vs = append(vs, tab.qthr[:]...)
		vs = append(vs, tab.norm[:]...)
		for b := range tab.sprd { //nolint:gocritic // false positive: tab.sprd[b][:] depends on the loop variable b, this flattens a 2D matrix, not a repeated value
			vs = append(vs, tab.sprd[b][:]...)
		}
		for _, v := range tab.partOfLine {
			us = append(us, uint16(v))
		}
		for _, v := range tab.partOfMdctLine {
			us = append(us, uint16(v))
		}
	}
	vs = append(vs, psyLog2TenOver10)
	gotF, gotU := sha256Float64s(vs...), sha256U16(us...)
	if psyTablesSHA == "" || psyLineMapsSHA == "" {
		t.Fatalf("FREEZE ME:\nconst psyTablesSHA = %q\nconst psyLineMapsSHA = %q", gotF, gotU)
	}
	if gotF != psyTablesSHA {
		t.Fatalf("psy float tables changed: %s, frozen %s", gotF, psyTablesSHA)
	}
	if gotU != psyLineMapsSHA {
		t.Fatalf("psy line maps changed: %s, frozen %s", gotU, psyLineMapsSHA)
	}
}
