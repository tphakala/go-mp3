package dec

// maxSamplesPerFrame mirrors upstream MINIMP3_MAX_SAMPLES_PER_FRAME
// (tools/oracle/minimp3.h:11): 1152 samples/channel * 2 channels, the
// largest interleaved-float PCM one DecodeFrame call can emit.
const maxSamplesPerFrame = 1152 * 2

// maxL3FramePayloadBytes mirrors upstream MAX_L3_FRAME_PAYLOAD_BYTES
// (tools/oracle/minimp3.h:54), which the pin defines equal to
// MAX_FREE_FORMAT_FRAME_SIZE. It bounds the current frame's main-data
// bytes that l3RestoreReservoir appends after the reservoir tail.
const maxL3FramePayloadBytes = maxFreeFormatFrameSize

// FrameInfo mirrors upstream mp3dec_frame_info_t
// (tools/oracle/minimp3.h:13-16). DecodeFrame fills it with the same
// semantics as mp3dec_decode_frame: FrameBytes is the offset to the frame
// plus the frame size (i + frame_size upstream, so it already includes
// FrameOffset), FrameOffset is the number of bytes skipped before the
// frame's header, and the rest describe the decoded frame's format.
type FrameInfo struct {
	FrameBytes, FrameOffset, Channels, SampleRateHz, Layer, BitrateKbps int
}

// mp3Scratch mirrors upstream mp3dec_scratch_t
// (tools/oracle/minimp3.h:232-239): the per-decode working buffers. Upstream
// declares one on the stack per mp3dec_decode_frame call; here it is a field
// of Decoder, reused across calls to stay allocation-free in steady state.
// Every field is written before it is read within a single frame's decode
// (grbuf is cleared per granule, maindata/scf/syn are overwritten in place,
// istPos is written by l3ReadScalefactors before l3StereoProcess reads it),
// so a persistent buffer produces byte-identical output to upstream's fresh
// stack scratch, which is itself deterministic only because of that same
// write-before-read discipline.
type mp3Scratch struct {
	maindata [maxBitreservoirBytes + maxL3FramePayloadBytes]byte
	grInfo   [4]grInfo
	grbuf    [2 * 576]float32
	scf      [40]float32
	syn      [(18 + 15) * 2 * 32]float32
	istPos   [2][39]uint8
}

// Decoder mirrors upstream mp3dec_t (tools/oracle/minimp3.h:18-23): the
// persistent decoder state carried from one frame to the next. mdctOverlap
// is the hybrid-IMDCT overlap-add memory (mdct_overlap[2][9*32]), qmfState
// the synthesis filterbank memory (qmf_state[15*2*32]), res the bit
// reservoir (reserv + reserv_buf[511]), header the cached header for the
// fast-path (header[4]), and freeFormatBytes the sticky free-format frame
// size (free_format_bytes). scratch is the working set (see mp3Scratch),
// which upstream keeps on the stack rather than in mp3dec_t.
type Decoder struct {
	mdctOverlap     [2][288]float32
	qmfState        [960]float32
	res             reservoir
	header          [4]byte
	freeFormatBytes int
	scratch         mp3Scratch
}

// NewDecoder returns a zero-valued Decoder, mirroring the oracle's
// `mp3dec_t dec = {0}; mp3dec_init(&dec);` (tools/oracle/mp3dump.c:228-229):
// Go zero-initializes every field, and mp3dec_init only clears header[0],
// which is already zero.
func NewDecoder() *Decoder {
	d := &Decoder{}
	d.initState()
	return d
}

// initState mirrors upstream mp3dec_init (tools/oracle/minimp3.h:1708-1711),
// which clears only header[0] so the next DecodeFrame skips the fast path
// and resyncs from scratch.
func (d *Decoder) initState() {
	d.header[0] = 0
}

// reset mirrors the `memset(dec, 0, sizeof(mp3dec_t))` in mp3dec_decode_frame
// (tools/oracle/minimp3.h:1730): when the fast-path header check misses, the
// whole persistent state is zeroed before resyncing. It clears exactly the
// mp3dec_t fields (not scratch, which is not part of mp3dec_t and is
// rewritten each frame).
func (d *Decoder) reset() {
	d.mdctOverlap = [2][288]float32{}
	d.qmfState = [960]float32{}
	d.res = reservoir{}
	d.header = [4]byte{}
	d.freeFormatBytes = 0
}
