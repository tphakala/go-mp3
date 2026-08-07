package dec

// shortBlockType mirrors upstream SHORT_BLOCK_TYPE (tools/oracle/minimp3.h:57).
const shortBlockType = 2

// maxBitreservoirBytes mirrors upstream MAX_BITRESERVOIR_BYTES
// (tools/oracle/minimp3.h:56): the largest number of trailing main-data
// bytes l3SaveReservoir carries into the next frame.
const maxBitreservoirBytes = 511

// grInfo mirrors upstream L3_gr_info_t (tools/oracle/minimp3.h:223-230):
// per-granule-channel side info parsed by l3ReadSideInfo and consumed by
// l3ReadScalefactors (and, from Task 7, l3Huffman).
type grInfo struct {
	sfbTab                  []uint8
	part23Length, bigValues uint16
	scalefacCompress        uint16
	globalGain, blockType   uint8
	mixedBlockFlag          uint8
	nLongSfb, nShortSfb     uint8
	tableSelect             [3]uint8
	regionCount             [3]uint8
	subblockGain            [3]uint8
	preflag, scalefacScale  uint8
	count1Table, scfsi      uint8
}

// reservoir mirrors the persistent bit-reservoir fields of upstream
// mp3dec_t (tools/oracle/minimp3.h:18-23: reserv, reserv_buf[511]). It is
// the slice of frame-to-frame decoder state this task needs, since
// l3ReadScalefactors's bit positions only match the oracle when the
// main-data bitstream is assembled through the same reservoir carry-over.
// The rest of mp3dec_t's persistent state (header, free_format_bytes,
// mdct_overlap, qmf_state) belongs to whichever task ports the full
// stateful frame loop.
type reservoir struct {
	buf [maxBitreservoirBytes]byte
	n   int
}
