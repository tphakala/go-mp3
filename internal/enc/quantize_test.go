package enc

import (
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// TestQuantizeKnownAnswers checks quantizeGranule against hand-computed
// expecteds: ix[i] = sign(xr) * floor((|xr| * invStep(gg))^0.75 - 0.0946 +
// 0.5), each row computed independently with math.Pow (not sqrt(x*sqrt(x)),
// the runtime implementation's substitution; that agreement is
// TestQuantizePowRef's job) and math.Floor (not the v+0.4054 truncation
// trick; that equivalence is argued in quantizeGranule's doc comment).
func TestQuantizeKnownAnswers(t *testing.T) {
	cases := []struct {
		name string
		xr   float64
		gg   int
		want int32
	}{
		{"positive, invStep=1 (gg=214)", 1000, 214, 178},
		{"negative, sign preserved", -1000, 214, -178},
		{"exact zero", 0, 214, 0},
		{"quantizes to 0", 0.3, 214, 0},
		{"invStep=2 (gg=210, positive q remainder 0)", 1000, 210, 299},
		{"invStep=0.5 (gg=218, negative q remainder 0)", 1000, 218, 106},
		{"invStep=2^0.25 (gg=213, positive q remainder 1)", 500, 213, 120},
		{"invStep=2^-0.25 (gg=215, negative q remainder 3)", 500, 215, 93},
		{"clamp engages (gg=0, huge invStep)", 1, 0, maxQuant},
		{"clamp engages, sign preserved", -1, 0, -maxQuant},
	}

	var sf scfState
	lay := &layoutLong[0]
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var xr [576]float64
			xr[0] = c.xr
			var ix [576]int32
			quantizeGranule(&xr, c.gg, &sf, lay, &ix)
			if ix[0] != c.want {
				t.Fatalf("quantizeGranule(xr=%v, gg=%d) = %d, want %d", c.xr, c.gg, ix[0], c.want)
			}
		})
	}
}

// TestQuantizePowRef samples magnitudes log-uniformly over 1e-3..1e6 with a
// fixed LCG (same PCG-style generator as TestMdctGolden, mdct_test.go) and
// checks sqrt(x*sqrt(x)) against math.Pow(x, 0.75), documenting the runtime
// substitution quantizeGranule relies on.
//
// The brief's stated tolerance is 2 ULP, but that does not hold as a
// per-sample hard bound over a large sweep: sqrt(x*sqrt(x)) and math.Pow(x,
// 0.75) are two independently-rounded computations of the same irrational
// value (two chained sqrt roundings versus Pow's own exp/log-based
// approximation, which is not documented as correctly-rounded), and their
// disagreement is ordinary compounding floating-point noise, not evidence
// either is wrong. In a one-off local development sweep (3,000,000
// samples with this same generator, not what CI runs), about 5% of
// samples landed at exactly 2 ULP and a small tail reached 3-4 ULP, with
// no samples seen above 4. maxULPSanity below is that observed max plus
// ample headroom, kept as a loose regression net (it would catch a real
// defect, e.g. an accidentally transposed sqrt argument, which would
// diverge far more than a few ULP); it is not the tolerance the brief
// cites.
//
// What the brief's parenthetical actually promises, and what quantizeGranule
// actually depends on, is checked as the hard requirement instead: after
// adding 0.4054 and truncating to int64 (the same nint step quantizeGranule
// applies), the two formulas' outputs agree on every sample. CI runs the
// 5,000-sample sweep below on every commit; a separate one-off local sweep
// (2,000,000 samples with this same generator, not part of CI) also saw
// zero mismatches.
func TestQuantizePowRef(t *testing.T) {
	const samples = 5000
	const maxULPSanity = 10

	var seed uint64 = 12345
	next := func() float64 {
		return testsignal.LCG(&seed)
	}

	for range samples {
		u := next()
		x := math.Pow(10, -3+float64(9*u)) // log-uniform over [1e-3, 1e6)

		got := math.Sqrt(x * math.Sqrt(x))
		want := math.Pow(x, 0.75)

		if diff := ulpDistance(got, want); diff > maxULPSanity {
			t.Fatalf("sqrt(x*sqrt(x)) vs math.Pow(x, 0.75) at x=%v: got %v, want %v, diff %d ULP (sanity bound %d)",
				x, got, want, diff, maxULPSanity)
		}

		gotNint := int64(got + 0.4054)
		wantNint := int64(want + 0.4054)
		if gotNint != wantNint {
			t.Fatalf("nint(sqrt(x*sqrt(x))) != nint(math.Pow(x, 0.75)) at x=%v: %d != %d",
				x, gotNint, wantNint)
		}
	}
}

// TestQuantizeMonotone sweeps gg from 0 to 255 over a 576-line spectrum with
// magnitudes spanning 1e-3..1e6 (including the region near gg=0 where invStep
// is astronomically large and quantizeGranule's clamp engages) and requires
// |ix[i]| to never increase as gg rises, for every line, on every layout and
// under zero, maximum and random scalefactor states. The exact scan's
// bisection on monoPairFloor relies on it.
func TestQuantizeMonotone(t *testing.T) {
	var seed uint64 = 99
	next := func() float64 {
		return testsignal.LCG(&seed)
	}
	for li, lay := range prefixCostLayouts {
		sfs := []scfState{{}, quantizeMaxScf(lay), randomScfState(&seed, lay), randomScfState(&seed, lay)}
		for si, sf := range sfs {
			// Random magnitudes over nine decades, with the special values
			// (signed zero, subnormals, NaN, infinities, overflowing lines)
			// pinned at the first lines: the exact scan's bisection needs
			// every one of them to be non-increasing in gg too.
			var xr [576]float64
			for i := range xr {
				u := next()
				mag := math.Pow(10, -3+float64(9*u))
				if u2 := next(); u2 < 0.5 {
					mag = -mag
				}
				xr[i] = mag
			}
			specials := [...]float64{
				math.Copysign(0, -1), 0, math.NaN(), math.Copysign(math.NaN(), -1),
				math.Inf(1), math.Inf(-1), 5e-324, -5e-324, 1e-310, -1e-310,
				1e300, -1e300, math.MaxFloat64, -math.MaxFloat64,
			}
			copy(xr[:], specials[:])

			var prev, cur [576]int32
			quantizeGranule(&xr, 0, &sf, lay, &prev)
			for gg := 1; gg < 256; gg++ {
				quantizeGranule(&xr, gg, &sf, lay, &cur)
				for i := range 576 {
					if abs32(cur[i]) > abs32(prev[i]) {
						t.Fatalf("%s sf%d line %d (xr=%v): |ix| rose from gg=%d to gg=%d: %d -> %d",
							prefixCostLayoutName(li), si, i, xr[i], gg-1, gg, prev[i], cur[i])
					}
				}
				prev = cur
			}
		}
	}
}

// rawQuant is an independent, unclamped reference for the quantized
// magnitude at a given gg, used to test minGlobalGain's boundary without
// relying on quantizeGranule's own hard clamp (which by design can never
// report a value "exceeding" maxQuant). It reuses invStep's own
// quantGainBase constant (quantize.go) rather than a duplicated literal,
// so this oracle cannot silently drift out of sync with the production
// value if quantGainBase is ever re-measured; see quantGainBase's doc
// comment for why 214, not the textbook ISO 210, is the value this
// encoder's decoder actually dequantizes against.
func rawQuant(xrAbs float64, gg int) float64 {
	t := xrAbs * math.Pow(2, float64(quantGainBase-gg)/4.0)
	v := math.Pow(t, 0.75)
	return math.Floor(v - 0.0946 + 0.5)
}

// TestMinGlobalGain uses a spectrum with a single peak of 5e5 (the rest
// zero). It checks that the raw (unclamped) quantized magnitude at the
// returned gg is within maxQuant, that gg-1's raw magnitude would exceed
// it, and that quantizeGranule agrees: no clamp at gg, clamp engaged
// (ix == maxQuant exactly) at gg-1.
func TestMinGlobalGain(t *testing.T) {
	var xr [576]float64
	xr[300] = 5e5
	var sf scfState
	lay := &layoutLong[0]

	gg := minGlobalGain(&xr, &sf, lay)
	if gg <= 0 || gg >= 256 {
		t.Fatalf("minGlobalGain returned out-of-range gg=%d", gg)
	}

	if raw := rawQuant(5e5, gg); raw > maxQuant {
		t.Fatalf("gg=%d: raw quantized magnitude %v > maxQuant %d", gg, raw, maxQuant)
	}
	if raw := rawQuant(5e5, gg-1); raw <= maxQuant {
		t.Fatalf("gg-1=%d: expected raw quantized magnitude > maxQuant, got %v", gg-1, raw)
	}

	var ix [576]int32
	quantizeGranule(&xr, gg, &sf, lay, &ix)
	if ix[300] > maxQuant {
		t.Fatalf("quantizeGranule at gg=%d: ix[300]=%d > maxQuant %d", gg, ix[300], maxQuant)
	}

	quantizeGranule(&xr, gg-1, &sf, lay, &ix)
	if ix[300] != maxQuant {
		t.Fatalf("quantizeGranule at gg-1=%d: ix[300]=%d, want the clamp to engage at %d",
			gg-1, ix[300], maxQuant)
	}
}

// TestPartitionSpectrum hand-builds ix arrays covering the scan's structural
// cases.
func TestPartitionSpectrum(t *testing.T) {
	t.Run("all zero", func(t *testing.T) {
		var ix [576]int32
		got := partitionSpectrum(&ix)
		want := spectrumPartition{bigValues: 0, count1: 0}
		if got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("trailing zeros only, no count1 quads", func(t *testing.T) {
		var ix [576]int32
		for i := range 100 {
			ix[i] = 5 // big-values region: 100 lines, 50 pairs
		}
		// lines [100, 576) stay zero: 476 lines, all absorbed into rzero.
		got := partitionSpectrum(&ix)
		want := spectrumPartition{bigValues: 50, count1: 0}
		if got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("count1 tail of mixed 0/1/-1 quads", func(t *testing.T) {
		var ix [576]int32
		for i := range 496 {
			ix[i] = 5 // big-values region: lines [0, 496), 248 pairs
		}
		pattern := [4]int32{0, 1, -1, 0}
		for i := 496; i < 536; i++ {
			ix[i] = pattern[(i-496)%4] // count1 region: lines [496, 536), 10 quads
		}
		// lines [536, 576) stay zero: rzero region, 40 lines.
		got := partitionSpectrum(&ix)
		want := spectrumPartition{bigValues: 248, count1: 10}
		if got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("values > 1 up to the last line", func(t *testing.T) {
		var ix [576]int32
		for i := range ix {
			ix[i] = 5
		}
		got := partitionSpectrum(&ix)
		want := spectrumPartition{bigValues: 288, count1: 0}
		if got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("quad straddling the count1/rzero boundary", func(t *testing.T) {
		var ix [576]int32
		for i := range 570 {
			ix[i] = 5 // big-values region: lines [0, 570), 285 pairs
		}
		// count1 region: lines [570, 574), one quad, straddling what would
		// be a naive fixed-grid quad boundary at [572, 576) if the scan
		// didn't first peel off the two exact-zero lines below it.
		ix[570], ix[571], ix[572], ix[573] = 1, -1, 0, 1
		// lines [574, 576) stay zero: rzero region, exactly one pair.
		got := partitionSpectrum(&ix)
		want := spectrumPartition{bigValues: 285, count1: 1}
		if got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})
}

// legacyQuantize is a verbatim copy of the Phase 3 quantizeGranule body (no
// scfState, no per-band step, invStep(gg) applied uniformly to every line):
// the equivalence reference TestQuantizeScaledKnownAnswers checks the
// generalized quantizeGranule against, under the zero scfState.
func legacyQuantize(xr *[576]float64, gg int, ix *[576]int32) {
	is := invStep(gg)
	for i := range 576 {
		t := math.Abs(xr[i]) * is
		v := math.Sqrt(t * math.Sqrt(t))

		var m int32
		if nint := v + 0.4054; nint <= float64(maxQuant) {
			m = int32(nint)
		} else {
			m = maxQuant
		}

		if xr[i] < 0 {
			m = -m
		}
		ix[i] = m
	}
}

func TestQuantizeScaledKnownAnswers(t *testing.T) {
	lay := &layoutLong[0]
	var xr [576]float64
	var ix [576]int32

	// Hand case: gg = quantGainBase (base step 1), scf[0] = 1,
	// scalefacScale = 1: band 0 multiplier is 2^(2*2*1/4) = 2 exactly.
	// xr = 100 -> t = 200 -> 200^0.75 = 53.183 -> +0.4054 trunc = 53.
	var sf scfState
	sf.scf[0] = 1
	sf.scalefacScale = 1
	xr[0] = 100
	quantizeGranule(&xr, quantGainBase, &sf, lay, &ix)
	if ix[0] != 53 {
		t.Errorf("scaled quantize: ix[0] = %d, want 53", ix[0])
	}

	// Zero state must reproduce the Phase 3 quantizer exactly.
	var zero scfState
	seed := uint64(7)
	for i := range xr {
		xr[i] = (float64(testsignal.LCG(&seed)) - 0.5) * 1e5
	}
	var ixA, ixB [576]int32
	quantizeGranule(&xr, 180, &zero, lay, &ixA)
	legacyQuantize(&xr, 180, &ixB) // test-local copy of the Phase 3 body
	if ixA != ixB {
		t.Fatal("zero scfState diverges from the Phase 3 quantizer")
	}

	// preflag: band 15 (pretab 2), preflag=1, scf 0, scalefacScale 0:
	// extra = 2*1*(0+2) = 4 quarter-steps = one power of two.
	var pf scfState
	pf.preflag = 1
	clear(xr[:])
	lo := 0
	for s := range 15 {
		lo += lay.width[s]
	}
	xr[lo] = 100
	quantizeGranule(&xr, quantGainBase, &pf, lay, &ix)
	if ix[lo] != 53 { // same 100 -> 200 -> 53 arithmetic as above
		t.Errorf("preflag quantize: ix[%d] = %d, want 53", lo, ix[lo])
	}
}

func TestNoiseGranuleKnownAnswer(t *testing.T) {
	lay := &layoutLong[0]
	var xr [576]float64
	var ix [576]int32
	var sf scfState
	sf.scf[0] = 1
	sf.scalefacScale = 1
	xr[0] = 100
	quantizeGranule(&xr, quantGainBase, &sf, lay, &ix) // ix[0] = 53
	var noise [39]float64
	noiseGranule(&xr, &ix, quantGainBase, &sf, lay, &noise)
	// dequant = pow43[53] * 2^(-4/4) = 53^(4/3)/2 = 99.535...; noise in
	// band 0 = (100 - dequant)^2; recompute test-side with math.Pow.
	deq := math.Pow(53, 4.0/3.0) / 2
	want := (100 - deq) * (100 - deq)
	if r := math.Abs(noise[0]-want) / want; r > 1e-12 {
		t.Errorf("noise[0] = %v, want %v (rel %.3g)", noise[0], want, r)
	}
	for s := 1; s < 22; s++ {
		if noise[s] != 0 {
			t.Errorf("noise[%d] = %v, want 0 (band is silent)", s, noise[s])
		}
	}
}

func TestNoiseDecreasesWithAmplification(t *testing.T) {
	// Amplifying a band strictly reduces (or keeps equal) its measured
	// noise: finer effective step, more precision.
	lay := &layoutLong[0]
	var xr [576]float64
	seed := uint64(11)
	for i := range 40 { // bands 0..~7 populated
		xr[i] = (float64(testsignal.LCG(&seed)) - 0.5) * 2000
	}
	var prev = math.Inf(1)
	for amp := range 8 {
		var sf scfState
		for s := range sf.scf {
			sf.scf[s] = amp
		}
		var ix [576]int32
		var noise [39]float64
		gg := minGlobalGain(&xr, &sf, lay)
		quantizeGranule(&xr, gg, &sf, lay, &ix)
		noiseGranule(&xr, &ix, gg, &sf, lay, &noise)
		total := 0.0
		for _, n := range noise {
			total += n
		}
		if total > prev*1.0001 {
			t.Fatalf("amp %d: total noise %g not decreasing (prev %g)", amp, total, prev)
		}
		prev = total
	}
}

func TestMinGlobalGainScaled(t *testing.T) {
	// Amplification raises the needed gg; the returned gg always keeps
	// every quantized magnitude within maxQuant WITHOUT the clamp firing.
	lay := &layoutLong[0]
	var xr [576]float64
	xr[0] = 5e5
	var sf scfState
	sf.scf[0] = 10
	gg := minGlobalGain(&xr, &sf, lay)
	var ix [576]int32
	quantizeGranule(&xr, gg, &sf, lay, &ix)
	if ix[0] > maxQuant {
		t.Fatalf("ix[0] = %d exceeds maxQuant at returned gg", ix[0])
	}
	if gg > 0 {
		sf2 := sf
		var ix2 [576]int32
		quantizeGranule(&xr, gg-1, &sf2, lay, &ix2)
		// quantizeGranule's clamp guarantees ix2[0] <= maxQuant always,
		// whether or not gg-1 truly fits, so "does gg-1 fit" has to be
		// read off the clamp engaging (ix2[0] == maxQuant exactly, the
		// else branch's fixed value), the same idiom TestMinGlobalGain
		// above uses for the unscaled case.
		if ix2[0] != maxQuant {
			t.Fatalf("gg-1 also fits: minGlobalGain not minimal (ix2[0]=%d, want the clamp to engage at %d)", ix2[0], maxQuant)
		}
	}
}

// TestBandExtraQuartersShort hand-checks bandExtraQuarters over a short
// layout's scalefactor-bearing bands: 2*(scalefacScale+1)*scf[b] plus the
// band's window's subblock_gain contribution (8 quarter-steps per unit),
// and confirms preflag (a long-only field) is ignored even when set.
func TestBandExtraQuartersShort(t *testing.T) {
	lay := &layoutShort[0]

	cases := []struct {
		name          string
		b             int
		scf           int
		scalefacScale int
		ssg           int
		preflag       int
		want          int
	}{
		{"scale 0, ssg 0", 5, 3, 0, 0, 0, 2 * 3},
		{"scale 1, ssg 0", 5, 3, 1, 0, 0, 4 * 3},
		{"scale 0, ssg nonzero", 5, 3, 0, 2, 0, 2*3 + 8*2},
		{"scale 1, ssg nonzero", 5, 3, 1, 2, 0, 4*3 + 8*2},
		{"preflag set: ignored for short", 5, 3, 0, 2, 1, 2*3 + 8*2},
		{"zero scf, nonzero ssg only", 5, 0, 1, 4, 0, 8 * 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var sf scfState
			sf.scf[c.b] = c.scf
			sf.scalefacScale = c.scalefacScale
			sf.subblockGain[lay.win[c.b]] = c.ssg
			sf.preflag = c.preflag
			if got := sf.bandExtraQuarters(c.b, lay); got != c.want {
				t.Fatalf("bandExtraQuarters(%d) = %d, want %d", c.b, got, c.want)
			}
		})
	}
}

// TestBandExtraQuartersScflessTriple is a dedicated regression for the
// scalefactor-less highest short triple (bands lay.nScf..lay.nBands-1,
// bands 36-38 at 44.1kHz): a classic high-frequency quantization-bug
// source, since these bands must get ONLY the window's subblock_gain
// contribution, never a scf term (they have no scf slot at all: scfState's
// scf array is sized exactly lay.nScf). Every real scf-bearing band is set
// to a large nonzero value that WOULD leak into the result if the
// scfless-triple branch ever mistakenly read a scf slot (e.g. a wraparound
// or off-by-one band index); the assertion proves it does not.
func TestBandExtraQuartersScflessTriple(t *testing.T) {
	lay := &layoutShort[0]
	var sf scfState
	for b := range lay.nScf {
		sf.scf[b] = 99
	}
	sf.subblockGain = [3]int{1, 4, 7}

	for b := lay.nScf; b < lay.nBands; b++ {
		win := lay.win[b]
		want := 8 * sf.subblockGain[win]
		if got := sf.bandExtraQuarters(b, lay); got != want {
			t.Errorf("band %d (win %d): bandExtraQuarters = %d, want %d (scfless triple must ignore scf entirely)",
				b, win, got, want)
		}
	}
}

// TestQuantizeShortKnownAnswers hand-checks quantizeGranule against a
// synthetic coding-order xr under a short layout with nonzero
// subblock_gain: band 4 (sfb 1, window 1 at 44.1kHz) gets
// scalefacScale=1, scf=0, subblockGain[1]=2, so bandExtraQuarters(4) =
// 2*2*0 + 8*2 = 16 quarter-steps, an exact integer power of two (stepQ(16)
// = 2^4 = 16) so the expected quantized magnitude can be hand-verified:
// (1000*16)^0.75 = 1422.6235..., +0.4054 truncated = 1423.
func TestQuantizeShortKnownAnswers(t *testing.T) {
	lay := &layoutShort[0]
	if lay.win[4] != 1 {
		t.Fatalf("test setup: layoutShort[0].win[4] = %d, want 1", lay.win[4])
	}

	off := 0
	for b := range 4 {
		off += lay.width[b]
	}

	var sf scfState
	sf.scalefacScale = 1
	sf.subblockGain[1] = 2
	if extra := sf.bandExtraQuarters(4, lay); extra != 16 {
		t.Fatalf("test setup: bandExtraQuarters(4) = %d, want 16", extra)
	}

	var xr [576]float64
	xr[off] = 1000

	var ix [576]int32
	quantizeGranule(&xr, quantGainBase, &sf, lay, &ix)

	const want = 1423
	if ix[off] != want {
		t.Fatalf("ix[%d] = %d, want %d", off, ix[off], want)
	}
	for i := range ix {
		if i == off {
			continue
		}
		if ix[i] != 0 {
			t.Errorf("ix[%d] = %d, want 0 (band silent elsewhere)", i, ix[i])
		}
	}
}

// BenchmarkQuantizeGranule isolates the power-law quantizer's per-granule cost.
// The dense case (gg = quantGainBase, every line non-zero) is dominated by the
// two math.Sqrt per line and guards against a regression there; the sparse case
// (a higher gg that pushes most of the spectrum below the zero boundary, the
// low-bitrate escalation regime issue #62 targets) exercises the zero fast path.
// Both are isolated here without the wall-clock noise of a whole frame encode.
// It measures the build's dispatcher: simd by default, the Go loop under noasm.
func BenchmarkQuantizeGranule(b *testing.B) {
	lay := &layoutLong[0]
	var sf scfState
	var xr [576]float64
	seed := uint64(1)
	for i := range xr {
		xr[i] = (float64(testsignal.LCG(&seed))*2 - 1) * 2000
	}
	var ix [576]int32
	// dense: gg=quantGainBase makes is~=1, so every line is non-zero, the worst
	// case for the zero fast path (it must not regress here). sparse: a higher
	// gg shrinks is until roughly half the spectrum falls below the zero
	// boundary, the low-bitrate escalation regime the skip targets (issue #62).
	cases := []struct {
		name string
		gg   int
	}{
		{"dense", quantGainBase},
		{"sparse", quantGainBase + 44},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				quantizeGranule(&xr, c.gg, &sf, lay, &ix)
			}
		})
	}
}

// quantRefNoSkip is quantizeGranuleGo's exact per-line body WITHOUT the zero fast
// path, the reference TestQuantizeZeroSkipExact checks the skip against.
func quantRefNoSkip(x, is float64) int32 {
	tt := math.Abs(x) * is
	v := math.Sqrt(tt * math.Sqrt(tt))
	var m int32
	if nint := v + 0.4054; nint <= float64(maxQuant) {
		m = int32(nint)
	} else {
		m = maxQuant
	}
	if x < 0 {
		m = -m
	}
	return m
}

// TestQuantizeZeroSkipExact pins the zero fast path of whichever quantizer the
// build links, bit-for-bit against the full sqrt chain: simd's proven bound on
// the default build, quantZeroMax under noasm (the CI noasm job runs it on both
// arches). Its quantZeroMax < t0 assertion guards the reference. The
// reference skip is only bit-exact if quantZeroMax sits strictly below the true zero boundary t0 = 0.5946^(4/3), the scaled magnitude
// at which the full chain first rounds to a nonzero value (v + 0.4054 reaches
// 1). The test asserts that invariant directly, so a mutation raising
// quantZeroMax to or past t0 (for example the round value 0.5) reddens it, then
// sweeps scaled magnitudes and checks quantizeGranule matches quantRefNoSkip on
// every line: a broad [0.35, 0.65] pass plus a fine pass densely straddling t0,
// both signs, across global gains 150 to 255 (a range spanning is from about
// 2^16 down to 2^-10). quantizeGranule and the
// reference compute the identical t = math.Abs(x)*is, so any divergence would be
// a line the skip zeroed that the full chain would not (issue #62).
func TestQuantizeZeroSkipExact(t *testing.T) {
	lay := &layoutLong[0]
	var sf scfState

	t0 := math.Pow(0.5946, 4.0/3.0) // ~= 0.4999960
	if quantZeroMax >= t0 {
		t.Fatalf("quantZeroMax %v must be < the zero boundary t0 %v", quantZeroMax, t0)
	}

	sweep := func(lo, step float64) {
		for gg := 150; gg <= 255; gg++ {
			is := stepQ(quantGainBase - gg) // sf zero: uniform is across bands
			var xr [576]float64
			for i := range xr {
				tt := lo + float64(i)*step
				mag := tt / is
				if i&1 == 0 {
					mag = -mag
				}
				xr[i] = mag
			}
			var got [576]int32
			quantizeGranule(&xr, gg, &sf, lay, &got)
			for i := range xr {
				if want := quantRefNoSkip(xr[i], is); got[i] != want {
					t.Fatalf("gg=%d i=%d xr=%g is=%g: got %d want %d", gg, i, xr[i], is, got[i], want)
				}
			}
		}
	}
	sweep(0.35, 0.30/575.0)     // broad: both the skip bound and t0
	sweep(0.4997, 0.0006/575.0) // fine: densely straddles t0 (~1e-6 step)
}

// minGlobalGainScan is the upward linear scan minGlobalGain replaced with a
// binary search, kept as the oracle: the smallest gg in 0..255 whose every
// band fits, 255 when none does.
func minGlobalGainScan(xr *[576]float64, sf *scfState, lay *bandLayout) int {
	var bandMax [39]float64
	i := 0
	for sfb := range lay.nBands {
		m := 0.0
		for end := i + lay.width[sfb]; i < end; i++ {
			m = max(m, math.Abs(xr[i]))
		}
		bandMax[sfb] = m
	}
	for gg := range 256 {
		fits := true
		for sfb := range lay.nBands {
			t := bandMax[sfb] * stepQ((quantGainBase-gg)+sf.bandExtraQuarters(sfb, lay))
			if math.Sqrt(t*math.Sqrt(t))+0.4054 > float64(maxQuant) {
				fits = false
				break
			}
		}
		if fits {
			return gg
		}
	}
	return 255
}

// randomScfState draws a scalefactor state within the bitstream's field limits
// (4-bit slen1 bands, 3-bit slen2 bands, 3-bit subblock gain).
func randomScfState(seed *uint64, lay *bandLayout) scfState {
	var sf scfState
	for b := range lay.nScf {
		if b < lay.slen1End {
			sf.scf[b] = int(testsignal.LCG(seed) * 16)
		} else {
			sf.scf[b] = int(testsignal.LCG(seed) * 8)
		}
	}
	sf.scalefacScale = int(float64(testsignal.LCG(seed)) * 2)
	if lay.short {
		for w := range sf.subblockGain {
			sf.subblockGain[w] = int(testsignal.LCG(seed) * 8)
		}
	} else {
		sf.preflag = int(float64(testsignal.LCG(seed)) * 2)
	}
	return sf
}

// TestMinGlobalGainMatchesScan requires the binary search to return exactly
// what the upward scan does, over all six layouts, random scalefactor states
// and log-uniform spectra, plus spectra whose loudest line sits a few ulps
// either side of one band's fit threshold at a random gg. The same cache's
// memoized minGain must agree on every one of them, across a sequence of
// scalefactor states served from one reset, and across spectra that change in
// place at one address (check resets the cache for each new content).
func TestMinGlobalGainMatchesScan(t *testing.T) {
	var seed uint64 = 99
	qc := new(quantCache)
	check := func(name string, xr *[576]float64, sf *scfState, lay *bandLayout) {
		t.Helper()
		want := minGlobalGainScan(xr, sf, lay)
		if got := minGlobalGain(xr, sf, lay); got != want {
			t.Fatalf("%s (short=%v nBands=%d): minGlobalGain = %d, scan = %d", name, lay.short, lay.nBands, got, want)
		}
		qc.reset(xr, lay)
		if got := qc.minGain(sf, lay); got != want {
			t.Fatalf("%s (short=%v nBands=%d): minGain = %d, scan = %d", name, lay.short, lay.nBands, got, want)
		}
	}
	// checkSequence runs minGain over several scalefactor states on one reset,
	// so a band's memo slot is filled by one state and read by the others, and
	// the first state is asked for again once the memo is full.
	checkSequence := func(name string, xr *[576]float64, states []scfState, lay *bandLayout) {
		t.Helper()
		qc.reset(xr, lay)
		for round := range 2 {
			for i := range states {
				want := minGlobalGainScan(xr, &states[i], lay)
				if got := qc.minGain(&states[i], lay); got != want {
					t.Fatalf("%s (short=%v nBands=%d) round %d state %d: minGain = %d, scan = %d", name, lay.short, lay.nBands, round, i, got, want)
				}
			}
		}
	}
	for _, lay := range prefixCostLayouts {
		for trial := range 300 {
			sf := randomScfState(&seed, lay)
			var xr [576]float64
			for i := range xr {
				// Log-uniform magnitudes over 1e-6..1e8, with some silent lines.
				if testsignal.LCG(&seed) < 0.2 {
					continue
				}
				v := math.Pow(10, float64(testsignal.LCG(&seed)*14)-6)
				if testsignal.LCG(&seed) < 0.5 {
					v = -v
				}
				xr[i] = v
			}
			check("random spectrum", &xr, &sf, lay)

			// Scalefactor states at the extremes of what the memo indexes
			// (zero and the maximum amplification, extra = maxExtraQuarters
			// on a short layout), plus two beyond it that no outerLoop state
			// produces: past maxExtraQuarters, and below zero.
			states := make([]scfState, 3, 9)
			states[0], states[2] = sf, quantizeMaxScf(lay)
			for range 4 {
				states = append(states, randomScfState(&seed, lay))
			}
			over, under := quantizeMaxScf(lay), scfState{}
			for b := range over.scf {
				over.scf[b] = 40
				under.scf[b] = -1
			}
			over.subblockGain = [3]int{9, 12, 14}
			states = append(states, over, under)
			checkSequence("random spectrum, state sequence", &xr, states, lay)

			// Put one band's loudest line at the exact fit threshold of a
			// random gg, then nudge it by up to 3 ulps each way.
			var quiet [576]float64
			sfb := trial % lay.nBands
			gg := int(testsignal.LCG(&seed) * 255)
			thr := math.Pow(float64(maxQuant)-0.4054, 4.0/3) / stepQ((quantGainBase-gg)+sf.bandExtraQuarters(sfb, lay))
			start := 0
			for b := range sfb {
				start += lay.width[b]
			}
			for k := -3; k <= 3; k++ {
				v := thr
				for range max(k, -k) {
					if k > 0 {
						v = math.Nextafter(v, math.Inf(1))
					} else {
						v = math.Nextafter(v, 0)
					}
				}
				quiet[start] = v
				check("threshold neighbourhood", &quiet, &sf, lay)
			}
		}
	}
}

// TestMinGlobalGainEdges pins the cases the search's upper bound decides:
// nothing in 0..254 fits (255 comes back whether or not 255 fits), only gg 255
// fits, and an empty layout.
func TestMinGlobalGainEdges(t *testing.T) {
	lay := &layoutLong[0]
	var sf scfState
	spectrum := func(v float64) *[576]float64 {
		var xr [576]float64
		xr[0] = v
		return &xr
	}
	// The fit threshold of band 0 at gg = 255; gg = 254 needs it 2^(1/4) lower.
	thr255 := math.Pow(float64(maxQuant)-0.4054, 4.0/3) / stepQ(quantGainBase-255)
	cases := []struct {
		name string
		xr   *[576]float64
		lay  *bandLayout
		want int
	}{
		{"nothing fits, huge finite", spectrum(1e300), lay, 255},
		{"nothing fits, +Inf", spectrum(math.Inf(1)), lay, 255},
		{"only gg 255 fits", spectrum(0.99 * thr255), lay, 255},
		{"silent spectrum", spectrum(0), lay, 0},
		{"no bands", spectrum(1e300), &bandLayout{}, 0},
	}
	qc := new(quantCache)
	for _, c := range cases {
		if got := minGlobalGain(c.xr, &sf, c.lay); got != c.want {
			t.Errorf("%s: minGlobalGain = %d, want %d", c.name, got, c.want)
		}
		qc.reset(c.xr, c.lay)
		if got := qc.minGain(&sf, c.lay); got != c.want {
			t.Errorf("%s: minGain = %d, want %d", c.name, got, c.want)
		}
		if got := minGlobalGainScan(c.xr, &sf, c.lay); got != c.want {
			t.Errorf("%s: oracle scan = %d, want %d (test case is wrong)", c.name, got, c.want)
		}
	}
}

// TestStepQTable pins stepQTable bit-for-bit against the Ldexp formula through
// stepQ (so an index offset error shows), and checks that every exponent the
// encoder can form under the maximum amplification state stays inside the
// table: the forward exponent at gg = 0 and gg = 255, and noiseGranule's
// negation of it.
func TestStepQTable(t *testing.T) {
	for q := -stepQOffset; q < stepQOffset; q++ {
		got := math.Float64bits(stepQ(q))
		want := math.Float64bits(math.Ldexp(pow2Quarter[q&3], q>>2))
		if got != want {
			t.Fatalf("stepQ(%d) bits %#x, want %#x", q, got, want)
		}
	}
	for li, lay := range prefixCostLayouts {
		sf := quantizeMaxScf(lay)
		for b := range lay.nBands {
			extra := sf.bandExtraQuarters(b, lay)
			for _, gg := range []int{0, 255} {
				fwd := (quantGainBase - gg) + extra
				for _, q := range []int{fwd, -fwd} {
					if q < -stepQOffset || q >= stepQOffset {
						t.Errorf("%s band %d gg %d: exponent %d outside the stepQ table", prefixCostLayoutName(li), b, gg, q)
					}
				}
				if extra > maxExtraQuarters {
					t.Errorf("%s band %d: extra %d exceeds maxExtraQuarters %d", prefixCostLayoutName(li), b, extra, maxExtraQuarters)
				}
				if k := fwd - qMin; k < 0 || k >= qSpan {
					t.Errorf("%s band %d gg %d: exponent %d outside the quantCache span [%d, %d)", prefixCostLayoutName(li), b, gg, fwd, qMin, qMin+qSpan)
				}
			}
		}
	}
}

// quantizeMaxScf is the maximum amplification state: scf 15 below slen1End and
// 7 above, scalefac_scale 1, preflag 1 on long layouts, subblock_gain 7.
func quantizeMaxScf(lay *bandLayout) (sf scfState) {
	for b := range sf.scf {
		sf.scf[b] = 7
		if b < lay.slen1End {
			sf.scf[b] = 15
		}
	}
	sf.subblockGain = [3]int{7, 7, 7}
	sf.scalefacScale = 1
	if !lay.short {
		sf.preflag = 1
	}
	return sf
}
