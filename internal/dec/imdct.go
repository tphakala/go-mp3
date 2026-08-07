package dec

// stopBlockType mirrors upstream STOP_BLOCK_TYPE (tools/oracle/minimp3.h:58).
// shortBlockType (types.go) is SHORT_BLOCK_TYPE; this constant lives here
// rather than types.go because this task's commit is scoped to imdct.go.
const stopBlockType = 3

// l3Dct3_9 mirrors upstream L3_dct3_9 (tools/oracle/minimp3.h:1047-1085): an
// in-place 9-point DCT-III applied twice per l3Imdct36 call, once for the
// "cosine" half (co) and once for the "sine" half (si) of the transform.
//
// Float discipline: every literal here is f-suffixed (float32), so there is
// no double promotion. Two lines have the a*b+c / a*b-c shape arm64 can
// fuse into a single FMA instruction: `t0 = s0 + s6*0.5f` and
// `s2 = s0 - s4*0.5f`. Each product is bound to its own named variable
// (half6, half4) before combining, so the combine is a separate rounding
// step from the multiply, matching unfused upstream semantics exactly.
// Every other multiply here stands alone (immediately assigned, e.g.
// `s3 *= 0.86602540f`, or preceded by a plain add/sub with no multiply
// involved, e.g. `(s4 + s2)*0.93969262f`), so no other site needs
// splitting.
func l3Dct3_9(y []float32) {
	var s0, s1, s2, s3, s4, s5, s6, s7, s8, t0, t2, t4 float32

	s0, s2, s4, s6, s8 = y[0], y[2], y[4], y[6], y[8]
	half6 := s6 * 0.5
	t0 = s0 + half6
	s0 -= s6
	t4 = (s4 + s2) * 0.93969262
	t2 = (s8 + s2) * 0.76604444
	s6 = (s4 - s8) * 0.17364818
	s4 += s8 - s2

	half4 := s4 * 0.5
	s2 = s0 - half4
	y[4] = s4 + s0
	s8 = t0 - t2 + s6
	s0 = t0 - t4 + t2
	s4 = t0 + t4 - s6

	s1, s3, s5, s7 = y[1], y[3], y[5], y[7]

	s3 *= 0.86602540
	t0 = (s5 + s1) * 0.98480775
	t4 = (s5 - s7) * 0.34202014
	t2 = (s1 + s7) * 0.64278761
	s1 = (s1 - s5 - s7) * 0.86602540

	s5 = t0 - s3 - t2
	s7 = t4 - s3 - t0
	s3 = t4 + s3 - t2

	y[0] = s4 - s7
	y[1] = s2 + s1
	y[2] = s0 - s3
	y[3] = s8 + s5
	y[5] = s8 - s5
	y[6] = s0 + s3
	y[7] = s2 - s1
	y[8] = s4 + s7
}

// l3Idct3 mirrors upstream L3_idct3 (tools/oracle/minimp3.h:1144-1151): a
// 3-point IDCT, the butterfly l3Imdct12 runs twice per call (once for co,
// once for si).
//
// Float discipline: `m1 = x1*0.86602540f` is a single multiply, immediately
// assigned, so there is no combine step to fuse. `a1 = x0 - x2*0.5f` is the
// one a*b+c shape here (c - a*b); its product is bound to half before
// combining, matching the pattern used throughout this package. Both
// literals are f-suffixed, so there is no double promotion.
func l3Idct3(x0, x1, x2 float32, dst []float32) {
	m1 := x1 * 0.86602540
	half := x2 * 0.5
	a1 := x0 - half
	dst[1] = x0 + x2
	dst[0] = a1 + m1
	dst[2] = a1 - m1
}

// l3Imdct12 mirrors upstream L3_imdct12 (tools/oracle/minimp3.h:1153-1171):
// a 12-point IMDCT built from two l3Idct3 calls plus a windowed
// overlap-add against g_twid3, called three times per short block by
// l3ImdctShort (once per interleaved sub-window). x, dst and overlap alias
// into the caller's buffers exactly as upstream's pointer arithmetic does
// (see l3ImdctShort's doc comment); x is read at indices 0, 3, 6, 9, 12 and
// 15 only, mirroring the pin's own strided access through its incoming
// pointer, so x needs no length beyond that.
//
// Float discipline: every gTwid3 entry is f-suffixed, so no double
// promotion. All four assignments in the loop (sum, overlap[i], dst[i],
// dst[5-i]) are a sum or difference of two independent float32 products
// (the same product-pair pattern as l3Antialias in stereo.go): each
// product is bound to its own named variable before combining, so arm64
// cannot fuse either combine step into an FMA against either multiply.
func l3Imdct12(x, dst, overlap []float32) {
	var co, si [3]float32

	l3Idct3(-x[0], x[6]+x[3], x[12]+x[9], co[:])
	l3Idct3(x[15], x[12]-x[9], x[6]-x[3], si[:])
	si[1] = -si[1]

	for i := range 3 {
		ovl := overlap[i]

		p0 := co[i] * gTwid3[3+i]
		p1 := si[i] * gTwid3[0+i]
		sum := p0 + p1

		p2 := co[i] * gTwid3[0+i]
		p3 := si[i] * gTwid3[3+i]
		overlap[i] = p2 - p3

		p4 := ovl * gTwid3[2-i]
		p5 := sum * gTwid3[5-i]
		dst[i] = p4 - p5

		p6 := ovl * gTwid3[5-i]
		p7 := sum * gTwid3[2-i]
		dst[5-i] = p6 + p7
	}
}

// l3ImdctShort mirrors upstream L3_imdct_short (tools/oracle/minimp3.h:1173-1184):
// runs l3Imdct12 across nbands short blocks (18 spectral floats and 9
// overlap floats each), decimating each block's 18 samples into three
// interleaved 6-sample sequences (upstream's tmp, tmp+1, tmp+2 strided
// pointers; here tmp[0:], tmp[1:], tmp[2:] slice views read at the same
// strided indices inside l3Imdct12). tmp holds a copy of grbuf's 18
// samples before grbuf is overwritten (upstream's
// `memcpy(tmp, grbuf, sizeof(tmp))`); grbuf's first 6 samples are then
// seeded from the incoming overlap state (upstream's
// `memcpy(grbuf, overlap, 6*sizeof(float))`) before the three l3Imdct12
// calls write grbuf[6:12], grbuf[12:18] and overlap[0:6] respectively, all
// reading and updating the shared overlap[6:9] accumulator in sequence,
// exactly mirroring upstream's pointer aliasing.
func l3ImdctShort(grbuf, overlap []float32, nbands int) {
	for range nbands {
		var tmp [18]float32
		copy(tmp[:], grbuf[:18])
		copy(grbuf[:6], overlap[:6])

		l3Imdct12(tmp[0:], grbuf[6:], overlap[6:])
		l3Imdct12(tmp[1:], grbuf[12:], overlap[6:])
		l3Imdct12(tmp[2:], overlap[0:], overlap[6:])

		grbuf = grbuf[18:]
		overlap = overlap[9:]
	}
}

// l3Imdct36 mirrors upstream L3_imdct36 (tools/oracle/minimp3.h:1087-1142;
// only the trailing scalar loop applies, since this port targets
// MINIMP3_NO_SIMD per the oracle build flags): a 36-point IMDCT applied to
// nbands long (or long/stop) blocks, windowed and overlap-added into grbuf
// via window (the caller's choice of gMdctWindow[0] or
// gMdctWindow[block_type == stopBlockType], per l3ImdctGr).
//
// Float discipline: every gTwid9 entry is f-suffixed, so no double
// promotion (window's entries are likewise float32, sourced from
// gMdctWindow). The four assignments derived from co[i]/si[i] products
// (sum, overlap[i], grbuf[i], grbuf[17-i]) are each a sum or difference of
// two independent float32 products, the same pattern as l3Antialias: each
// product is bound to its own named variable before combining, blocking
// arm64 FMA fusion of the combine step against either multiply.
func l3Imdct36(grbuf, overlap, window []float32, nbands int) {
	for range nbands {
		var co, si [9]float32
		co[0] = -grbuf[0]
		si[0] = grbuf[17]
		for i := range 4 {
			si[8-2*i] = grbuf[4*i+1] - grbuf[4*i+2]
			co[1+2*i] = grbuf[4*i+1] + grbuf[4*i+2]
			si[7-2*i] = grbuf[4*i+4] - grbuf[4*i+3]
			co[2+2*i] = -(grbuf[4*i+3] + grbuf[4*i+4])
		}
		l3Dct3_9(co[:])
		l3Dct3_9(si[:])

		si[1] = -si[1]
		si[3] = -si[3]
		si[5] = -si[5]
		si[7] = -si[7]

		for i := range 9 {
			ovl := overlap[i]

			p0 := co[i] * gTwid9[9+i]
			p1 := si[i] * gTwid9[0+i]
			sum := p0 + p1

			p2 := co[i] * gTwid9[0+i]
			p3 := si[i] * gTwid9[9+i]
			overlap[i] = p2 - p3

			p4 := ovl * window[0+i]
			p5 := sum * window[9+i]
			grbuf[i] = p4 - p5

			p6 := ovl * window[9+i]
			p7 := sum * window[0+i]
			grbuf[17-i] = p6 + p7
		}

		grbuf = grbuf[18:]
		overlap = overlap[9:]
	}
}

// l3ImdctGr mirrors upstream L3_imdct_gr (tools/oracle/minimp3.h:1194-1210):
// applies the hybrid IMDCT to a granule-channel's 576-float spectrum
// (grbuf) and its persisted 288-float overlap state (overlap, upstream's
// mdct_overlap[ch], carried across granules and frames by the caller),
// dispatching nLongBands long blocks through l3Imdct36 with the plain
// window and the remainder through either l3ImdctShort (short blocks) or
// l3Imdct36 again (long/start/stop blocks, selecting gMdctWindow[1] only
// for stop blocks).
//
// L3_change_sign, the pin function called immediately after L3_imdct_gr at
// its one call site (tools/oracle/minimp3.h:1300-1301), is out of scope
// here: this task's oracle hook (grbuf_post_imdct) is placed between the
// two calls specifically so the differential gate exercises l3ImdctGr's
// output in isolation; L3_change_sign belongs to whichever task ports the
// L3_decode granule loop that calls both in sequence.
func l3ImdctGr(grbuf, overlap []float32, blockType uint8, nLongBands int) {
	if nLongBands != 0 {
		l3Imdct36(grbuf, overlap, gMdctWindow[0][:], nLongBands)
		grbuf = grbuf[18*nLongBands:]
		overlap = overlap[9*nLongBands:]
	}
	if blockType == shortBlockType {
		l3ImdctShort(grbuf, overlap, 32-nLongBands)
	} else {
		winIdx := 0
		if blockType == stopBlockType {
			winIdx = 1
		}
		l3Imdct36(grbuf, overlap, gMdctWindow[winIdx][:], 32-nLongBands)
	}
}
