package enc

import "github.com/tphakala/go-mp3/internal/bits"

// writeSideInfo packs the side-info block for one frame: main_data_begin=0,
// private_bits=0, scfsi=0 per channel, then for granule 0..1 and channel
// 0..nch-1 the granule-channel fields from gr[g][ch]: part2_3_length(12),
// big_values(9), global_gain(8), scalefac_compress(4)=0,
// window_switching_flag(1)=0, table_select[0..2](5 each), region0_count(4),
// region1_count(3), preflag(1)=0, scalefac_scale(1)=0,
// count1table_select(1). This is MPEG-1 write order, mirroring
// l3ReadSideInfo's read order (internal/dec/sideinfo.go:69). Phase 3 never
// derives scalefactors, so every *=0 field above is always literally 0: no
// scalefactor-band reuse (scfsi), no HF-boost amplification (preflag), and
// no doubled scalefactor step (scalefac_scale).
//
// Bits packed: 9 + privateBits(5 mono/3 stereo) + 4*nch + 2*nch*59 = 136
// (mono) or 256 (stereo), matching sideInfoBits.
func writeSideInfo(w *bits.Writer, gr *[2][2]granuleCoding, nch int) {
	w.WriteBits(0, 9) // main_data_begin

	privateBits := 5
	if nch == 2 {
		privateBits = 3
	}
	w.WriteBits(0, privateBits)
	for range nch {
		w.WriteBits(0, 4) // scfsi
	}

	for g := range 2 {
		for ch := range nch {
			gc := &gr[g][ch]
			w.WriteBits(uint32(gc.part23Length), 12)
			w.WriteBits(uint32(gc.part.bigValues), 9)
			w.WriteBits(uint32(gc.globalGain), 8)
			w.WriteBits(0, 4) // scalefac_compress
			w.WriteBits(0, 1) // window_switching_flag
			for _, t := range gc.ri.tableSelect {
				w.WriteBits(uint32(t), 5)
			}
			w.WriteBits(uint32(gc.ri.region0Count), 4)
			w.WriteBits(uint32(gc.ri.region1Count), 3)
			w.WriteBits(0, 1) // preflag
			w.WriteBits(0, 1) // scalefac_scale
			w.WriteBits(uint32(gc.ri.count1Table), 1)
		}
	}
}
