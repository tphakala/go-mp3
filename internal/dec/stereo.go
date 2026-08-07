package dec

// hdrTestMsStereo mirrors upstream HDR_TEST_MS_STEREO
// (tools/oracle/minimp3.h:70). Defined here rather than header.go because
// this task's commit is scoped to stereo.go/reorder.go/tables.go; compare
// hdrIsMsStereo (scalefactors.go), which mirrors the stricter HDR_IS_MS_STEREO
// (channel mode itself must be joint stereo, not just the extension bit).
func hdrTestMsStereo(hdr []byte) bool { return hdr[3]&0x20 != 0 }

// l3StereoProcess mirrors the stereo dispatch inlined in upstream
// L3_decode (tools/oracle/minimp3.h:1249-1255): intensity stereo when the
// header's mode-extension I_STEREO bit is set, mid/side stereo when the
// header is full MS stereo, otherwise no cross-channel processing at all.
//
// left and right are the granule's already-Huffman-decoded 576-float
// spectra for channel 0 and channel 1 (upstream's s->grbuf[0] and
// s->grbuf[1], contiguous there so L3_midside_stereo/L3_intensity_stereo
// reach the right channel via left+576; independent slices here since Go
// has no such aliasing). istPos is the persisted intensity-position array
// for channel index 1 (upstream's s->ist_pos[1], 39 bytes, carried across
// granules/frames by the caller). gr is the two grInfo values for this
// granule, gr[0] for channel 0 and gr[1] for channel 1 (upstream's
// gr_info pointer for this granule, indexed gr_info[0]/gr_info[1] by
// L3_intensity_stereo).
func l3StereoProcess(left, right []float32, hdr []byte, istPos []uint8, gr []grInfo) {
	switch {
	case hdrTestIStereo(hdr):
		l3IntensityStereo(left, right, istPos, gr, hdr)
	case hdrIsMsStereo(hdr):
		l3MidsideStereo(left, right, 576)
	}
}

// l3MidsideStereo mirrors upstream L3_midside_stereo
// (tools/oracle/minimp3.h:879-909; only the trailing scalar loop applies,
// since this port targets MINIMP3_NO_SIMD per the oracle build flags).
// left and right are independent 576-float slices mirroring grbuf[0] and
// grbuf[1] (contiguous there, separate here); n is the sample count to
// mix (always 576 at l3StereoProcess's call site, mirroring upstream's
// single call site tools/oracle/minimp3.h:1254).
//
// Float discipline: a+b and a-b are plain float32 add/sub with no
// multiply involved, so there is no a*b+c fusion site here.
func l3MidsideStereo(left, right []float32, n int) {
	for i := range n {
		a := left[i]
		b := right[i]
		left[i] = a + b
		right[i] = a - b
	}
}

// l3IntensityStereoBand mirrors upstream L3_intensity_stereo_band
// (tools/oracle/minimp3.h:911-919, scalar path). left/right are slices
// already offset to the current scalefactor band's first sample
// (mirroring upstream's left pointer advancing through L3_stereo_process,
// with the right channel tracked there as left+576 and here as an
// independent slice at the same offset); n is sfb[i] for that band.
//
// Float discipline: each output is a single float32*float32 product with
// no addition, so there is no a*b+c fusion site. right[i] is computed
// from left[i] before left[i] is overwritten, matching upstream's
// statement order exactly (needed there only because it writes through
// the same underlying array; kept here for faithful order even though
// left and right are independent slices).
func l3IntensityStereoBand(left, right []float32, n int, kl, kr float32) {
	for i := range n {
		right[i] = left[i] * kr
		left[i] *= kl
	}
}

// l3StereoTopBand mirrors upstream L3_stereo_top_band
// (tools/oracle/minimp3.h:921-939): for each of nbands scalefactor bands,
// records in maxBand[i%3] the highest band index (per one of 3 short-block
// windows) whose right-channel coefficients are not all zero, leaving -1
// where no such band exists. right is the full (offset-0) right-channel
// spectrum (upstream's left+576 at its L3_intensity_stereo call site,
// itself grbuf[0]+576 there, i.e. grbuf[1] here); sfb is gr.sfbTab.
func l3StereoTopBand(right []float32, sfb []uint8, nbands int, maxBand *[3]int) {
	maxBand[0], maxBand[1], maxBand[2] = -1, -1, -1

	off := 0
	for i := range nbands {
		n := int(sfb[i])
		for k := 0; k < n; k += 2 {
			if right[off+k] != 0 || right[off+k+1] != 0 {
				maxBand[i%3] = i
				break
			}
		}
		off += n
	}
}

// l3StereoProcessBands mirrors upstream L3_stereo_process
// (tools/oracle/minimp3.h:941-973): walks left/right's scalefactor bands
// (sfb, zero-terminated) and, per band, applies intensity stereo when the
// band lies beyond max_band's top nonzero band and the persisted
// intensity position is below maxPos, mid/side stereo when the header is
// MS stereo, or leaves the band untouched otherwise. Named
// l3StereoProcessBands (not l3StereoProcess) because that name is taken
// by this task's required dispatcher (the brief's sketched signature),
// which mirrors a different call site (the inline if/else in L3_decode)
// than this function (L3_intensity_stereo's single call site,
// tools/oracle/minimp3.h:992).
//
// Float discipline: kl*s and kr*s are each a plain float32*float32
// product consumed only by l3IntensityStereoBand's own product (no
// addition anywhere in this function), so there is no a*b+c fusion site.
// The literal 1.41421356 is f-suffixed in C (a genuine float32 literal,
// not a double); Go's untyped constant, assigned directly to the float32
// variable s, rounds to the identical float32 value with no double
// promotion.
func l3StereoProcessBands(left, right []float32, istPos, sfb []uint8, hdr []byte, maxBand *[3]int, mpeg2Sh int) {
	maxPos := 64
	if hdrTestMPEG1(hdr) {
		maxPos = 7
	}

	off := 0
	for i := 0; sfb[i] != 0; i++ {
		n := int(sfb[i])
		ipos := int(istPos[i])

		switch {
		case i > maxBand[i%3] && ipos < maxPos:
			s := float32(1)
			if hdrTestMsStereo(hdr) {
				s = 1.41421356
			}
			var kl, kr float32
			if hdrTestMPEG1(hdr) {
				kl = gPan[2*ipos]
				kr = gPan[2*ipos+1]
			} else {
				kl = 1
				kr = l3LdexpQ2(1, (ipos+1)>>1<<mpeg2Sh)
				if ipos&1 != 0 {
					kl, kr = kr, 1
				}
			}
			l3IntensityStereoBand(left[off:off+n], right[off:off+n], n, kl*s, kr*s)
		case hdrTestMsStereo(hdr):
			l3MidsideStereo(left[off:off+n], right[off:off+n], n)
		}

		off += n
	}
}

// l3IntensityStereo mirrors upstream L3_intensity_stereo
// (tools/oracle/minimp3.h:975-993): derives each block's persisted
// intensity position from the top nonzero right-channel band per third
// (via l3StereoTopBand), then dispatches to l3StereoProcessBands.
//
// left/right are the granule's channel-0/channel-1 spectra (grbuf[0]/
// grbuf[1] upstream, independent slices here); istPos is ist_pos[1]
// upstream (persisted across granules/frames for channel index 1); gr is
// the two grInfo values for this granule: gr[0] supplies sfbTab/
// nLongSfb/nShortSfb (upstream's gr->... where gr is the same gr_info
// pointer, i.e. gr_info[0]), and gr[1].scalefacCompress supplies the
// MPEG-2 shift bit (upstream's gr[1].scalefac_compress, i.e.
// gr_info[1], the granule's channel-1 grInfo).
func l3IntensityStereo(left, right []float32, istPos []uint8, gr []grInfo, hdr []byte) {
	g0 := &gr[0]
	nSfb := int(g0.nLongSfb) + int(g0.nShortSfb)
	maxBlocks := 1
	if g0.nShortSfb != 0 {
		maxBlocks = 3
	}

	var maxBand [3]int
	l3StereoTopBand(right, g0.sfbTab, nSfb, &maxBand)
	if g0.nLongSfb != 0 {
		m := max(maxBand[0], maxBand[1], maxBand[2])
		maxBand[0], maxBand[1], maxBand[2] = m, m, m
	}
	for i := range maxBlocks {
		defaultPos := 0
		if hdrTestMPEG1(hdr) {
			defaultPos = 3
		}
		itop := nSfb - maxBlocks + i
		prev := itop - maxBlocks
		if maxBand[i] >= prev {
			istPos[itop] = uint8(defaultPos)
		} else {
			istPos[itop] = istPos[prev]
		}
	}

	mpeg2Sh := int(gr[1].scalefacCompress) & 1
	l3StereoProcessBands(left, right, istPos, g0.sfbTab, hdr, &maxBand, mpeg2Sh)
}

// l3Antialias mirrors upstream L3_antialias (tools/oracle/minimp3.h:1012-1045;
// only the trailing scalar loop applies, since this port targets
// MINIMP3_NO_SIMD per the oracle build flags): butterflies the 8 samples
// straddling each of nbands subband boundaries (18 floats apart) against
// the gAA coefficients. A negative or zero nbands runs zero iterations,
// mirroring upstream's `for (; nbands > 0; ...)` (the caller can compute
// aa_bands as n_long_bands-1, which is negative when n_long_bands is 0).
//
// Float discipline: grbuf[18+i] = u*g_aa[0][i] - d*g_aa[1][i] and
// grbuf[17-i] = u*g_aa[1][i] + d*g_aa[0][i] are each a difference/sum of
// two independent float32 products, not a single a*b+c fusion site, but
// the same arm64-fusion risk applies to product-pair patterns (the
// hardware can fuse the second multiply into an FMA against the first
// product): each product is bound to its own named float32 variable
// before combining, so the combine is a separate rounding step from
// either multiply, matching unfused upstream semantics exactly.
func l3Antialias(grbuf []float32, nbands int) {
	for b := range nbands {
		g := grbuf[b*18:]
		for i := range 8 {
			u := g[18+i]
			d := g[17-i]
			up0 := u * gAA[0][i]
			dp1 := d * gAA[1][i]
			up1 := u * gAA[1][i]
			dp0 := d * gAA[0][i]
			g[18+i] = up0 - dp1
			g[17-i] = up1 + dp0
		}
	}
}
