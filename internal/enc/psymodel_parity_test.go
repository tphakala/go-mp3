package enc

import (
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// fillPsySpreadParity fills the partition vector v, of which a rate uses the
// first n entries. Those get frontParitySample values (signed zeros,
// subnormals and 1e150 at its fixed positions among ordinary ones). The
// entries past n get NaN, which reaches an output only if a form reads a row
// it must not. With inf set, one used entry drawn from seed becomes an
// infinity of random sign: times an exact zero of the spreading matrix that
// is NaN, times anything else an infinity, so the outputs must be NaN in
// exactly the reference's places.
func fillPsySpreadParity(seed *uint64, v []float64, n int, scale float64, inf bool) {
	for i := range v {
		v[i] = frontParitySample(seed, i, scale, false)
		if i >= n {
			v[i] = math.NaN()
		}
	}
	if inf {
		k := int(testsignal.LCG(seed) * float64(n))
		v[k] = math.Copysign(math.Inf(1), testsignal.LCGSigned(seed))
	}
}

// checkPsySpreadParity fills one model's partition vectors for rate sri and
// requires spread and spreadShort to match the spreading convolution written
// one partition at a time: each sum from +0.0 in ascending source partition
// with every product rounded before its add. It is the reference both forms
// must match bit for bit in every build (simd kernels, simd's portable path,
// noasm).
func checkPsySpreadParity(t *testing.T, seed *uint64, scale float64, sri int, inf bool) {
	t.Helper()
	var p PsyModel
	p.Reset(sri)

	tab := p.tab
	n := tab.nParts
	fillPsySpreadParity(seed, p.e[:], n, scale, inf)
	fillPsySpreadParity(seed, p.ct[:], n, scale, inf)
	p.spread()
	for b := range n {
		var ecb, cbs float64
		for bb := range n {
			ecb += float64(p.e[bb] * tab.sprd[b][bb])
			cbs += float64(p.ct[bb] * tab.sprd[b][bb])
		}
		if !sameFloatBits(p.ecb[b], ecb) || !sameFloatBits(p.cbs[b], cbs) {
			t.Fatalf("rate %d long partition %d: ecb = %v (bits %x), want %v (bits %x); cbs = %v (bits %x), want %v (bits %x)",
				sri, b, p.ecb[b], math.Float64bits(p.ecb[b]), ecb, math.Float64bits(ecb),
				p.cbs[b], math.Float64bits(p.cbs[b]), cbs, math.Float64bits(cbs))
		}
	}

	st := &psyShortTables[sri]
	ns := st.nParts
	fillPsySpreadParity(seed, p.eS[:], ns, scale, inf)
	p.spreadShort()
	for b := range ns {
		var ecb float64
		for bb := range ns {
			ecb += float64(p.eS[bb] * st.sprd[b][bb])
		}
		if !sameFloatBits(p.ecbS[b], ecb) {
			t.Fatalf("rate %d short partition %d: ecbS = %v (bits %x), want %v (bits %x)",
				sri, b, p.ecbS[b], math.Float64bits(p.ecbS[b]), ecb, math.Float64bits(ecb))
		}
	}
}

// TestPsySpreadParity requires the long and short spreading convolutions to
// match the partition-at-a-time reference bit for bit at all three sample
// rates, across twelve decades of magnitude, with signed zeros, subnormals
// and large values in the input, and again with an infinity in each vector.
func TestPsySpreadParity(t *testing.T) {
	var seed uint64 = 17
	for trial := range 48 {
		scale := math.Pow(10, float64(trial%12)-6)
		for sri := range psyRateTables {
			checkPsySpreadParity(t, &seed, scale, sri, trial%4 == 3)
		}
	}
}

// FuzzPsySpreadParity drives the seed, magnitude, sample rate and the
// presence of an infinity beyond the fixed sweep.
func FuzzPsySpreadParity(f *testing.F) {
	f.Add(uint64(1), uint8(6), uint8(0), false)
	f.Add(uint64(2), uint8(0), uint8(1), false)
	f.Add(uint64(3), uint8(11), uint8(2), true)
	f.Fuzz(func(t *testing.T, seed uint64, scaleR, rateR uint8, inf bool) {
		scale := math.Pow(10, float64(scaleR%12)-6)
		checkPsySpreadParity(t, &seed, scale, int(rateR)%len(psyRateTables), inf)
	})
}
