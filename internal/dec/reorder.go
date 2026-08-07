package dec

// l3Reorder mirrors upstream L3_reorder (tools/oracle/minimp3.h:995-1010):
// de-interleaves a mixed/short block's three interleaved subwindow
// sequences (window 0, window 1, window 2 samples grouped by window
// rather than by frequency) back into per-window contiguous order.
//
// grbuf is already offset to the short-block region (mirrors upstream's
// call-site offset s->grbuf[ch] + n_long_bands*18,
// tools/oracle/minimp3.h:1265); the caller is responsible for that
// offset and for gating the call on gr.nShortSfb != 0, exactly mirroring
// the call site. scratch must have room for at least as many floats as
// this call ends up writing (mirrors upstream reusing s->syn[0] as
// scratch space; its contents beyond what this function writes are
// irrelevant since the final copy-back only touches the floats actually
// produced). gr.sfbTab[gr.nLongSfb:] mirrors the call site's
// gr_info->sfbtab + gr_info->n_long_sfb (a zero-terminated sequence of
// 3-tuples, one window width per short scalefactor band).
func l3Reorder(grbuf, scratch []float32, gr *grInfo) {
	sfb := gr.sfbTab[gr.nLongSfb:]
	srcOff, dstOff := 0, 0

	for sfb[0] != 0 {
		length := int(sfb[0])
		for i := range length {
			scratch[dstOff+0] = grbuf[srcOff+i]
			scratch[dstOff+1] = grbuf[srcOff+length+i]
			scratch[dstOff+2] = grbuf[srcOff+2*length+i]
			dstOff += 3
		}
		srcOff += 3 * length
		sfb = sfb[3:]
	}

	copy(grbuf[:dstOff], scratch[:dstOff])
}
