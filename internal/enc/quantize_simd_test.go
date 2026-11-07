//go:build !noasm

package enc

import (
	"fmt"
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// quantizeParityInput draws a granule of lines scaled by scale, with special
// values planted at fixed spacings, and a scalefactor state for lay whose
// scalefac_scale and preflag come from flags. Each scalefactor spans its
// band's full slen range: four bits below lay.slen1End, three bits from there
// on; subblock_gain spans 0..7.
func quantizeParityInput(seed *uint64, scale float64, flags int, lay *bandLayout) (xr [576]float64, sf scfState) {
	for i := range xr {
		v := (float64(testsignal.LCG(seed)) - 0.5) * (2 * scale)
		switch i % 97 {
		case 3:
			v = math.Copysign(0, -1)
		case 5:
			v = 0
		case 7:
			v = math.Copysign(1e300, v)
		case 11:
			v = math.NaN()
		case 13:
			v = math.Copysign(math.NaN(), -1)
		case 17:
			v = math.Inf(1)
		case 19:
			v = math.Inf(-1)
		case 23:
			v = math.Copysign(5e-324, v)
		case 29:
			v = math.Copysign(1e-310, v)
		case 31:
			v = math.Copysign(math.MaxFloat64, v)
		}
		xr[i] = v
	}
	sf = quantizeParityScf(seed, flags, lay)
	return xr, sf
}

// quantizeParityScf draws a scalefactor state for lay, see quantizeParityInput.
func quantizeParityScf(seed *uint64, flags int, lay *bandLayout) (sf scfState) {
	for b := range sf.scf {
		span := 8.0
		if b < lay.slen1End {
			span = 16
		}
		sf.scf[b] = int(testsignal.LCG(seed) * span)
	}
	sf.subblockGain = [3]int{int(testsignal.LCG(seed) * 8), int(testsignal.LCG(seed) * 8), int(testsignal.LCG(seed) * 8)}
	sf.scalefacScale = flags % 2
	if !lay.short {
		sf.preflag = (flags / 2) % 2
	}
	return sf
}

// quantizeEdgeInput builds a granule at gg whose special values sit at fixed
// line indices 0..13 and whose other lines are scaled so that, band by band,
// the scaled magnitude straddles the zero boundary t0, the half-integer
// rounding steps and the maxQuant clamp boundary within a few ulps.
func quantizeEdgeInput(gg int, sf *scfState, lay *bandLayout) (xr [576]float64) {
	// t at which sqrt(t*sqrt(t)) + quantRounding crosses maxQuant and 1.
	tClamp := math.Pow(maxQuant-quantRounding, 4.0/3)
	t0 := math.Pow(1-quantRounding, 4.0/3)
	i := 0
	for sfb := range lay.nBands {
		is := stepQ((quantGainBase - gg) + sf.bandExtraQuarters(sfb, lay))
		for end := i + lay.width[sfb]; i < end; i++ {
			var t float64
			k := float64(i%9 - 4)
			switch i % 4 {
			case 0:
				t = t0 * (1 + float64(k*1e-9))
			case 1:
				t = tClamp * (1 + float64(k*1e-9))
			case 2:
				t = math.Pow(float64(1+i%40)-quantRounding, 4.0/3) * (1 + float64(k*1e-12))
			default:
				t = math.Pow(10, float64(i%17)-8)
			}
			x := t / is
			if i%2 == 1 {
				x = -x
			}
			xr[i] = x
		}
	}
	specials := [...]float64{
		math.Copysign(0, -1), 0, math.NaN(), math.Copysign(math.NaN(), -1),
		math.Inf(1), math.Inf(-1), 5e-324, -5e-324, 1e-310, -1e-310,
		1e300, -1e300, math.MaxFloat64, -math.MaxFloat64,
	}
	copy(xr[:], specials[:])
	return xr
}

// checkQuantizeGranule requires quantizeGranule to overwrite every line of a
// buffer prefilled with maxQuant+1, a value no line quantizes to, and to match
// quantizeGranuleGo (prefilled the same) over the whole array, so a skipped
// store or a write past the last band shows up on either side.
func checkQuantizeGranule(t *testing.T, xr *[576]float64, gg int, sf *scfState, lay *bandLayout, ctx string) {
	t.Helper()
	var got, want [576]int32
	for i := range got {
		got[i] = maxQuant + 1
		want[i] = maxQuant + 1
	}
	quantizeGranule(xr, gg, sf, lay, &got)
	quantizeGranuleGo(xr, gg, sf, lay, &want)

	// The per-band cache assembles a granule from quantizeBand calls; they must
	// reproduce the whole-granule lines exactly.
	var banded [576]int32
	for i := range banded {
		banded[i] = maxQuant + 1
	}
	off := 0
	for b := range lay.nBands {
		w := lay.width[b]
		quantizeBand(banded[off:off+w], xr[off:off+w], stepQ((quantGainBase-gg)+sf.bandExtraQuarters(b, lay)))
		off += w
	}
	if banded != want {
		for i := range banded {
			if banded[i] != want[i] {
				t.Fatalf("%s short=%v gg=%d line %d (xr=%v bits=%#x): quantizeBand got %d want %d",
					ctx, lay.short, gg, i, xr[i], math.Float64bits(xr[i]), banded[i], want[i])
			}
		}
	}
	if got == want {
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s short=%v gg=%d line %d (xr=%v bits=%#x): got %d want %d",
				ctx, lay.short, gg, i, xr[i], math.Float64bits(xr[i]), got[i], want[i])
		}
	}
}

// TestQuantizeGranuleParity requires the simd quantizer to match
// quantizeGranuleGo line for line: a pinned edge granule (NaN, infinities,
// signed zeros, subnormals, overflowing lines, the t0 straddle and the clamp
// boundary) on every layout at extreme gains and scalefactor states, then a
// sweep of magnitudes, gains and random scalefactor states.
func TestQuantizeGranuleParity(t *testing.T) {
	for li, lay := range prefixCostLayouts {
		name := prefixCostLayoutName(li)
		var zero scfState
		maxSf := quantizeMaxScf(lay)
		for _, gg := range []int{0, 41, 214, 255} {
			for si, sf := range []*scfState{&zero, &maxSf} {
				xr := quantizeEdgeInput(gg, sf, lay)
				checkQuantizeGranule(t, &xr, gg, sf, lay, fmt.Sprintf("edge %s sf%d", name, si))
			}
		}
	}

	var seed uint64 = 99
	for li, lay := range prefixCostLayouts {
		for trial := range 60 {
			xr, sf := quantizeParityInput(&seed, math.Pow(10, float64(trial%12)-4), trial, lay)
			for gg := 0; gg < 256; gg += 1 + trial%7 {
				checkQuantizeGranule(t, &xr, gg, &sf, lay, fmt.Sprintf("sweep %s trial %d", prefixCostLayoutName(li), trial))
			}
		}
	}
}

// FuzzQuantizeGranuleParity drives the layout, magnitude scale, global gain and
// scalefactor flags beyond the fixed sweep, with the lines and scalefactors
// drawn from a seeded LCG.
func FuzzQuantizeGranuleParity(f *testing.F) {
	f.Add(uint8(0), uint8(4), uint8(120), uint8(1), uint64(1))
	f.Add(uint8(3), uint8(11), uint8(0), uint8(2), uint64(2))
	f.Add(uint8(5), uint8(0), uint8(255), uint8(3), uint64(3))
	f.Add(uint8(1), uint8(7), uint8(41), uint8(0), uint64(4))   // NaN, Inf and subnormal lines at a mid gain
	f.Add(uint8(4), uint8(10), uint8(214), uint8(1), uint64(5)) // overflow and clamp-boundary magnitudes
	f.Fuzz(func(t *testing.T, layR, scaleR, gg, flags uint8, seed uint64) {
		lay := prefixCostLayouts[int(layR)%len(prefixCostLayouts)]
		xr, sf := quantizeParityInput(&seed, math.Pow(10, float64(scaleR%12)-4), int(flags), lay)
		checkQuantizeGranule(t, &xr, int(gg), &sf, lay, "fuzz")
	})
}
