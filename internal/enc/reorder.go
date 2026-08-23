package enc

// reorderShort maps a short granule's MDCT-order spectrum into coding
// order: the layout the bitstream actually carries for a short block
// (ISO/IEC 11172-3 2.4.2.7). MDCTGranuleBlock(blockShort) (mdct.go)
// produces src window-major within each subband: src[b*18+w*6+k] holds
// window w's k-th of 6 lines in subband b (b in 0..31, w in 0..2, k in
// 0..5), so window w's full 192-line sequence is X_w[6b+k] = src[b*18+w*6+k].
//
// Coding order groups by scalefactor band instead of by subband: for
// short sfb s at cumulative frequency F (sum of the preceding widths) and
// width W = widths[s], the three windows' W-line slices sit back to back:
// dst[3F+0*W : 3F+1*W) = X_0[F:F+W), dst[3F+1*W : 3F+2*W) = X_1[F:F+W),
// dst[3F+2*W : 3F+3*W) = X_2[F:F+W).
//
// This is a pure index copy (no arithmetic feeding a +/-), so it carries
// no FMA surface.
func reorderShort(src, dst *[576]float64, widths *[13]int) {
	freq := 0
	for _, w := range widths {
		for win := range 3 {
			base := 3*freq + win*w
			for i := range w {
				f := freq + i
				b, k := f/6, f%6
				dst[base+i] = src[b*18+win*6+k]
			}
		}
		freq += w
	}
}

// ReorderShort exposes reorderShort to internal/dec's white-box inverse
// gate (encx_reorder_test.go), the sanctioned cross-package test
// exception (see doc.go and huffman.go's identical "Test-only
// cross-package surface" precedent): the gate needs to drive reorderShort
// directly and feed its output through the decoder's unexported l3Reorder
// to check the round trip.
func ReorderShort(src, dst *[576]float64, widths *[13]int) { reorderShort(src, dst, widths) }
