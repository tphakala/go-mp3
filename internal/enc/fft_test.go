package enc

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// sha256U16 hashes uint16s as 2-byte little-endian words, the bit-reversal
// tables' analog of sha256Float64s.
func sha256U16(vs ...uint16) string {
	h := sha256.New()
	var b [2]byte
	for _, v := range vs {
		binary.LittleEndian.PutUint16(b[:], v)
		h.Write(b[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestFFTTwiddlesKnownAnswer(t *testing.T) {
	cases := []struct {
		re, im []float64
		n      int
	}{
		{fftTwRe1024[:], fftTwIm1024[:], 1024},
		{fftTwRe256[:], fftTwIm256[:], 256},
	}
	for _, c := range cases {
		if len(c.re) != c.n/2 || len(c.im) != c.n/2 {
			t.Fatalf("N=%d: twiddle tables have %d/%d entries, want %d",
				c.n, len(c.re), len(c.im), c.n/2)
		}
		// Exact spots: W^0 = 1 + 0i.
		if c.re[0] != 1 || c.im[0] != 0 {
			t.Errorf("N=%d: W^0 = (%v, %v), want (1, 0)", c.n, c.re[0], c.im[0])
		}
		// Recompute with test-side libm within 1 ulp (the committed
		// literals are the runtime truth; math.Cos/Sin vary per arch by
		// about 1 ulp, the Phase 3 table-test convention).
		for k := range c.re {
			ang := 2 * math.Pi * float64(k) / float64(c.n)
			if d := ulpDistance(c.re[k], math.Cos(ang)); d > 1 {
				t.Errorf("N=%d twRe[%d]: %d ulp from math.Cos", c.n, k, d)
			}
			if d := ulpDistance(c.im[k], -math.Sin(ang)); d > 1 {
				t.Errorf("N=%d twIm[%d]: %d ulp from -math.Sin", c.n, k, d)
			}
		}
	}
}

func TestFFTBitrevKnownAnswer(t *testing.T) {
	cases := []struct {
		tab  []uint16
		logN int
	}{
		{fftBitrev1024[:], 10},
		{fftBitrev256[:], 8},
	}
	for _, c := range cases {
		n := 1 << c.logN
		if len(c.tab) != n {
			t.Fatalf("logN=%d: table has %d entries, want %d", c.logN, len(c.tab), n)
		}
		for i := range n {
			r := 0
			for b := range c.logN {
				if i&(1<<b) != 0 {
					r |= 1 << (c.logN - 1 - b)
				}
			}
			if int(c.tab[i]) != r {
				t.Errorf("logN=%d bitrev[%d] = %d, want %d", c.logN, i, c.tab[i], r)
			}
		}
	}
}

func TestHannWindowsKnownAnswer(t *testing.T) {
	cases := []struct {
		w []float64
		n int
	}{
		{hannWindow1024[:], 1024},
		{hannWindow256[:], 256},
	}
	norm := math.Sqrt(8.0 / 3.0) // correctly rounded, cross-arch exact
	for _, c := range cases {
		if len(c.w) != c.n {
			t.Fatalf("N=%d: window has %d entries", c.n, len(c.w))
		}
		if c.w[0] != 0 {
			t.Errorf("N=%d: w[0] = %v, want exactly 0", c.n, c.w[0])
		}
		// The generator computes i in [0, N/2] and mirrors, so symmetry is
		// bit-exact by construction, and w[N/2] hits cos(pi) = -1 exactly:
		// w[N/2] = sqrt(8/3) exactly.
		if math.Float64bits(c.w[c.n/2]) != math.Float64bits(norm) {
			t.Errorf("N=%d: w[N/2] = %x, want sqrt(8/3) = %x", c.n, c.w[c.n/2], norm)
		}
		for i := 1; i < c.n/2; i++ {
			if math.Float64bits(c.w[i]) != math.Float64bits(c.w[c.n-i]) {
				t.Errorf("N=%d: w[%d] != w[%d] (mirror symmetry broken)", c.n, i, c.n-i)
			}
		}
		for i := 0; i <= c.n/2; i++ {
			// closeToFormula (not a raw ulp bound): the reference chains
			// several roundings (2*pi*i/N, then math.Cos, then two more
			// mults and a subtract), and math.Cos is not bit-identical
			// across amd64 and arm64, so the recompute lands up to ~2 ulp
			// from the committed literal on arm64 (e.g. w[154] at N=1024).
			// The literal is the runtime truth, frozen by the checksum and
			// symmetry checks above; this only proves the transcription. The
			// 1e-12 absolute tolerance also sidesteps ulpDistance's cap of 4,
			// which would mask a >4 ulp divergence.
			want := norm * 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(c.n)))
			if !closeToFormula(c.w[i], want) {
				t.Errorf("N=%d w[%d] = %x: not close to closed form %x", c.n, i, c.w[i], want)
			}
		}
	}
}

// fftTablesSHA / fftBitrevSHA freeze the FFT tables; same freeze procedure
// as detmathTablesSHA.
const (
	fftTablesSHA = "5a7539933c72d5fcae937acff798452f790e9518dd22f384a23b458c01865f58"
	fftBitrevSHA = "92f405b9b9a4d010887ad8c5973d2af17731e4113b823dfbf57b6cff55d06160"
)

func TestFFTTablesChecksum(t *testing.T) {
	vs := make([]float64, 0, len(fftTwRe1024)+len(fftTwIm1024)+len(fftTwRe256)+len(fftTwIm256)+len(hannWindow1024)+len(hannWindow256))
	vs = append(vs, fftTwRe1024[:]...)
	vs = append(vs, fftTwIm1024[:]...)
	vs = append(vs, fftTwRe256[:]...)
	vs = append(vs, fftTwIm256[:]...)
	vs = append(vs, hannWindow1024[:]...)
	vs = append(vs, hannWindow256[:]...)
	gotF := sha256Float64s(vs...)

	us := make([]uint16, 0, len(fftBitrev1024)+len(fftBitrev256))
	us = append(us, fftBitrev1024[:]...)
	us = append(us, fftBitrev256[:]...)
	gotU := sha256U16(us...)

	if fftTablesSHA == "" || fftBitrevSHA == "" {
		t.Fatalf("FREEZE ME:\nconst fftTablesSHA = %q\nconst fftBitrevSHA = %q", gotF, gotU)
	}
	if gotF != fftTablesSHA {
		t.Fatalf("FFT float tables changed: %s, frozen %s", gotF, fftTablesSHA)
	}
	if gotU != fftBitrevSHA {
		t.Fatalf("FFT bitrev tables changed: %s, frozen %s", gotU, fftBitrevSHA)
	}
}

// naiveDFT is the O(n^2) reference transform, test-side libm allowed.
func naiveDFT(re, im []float64) (outRe, outIm []float64) {
	n := len(re)
	outRe = make([]float64, n)
	outIm = make([]float64, n)
	for k := range n {
		var sr, si float64
		for x := range n {
			ang := -2 * math.Pi * float64(k) * float64(x) / float64(n)
			c, s := math.Cos(ang), math.Sin(ang)
			sr += re[x]*c - im[x]*s
			si += re[x]*s + im[x]*c
		}
		outRe[k], outIm[k] = sr, si
	}
	return outRe, outIm
}

// runFFT dispatches on length so the shared test bodies below cover both
// sizes through the public per-size entry points.
func runFFT(t *testing.T, re, im []float64) {
	t.Helper()
	switch len(re) {
	case 1024:
		fft1024((*[1024]float64)(re), (*[1024]float64)(im))
	case 256:
		fft256((*[256]float64)(re), (*[256]float64)(im))
	default:
		t.Fatalf("runFFT: unsupported length %d", len(re))
	}
}

func TestFFTImpulseExact(t *testing.T) {
	// A unit impulse at n = 0 must transform to exactly 1+0i in EVERY bin:
	// index 0 never moves under bit reversal, and at every stage the
	// nonzero values sit in butterfly positions whose twiddle is W^0 = 1
	// (exact in the table), so no rounding ever occurs. Verified exact at
	// plan time for both sizes; this is structural, not a tolerance.
	for _, n := range []int{1024, 256} {
		re := make([]float64, n)
		im := make([]float64, n)
		re[0] = 1
		runFFT(t, re, im)
		for k := range n {
			if re[k] != 1 || im[k] != 0 {
				t.Fatalf("N=%d: X[%d] = (%v, %v), want exactly (1, 0)", n, k, re[k], im[k])
			}
		}
	}
}

func TestFFTDCExact(t *testing.T) {
	// All-ones input: X[0] = N exactly (pure additions of exact values),
	// and every other bin is exactly 0: each stage's butterflies cancel
	// equal values exactly. Verified exact at plan time for both sizes.
	for _, n := range []int{1024, 256} {
		re := make([]float64, n)
		im := make([]float64, n)
		for i := range re {
			re[i] = 1
		}
		runFFT(t, re, im)
		if re[0] != float64(n) || im[0] != 0 {
			t.Fatalf("N=%d: X[0] = (%v, %v), want exactly (%d, 0)", n, re[0], im[0], n)
		}
		for k := 1; k < n; k++ {
			if re[k] != 0 || im[k] != 0 {
				t.Fatalf("N=%d: X[%d] = (%v, %v), want exactly (0, 0)", n, k, re[k], im[k])
			}
		}
	}
}

func TestFFTMatchesNaiveDFT(t *testing.T) {
	// Tolerance: measured max component diff 2.4e-11 at N=1024 with inputs
	// in [-1, 1] at plan time (dominated by the naive DFT's own error);
	// 1e-9 is a 40x backstop.
	for _, n := range []int{1024, 256} {
		seed := uint64(5)
		re := make([]float64, n)
		im := make([]float64, n)
		for i := range re {
			re[i] = testsignal.LCGSigned(&seed)
			im[i] = testsignal.LCGSigned(&seed)
		}
		wantRe, wantIm := naiveDFT(re, im)
		runFFT(t, re, im)
		for k := range n {
			if d := math.Hypot(re[k]-wantRe[k], im[k]-wantIm[k]); d > 1e-9 {
				t.Fatalf("N=%d bin %d: |FFT-DFT| = %.3g > 1e-9", n, k, d)
			}
		}
	}
}

func TestFFTParseval(t *testing.T) {
	for _, n := range []int{1024, 256} {
		seed := uint64(6)
		re := make([]float64, n)
		im := make([]float64, n)
		var eIn float64
		for i := range re {
			re[i] = testsignal.LCGSigned(&seed)
			im[i] = testsignal.LCGSigned(&seed)
			eIn += re[i]*re[i] + im[i]*im[i]
		}
		runFFT(t, re, im)
		var eOut float64
		for k := range re {
			eOut += re[k]*re[k] + im[k]*im[k]
		}
		if r := math.Abs(eOut/float64(n)-eIn) / eIn; r > 1e-12 {
			t.Fatalf("N=%d: Parseval rel err %.3g > 1e-12", n, r)
		}
	}
}

func TestFFTToneBin(t *testing.T) {
	// A real cosine at exact bin b concentrates in bins b and N-b with
	// value N/2; all other bins near zero. Measured off-bin magnitude
	// <= 9e-14 at plan time; 1e-8 backstop.
	const b = 37
	for _, n := range []int{1024, 256} {
		re := make([]float64, n)
		im := make([]float64, n)
		for i := range re {
			re[i] = math.Cos(2 * math.Pi * b * float64(i) / float64(n))
		}
		runFFT(t, re, im)
		half := float64(n) / 2
		for k := range n {
			mag := math.Hypot(re[k], im[k])
			if k == b || k == n-b {
				if math.Abs(mag-half) > 1e-8 {
					t.Fatalf("N=%d: |X[%d]| = %v, want %v within 1e-8", n, k, mag, half)
				}
			} else if mag > 1e-8 {
				t.Fatalf("N=%d: off-bin |X[%d]| = %.3g > 1e-8", n, k, mag)
			}
		}
	}
}

func TestFFTLinearity(t *testing.T) {
	const n = 1024
	seed := uint64(8)
	x1 := make([]float64, n)
	y1 := make([]float64, n)
	x2 := make([]float64, n)
	y2 := make([]float64, n)
	xs := make([]float64, n)
	ys := make([]float64, n)
	for i := range x1 {
		x1[i] = testsignal.LCGSigned(&seed)
		y1[i] = testsignal.LCGSigned(&seed)
		x2[i] = testsignal.LCGSigned(&seed)
		y2[i] = testsignal.LCGSigned(&seed)
		xs[i] = 2*x1[i] + 3*x2[i]
		ys[i] = 2*y1[i] + 3*y2[i]
	}
	runFFT(t, x1, y1)
	runFFT(t, x2, y2)
	runFFT(t, xs, ys)
	for k := range xs {
		wr := 2*x1[k] + 3*x2[k]
		wi := 2*y1[k] + 3*y2[k]
		if d := math.Hypot(xs[k]-wr, ys[k]-wi); d > 1e-9 {
			t.Fatalf("linearity broken at bin %d: %.3g > 1e-9", k, d)
		}
	}
}

// fftGoldenSHA: same contract as plogGoldenSHA (cross-arch determinism
// gate on the amd64+arm64 CI matrix; never re-freeze on an arch mismatch).
const fftGoldenSHA = "32318d9e554ffda71cbe885e2baed65441723b5dbe09350a0efb0c2fca5bacc8"

func TestFFTGolden(t *testing.T) {
	var out []float64
	for _, n := range []int{1024, 256} {
		seed := uint64(21)
		re := make([]float64, n)
		im := make([]float64, n)
		for i := range re {
			re[i] = testsignal.LCGSigned(&seed)
			im[i] = testsignal.LCGSigned(&seed)
		}
		runFFT(t, re, im)
		out = append(out, re...)
		out = append(out, im...)
	}
	got := sha256Float64s(out...)
	if fftGoldenSHA == "" {
		t.Fatalf("FREEZE ME: const fftGoldenSHA = %q", got)
	}
	if got != fftGoldenSHA {
		t.Fatalf("FFT output changed: sha256 = %s, frozen %s", got, fftGoldenSHA)
	}
}

func TestFFTAllocs(t *testing.T) {
	var re, im [1024]float64
	re[3] = 1
	if n := testing.AllocsPerRun(100, func() { fft1024(&re, &im) }); n != 0 {
		t.Fatalf("fft1024 allocates: %v allocs per run, want 0", n)
	}
}

func BenchmarkFFT1024(b *testing.B) {
	// Copy a pristine source into re/im every iteration. The forward DFT
	// scales magnitudes by ~sqrt(N) per pass, so transforming the same array
	// in place repeatedly overflows to +Inf then NaN within ~60 iterations,
	// after which the benchmark would only measure NaN-propagation speed. The
	// array assignment is a value copy (no allocation), so the zero-alloc
	// property under measurement is preserved.
	var sourceRe, sourceIm, re, im [1024]float64
	seed := uint64(1)
	for i := range sourceRe {
		sourceRe[i] = testsignal.LCGSigned(&seed)
	}
	for b.Loop() {
		re, im = sourceRe, sourceIm
		fft1024(&re, &im)
	}
}
