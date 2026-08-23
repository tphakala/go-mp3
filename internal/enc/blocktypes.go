package enc

// Block types (ISO/IEC 11172-3:1993, section 2.4.1.7, "block_type"): the
// granule's window shape. blockLong is the plain 36-point sine window used
// by a stationary granule; blockStart and blockStop bracket a run of
// blockShort granules, carrying the long window's rise (start) or fall
// (stop) so the overlap-add TDAC chain stays consistent across the
// transition; blockShort switches to three 12-point windows per subband for
// better time resolution around a transient. MDCTGranuleBlock (mdct.go)
// dispatches on these values; the decoder's l3ImdctGr (internal/dec/imdct.go)
// dispatches on the same four values via its own blockType parameter.
const (
	blockLong  = 0
	blockStart = 1
	blockShort = 2
	blockStop  = 3
)

// bandLayout describes one granule-channel's coding-order scalefactor-band
// geometry: how many bands there are, how wide each one is (in spectral
// lines), and which short window (if any) each band belongs to. A long
// granule (or a start/stop granule, which keeps the full long window for
// scalefactor purposes, ISO/IEC 11172-3 2.4.2.7) has 22 bands, each
// spanning the whole spectrum with no window association (win -1). A
// short granule has 39 bands: 13 short sfb's times the 3 windows each is
// split into, band b = 3*sfb + w carrying window w's width-sfbWidthsShort
// slice.
//
// nScf is narrower than nBands in both cases: per the scalefac_compress
// convention already established for long blocks (loop.go's sf.scf, len
// 21), the highest-indexed sfb of the long geometry (index 21) and,
// correspondingly, the highest-indexed short sfb across all three windows
// (bands 36..38) carry no explicit scalefactor. slen1End marks where the
// scalefac_compress slen1/slen2 split falls among the scalefactor-
// carrying bands: 11 long, 18 short (6 short sfb's times 3 windows).
type bandLayout struct {
	nBands   int      // 22 long, 39 short
	nScf     int      // 21 long, 36 short (bands carrying scalefactors)
	width    [39]int  // per coding band, in lines
	win      [39]int8 // -1 for long bands, else the short window 0..2
	slen1End int      // bands [0,slen1End) use slen1: 11 long, 18 short
	short    bool
}

// layoutLong[r] and layoutShort[r] are the long- and short-block
// bandLayouts for MPEG-1 rate r (0=44100, 1=48000, 2=32000), built once at
// init from sfbWidthsLong and sfbWidthsShort (integer-only, deterministic:
// no floating point or table lookups feed these loops).
var layoutLong, layoutShort [3]bandLayout

func init() {
	for r := range 3 {
		long := &layoutLong[r]
		long.nBands = 22
		long.nScf = 21
		long.slen1End = 11
		for sfb := range 22 {
			long.width[sfb] = sfbWidthsLong[r][sfb]
			long.win[sfb] = -1
		}

		short := &layoutShort[r]
		short.nBands = 39
		short.nScf = 36
		short.slen1End = 18
		short.short = true
		for sfb := range 13 {
			w := sfbWidthsShort[r][sfb]
			for win := range 3 {
				b := 3*sfb + win
				short.width[b] = w
				short.win[b] = int8(win)
			}
		}
	}
}

// layoutFor returns the coding-order band geometry for a granule of the
// given block type at sample-rate index srIndex: layoutShort for
// blockShort, layoutLong for everything else (blockLong and the
// start/stop transition types, which carry the full long-window
// scalefactor geometry, ISO/IEC 11172-3 2.4.2.7).
func layoutFor(blockType, srIndex int) *bandLayout {
	if blockType == blockShort {
		return &layoutShort[srIndex]
	}
	return &layoutLong[srIndex]
}
