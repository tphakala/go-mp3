package enc

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// psyKernelRand returns a value in [0, 1) from seed, shared by the
// psychoacoustic kernel parity tests.
func psyKernelRand(seed *uint64) float64 {
	return testsignal.LCG(seed)
}

// psyKernelPositiveFinite returns a positive finite float64 whose bit pattern
// r draws uniformly from the smallest subnormal to math.MaxFloat64, so every
// mantissa bit varies.
func psyKernelPositiveFinite(r *rand.Rand) float64 {
	return math.Float64frombits(1 + r.Uint64N(0x7fefffffffffffff))
}

// psyKernelScaled returns a signed random value scaled by a random power of
// two in [2^lo, 2^hi), so the kernels see every binade the spectra reach.
func psyKernelScaled(seed *uint64, lo, hi int) float64 {
	v := testsignal.LCGSigned(seed)
	e := lo + int(psyKernelRand(seed)*float64(hi-lo))
	return math.Ldexp(v, e)
}

// fillUnpredInput resets p to rate r and fills its spectrum and two-frame
// history with values in [2^lo, 2^hi), with bins that take each no-history
// branch (r1 == 0, r2m == 0, r == 0 with rp == 0) and bins whose prediction
// error drives c past 1.
func fillUnpredInput(seed *uint64, p *PsyModel, r, lo, hi int) {
	p.Reset(r)
	for i := range 1024 {
		p.fftRe[i] = psyKernelScaled(seed, lo, hi)
		p.fftIm[i] = psyKernelScaled(seed, lo, hi)
	}
	for h := range 2 {
		for i := range 513 {
			p.prevRe[h][i] = psyKernelScaled(seed, lo, hi)
			p.prevIm[h][i] = psyKernelScaled(seed, lo, hi)
		}
	}
	for i := range 513 {
		switch k := int(psyKernelRand(seed) * 8); k {
		case 0:
			p.prevRe[0][i], p.prevIm[0][i] = 0, 0
		case 1:
			p.prevRe[1][i], p.prevIm[1][i] = 0, 0
		case 2:
			// r == 0 and rp == 2*r1 - r2m == 0.
			p.fftRe[i], p.fftIm[i] = 0, 0
			p.prevRe[0][i], p.prevIm[0][i] = 1, 0
			p.prevRe[1][i], p.prevIm[1][i] = 2, 0
		case 3:
			// A prediction opposite to the current bin: c exceeds 1 before
			// the clamp whenever rounding lands above the bound.
			p.prevRe[0][i], p.prevIm[0][i] = p.fftRe[i], p.fftIm[i]
			p.prevRe[1][i], p.prevIm[1][i] = -p.fftRe[i], -p.fftIm[i]
		}
	}
}

// checkUnpredictability requires the unpredictability kernel to match
// unpredictabilityGo bit for bit on p.
func checkUnpredictability(t *testing.T, p *PsyModel) {
	t.Helper()
	got, want := *p, *p
	got.unpredictability()
	want.unpredictabilityGo()
	for i := range 513 {
		if math.Float64bits(got.r2[i]) != math.Float64bits(want.r2[i]) || math.Float64bits(got.cw[i]) != math.Float64bits(want.cw[i]) {
			t.Fatalf("bin %d: r2 %v cw %v, want r2 %v cw %v", i, got.r2[i], got.cw[i], want.r2[i], want.cw[i])
		}
	}
}

// TestUnpredictabilityKernelParity pins the unpredictability kernel to
// unpredictabilityGo bit for bit on random spectra and histories, with bins
// that take each no-history branch and bins whose prediction error drives c
// past 1.
func TestUnpredictabilityKernelParity(t *testing.T) {
	seed := uint64(9001)
	var p PsyModel
	for trial := range 300 {
		lo, hi := -40, 20
		if trial%5 == 0 {
			lo, hi = -1060, -1000
		}
		fillUnpredInput(&seed, &p, trial%3, lo, hi)
		checkUnpredictability(t, &p)
	}
}

// FuzzUnpredictabilityKernelParity drives the rate and the binade window of
// the spectra beyond the fixed sweep, with the values drawn from a seeded LCG.
// The window stays below 2^60, so no squared magnitude overflows, as no
// spectrum of PCM input does.
func FuzzUnpredictabilityKernelParity(f *testing.F) {
	f.Add(uint8(0), int16(-40), uint8(60), uint64(1))
	f.Add(uint8(1), int16(-1060), uint8(60), uint64(2))
	f.Add(uint8(2), int16(30), uint8(30), uint64(3))
	f.Fuzz(func(t *testing.T, rateR uint8, lo int16, span uint8, seed uint64) {
		l := min(max(int(lo), -1074), 40)
		var p PsyModel
		fillUnpredInput(&seed, &p, int(rateR)%3, l, min(l+1+int(span), 60))
		checkUnpredictability(t, &p)
	})
}

// fillMaskingInput resets p to rate r and fills the partition energies,
// tonality-weighted energies and, when history, the threshold history, with
// energies that reach zero and cbs/ecb ratios down to the subnormal range;
// silent zeroes every partition.
func fillMaskingInput(seed *uint64, p *PsyModel, r int, history, silent bool) {
	p.Reset(r)
	n := p.tab.nParts
	for b := range n {
		p.e[b] = math.Abs(psyKernelScaled(seed, -30, 40))
		p.ct[b] = p.e[b] * psyKernelRand(seed)
		switch k := int(psyKernelRand(seed) * 10); k {
		case 0:
			p.e[b], p.ct[b] = 0, 0
		case 1:
			p.ct[b] = 0
		case 2:
			p.ct[b] = p.e[b] * 0x1p-1060
		case 3:
			p.ct[b] = p.e[b] * psyKernelRand(seed) * 0.5
		}
		if history {
			p.nbPrev[0][b] = math.Abs(psyKernelScaled(seed, -40, 40))
			p.nbPrev[1][b] = math.Abs(psyKernelScaled(seed, -40, 40))
		}
	}
	if silent {
		for b := range n {
			p.e[b], p.ct[b] = 0, 0
		}
	}
}

// checkMaskingThresholds requires the masking-threshold kernel to match
// maskingThresholdsGo bit for bit on p.
func checkMaskingThresholds(t *testing.T, p *PsyModel) {
	t.Helper()
	got, want := *p, *p
	got.maskingThresholds()
	want.maskingThresholdsGo()
	for b := range got.tab.nParts {
		if math.Float64bits(got.ecb[b]) != math.Float64bits(want.ecb[b]) ||
			math.Float64bits(got.cbs[b]) != math.Float64bits(want.cbs[b]) ||
			math.Float64bits(got.nb[b]) != math.Float64bits(want.nb[b]) {
			t.Fatalf("part %d: ecb %v cbs %v nb %v, want %v %v %v", b, got.ecb[b], got.cbs[b], got.nb[b], want.ecb[b], want.cbs[b], want.nb[b])
		}
	}
}

// TestMaskingThresholdsKernelParity pins the masking-threshold kernel to
// maskingThresholdsGo bit for bit at every rate, with partition energies that
// reach zero, a cbs/ecb ratio down to the subnormal range, and threshold
// histories where the pre-echo caps and the qthr floor bind.
func TestMaskingThresholdsKernelParity(t *testing.T) {
	seed := uint64(9002)
	var p PsyModel
	for trial := range 600 {
		fillMaskingInput(&seed, &p, trial%3, trial%4 != 0, trial%7 == 0)
		checkMaskingThresholds(t, &p)
	}
}

// FuzzMaskingThresholdsKernelParity drives the rate, the threshold history and
// silence beyond the fixed sweep, with the energies drawn from a seeded LCG.
func FuzzMaskingThresholdsKernelParity(f *testing.F) {
	f.Add(uint8(0), true, false, uint64(1))
	f.Add(uint8(1), false, false, uint64(2))
	f.Add(uint8(2), true, true, uint64(3))
	f.Fuzz(func(t *testing.T, rateR uint8, history, silent bool, seed uint64) {
		var p PsyModel
		fillMaskingInput(&seed, &p, int(rateR)%3, history, silent)
		checkMaskingThresholds(t, &p)
	})
}

// fillShortInput resets p to rate r and fills the short-window partition
// energies, zeroing each with probability zeroFrac.
func fillShortInput(seed *uint64, p *PsyModel, r int, zeroFrac float64) {
	p.Reset(r)
	for b := range psyShortTables[r].nParts {
		p.eS[b] = math.Abs(psyKernelScaled(seed, -40, 30))
		if psyKernelRand(seed) < zeroFrac {
			p.eS[b] = 0
		}
	}
}

// checkShortThresholds requires the short-window threshold kernel to match
// shortThresholdsGo bit for bit on p at rate r.
func checkShortThresholds(t *testing.T, p *PsyModel, r int) {
	t.Helper()
	tab := &psyShortTables[r]
	got, want := *p, *p
	got.shortThresholds(tab)
	want.shortThresholdsGo(tab)
	for b := range tab.nParts {
		if math.Float64bits(got.nbS[b]) != math.Float64bits(want.nbS[b]) {
			t.Fatalf("part %d: nbS %v, want %v", b, got.nbS[b], want.nbS[b])
		}
	}
}

// TestShortThresholdsKernelParity pins the short-window threshold kernel to
// shortThresholdsGo bit for bit at every rate, including silent partitions
// that take the qthr floor.
func TestShortThresholdsKernelParity(t *testing.T) {
	seed := uint64(9003)
	var p PsyModel
	for trial := range 600 {
		r := trial % 3
		fillShortInput(&seed, &p, r, 0.2)
		checkShortThresholds(t, &p, r)
	}
}

// FuzzShortThresholdsKernelParity drives the rate and the share of silent
// partitions beyond the fixed sweep, with the energies drawn from a seeded
// LCG.
func FuzzShortThresholdsKernelParity(f *testing.F) {
	f.Add(uint8(0), uint8(51), uint64(1))
	f.Add(uint8(1), uint8(0), uint64(2))
	f.Add(uint8(2), uint8(255), uint64(3))
	f.Fuzz(func(t *testing.T, rateR, zeroR uint8, seed uint64) {
		r := int(rateR) % 3
		var p PsyModel
		fillShortInput(&seed, &p, r, float64(zeroR)/255)
		checkShortThresholds(t, &p, r)
	})
}

// plogSpecial are the edge inputs of plog: subnormals, the binade edges of
// the normal range, both sides of the sqrtHalf recentring boundary, and the
// neighbours of 1 and 2.
var plogSpecial = []float64{
	math.SmallestNonzeroFloat64, 0x1p-1060, 0x1.fffffffffffffp-1023, 0x1p-1022,
	sqrtHalf, math.Nextafter(sqrtHalf, 0), math.Nextafter(sqrtHalf, 1),
	0.5, 1, math.Nextafter(1, 0), math.Nextafter(1, 2), 2, math.MaxFloat64,
}

// checkPlogs requires plogs to match plogsGo bit for bit on src.
func checkPlogs(t *testing.T, src []float64) {
	t.Helper()
	got := make([]float64, len(src))
	want := make([]float64, len(src))
	plogs(got, src)
	plogsGo(want, src)
	for i := range src {
		if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
			t.Fatalf("plog(%v) = %v, want %v", src[i], got[i], want[i])
		}
	}
}

// TestPlogsKernelParity pins plogs to plog bit for bit over every binade of
// positive finite float64 (subnormals, powers of two, both sides of the
// sqrtHalf recentring boundary) and over lengths that exercise the
// overlapped last group.
func TestPlogsKernelParity(t *testing.T) {
	seed := uint64(9004)
	r := rand.New(rand.NewPCG(9004, 0))
	src := make([]float64, 0, 80)
	for trial := range 4000 {
		n := 4 + trial%67
		src = src[:0]
		for i := range n {
			var x float64
			switch {
			case trial < 20 && i < len(plogSpecial):
				x = plogSpecial[i]
			case trial%3 == 0:
				x = psyKernelPositiveFinite(r)
			default:
				x = math.Abs(psyKernelScaled(&seed, -1074, 1024))
			}
			if x == 0 {
				x = math.SmallestNonzeroFloat64
			}
			src = append(src, x)
		}
		checkPlogs(t, src)
	}
}

// FuzzPlogsKernelParity drives the batch length and the bit patterns of every
// positive finite input, the domain plogs is defined on, from a seeded PCG.
func FuzzPlogsKernelParity(f *testing.F) {
	f.Add(uint8(4), uint64(1))
	f.Add(uint8(13), uint64(2))
	f.Add(uint8(70), uint64(3))
	f.Fuzz(func(t *testing.T, nR uint8, seed uint64) {
		r := rand.New(rand.NewPCG(seed, 0))
		src := make([]float64, 1+int(nR%70))
		for i := range src {
			src[i] = psyKernelPositiveFinite(r)
			if i < len(plogSpecial) && nR&0x80 != 0 {
				src[i] = plogSpecial[i]
			}
		}
		checkPlogs(t, src)
	})
}

// TestPsyKernelsAllocFree pins the kernel dispatchers to zero allocations.
func TestPsyKernelsAllocFree(t *testing.T) {
	var p PsyModel
	p.Reset(0)
	for b := range p.tab.nParts {
		p.e[b], p.ct[b] = float64(b+1), float64(b+1)/3
	}
	src := []float64{0.5, 2, 3, 4, 5}
	dst := make([]float64, len(src))
	if n := testing.AllocsPerRun(100, func() {
		p.unpredictability()
		p.maskingThresholds()
		p.shortThresholds(&psyShortTables[0])
		plogs(dst, src)
	}); n != 0 {
		t.Fatalf("psychoacoustic kernels allocated %v times per run, want 0", n)
	}
}
