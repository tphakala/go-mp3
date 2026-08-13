package dec

import (
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/bits"
	"github.com/tphakala/go-mp3/internal/enc"
	"github.com/tphakala/go-mp3/internal/testsignal"
)

// TestEncPretabMatchesDec: the encoder's preemphasis table (ISO 2.4.3.4.5,
// transcribed) must equal the decoder's independently derived preampTable
// (minimp3 g_preamp, CC0) on the bands that carry preemphasis (11..20).
func TestEncPretabMatchesDec(t *testing.T) {
	pre := enc.PretabLongPin()
	for i := range 11 {
		if pre[i] != 0 {
			t.Errorf("pretab[%d] = %d, want 0", i, pre[i])
		}
	}
	for i := range 10 {
		if pre[11+i] != int(preampTable[i]) {
			t.Errorf("pretab[%d] = %d, dec preampTable[%d] = %d",
				11+i, pre[11+i], i, preampTable[i])
		}
	}
}

// readFrameScf parses one encoder frame and returns, per granule-channel,
// the decoder's float32 per-band dequant gains for the long sfbs, driving
// l3ReadSideInfo + l3ReadScalefactors exactly as l3Decode does
// (internal/dec/decode.go:37): per granule, per channel, with the
// per-channel istPos persisting across granules (that persistence is the
// scfsi reuse mechanism).
func readFrameScf(t *testing.T, frame []byte, nch int) (gr [2][2]grInfo, gains [2][2][22]float32) {
	t.Helper()
	hdr := frame[:4]
	bs := bits.NewReader(frame[4:])
	var g [4]grInfo
	if l3ReadSideInfo(&bs, g[:2*nch], hdr, len(frame)-4) < 0 {
		t.Fatal("side info rejected")
	}
	// main_data_begin is 0: main data follows the side info directly.
	main := bits.NewReader(frame[4+sideInfoBitsFor(nch)/8:])
	var istPos [2][40]uint8
	var scf [40]float32
	pos := 0
	for gi := range 2 {
		for ch := range nch {
			gr[gi][ch] = g[gi*nch+ch]
			// Position main at this granule-channel's part2 start.
			for main.Pos() < pos {
				main.Bits(1)
			}
			l3ReadScalefactors(hdr, scf[:], istPos[ch][:], &gr[gi][ch], &main, ch)
			copy(gains[gi][ch][:], scf[:22])
			pos += int(gr[gi][ch].part23Length)
		}
	}
	return gr, gains
}

// TestEncScalefacReadback is the inverse-oracle gate for the whole
// scalefactor pathway. For pinned scalefactor states, the DECODER's
// per-band gain, relative to the all-zero-scf frame with the same
// spectrum, must equal 2^(0.25*(pinGG-baseGG) - (scf+preflag*pretab)*
// (scalefacScale+1)/2): the ratio isolates the scalefactor semantics from
// minimp3's internal gain conventions (the quantGainBase=214 class), and
// float32 rounding is the only tolerance. The global_gain term is
// REQUIRED (agy finding folded): AppendFrameScfPin runs the inner rate
// loop, which picks a DIFFERENT global_gain for the pinned frame than for
// the base frame (the pinned amplification changes the quantized
// magnitudes and hence the bit demand), so both granules' global_gain
// values are read back from the DECODED side info and factored out;
// without that term the assertion is off by 2^(0.25*(pinGG-baseGG)) and
// fails unconditionally.
func TestEncScalefacReadback(t *testing.T) {
	var xr [2][2][576]float64
	seed := uint64(21)
	for g := range 2 {
		for i := range 300 {
			v := testsignal.LCG(&seed) * 1e4
			xr[g][0][i] = v // FMA-safe: stored, no product-into-sum
		}
	}
	zero := func() *[2][2]enc.ScfPin { return &[2][2]enc.ScfPin{} }

	base := enc.AppendFrameScfPin(nil, 9, 0, 0, 3, &xr, zero(), false, 1)
	baseGr, baseGains := readFrameScf(t, base, 1)

	cases := []enc.ScfPin{
		{Scf: [21]int{0: 1}, ScalefacScale: 0},
		{Scf: [21]int{0: 3, 5: 2, 12: 5, 20: 7}, ScalefacScale: 0},
		{Scf: [21]int{0: 3, 5: 2, 12: 5, 20: 7}, ScalefacScale: 1},
		{Preflag: 1},
		{Scf: [21]int{11: 2, 15: 1}, Preflag: 1, ScalefacScale: 1},
	}
	pre := enc.PretabLongPin()
	for ci, pin := range cases {
		pins := zero()
		pins[0][0], pins[1][0] = pin, pin
		frame := enc.AppendFrameScfPin(nil, 9, 0, 0, 3, &xr, pins, false, 1)
		gr, gains := readFrameScf(t, frame, 1)
		for g := range 2 {
			if int(gr[g][0].scalefacScale) != pin.ScalefacScale ||
				int(gr[g][0].preflag) != pin.Preflag {
				t.Fatalf("case %d gr %d: side info fields not round-tripped", ci, g)
			}
			ggDelta := int(gr[g][0].globalGain) - int(baseGr[g][0].globalGain)
			for sfb := range 21 {
				eff := pin.Scf[sfb] + pin.Preflag*pre[sfb]
				wantRatio := math.Pow(2,
					0.25*float64(ggDelta)-float64(eff*(pin.ScalefacScale+1))/2)
				got := float64(gains[g][0][sfb]) / float64(baseGains[g][0][sfb])
				if r := math.Abs(got-wantRatio) / wantRatio; r > 1e-5 {
					t.Fatalf("case %d gr %d sfb %d: gain ratio %v, want %v (rel %.3g, ggDelta %d)",
						ci, g, sfb, got, wantRatio, r, ggDelta)
				}
			}
		}
	}
}

// TestEncScfsiReadback: with useScfsi and identical pinned states, the
// decoder must reconstruct granule 1's gains EQUAL to granule 0's on the
// shared groups while granule 1's part2 shrinks (verified by the
// validator's bit accounting; here we verify the semantics).
func TestEncScfsiReadback(t *testing.T) {
	var xr [2][2][576]float64
	seed := uint64(31)
	for i := range 200 {
		xr[0][0][i] = testsignal.LCG(&seed) * 5e3
	}
	// Granule 1 gets the IDENTICAL spectrum: the decoder's scf floats fold
	// each granule's own global_gain in, so the direct gain-equality
	// assertion below is only valid when both granules' inner loops pick
	// the same global_gain, which identical spectra plus identical pinned
	// states guarantee (the same finding class as TestEncScalefacReadback's
	// ggDelta term, handled here by construction instead of correction).
	xr[1][0] = xr[0][0]
	pin := enc.ScfPin{Scf: [21]int{0: 2, 3: 1, 7: 3, 12: 4, 18: 2}}
	pins := &[2][2]enc.ScfPin{}
	pins[0][0], pins[1][0] = pin, pin
	frame := enc.AppendFrameScfPin(nil, 9, 0, 0, 3, &xr, pins, true, 1)
	gr, gains := readFrameScf(t, frame, 1)
	if gr[1][0].scfsi == 0 {
		t.Fatal("identical granule states: scfsi not detected")
	}
	for sfb := range 21 {
		if gains[1][0][sfb] != gains[0][0][sfb] {
			t.Fatalf("sfb %d: granule 1 gain %v != granule 0 %v under scfsi",
				sfb, gains[1][0][sfb], gains[0][0][sfb])
		}
	}
}
