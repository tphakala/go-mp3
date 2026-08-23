package enc

import (
	"math"
	"slices"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
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
func TestPsyPartitionStructure(t *testing.T) { //nolint:dupl // structurally identical to TestPsyShortPartitionStructure by design (same construction discipline, decision 8); the long table's dimensions (513 lines, psyRateTables, tstFLine) differ from the short one's, so this is not the same test
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
			idx := min(int((float64(k)+0.5)*1024/1152+0.5), 512)
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

// tstFLineShort is tstFLine's short-path sibling: FFT line i of the
// 256-point transform maps to frequency i*sr/256 (four times the spacing
// of tstFLine's i*sr/1024, since the short window is a quarter of the long
// one's length but still spans the full 0..sr/2 Nyquist range in 129 bins
// instead of 513), with the same half-bin floor at DC.
func tstFLineShort(i, sr int) float64 {
	x := float64(i)
	if i == 0 {
		x = 0.5
	}
	return x * float64(sr) / 256
}

// TestPsyShortPartitionStructure is TestPsyPartitionStructure's short-path
// sibling: same structural checks (contiguity, coverage, the 1/3-Bark
// rule, strictly increasing bval) over psyShortTables' 129-line domain
// instead of psyRateTables' 513-line one.
func TestPsyShortPartitionStructure(t *testing.T) { //nolint:dupl // structurally identical to TestPsyPartitionStructure by design (same construction discipline, decision 8); the short table's dimensions (129 lines, psyShortTables, tstFLineShort) differ from the long one's, so this is not the same test
	for sri, sr := range tstSampleRates {
		tab := &psyShortTables[sri]
		if tab.nParts < 1 || tab.nParts > psyMaxPartsS {
			t.Fatalf("sr=%d: nParts = %d out of range", sr, tab.nParts)
		}
		if tab.partOfLine[0] != 0 || int(tab.partOfLine[128]) != tab.nParts-1 {
			t.Fatalf("sr=%d: partOfLine endpoints %d..%d", sr, tab.partOfLine[0], tab.partOfLine[128])
		}
		counts := make([]int, tab.nParts)
		for i := range 129 {
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
			lo, hi := -1, -1
			for i := range 129 {
				if int(tab.partOfLine[i]) == b {
					if lo < 0 {
						lo = i
					}
					hi = i
				}
			}
			if span := tstBark(tstFLineShort(hi, sr)) - tstBark(tstFLineShort(lo, sr)); span > 1.0/3.0+1e-9 {
				t.Fatalf("sr=%d part %d: bark span %.4f > 1/3", sr, b, span)
			}
			if bv := tab.bval[b]; bv < tstBark(tstFLineShort(lo, sr))-1e-9 || bv > tstBark(tstFLineShort(hi, sr))+1e-9 {
				t.Fatalf("sr=%d part %d: bval %.4f outside [%f, %f]", sr, b,
					bv, tstBark(tstFLineShort(lo, sr)), tstBark(tstFLineShort(hi, sr)))
			}
		}
		for b := 1; b < tab.nParts; b++ {
			if tab.bval[b] <= tab.bval[b-1] {
				t.Fatalf("sr=%d: bval not increasing at %d", sr, b)
			}
		}
	}
}

// TestPsyShortMdctLineMap recomputes partOfBand exactly: short-window
// coding-order line k maps to the FFT line nearest (k+0.5)*256/384 (this
// task's derivation of the long path's (k+0.5)*1024/1152 ratio, psytables.go's
// psyShortTable doc comment); the same numerator/denominator parity that
// rules out rounding ties on the long grid rules them out here, so the
// recomputation is platform-exact. Also confirms mlines is never zero: the
// short path's invMlines-style division (analyzeShort) would panic on a
// zero-count partition.
func TestPsyShortMdctLineMap(t *testing.T) {
	for sri := range tstSampleRates {
		tab := &psyShortTables[sri]
		counts := make([]float64, tab.nParts)
		for k := range 192 {
			idx := min(int((float64(k)+0.5)*256/384+0.5), 128)
			if tab.partOfBand[k] != tab.partOfLine[idx] {
				t.Fatalf("sri=%d: partOfBand[%d] = %d, want %d",
					sri, k, tab.partOfBand[k], tab.partOfLine[idx])
			}
			counts[tab.partOfBand[k]]++
		}
		for b := range tab.nParts {
			if tab.mlines[b] != counts[b] {
				t.Fatalf("sri=%d part %d: mlines %v, want %v", sri, b, tab.mlines[b], counts[b])
			}
			if tab.mlines[b] == 0 {
				t.Fatalf("sri=%d part %d: mlines is zero, invMlines-style division would divide by zero", sri, b)
			}
		}
	}
}

// TestPsyShortTableValues is TestPsyTableValues's short-path sibling:
// recomputes qthr, norm, and sprd from the same defining formulas, using
// the 256-point Hann window's own full-scale-sine anchor energy instead of
// the 1024-point window's.
func TestPsyShortTableValues(t *testing.T) {
	const relTol = 1e-9
	relOK := func(got, want float64) bool {
		if want == 0 {
			return got == 0
		}
		return math.Abs((got-want)/want) <= relTol
	}
	norm := math.Sqrt(8.0 / 3.0)
	var wsum float64
	for i := range 256 {
		wsum += norm * 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/256))
	}
	fullBin := (wsum / 2) * (wsum / 2)

	for sri, sr := range tstSampleRates {
		tab := &psyShortTables[sri]
		for b := range tab.nParts {
			var q float64
			for i := range 129 {
				if int(tab.partOfLine[i]) == b {
					q += fullBin * math.Pow(10, (tstAthDB(tstFLineShort(i, sr))-tstAnchorDB)/10)
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
	}
}

// psyShortTablesSHA / psyShortLineMapsSHA freeze psyShortTables, the same
// procedure as psyTablesSHA / psyLineMapsSHA above.
const (
	psyShortTablesSHA   = "4dc56f329bac85c1b055b5816eca465b7b9f53b98024d191a5651f209f47d29a"
	psyShortLineMapsSHA = "95a2546db6a749059103ddc7cc5a4f2dca4e139eebbe9e2d92b188e057a2ec2a"
)

func TestPsyShortTablesChecksum(t *testing.T) {
	var vs []float64
	var us []uint16
	for sri := range psyShortTables {
		tab := &psyShortTables[sri]
		vs = append(vs, float64(tab.nParts))
		vs = append(vs, tab.lines[:]...)
		vs = append(vs, tab.bval[:]...)
		vs = append(vs, tab.qthr[:]...)
		vs = append(vs, tab.norm[:]...)
		vs = append(vs, tab.mlines[:]...)
		for b := range tab.sprd { //nolint:gocritic // false positive: tab.sprd[b][:] depends on the loop variable b, this flattens a 2D matrix, not a repeated value
			vs = append(vs, tab.sprd[b][:]...)
		}
		for _, v := range tab.partOfLine {
			us = append(us, uint16(v))
		}
		for _, v := range tab.partOfBand {
			us = append(us, uint16(v))
		}
	}
	gotF, gotU := sha256Float64s(vs...), sha256U16(us...)
	if psyShortTablesSHA == "" || psyShortLineMapsSHA == "" {
		t.Fatalf("FREEZE ME:\nconst psyShortTablesSHA = %q\nconst psyShortLineMapsSHA = %q", gotF, gotU)
	}
	if gotF != psyShortTablesSHA {
		t.Fatalf("psy short float tables changed: %s, frozen %s", gotF, psyShortTablesSHA)
	}
	if gotU != psyShortLineMapsSHA {
		t.Fatalf("psy short line maps changed: %s, frozen %s", gotU, psyShortLineMapsSHA)
	}
}

// polarUnpredictability is the annex's polar form (r/f extrapolation),
// evaluated with unrestricted test-side libm: the independent reference
// proving the runtime's Cartesian reformulation (roadmap D1, constraint
// R2) computes the same quantity. Plan-time measurement: max abs diff
// 1e-15 over 1e6 random triples.
func polarUnpredictability(re, im, re1, im1, re2, im2 float64) float64 {
	r := math.Hypot(re, im)
	r1 := math.Hypot(re1, im1)
	r2m := math.Hypot(re2, im2)
	if r1 == 0 || r2m == 0 || (r == 0 && 2*r1-r2m == 0) {
		return 1
	}
	f := math.Atan2(im, re)
	f1 := math.Atan2(im1, re1)
	f2 := math.Atan2(im2, re2)
	rp := 2*r1 - r2m
	fp := 2*f1 - f2
	num := math.Hypot(r*math.Cos(f)-rp*math.Cos(fp), r*math.Sin(f)-rp*math.Sin(fp))
	return num / (r + math.Abs(rp))
}

func TestPsyCartesianMatchesPolar(t *testing.T) {
	var p PsyModel
	p.Reset(0)
	seed := uint64(31)
	// Inject two random history spectra directly, then analyze a random
	// third; compare every bin's cw against the polar reference.
	var h1re, h1im, h2re, h2im [513]float64
	for i := range 513 {
		h2re[i], h2im[i] = testsignal.LCGSigned(&seed)*100, testsignal.LCGSigned(&seed)*100
		h1re[i], h1im[i] = testsignal.LCGSigned(&seed)*100, testsignal.LCGSigned(&seed)*100
	}
	p.prevRe[1], p.prevIm[1] = h2re, h2im
	p.prevRe[0], p.prevIm[0] = h1re, h1im
	pcm := make([]float64, 1024)
	for i := range pcm {
		pcm[i] = testsignal.LCGSigned(&seed)
	}
	p.analyzeSpectrum(pcm)
	for i := range 513 {
		// The current spectrum is now history[0]; recover it from there.
		re, im := p.prevRe[0][i], p.prevIm[0][i]
		want := polarUnpredictability(re, im, h1re[i], h1im[i], h2re[i], h2im[i])
		if d := math.Abs(p.cw[i] - want); d > 1e-12 {
			t.Fatalf("bin %d: cw = %v, polar reference %v (diff %.3g)", i, p.cw[i], want, d)
		}
		if p.cw[i] < 0 || p.cw[i] > 1+1e-9 {
			t.Fatalf("bin %d: cw = %v outside [0, 1]", i, p.cw[i])
		}
	}
}

func TestPsyZeroHistoryFallback(t *testing.T) {
	var p PsyModel
	p.Reset(0)
	pcm := make([]float64, 1024)
	seed := uint64(41)
	for i := range pcm {
		pcm[i] = testsignal.LCGSigned(&seed)
	}
	// First and second granule after Reset: zero history means r1 == 0 or
	// r2 == 0 at every bin, so R3 forces cw == 1 everywhere.
	for g := range 2 {
		p.analyzeSpectrum(pcm)
		for i := range 513 {
			if p.cw[i] != 1 {
				t.Fatalf("granule %d bin %d: cw = %v, want exactly 1 (R3)", g, i, p.cw[i])
			}
		}
	}
}

func TestPsySineUnpredictability(t *testing.T) {
	// A steady exact-bin sine is perfectly predictable: after two warmup
	// granules, cw at the tone's bin must be near 0. White noise is not:
	// its mean cw must be large. Measured once (this deterministic
	// computation is bit-identical across arches, so the margins below are
	// not a source of cross-arch flakiness): sine cw[64] = 1.49306328389232e-16,
	// noise mean cw = 0.731515056185343. Floors are tightened to those
	// measurements with a 10x/0.7x margin per the brief.
	const bin = 64                        // 64 * 44100/1024 = 2756 Hz
	const sineCeil = 1.49306328389232e-15 // measured 1.49306328389232e-16 * 10
	const noiseFloor = 0.5120605393297401 // measured 0.731515056185343 * 0.7
	var p PsyModel
	p.Reset(0)
	pcm := make([]float64, 1024)
	for g := range 5 {
		for i := range pcm {
			n := g*576 + i // causal sliding window, hop 576
			pcm[i] = 0.5 * math.Cos(2*math.Pi*float64(bin)*float64(n)/1024)
		}
		p.analyzeSpectrum(pcm)
	}
	if p.cw[bin] > sineCeil {
		t.Errorf("steady sine: cw[%d] = %v, want < %v", bin, p.cw[bin], sineCeil)
	}

	p.Reset(0)
	seed := uint64(51)
	var meanCW float64
	for range 5 {
		for i := range pcm {
			pcm[i] = testsignal.LCGSigned(&seed)
		}
		p.analyzeSpectrum(pcm)
	}
	for i := 1; i < 512; i++ {
		meanCW += p.cw[i]
	}
	meanCW /= 511
	if meanCW < noiseFloor {
		t.Errorf("white noise: mean cw = %v, want >= %v", meanCW, noiseFloor)
	}
}

// psySpectrumGoldenSHA: cross-arch determinism gate; never re-freeze on an
// arch mismatch.
const psySpectrumGoldenSHA = "0ee1f74fc294638167ef90f6e7d028626d5edfc118b51b44fbe3576a27a0f6c2" // FROZEN in Task 2 Step 4

func TestPsySpectrumGolden(t *testing.T) {
	var p PsyModel
	p.Reset(1) // 48 kHz
	pcm := make([]float64, 1024)
	seed := uint64(61)
	out := make([]float64, 0, (len(p.r2)+len(p.cw))*4)
	for range 4 {
		for i := range pcm {
			pcm[i] = testsignal.LCGSigned(&seed)
		}
		p.analyzeSpectrum(pcm)
		out = append(out, p.r2[:]...)
		out = append(out, p.cw[:]...)
	}
	got := sha256Float64s(out...)
	if psySpectrumGoldenSHA == "" {
		t.Fatalf("FREEZE ME: const psySpectrumGoldenSHA = %q", got)
	}
	if got != psySpectrumGoldenSHA {
		t.Fatalf("spectrum output changed: %s, frozen %s", got, psySpectrumGoldenSHA)
	}
}

// TestPsyAnalyzeSpectrumAllocs pins analyzeSpectrum to zero heap
// allocations per call: fftRe, fftIm, r2, cw, and the spectral history are
// all PsyModel fields sized once in Reset, so a warm call must not grow
// anything on the heap.
func TestPsyAnalyzeSpectrumAllocs(t *testing.T) {
	var p PsyModel
	p.Reset(0)
	pcm := make([]float64, 1024)
	seed := uint64(71)
	for i := range pcm {
		pcm[i] = testsignal.LCGSigned(&seed)
	}
	for range 2 { // prime both history slots so the measured call takes the full R2 branch, not the R3 fallback
		p.analyzeSpectrum(pcm)
	}

	if n := testing.AllocsPerRun(50, func() { p.analyzeSpectrum(pcm) }); n != 0 {
		t.Fatalf("analyzeSpectrum allocates: %v allocs per run, want 0", n)
	}
}

// analyzeN runs n granules of a generator function through spectrum +
// thresholds, returning the model for inspection.
func analyzeN(t *testing.T, srIndex, n int, gen func(g int, pcm []float64)) *PsyModel {
	t.Helper()
	var p PsyModel
	p.Reset(srIndex)
	pcm := make([]float64, 1024)
	for g := range n {
		gen(g, pcm)
		p.analyzeSpectrum(pcm)
		p.computeThresholds()
	}
	return &p
}

func sineGen(bin int, amp float64) func(int, []float64) {
	return func(g int, pcm []float64) {
		for i := range pcm {
			n := g*576 + i
			pcm[i] = amp * math.Cos(2*math.Pi*float64(bin)*float64(n)/1024)
		}
	}
}

// toneFS4 is a bit-portable steady tone at fs/4 (bin 256 of the 1024-point
// analysis). cos(2*pi*(fs/4)*n/fs) = cos(pi*n/2) folds to the EXACT sequence
// [1, 0, -1, 0][n mod 4], so the generated PCM contains no libm cos and is
// bit-identical on every architecture (amd64 and arm64 differ by ~1 ulp in
// math.Cos). The frozen PsyModel golden hashes its output, so its tone input
// must be cross-arch exact; a plain amp*{1,0,-1} product is a bare multiply
// (no add to fuse) and stays exact. The hop of 576 is a multiple of 4, so the
// tone is phase-locked granule to granule: a perfectly steady, tonal input.
func toneFS4(amp float64) func(int, []float64) {
	quarter := [4]float64{1, 0, -1, 0}
	return func(g int, pcm []float64) {
		for i := range pcm {
			n := g*576 + i
			pcm[i] = amp * quarter[n&3]
		}
	}
}

func noiseGen(seed uint64) func(int, []float64) {
	s := seed
	return func(_ int, pcm []float64) {
		for i := range pcm {
			pcm[i] = testsignal.LCGSigned(&s) * 0.5
		}
	}
}

func TestPsyThresholdFloor(t *testing.T) {
	for _, gen := range []func(int, []float64){
		sineGen(64, 0.5), noiseGen(71),
		func(_ int, pcm []float64) { clear(pcm) }, // silence
	} {
		p := analyzeN(t, 0, 4, gen)
		for b := range p.tab.nParts {
			if p.nb[b] < p.tab.qthr[b] {
				t.Fatalf("part %d: nb = %g below qthr = %g", b, p.nb[b], p.tab.qthr[b])
			}
		}
	}
}

func TestPsyTonalityContrast(t *testing.T) {
	// A steady sine demands more SNR (tonal masker, flat TMN 15.5 dB)
	// than white noise (NMT 5.5 dB): e/nb at the masker's partition must
	// be clearly larger for the sine. Measured once (this deterministic
	// computation is bit-identical across arches, so the margins below
	// are not a source of cross-arch flakiness): sineDB = 20.899761521043345,
	// noiseDB = 4.529705071145088 (gap 16.37 dB). Floors are tightened to
	// those measurements with margin: sine >= 18 (2.9 dB below measured),
	// gap >= 10 dB (6.37 dB below measured).
	const bin = 64
	ps := analyzeN(t, 0, 5, sineGen(bin, 0.5))
	bSine := int(ps.tab.partOfLine[bin])
	sineDB := 10 * math.Log10(ps.e[bSine]/ps.nb[bSine])

	pn := analyzeN(t, 0, 5, noiseGen(81))
	var noiseDB float64
	cnt := 0
	for b := 5; b < pn.tab.nParts-5; b++ {
		if pn.e[b] > 0 && pn.nb[b] > pn.tab.qthr[b] {
			noiseDB += 10 * math.Log10(pn.e[b]/pn.nb[b])
			cnt++
		}
	}
	if cnt == 0 {
		t.Fatal("noise contrast: no partition exceeded qthr, nothing measured")
	}
	noiseDB /= float64(cnt)

	if sineDB < 18 {
		t.Errorf("sine: e/nb = %.1f dB at masker partition, want >= 18", sineDB)
	}
	if noiseDB > sineDB-10 {
		t.Errorf("noise mean e/nb = %.1f dB, want at least 10 dB below sine's %.1f", noiseDB, sineDB)
	}
}

func TestPsySpreadingEffect(t *testing.T) {
	// A single tone must raise thresholds in NEIGHBOR partitions above
	// their quiet floor, decaying with distance.
	const bin = 128
	p := analyzeN(t, 0, 5, sineGen(bin, 0.5))
	b := int(p.tab.partOfLine[bin])
	if p.nb[b+1] <= p.tab.qthr[b+1] || p.nb[b-1] <= p.tab.qthr[b-1] {
		t.Fatalf("neighbors of tone partition %d not elevated above qthr", b)
	}
	if !(p.nb[b] > p.nb[b+2] && p.nb[b+2] > p.nb[b+4]) {
		t.Errorf("upward spreading not decaying: nb[b]=%g nb[b+2]=%g nb[b+4]=%g",
			p.nb[b], p.nb[b+2], p.nb[b+4])
	}
}

func TestPsyPreEchoRule(t *testing.T) {
	// Quiet history then a loud attack: the attack granule's nb is capped
	// by rpelev*nb1 computed from the QUIET frames, far below the raw
	// spread threshold the loud frame alone would produce.
	var p PsyModel
	p.Reset(0)
	pcm := make([]float64, 1024)
	quiet := sineGen(64, 1e-4)
	loud := sineGen(64, 0.9)
	for g := range 4 {
		quiet(g, pcm)
		p.analyzeSpectrum(pcm)
		p.computeThresholds()
	}
	nbQuiet := p.nb[p.tab.partOfLine[64]]
	loud(4, pcm)
	p.analyzeSpectrum(pcm)
	p.computeThresholds()
	b := p.tab.partOfLine[64]
	cap1 := psyRpelev * nbQuiet
	if p.nb[b] > cap1*(1+1e-9) && p.nb[b] > p.tab.qthr[b]*(1+1e-9) {
		t.Errorf("attack nb = %g exceeds pre-echo cap %g (quiet nb was %g)", p.nb[b], cap1, nbQuiet)
	}
}

const psyThresholdGoldenSHA = "32e0a8cf58619116d85c52001d236ef958cea04531aac1d269ee0e798b30f460" // FROZEN in Task 3 Step 4

func TestPsyThresholdGolden(t *testing.T) {
	p := analyzeN(t, 2, 4, noiseGen(91)) // 32 kHz leg for variety
	out := make([]float64, 0, p.tab.nParts+p.tab.nParts)
	out = append(out, p.e[:p.tab.nParts]...)
	out = append(out, p.nb[:p.tab.nParts]...)
	got := sha256Float64s(out...)
	if psyThresholdGoldenSHA == "" {
		t.Fatalf("FREEZE ME: const psyThresholdGoldenSHA = %q", got)
	}
	if got != psyThresholdGoldenSHA {
		t.Fatalf("threshold output changed: %s, frozen %s", got, psyThresholdGoldenSHA)
	}
}

func TestPsyModelSilenceAndTransientPE(t *testing.T) {
	// Measured once (deterministic across arches, so the margins below are
	// not a source of cross-arch flakiness): silence PE = 0 exactly (every
	// partition's e/nb ratio sits at or below 1, so plog never
	// accumulates); transient PE = 2423.6513074428426. Floors are
	// tightened to those measurements with margin: silence <= 0.01 (a
	// hair above exact 0), transient >= 2000 (about 17.5% below
	// measured).
	var p PsyModel
	p.Reset(0)
	pcm := make([]float64, 1024)
	var out PsyOut
	for range 4 {
		clear(pcm)
		p.AnalyzeGranule(pcm, &out)
	}
	if out.PE > 0.01 {
		t.Errorf("silence PE = %v, want near 0", out.PE)
	}
	// A click after silence: PE must spike.
	clear(pcm)
	for i := 900; i < 1024; i++ {
		pcm[i] = 0.9
	}
	p.AnalyzeGranule(pcm, &out)
	if out.PE < 2000 {
		t.Errorf("transient PE = %v, want a spike (>= 2000)", out.PE)
	}
}

// TestPsyModelLevelAnchor gates roadmap D9: a tone at the assumed quiet
// threshold (ATH at its frequency, under the full-scale = 96 dB SPL
// anchor) must land within a few dB of its partition's qthr.
func TestPsyModelLevelAnchor(t *testing.T) {
	const bin = 90 // 90*44100/1024 ~ 3876 Hz, near the ATH minimum
	f := float64(bin) * 44100 / 1024
	ampDB := tstAthDB(f) - tstAnchorDB // dB below full scale
	amp := math.Pow(10, ampDB/20)      // test-side libm fine
	var p PsyModel
	p.Reset(0)
	pcm := make([]float64, 1024)
	var out PsyOut
	gen := sineGen(bin, amp)
	for g := range 5 {
		gen(g, pcm)
		p.AnalyzeGranule(pcm, &out)
	}
	b := int(p.tab.partOfLine[bin])
	got := 10 * math.Log10(p.e[b]/p.tab.qthr[b])
	if math.Abs(got) > 6 {
		t.Errorf("quiet-threshold tone: partition energy %.1f dB relative to qthr, want within +-6 dB", got)
	}
}

func TestPsyModelSfbConservation(t *testing.T) {
	// The per-MDCT-line density mapping conserves total threshold energy:
	// sum of Xmin over the 22 sfbs (which tile all 576 MDCT lines) equals
	// sum of nb over partitions, within accumulation rounding.
	p := analyzeN(t, 0, 4, noiseGen(111))
	var out PsyOut
	pcm := make([]float64, 1024)
	noiseGen(112)(0, pcm)
	p.AnalyzeGranule(pcm, &out)
	var sumX, sumNb float64
	for s := range 22 {
		sumX += out.Xmin[s]
	}
	for b := range p.tab.nParts {
		sumNb += p.nb[b]
	}
	if r := math.Abs((sumX - sumNb) / sumNb); r > 1e-9 {
		t.Errorf("sfb mapping not conservative: sum Xmin %g vs sum nb %g (rel %.3g)", sumX, sumNb, r)
	}
	for s := range 22 {
		if out.Xmin[s] <= 0 {
			t.Errorf("Xmin[%d] = %v, want > 0 (qthr floor)", s, out.Xmin[s])
		}
	}
}

func TestPsyModelEnergyMonotone(t *testing.T) {
	// Doubling the input amplitude quadruples En (energy domain) and
	// never lowers Xmin (thresholds track the masker).
	run := func(amp float64) ([22]float64, [22]float64) {
		var p PsyModel
		p.Reset(0)
		pcm := make([]float64, 1024)
		var out PsyOut
		gen := sineGen(64, amp)
		for g := range 5 {
			gen(g, pcm)
			p.AnalyzeGranule(pcm, &out)
		}
		return out.En, out.Xmin
	}
	en1, x1 := run(0.2)
	en2, x2 := run(0.4)
	b := 0
	for s := range 22 {
		if en1[s] > en1[b] {
			b = s
		}
	}
	if r := en2[b] / en1[b]; math.Abs(r-4) > 0.2 {
		t.Errorf("En scaling: x4 expected on 2x amplitude, got %v", r)
	}
	for s := range 22 {
		if x2[s] < x1[s]*(1-1e-9) {
			t.Errorf("Xmin[%d] decreased on louder input: %g -> %g", s, x1[s], x2[s])
		}
	}
}

const psyModelGoldenSHA = "7f9a7876b404fecead2ab1c1f2f10aa61dd0b2d612a15c6c3113c48155799e40" // FROZEN in Task 4 Step 4 (bit-portable fs/4 tone input)

func TestPsyModelGolden(t *testing.T) {
	// Three programs x three rates: LCG noise, a steady tone, and a
	// silence-then-click transient; hash all PsyOut fields.
	out := make([]float64, 0, 3*3*(len(PsyOut{}.Xmin)+len(PsyOut{}.En)+len(PsyOut{}.SMR)+1))
	for sri := range 3 {
		for prog := range 3 {
			var p PsyModel
			p.Reset(sri)
			pcm := make([]float64, 1024)
			var po PsyOut
			seed := uint64(120 + prog)
			for g := range 4 {
				switch prog {
				case 0:
					for i := range pcm {
						pcm[i] = testsignal.LCGSigned(&seed) * 0.6
					}
				case 1:
					toneFS4(0.5)(g, pcm)
				case 2:
					clear(pcm)
					if g == 3 {
						for i := 800; i < 1024; i++ {
							pcm[i] = 0.8
						}
					}
				}
				p.AnalyzeGranule(pcm, &po)
			}
			out = append(out, po.Xmin[:]...)
			out = append(out, po.En[:]...)
			out = append(out, po.SMR[:]...)
			out = append(out, po.PE)
		}
	}
	got := sha256Float64s(out...)
	if psyModelGoldenSHA == "" {
		t.Fatalf("FREEZE ME: const psyModelGoldenSHA = %q", got)
	}
	if got != psyModelGoldenSHA {
		t.Fatalf("PsyModel output changed: %s, frozen %s", got, psyModelGoldenSHA)
	}
}

// shortWindowCenters are the three 256-sample analysis window centers
// (decision 8: gStart + 192w - 32) within the re-centered 1024-sample
// buffer AnalyzeGranule receives, where gStart = 224 is the granule's first
// sample inside that buffer (decision 11: the granule sits centered in the
// 1024-sample window, with 224 samples of history before it and 224 of
// lookahead after, rather than the pre-increment-7 causal placement at
// gStart = 448). Test-only convenience shared by every short-path test
// below that drives analyzeShortWindow/computeShortThresholds directly
// instead of through AnalyzeGranule.
var shortWindowCenters = [3]int{224 - 32, 224 + 160, 224 + 352}

// TestPsyShortSpreadingEffect is TestPsySpreadingEffect's short-path
// sibling: a steady tone must raise thresholds in NEIGHBOR partitions
// above their quiet floor, decaying with distance, checked independently
// at each of the three window centers (the short path shares no state
// across windows, decision 8).
func TestPsyShortSpreadingEffect(t *testing.T) {
	const shortBin = 40 // 40*44100/256 ~ 6891 Hz; the matching 1024-domain bin is 4x
	tab := &psyShortTables[0]
	var p PsyModel
	p.Reset(0)
	pcm := make([]float64, 1024)
	sineGen(4*shortBin, 0.5)(0, pcm)
	for w, center := range shortWindowCenters {
		p.analyzeShortWindow(pcm, center)
		p.computeShortThresholds(tab)
		b := int(tab.partOfLine[shortBin])
		if b < 2 || b > tab.nParts-3 {
			t.Fatalf("bin %d partition %d too close to an edge for this test", shortBin, b)
		}
		if p.nbS[b+1] <= tab.qthr[b+1] || p.nbS[b-1] <= tab.qthr[b-1] {
			t.Fatalf("window %d: neighbors of tone partition %d not elevated above qthr", w, b)
		}
		if !(p.nbS[b] > p.nbS[b+2] && p.nbS[b+2] > p.nbS[b+4]) {
			t.Errorf("window %d: upward spreading not decaying: nbS[b]=%g nbS[b+2]=%g nbS[b+4]=%g",
				w, p.nbS[b], p.nbS[b+2], p.nbS[b+4])
		}
	}
}

// TestPsyShortNoiseFloor confirms the short path's required SNR is the
// fixed, NMT-dominated constant decision 8 and resolve-at-impl item 7
// settle on (psyNmtDB, no per-partition tonality estimate). Measured once
// (deterministic across arches, so the ceiling below is not a source of
// cross-arch flakiness): white noise's mean e/nb ratio over interior
// partitions (away from edges where qthr dominates) is 3.60 dB, below the
// flat psyNmtDB=5.5 constant itself because the spreading normalization
// pulls each partition's threshold from a weighted average of its
// neighbors (the same dilution TestPsyTonalityContrast's noiseDB=4.53
// measurement shows on the long path's own NMT floor). The ceiling gates
// against ever drifting toward the long path's 15.5 dB tonal (TMN) level,
// which would mean a stray tonality estimate crept into this path.
func TestPsyShortNoiseFloor(t *testing.T) {
	tab := &psyShortTables[0]
	var p PsyModel
	p.Reset(0)
	pcm := make([]float64, 1024)
	seed := uint64(211)
	for i := range pcm {
		pcm[i] = testsignal.LCGSigned(&seed) * 0.5
	}
	p.analyzeShortWindow(pcm, shortWindowCenters[0])
	p.computeShortThresholds(tab)
	var sumDB float64
	cnt := 0
	for b := 5; b < tab.nParts-5; b++ {
		if p.eS[b] > 0 && p.nbS[b] > tab.qthr[b] {
			sumDB += 10 * math.Log10(p.eS[b]/p.nbS[b])
			cnt++
		}
	}
	if cnt == 0 {
		t.Fatal("noise floor: no partition exceeded qthr, nothing measured")
	}
	meanDB := sumDB / float64(cnt)
	const noiseCeil = 10.0 // well below psyTmnDB=15.5, comfortably above the measured 3.60
	if meanDB > noiseCeil {
		t.Errorf("white noise: mean e/nb = %.2f dB, want <= %.1f dB (NMT-dominated, not tonal)", meanDB, noiseCeil)
	}
}

// TestPsyShortThresholdFloor is TestPsyThresholdFloor's short-path
// sibling: nbS must never undercut qthr, for tonal, noise, and silent
// content, at every one of the three window centers.
func TestPsyShortThresholdFloor(t *testing.T) {
	tab := &psyShortTables[0]
	var p PsyModel
	p.Reset(0)
	pcm := make([]float64, 1024)
	seed := uint64(221)
	programs := []func([]float64){
		func(buf []float64) { sineGen(160, 0.5)(0, buf) },
		func(buf []float64) {
			for i := range buf {
				buf[i] = testsignal.LCGSigned(&seed) * 0.5
			}
		},
		func(buf []float64) { clear(buf) },
	}
	for _, gen := range programs {
		gen(pcm)
		for _, center := range shortWindowCenters {
			p.analyzeShortWindow(pcm, center)
			p.computeShortThresholds(tab)
			for b := range tab.nParts {
				if p.nbS[b] < tab.qthr[b] {
					t.Fatalf("part %d: nbS = %g below qthr = %g", b, p.nbS[b], tab.qthr[b])
				}
			}
		}
	}
}

// TestPsyShortPerWindowResolution is the reason short blocks exist: a
// burst confined to window 2's own span must raise window 2's XminS bands
// relative to window 0's, which sees none of it. Under the decision-11
// re-centered window (gStart = 224: window 0 spans [64,320), window 1
// [256,512), window 2 [448,704)), the burst sits in pcm[600:678), inside
// window 2's span but outside both window 0's and window 1's. Measured once
// (deterministic across arches) after the decision-11 re-centering: sum0 =
// 980.6863938210277, sum2 = 3476.569584518944 (ratio 3.545x). The floor
// below is tightened to that measurement with margin (2.5x, comfortably
// below the measured 3.545x).
func TestPsyShortPerWindowResolution(t *testing.T) {
	var p PsyModel
	p.Reset(0)
	pcm := make([]float64, 1024)
	clear(pcm)
	for i := 600; i < 678; i++ {
		pcm[i] = 0.9
	}
	var out PsyOut
	p.AnalyzeGranule(pcm, &out)
	var sum0, sum2 float64
	for s := range 13 {
		sum0 += out.XminS[3*s+0]
		sum2 += out.XminS[3*s+2]
	}
	if sum2 <= sum0*2.5 {
		t.Errorf("window 2 (containing the burst) XminS sum = %g, want clearly above window 0's %g", sum2, sum0)
	}
}

// TestPsyShortPESSpike is TestPsyModelSilenceAndTransientPE's short-path
// sibling. Measured once (deterministic across arches, so the margin below
// is not a source of cross-arch flakiness): silence PES = 0 exactly;
// transient PES (the same repositioned burst TestPsyShortPerWindowResolution
// uses, decision-11 re-centering) = 157.4854063118873. The floor is
// tightened to that measurement with margin: >= 100 (about 37% below
// measured).
func TestPsyShortPESSpike(t *testing.T) {
	var p PsyModel
	p.Reset(0)
	pcm := make([]float64, 1024)
	var out PsyOut
	for range 4 {
		clear(pcm)
		p.AnalyzeGranule(pcm, &out)
	}
	if out.PES > 0.01 {
		t.Errorf("silence PES = %v, want near 0", out.PES)
	}
	clear(pcm)
	for i := 600; i < 678; i++ {
		pcm[i] = 0.9
	}
	p.AnalyzeGranule(pcm, &out)
	if out.PES < 100 {
		t.Errorf("transient PES = %v, want a spike (>= 100)", out.PES)
	}
}

// psyShortGoldenSHA: cross-arch determinism gate; never re-freeze on an
// arch mismatch (arm64 verification is task B4's job; this is the local
// amd64 freeze).
//
// Re-frozen in Phase 4 increment 7 Task B2 (decision-11 psymodel
// re-centering, deferred from Task B1's causal placeholder slice): moving
// analyzeShort's internal gStart from 448 (causal) to 224 (centered)
// changes every short-path threshold this golden hashes, even though none
// of the analysis formulas themselves changed.
const psyShortGoldenSHA = "1b4ff08b4f5218beb3bad93a88e01dd89ee9e3b1047e45724546cd97a249f101"

func TestPsyShortGolden(t *testing.T) {
	// Three rates x two programs: LCG noise and a silence-then-burst
	// transient confined to pcm[600:678) on the final granule (the same
	// per-window-confined burst TestPsyShortPerWindowResolution uses, inside
	// short window 2's re-centered [448,704) span so it genuinely exercises
	// the short path rather than landing outside every analysis window);
	// both are bit-portable (no libm in the input generator).
	out := make([]float64, 0, 3*2*(39+39+1))
	for sri := range 3 {
		for prog := range 2 {
			var p PsyModel
			p.Reset(sri)
			pcm := make([]float64, 1024)
			var po PsyOut
			seed := uint64(250 + prog)
			for g := range 4 {
				switch prog {
				case 0:
					for i := range pcm {
						pcm[i] = testsignal.LCGSigned(&seed) * 0.6
					}
				case 1:
					clear(pcm)
					if g == 3 {
						for i := 600; i < 678; i++ {
							pcm[i] = 0.8
						}
					}
				}
				p.AnalyzeGranule(pcm, &po)
			}
			out = append(out, po.XminS[:]...)
			out = append(out, po.EnS[:]...)
			out = append(out, po.PES)
		}
	}
	got := sha256Float64s(out...)
	if psyShortGoldenSHA == "" {
		t.Fatalf("FREEZE ME: const psyShortGoldenSHA = %q", got)
	}
	if got != psyShortGoldenSHA {
		t.Fatalf("PsyModel short output changed: %s, frozen %s", got, psyShortGoldenSHA)
	}
}

func TestPsyModelAllocs(t *testing.T) {
	var p PsyModel
	p.Reset(0)
	pcm := make([]float64, 1024)
	var out PsyOut
	seed := uint64(131)
	for i := range pcm {
		pcm[i] = testsignal.LCGSigned(&seed)
	}
	p.AnalyzeGranule(pcm, &out) // warm
	if n := testing.AllocsPerRun(50, func() { p.AnalyzeGranule(pcm, &out) }); n != 0 {
		t.Fatalf("AnalyzeGranule allocates: %v allocs per run, want 0", n)
	}
}

func BenchmarkPsyAnalyzeGranule(b *testing.B) {
	var p PsyModel
	p.Reset(0)
	pcm := make([]float64, 1024)
	seed := uint64(1)
	for i := range pcm {
		pcm[i] = testsignal.LCGSigned(&seed)
	}
	var out PsyOut
	for b.Loop() {
		p.AnalyzeGranule(pcm, &out)
	}
}

// psyXrCalibrationShortWarmup/Granules mirror internal/dec's
// TestPsyXrCalibration constants (psyXrCalibrationWarmup/Granules): enough
// leading granules for the filterbank+MDCT overlap history and the
// re-centered psymodel window (design decision 11: pcmWindowCenterOffset =
// 224 samples of lookahead) to fill with entirely real content before
// accumulation starts.
const (
	psyXrCalibrationShortWarmup   = 8
	psyXrCalibrationShortGranules = 20
)

// TestPsyXrCalibrationShort measures and freezes XminScaleShort (design
// decision 14's contingency): the SAME ratio methodology internal/dec's
// TestPsyXrCalibration uses for XminScale (sum(xr[i]^2 over a coding
// band)/PsyOut.EnS[band], density-floored, median-of-medians across a
// 3-sample-rate x 2-program grid of STATIONARY content), generalized to
// short coding bands. Lives in this package (not internal/dec, where
// XminScale's own calibration lives) purely for direct access to the
// unexported blockShort/MDCTGranuleBlock/reorderShort/pcmWindowCenterOffset
// symbols the short-block DSP chain needs; the independence property that
// matters (bypassing Encoder/quantizeGranule/outerLoop entirely, so the
// freeze is not tautological) is preserved identically.
//
// Decision 14 predicted XminScale itself would apply unchanged to XminS.
// That prediction did not hold: this measurement found a systematic,
// tightly-clustered factor of about 15.76x XminScale (the six per-case
// medians span only 1.31e-05 to 1.53e-05, TIGHTER than XminScale's own
// long-block calibration grid), surfaced by a genuine
// TestEncoderMaskingContract regression (a hard silence-to-full-amplitude
// onset became fixable only after freezing this separate constant).
// psyXrCalibrationShortMedian measures ONE (sample rate, program) case's
// median sum(xr[i]^2 over a short coding band)/PsyOut.EnS[band] ratio,
// density-floored: the per-case body TestPsyXrCalibrationShort's grid
// loop calls, factored out to keep that loop's own complexity low.
func psyXrCalibrationShortMedian(t *testing.T, sr, srIndex int, gen func(sr, n int) []float64) (med float64, n int) {
	t.Helper()
	swidths := &sfbWidthsShort[srIndex]

	// pcmWindowCenterOffset leading samples so granule 0's re-centered
	// window (decision 11) never reads before the buffer; granule g's own
	// samples then start at offset pcmWindowCenterOffset + g*576.
	total := pcmWindowCenterOffset + (psyXrCalibrationShortWarmup+psyXrCalibrationShortGranules+1)*576
	pcm := gen(sr, total)

	var fb Filterbank
	var prev, cur [18][32]float64
	var xrRaw, xr [576]float64
	var psy PsyModel
	psy.Reset(srIndex)
	var psyOut PsyOut

	var sumXr, sumEn [39]float64
	for g := range psyXrCalibrationShortWarmup + psyXrCalibrationShortGranules {
		var in [576]float64
		for i := range 576 {
			in[i] = pcm[pcmWindowCenterOffset+g*576+i] * PCMScale
		}
		fb.AnalyzeGranule(in[:], &cur)
		FlipOddSubbands(&cur)
		MDCTGranuleBlock(&prev, &cur, &xrRaw, blockShort)
		prev = cur
		reorderShort(&xrRaw, &xr, swidths)

		start := g * 576 // pcmWindowCenterOffset earlier than granule g's own first sample (at pcmWindowCenterOffset+g*576)
		var win [1024]float64
		copy(win[:], pcm[start:start+1024])
		psy.AnalyzeGranule(win[:], &psyOut)

		if g < psyXrCalibrationShortWarmup {
			continue
		}
		i := 0
		for s := range 13 {
			w := swidths[s]
			for win2 := range 3 {
				b := 3*s + win2
				for range w {
					sumXr[b] += xr[i] * xr[i]
					i++
				}
				sumEn[b] += psyOut.EnS[b]
			}
		}
	}

	maxDensity := 0.0
	var density [39]float64
	for b := range 39 {
		w := swidths[b/3]
		if w == 0 {
			continue
		}
		density[b] = sumEn[b] / float64(w)
		if density[b] > maxDensity {
			maxDensity = density[b]
		}
	}

	var ratios []float64
	for b := range 36 { // nScf for short: the highest triple carries no scalefactor
		if sumEn[b] <= 0 || maxDensity <= 0 || density[b]/maxDensity < psyXrCalibrationDensityFloorShort {
			continue
		}
		ratios = append(ratios, sumXr[b]/sumEn[b])
	}
	slices.Sort(ratios)
	if len(ratios) == 0 {
		t.Fatalf("sr=%d: no band survived the density floor; nothing to calibrate against", sr)
	}
	return ratios[len(ratios)/2], len(ratios)
}

// TestPsyXrCalibrationShort measures and freezes XminScaleShort (design
// decision 14's contingency): the SAME ratio methodology internal/dec's
// TestPsyXrCalibration uses for XminScale (sum(xr[i]^2 over a coding
// band)/PsyOut.EnS[band], density-floored, median-of-medians across a
// 3-sample-rate x 2-program grid of STATIONARY content), generalized to
// short coding bands. Lives in this package (not internal/dec, where
// XminScale's own calibration lives) purely for direct access to the
// unexported blockShort/MDCTGranuleBlock/reorderShort/pcmWindowCenterOffset
// symbols the short-block DSP chain needs; the independence property that
// matters (bypassing Encoder/quantizeGranule/outerLoop entirely, so the
// freeze is not tautological) is preserved identically.
//
// Decision 14 predicted XminScale itself would apply unchanged to XminS.
// That prediction did not hold: this measurement found a systematic,
// tightly-clustered factor of about 15.76x XminScale (the six per-case
// medians span only 1.31e-05 to 1.53e-05, TIGHTER than XminScale's own
// long-block calibration grid), surfaced by a genuine
// TestEncoderMaskingContract regression (a hard silence-to-full-amplitude
// onset became fixable only after freezing this separate constant).
func TestPsyXrCalibrationShort(t *testing.T) {
	rates := []int{44100, 48000, 32000}
	programs := []struct {
		name string
		gen  func(sr, n int) []float64
	}{
		{"lcgNoise", func(_, n int) []float64 {
			seed := uint64(0xC0FFEE)
			x := make([]float64, n)
			for i := range x {
				x[i] = testsignal.LCGSigned(&seed) * 0.3
			}
			return x
		}},
		{"multiTone", func(sr, n int) []float64 {
			return testsignal.MultiTone(sr, n, 0, 0.5)
		}},
	}

	caseMedians := make([]float64, 0, len(rates)*len(programs))
	for srIndex, sr := range rates {
		for _, prog := range programs {
			med, n := psyXrCalibrationShortMedian(t, sr, srIndex, prog.gen)
			t.Logf("sr=%d program=%s median ratio = %v (n=%d surviving bands)", sr, prog.name, med, n)
			caseMedians = append(caseMedians, med)
		}
	}

	slices.Sort(caseMedians)
	overall := caseMedians[len(caseMedians)/2]
	t.Logf("median-of-medians (candidate XminScaleShort) = %#v", overall)

	if XminScaleShort == 0 {
		t.Fatalf("FREEZE ME: const XminScaleShort float64 = %#v", overall)
	}
	for _, r := range caseMedians {
		if rel := r / XminScaleShort; rel < 0.25 || rel > 4 {
			t.Errorf("case median %v is more than 4x from frozen XminScaleShort %v (ratio %v)", r, XminScaleShort, rel)
		}
	}
}

// psyXrCalibrationDensityFloorShort mirrors internal/dec's
// psyXrCalibrationDensityFloor (0.02): the same density-relative exclusion
// rule, needed here because that constant lives in package dec.
const psyXrCalibrationDensityFloorShort = 0.02
