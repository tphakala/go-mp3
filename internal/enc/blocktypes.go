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
