package enc

import (
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// quantizeParityLayouts are the long and short band layouts of all three
// sample rates.
var quantizeParityLayouts = []*bandLayout{&layoutLong[0], &layoutLong[1], &layoutLong[2], &layoutShort[0], &layoutShort[1], &layoutShort[2]}

// quantizeParityInput draws a granule of lines scaled by scale, with negative
// zeros, zeros and lines far past maxQuant planted at fixed spacings, and a
// scalefactor state for lay whose scalefac_scale and preflag come from
// flags. Each scalefactor spans its band's full slen range: four bits below
// lay.slen1End, three bits from there on.
func quantizeParityInput(seed *uint64, scale float64, flags int, lay *bandLayout) (xr [576]float64, sf scfState) {
	for i := range xr {
		v := (testsignal.LCG(seed) - 0.5) * scale * 2
		switch i % 97 {
		case 3:
			v = math.Copysign(0, -1)
		case 5:
			v = 0
		case 7:
			v = math.Copysign(1e300, v)
		}
		xr[i] = v
	}
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
	return xr, sf
}

// checkQuantizeGranule requires quantizeGranule to overwrite every line of a
// buffer prefilled with maxQuant+1, a value no line quantizes to, and to match
// quantizeGranuleGo line for line.
func checkQuantizeGranule(t *testing.T, xr *[576]float64, gg int, sf *scfState, lay *bandLayout) {
	t.Helper()
	var got, want [576]int32
	for i := range got {
		got[i] = maxQuant + 1
	}
	quantizeGranule(xr, gg, sf, lay, &got)
	quantizeGranuleGo(xr, gg, sf, lay, &want)
	if got != want {
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("short=%v gg=%d line %d (xr=%v): got %d want %d", lay.short, gg, i, xr[i], got[i], want[i])
			}
		}
	}
}

// TestQuantizeGranuleParity sweeps lines of every magnitude, global gains and
// scalefactor states, including negative zeros, lines clamped to maxQuant and
// overflowing lines, and requires the SIMD dispatcher to match
// quantizeGranuleGo line for line.
func TestQuantizeGranuleParity(t *testing.T) {
	var seed uint64 = 99
	for _, lay := range quantizeParityLayouts {
		for trial := range 60 {
			xr, sf := quantizeParityInput(&seed, math.Pow(10, float64(trial%12)-4), trial, lay)
			for gg := 0; gg < 256; gg += 1 + trial%7 {
				checkQuantizeGranule(t, &xr, gg, &sf, lay)
			}
		}
	}
}

// FuzzQuantizeGranuleParity drives the layout, magnitude scale, global gain
// and scalefactor flags beyond the fixed sweep, with the lines and
// scalefactors drawn from a seeded LCG.
func FuzzQuantizeGranuleParity(f *testing.F) {
	f.Add(uint8(0), uint8(4), uint8(120), uint8(1), uint64(1))
	f.Add(uint8(3), uint8(11), uint8(0), uint8(2), uint64(2))
	f.Add(uint8(5), uint8(0), uint8(255), uint8(3), uint64(3))
	f.Fuzz(func(t *testing.T, layR, scaleR, gg, flags uint8, seed uint64) {
		lay := quantizeParityLayouts[int(layR)%len(quantizeParityLayouts)]
		xr, sf := quantizeParityInput(&seed, math.Pow(10, float64(scaleR%12)-4), int(flags), lay)
		checkQuantizeGranule(t, &xr, int(gg), &sf, lay)
	})
}
