package enc

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"
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
			want := norm * 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(c.n)))
			if d := ulpDistance(c.w[i], want); d > 1 {
				t.Errorf("N=%d w[%d]: %d ulp from the closed form", c.n, i, d)
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
