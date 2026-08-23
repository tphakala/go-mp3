package enc

import "github.com/tphakala/go-mp3/internal/bits"

// writeSideInfo packs the side-info block for one frame: main_data_begin
// (9 bits, from mainDataBegin), private_bits=0, scfsi per channel (4
// bits, from gr[1][ch].scfsi: scfsi is a per-channel field describing
// granule 1's reuse of granule 0's scalefactor groups, granule 0 never
// masks anything), then for granule 0..1 and channel 0..nch-1 the
// granule-channel fields from gr[g][ch]: part2_3_length(12),
// big_values(9), global_gain(8), scalefac_compress(4, from
// gc.scfCompress), then either the long-block fields (design decision 5's
// flag=0 branch, unchanged from before this task) or the window-switching
// fields (flag=1, gc.blockType != blockLong): window_switching_flag(1)=1,
// block_type(2, from gc.blockType), mixed_block_flag(1)=0 (mixed blocks
// are never emitted, ISO 2.4.1.7's third window-switching flavor is out
// of this task's scope), table_select[0..1](5 each, from
// gc.ri.tableSelect[0:2]; table_select[2] is never transmitted - the
// decoder derives regionCount and never reads a third table for a
// window-switching granule, internal/dec/sideinfo.go:138-153),
// subblock_gain[0..2](3 each, from gc.sf.subblockGain). Both branches
// finish with preflag(1, from gc.sf.preflag; always 0 for a short granule
// per gc.sf's own convention, but the MPEG-1 bit is still physically
// transmitted - design decision 2, verified against
// l3ReadSideInfo's unconditional read under hdrTestMPEG1), scalefac_scale
// (1, from gc.sf.scalefacScale), count1table_select(1). This is MPEG-1
// write order, mirroring l3ReadSideInfo's read order
// (internal/dec/sideinfo.go:69).
//
// Bits packed: 9 + privateBits(5 mono/3 stereo) + 4*nch + 2*nch*59 = 136
// (mono) or 256 (stereo), matching sideInfoBits. Both per-granule-channel
// branches total exactly 59 bits (12+9+8+4=33 shared, then 23 bits of
// window-switching-or-not fields, then 3 bits shared tail), so a mix of
// long and window-switching granules in the same frame never changes the
// frame's side-info length.
func writeSideInfo(w *bits.Writer, gr *[2][2]granuleCoding, nch, mainDataBegin int) {
	w.WriteBits(uint32(mainDataBegin), 9) // main_data_begin

	privateBits := 5
	if nch == 2 {
		privateBits = 3
	}
	w.WriteBits(0, privateBits)
	for ch := range nch {
		w.WriteBits(uint32(gr[1][ch].scfsi), 4)
	}

	for g := range 2 {
		for ch := range nch {
			gc := &gr[g][ch]
			w.WriteBits(uint32(gc.part23Length), 12)
			w.WriteBits(uint32(gc.part.bigValues), 9)
			w.WriteBits(uint32(gc.globalGain), 8)
			w.WriteBits(uint32(gc.scfCompress), 4)
			if gc.blockType != blockLong {
				w.WriteBits(1, 1) // window_switching_flag
				w.WriteBits(uint32(gc.blockType), 2)
				w.WriteBits(0, 1) // mixed_block_flag: never emitted (mixed blocks out of scope)
				w.WriteBits(uint32(gc.ri.tableSelect[0]), 5)
				w.WriteBits(uint32(gc.ri.tableSelect[1]), 5)
				w.WriteBits(uint32(gc.sf.subblockGain[0]), 3)
				w.WriteBits(uint32(gc.sf.subblockGain[1]), 3)
				w.WriteBits(uint32(gc.sf.subblockGain[2]), 3)
			} else {
				w.WriteBits(0, 1) // window_switching_flag
				for _, t := range gc.ri.tableSelect {
					w.WriteBits(uint32(t), 5)
				}
				w.WriteBits(uint32(gc.ri.region0Count), 4)
				w.WriteBits(uint32(gc.ri.region1Count), 3)
			}
			w.WriteBits(uint32(gc.sf.preflag), 1)
			w.WriteBits(uint32(gc.sf.scalefacScale), 1)
			w.WriteBits(uint32(gc.ri.count1Table), 1)
		}
	}
}
