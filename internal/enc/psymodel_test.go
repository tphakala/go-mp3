package enc

import (
	"math"
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
