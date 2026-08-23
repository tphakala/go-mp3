package dec

import (
	"fmt"
	"testing"

	"github.com/tphakala/go-mp3/internal/bits"
	"github.com/tphakala/go-mp3/internal/enc"
	"github.com/tphakala/go-mp3/internal/testsignal"
)

// validateFrames is a white-box test helper (package dec, not dec_test): it
// imports internal/enc, the sanctioned exception to "enc must never import
// dec" for _test.go files only (see PROVENANCE.md and internal/enc/doc.go).
//
// It re-parses a Phase 3 encoder byte stream with the decoder's own header
// and side-info parsers and asserts every emitted frame's structural
// invariants, then feeds the whole stream through the internal Decoder's
// frame loop (the same engine the public mp3.Decoder delegates to; a
// package dec test file cannot import the root mp3 package without an
// import cycle, since mp3 itself imports internal/dec) and requires every
// frame decodes cleanly. nFrames is the expected frame count.
//
// reservoirManaged distinguishes the two families of stream this validator
// sees: AppendFramePin/AppendFrameScfPin assemble each frame independently
// via assembleFrame with main_data_begin fixed at 0 (see assembleFrame's
// doc comment; those callers are not wired to the reservoir), so a
// multi-frame stream built from repeated AppendFramePin calls is not a
// genuine reservoir-managed stream even though it has many frames. Only
// streams produced by the real, stateful enc.Encoder actually carry
// main-data occupancy across frames, so only those pass reservoirManaged
// true and get the full reservoirReplay policy check; AppendFramePin-based
// streams pass false and keep the simpler mdb-must-be-0 invariant that has
// always held for them.
func validateFrames(t *testing.T, stream []byte, wantSampleRate, wantKbps, wantNch, nFrames int, reservoirManaged bool) {
	t.Helper()
	validateFrameHeaders(t, stream, wantSampleRate, wantKbps, wantNch, nFrames, reservoirManaged)
	validateFrameDecode(t, stream, nFrames)
}

// reservoirReplay independently re-derives the main-data geometry of the
// whole stream and checks every decodability invariant, R1 byte alignment
// included. Coordinates are bytes in the main-data stream (the
// concatenation of every frame's area bytes in order). It proves both
// NO-GAP placement and MINIMAL ancillary burn by recomputing the encoder's
// anti-overflow floor INDEPENDENTLY (agy finding folded: a spent-vs-derived-
// end comparison would be a tautology, since the only source for the
// previous frame's end IS the current frame's begin; the policy
// recomputation is what pins the placement).
type reservoirReplay struct {
	areaStart  int // current frame's area start in main-data coordinates
	prevBegin  int // previous frame's main-data begin
	prevPart23 int // previous frame's summed part2_3_length bits
	prevMdb    int // previous frame's main_data_begin
	prevArea   int // previous frame's area bytes
	havePrev   bool
	resCap     int // enc.ResCapBytesPin for this stream's rate/channels
}

// frame is called once per parsed frame. Frame n's spend is pinned by
// frame n+1's begin, so each call finalizes the PREVIOUS frame: its spend
// must equal EXACTLY max(byte-aligned need, anti-overflow floor), which
// simultaneously proves no-gap placement (no unexplained bytes) and
// minimal legally-required burn (every ancillary byte was forced by the
// occupancy cap).
func (rr *reservoirReplay) frame(t *testing.T, idx, mdb, part23SumBits, areaBytes int) {
	t.Helper()
	if mdb < 0 || mdb > 511 {
		t.Fatalf("frame %d: main_data_begin %d outside [0, 511]", idx, mdb)
	}
	begin := rr.areaStart - mdb
	if begin < 0 {
		t.Fatalf("frame %d: main data begins %d bytes before the stream", idx, -begin)
	}
	if rr.havePrev {
		spentPrev := begin - rr.prevBegin
		needPrev := (rr.prevPart23 + 7) / 8
		if spentPrev < needPrev {
			t.Fatalf("frame %d-1: spent %d bytes < byte-aligned need %d (R1 violation)",
				idx, spentPrev, needPrev)
		}
		// Independent policy replay: the previous frame's anti-overflow
		// floor from ITS occupancy (= its mdb) and area.
		loPrev := max(0, rr.prevMdb+rr.prevArea-rr.resCap)
		expectedSpend := max(needPrev, loPrev)
		if spentPrev != expectedSpend {
			t.Fatalf("frame %d-1: spent %d bytes, policy replay expects %d (need %d, lo %d): gap or excess burn",
				idx, spentPrev, expectedSpend, needPrev, loPrev)
		}
	}
	rr.prevBegin, rr.prevPart23, rr.prevMdb, rr.prevArea = begin, part23SumBits, mdb, areaBytes
	rr.havePrev = true
	rr.areaStart += areaBytes
}

// finish checks the final frame, whose spend no successor pins: its
// byte-aligned need must fit inside the stream's remaining area.
func (rr *reservoirReplay) finish(t *testing.T) {
	t.Helper()
	if rr.havePrev && rr.prevBegin+(rr.prevPart23+7)/8 > rr.areaStart {
		t.Fatalf("final frame: main data overruns the stream's area bytes")
	}
}

// frameModeExt returns header byte 3's mode (bits 7-6) and mode_extension
// (bits 5-4) fields. Shared by validateFrameHeaders and countModes
// (encx_mstereo_test.go), both in this package's test binary; internal/enc's
// countModes carries its own copy across the package boundary and cannot
// reuse this one.
func frameModeExt(h []byte) (mode, modeExt byte) {
	return (h[3] >> 6) & 3, (h[3] >> 4) & 3
}

// validateFrameHeaders walks stream frame by frame using only the decoder's
// header and side-info parsers (no decode), checking every header field,
// exact frame length, and every granule-channel's side-info invariants.
// When reservoirManaged, it also runs reservoirReplay over the whole
// stream's main-data placement and occupancy policy; otherwise it keeps the
// simpler per-frame mdb-must-be-0 check (see validateFrames' doc comment).
func validateFrameHeaders(t *testing.T, stream []byte, wantSampleRate, wantKbps, wantNch, nFrames int, reservoirManaged bool) {
	t.Helper()

	var rr reservoirReplay
	if reservoirManaged {
		rr = reservoirReplay{resCap: enc.ResCapBytesPin(wantKbps, wantSampleRate, wantNch)}
	}
	// prevBlockType[ch] tracks the last granule's block_type seen for
	// channel ch, across the WHOLE stream (frame boundaries included), for
	// the decision-10 transition grammar validateGranules enforces; -1
	// means "no previous granule yet". The very first granule is not
	// grammar-checked here because this validator also runs over isolated,
	// pin-constructed single-frame fixtures (TestEncShortValidatorGrammar)
	// that deliberately open on a short block. Real stream-start legality
	// (the first granule must be a legal successor of the decoder's implicit
	// initial long state, never a bare short) is guarded encoder-side by
	// TestEncoderFirstBlockLegal and TestBlockTypeForGrammarSimulation.
	prevBlockType := make([]int, wantNch)
	for i := range prevBlockType {
		prevBlockType[i] = -1
	}
	pos := 0
	frames := 0
	for pos < len(stream) {
		h := stream[pos:]
		if !hdrValid(h) {
			t.Fatalf("frame %d at byte %d: hdrValid = false", frames, pos)
		}
		if got := int(hdrBitrateKbps(h)); got != wantKbps {
			t.Fatalf("frame %d: hdrBitrateKbps = %d, want %d", frames, got, wantKbps)
		}
		if got := int(hdrSampleRateHz(h)); got != wantSampleRate {
			t.Fatalf("frame %d: hdrSampleRateHz = %d, want %d", frames, got, wantSampleRate)
		}

		mode, modeExt := frameModeExt(h)
		switch {
		case wantNch == 1:
			if mode != 3 {
				t.Fatalf("frame %d: mode = %d, want 3 (mono)", frames, mode)
			}
		case mode == 1:
			if modeExt != 2 {
				t.Fatalf("frame %d: mode 1 (joint stereo), mode_extension = %d, want 2 (M/S on, intensity off: this encoder never emits intensity)", frames, modeExt)
			}
		case mode == 0:
			if modeExt != 0 {
				t.Fatalf("frame %d: mode 0 (L/R stereo), mode_extension = %d, want 0", frames, modeExt)
			}
		default:
			t.Fatalf("frame %d: mode = %d, want 0 or 1 for a stereo stream", frames, mode)
		}

		frameBytes := hdrFrameBytes(h, 0) + hdrPadding(h)
		if pos+frameBytes > len(stream) {
			t.Fatalf("frame %d: frame length %d overruns the remaining %d stream bytes", frames, frameBytes, len(stream)-pos)
		}
		frame := h[:frameBytes]

		rd := bits.NewReader(frame[4:])
		gr := make([]grInfo, 2*wantNch)
		mdb := l3ReadSideInfo(&rd, gr, frame[:4], len(frame)-4)
		if reservoirManaged {
			part23Sum := 0
			for i := range gr {
				part23Sum += int(gr[i].part23Length)
			}
			areaBytes := frameBytes - 4 - sideInfoBitsFor(wantNch)/8
			rr.frame(t, frames, mdb, part23Sum, areaBytes)
		} else if mdb != 0 {
			t.Fatalf("frame %d: l3ReadSideInfo = %d, want 0 (main_data_begin=0, no malformed signal)", frames, mdb)
		}

		frameMainBits := frameBytes*8 - 32 - sideInfoBitsFor(wantNch)
		mainData := frame[4+sideInfoBitsFor(wantNch)/8:]
		validateGranules(t, frames, frame[:4], gr, mainData, wantNch, frameMainBits, reservoirManaged, prevBlockType)

		pos += frameBytes
		frames++
	}
	if reservoirManaged {
		rr.finish(t)
	}
	if pos != len(stream) {
		t.Fatalf("stream not fully consumed by header walk: pos = %d, len(stream) = %d", pos, len(stream))
	}
	if frames != nFrames {
		t.Fatalf("frame count = %d, want %d", frames, nFrames)
	}
}

// legalBlockSuccessors is design decision 10's per-channel state-machine
// grammar (docs/superpowers/plans/2026-08-23-go-mp3-phase4-inc7-short-blocks.md):
// with one granule of lookahead and no 1->3 transition ever emitted, block
// N+1's type must be a member of legalBlockSuccessors[block N's type].
// blockValidateGranules enforces this ACROSS the whole stream's granule
// sequence per channel (frame boundaries included), even though PR A never
// wires the real attack-driven state machine into codeFrame (that is PR
// B's job): the window-switching plumbing this task adds must already
// accept every stream a conforming encoder could produce.
var legalBlockSuccessors = [4][]int{
	0: {0, 1},
	1: {2},
	2: {2, 3},
	3: {0, 1},
}

// legalBlockTransition reports whether to is a legal successor of from
// under legalBlockSuccessors.
func legalBlockTransition(from, to int) bool {
	for _, w := range legalBlockSuccessors[from] {
		if w == to {
			return true
		}
	}
	return false
}

// validateGranules checks every granule-channel's side-info invariants
// (valid codebook numbers, legal scalefac_compress/preflag/scalefac_scale
// range, granule 0 never masks via scfsi) and that l3ReadScalefactors
// consumes EXACTLY the part2 bits expectedPart2Bits predicts from
// scalefacCompress and the scfsi mask (walking the frame's main data with
// a bits.Reader, the same technique readFrameScf uses); both are
// content-independent (derived from side-info fields, not from what bytes
// mainData actually holds), so they stay meaningful regardless of where
// the granule's real bytes physically live.
//
// Since this task, block_type may be 0 (long) or a window-switching value
// (1 start, 2 short, 3 stop; l3ReadSideInfo itself already rejects the
// unused encoding block_type==0-with-flag-set, so any parsed non-zero
// block_type is by construction a real window-switching granule): a
// non-zero block_type requires mixed_block_flag==0 (mixed blocks are
// never emitted, out of this task's scope) and every subblock_gain in
// [0,7] (guaranteed by the 3-bit field width, checked anyway for
// documentation); a channel with ANY non-long granule in the frame must
// have scfsi==0 for granule 1 (design decision 6: scfsi is valid only
// when window_switching_flag==0 for BOTH granules of a channel); and the
// per-channel block_type sequence (this granule against the previous one
// seen for the same channel, prevBlockType, threaded in from
// validateFrameHeaders) must respect legalBlockSuccessors (design
// decision 10).
//
// When NOT reservoirManaged, it additionally requires the granule-channels'
// combined part2_3 length to fit the frame's OWN main-data budget
// (frameMainBits): true for AppendFramePin-assembled frames, which are
// always self-contained (main_data_begin fixed at 0). For a
// reservoir-managed stream that check does not apply and is skipped: a
// frame's part23 sum can legitimately exceed its own physical mainBits,
// because the reservoir lets a granule's Huffman data spill backward into
// bytes physically owned by earlier frames (addressed via
// main_data_begin). reservoirReplay (in validateFrameHeaders) already
// proves the correct whole-stream byte accounting for that case, and
// validateFrameDecode proves full content decodability via the production
// decoder's reservoir-aware l3RestoreReservoir; this per-frame check would
// only be redundant-and-wrong on top of those.
// validateSideInfoInvariants checks one granule-channel's side-info field
// invariants (bigValues bound, block_type/mixed_block_flag/subblock_gain,
// the decision-10 transition grammar against prevBlockType, legal
// scalefac_compress/preflag/scalefac_scale range, valid codebook numbers,
// and the scfsi rules: granule 0 never masks, and design decision 6
// disables scfsi for granule 1 of any channel with a non-long granule).
// Split out of validateGranules to keep that function's cognitive
// complexity down; nonLong[ch] is updated in place so the caller's
// gi==1 scfsi check sees whether EITHER granule of the channel went
// non-long.
func validateSideInfoInvariants(t *testing.T, frameIdx, gi, ch int, g *grInfo, prevBlockType []int, nonLong []bool) {
	t.Helper()
	if g.bigValues > 288 {
		t.Fatalf("frame %d gr %d ch %d: bigValues = %d, want <= 288", frameIdx, gi, ch, g.bigValues)
	}
	bt := int(g.blockType)
	if bt > 3 {
		t.Fatalf("frame %d gr %d ch %d: blockType = %d, want 0..3", frameIdx, gi, ch, bt)
	}
	if bt != 0 {
		nonLong[ch] = true
		for w, sg := range g.subblockGain {
			if sg > 7 {
				t.Fatalf("frame %d gr %d ch %d: subblockGain[%d] = %d, want 0..7", frameIdx, gi, ch, w, sg)
			}
		}
	}
	if g.mixedBlockFlag != 0 {
		t.Fatalf("frame %d gr %d ch %d: mixedBlockFlag = %d, want 0", frameIdx, gi, ch, g.mixedBlockFlag)
	}
	if prevBlockType[ch] >= 0 && !legalBlockTransition(prevBlockType[ch], bt) {
		t.Fatalf("frame %d gr %d ch %d: block_type transition %d -> %d illegal under decision 10's grammar",
			frameIdx, gi, ch, prevBlockType[ch], bt)
	}
	prevBlockType[ch] = bt
	if g.scalefacCompress > 15 {
		t.Fatalf("frame %d gr %d ch %d: scalefacCompress = %d, want in [0,15]", frameIdx, gi, ch, g.scalefacCompress)
	}
	if g.preflag > 1 {
		t.Fatalf("frame %d gr %d ch %d: preflag = %d, want 0 or 1", frameIdx, gi, ch, g.preflag)
	}
	if g.scalefacScale > 1 {
		t.Fatalf("frame %d gr %d ch %d: scalefacScale = %d, want 0 or 1", frameIdx, gi, ch, g.scalefacScale)
	}
	for r, ts := range g.tableSelect {
		if ts == 4 || ts == 14 {
			t.Fatalf("frame %d gr %d ch %d region %d: tableSelect = %d, invalid codebook number", frameIdx, gi, ch, r, ts)
		}
	}
	if gi == 0 && g.scfsi != 0 {
		t.Fatalf("frame %d gr 0 ch %d: scfsi = %04b, want 0 (granule 0 never masks)", frameIdx, ch, g.scfsi)
	}
	if gi == 1 && nonLong[ch] && g.scfsi != 0 {
		t.Fatalf("frame %d gr 1 ch %d: scfsi = %04b, want 0 (decision 6: a non-long granule in this channel disables scfsi)", frameIdx, ch, g.scfsi)
	}
}

func validateGranules(t *testing.T, frameIdx int, hdr []byte, gr []grInfo, mainData []byte, nch, frameMainBits int, reservoirManaged bool, prevBlockType []int) {
	t.Helper()

	main := bits.NewReader(mainData)
	var istPos [2][40]uint8
	var scf [40]float32
	pos := 0
	sumPart23 := 0
	nonLong := make([]bool, nch) // per channel: does EITHER granule carry a non-long block_type

	for gi := range 2 {
		for ch := range nch {
			i := gi*nch + ch
			g := &gr[i]
			validateSideInfoInvariants(t, frameIdx, gi, ch, g, prevBlockType, nonLong)

			wantPart2 := expectedPart2Bits(int(g.scalefacCompress), int(g.scfsi), int(g.nLongSfb), int(g.nShortSfb))

			main.SetPos(pos)
			before := main.Pos()
			l3ReadScalefactors(hdr, scf[:], istPos[ch][:], g, &main, ch)
			gotPart2 := main.Pos() - before
			if gotPart2 != wantPart2 {
				t.Fatalf("frame %d gr %d ch %d: l3ReadScalefactors consumed %d bits, want %d (scalefacCompress %d, scfsi %04b)",
					frameIdx, gi, ch, gotPart2, wantPart2, g.scalefacCompress, g.scfsi)
			}
			if int(g.part23Length) < wantPart2 {
				t.Fatalf("frame %d gr %d ch %d: part23Length = %d, want >= part2 = %d", frameIdx, gi, ch, g.part23Length, wantPart2)
			}

			pos += int(g.part23Length)
			sumPart23 += int(g.part23Length)
		}
	}
	if !reservoirManaged && sumPart23 > frameMainBits {
		t.Fatalf("frame %d: sum(part23Length) = %d, exceeds mainBits = %d", frameIdx, sumPart23, frameMainBits)
	}
}

// expectedPart2Bits recomputes the exact part2 (scalefactor) bit count a
// granule-channel's side info predicts: scalefacCompress selects the
// {slen1, slen2} pair via the decoder's own scfcDecodeTable (mirroring
// ISO 2.4.2.7's slen table, cross-checked independently by
// internal/enc's TestSlenTabKnownAnswer), and scfsi masks out groups
// reused from granule 0 (bit 3 = group 0 ... bit 0 = group 3, the
// side-info order); group widths come from scfPartitionsTable's row 0
// (long: 6,5,5,5) or row 2 (pure short, nLongSfb==0: 9,9,6,12), the same
// MPEG1 scfCount l3ReadScalefactorsRaw consumes for each geometry
// (l3ReadScalefactors' own row selection, internal/dec/scalefactors.go:
// 121-127; mixed blocks, row 1, are out of this task's scope and never
// appear in a validated stream). scfsi masking never applies to a short
// granule in practice (the encoder always writes scfsi=0 for a channel
// with any non-long granule, design decision 6), but the mask is still
// honored here for uniformity with the long-block formula.
func expectedPart2Bits(scalefacCompress, scfsi, nLongSfb, nShortSfb int) int {
	row := 0
	if nShortSfb != 0 {
		row++
	}
	if nLongSfb == 0 {
		row++
	}
	part := int(scfcDecodeTable[scalefacCompress])
	slen1, slen2 := part>>2, part&3
	widths := scfPartitionsTable[row][:4]
	total := 0
	for i, w := range widths {
		if scfsi&(1<<(3-i)) != 0 {
			continue
		}
		slen := slen1
		if i >= 2 {
			slen = slen2
		}
		total += int(w) * slen
	}
	return total
}

// validateFrameDecode feeds stream through the internal decoder, not the
// public mp3.Decoder (see validateFrames' doc comment for the import-cycle
// reason). A plain advance-by-FrameBytes loop needs no sentinel header:
// decode.go's fast path accepts an exact-fit final frame
// (frameSize == mp3Bytes, decode.go:98) and findFrame's resync path accepts
// the same (i == 0 && frameAndPadding == mp3Bytes, header.go:172-173), the
// same behavior TestFullStreamMatchesOracle already relies on.
func validateFrameDecode(t *testing.T, stream []byte, nFrames int) {
	t.Helper()

	d := NewDecoder()
	pcm := make([]float32, maxSamplesPerFrame)
	var fi FrameInfo
	pos := 0
	decoded := 0
	for pos < len(stream) {
		n := d.DecodeFrame(stream[pos:], pcm, &fi)
		if n != 1152 {
			t.Fatalf("decode frame %d at byte %d: n = %d, want 1152", decoded, pos, n)
		}
		if fi.FrameOffset != 0 {
			t.Fatalf("decode frame %d: FrameOffset = %d, want 0", decoded, fi.FrameOffset)
		}
		if fi.Layer != 3 {
			t.Fatalf("decode frame %d: Layer = %d, want 3", decoded, fi.Layer)
		}
		if fi.FrameBytes <= 0 {
			t.Fatalf("decode frame %d: FrameBytes = %d, want > 0", decoded, fi.FrameBytes)
		}
		pos += fi.FrameBytes
		decoded++
	}
	if pos != len(stream) {
		t.Fatalf("stream not fully consumed by decode loop: pos = %d, len(stream) = %d", pos, len(stream))
	}
	if decoded != nFrames {
		t.Fatalf("decoded frame count = %d, want %d", decoded, nFrames)
	}
}

// sideInfoBitsFor returns the exact packed side-info size in bits for nch
// channels: 136 mono, 256 stereo. Independently derived here (not calling
// into internal/enc) from the same field widths l3ReadSideInfo reads
// (internal/dec/sideinfo.go:69): main_data_begin(9) + private_bits(5 mono/3
// stereo) + scfsi(4/channel) + 2 granules * nch channels * 59
// bits/granule-channel.
func sideInfoBitsFor(nch int) int {
	privateBits := 5
	if nch == 2 {
		privateBits = 3
	}
	return 9 + privateBits + nch*4 + 2*nch*59
}

// grid amplitudes for the LCG-driven synthetic spectra: silence, and three
// scaled levels reaching well up toward enc.maxQuant, so the rate loop
// exercises easy, moderate, and heavily-compressed granules across a
// 20-frame run.
var structuralGridAmplitudes = [4]float64{0, 50, 2000, 8000}

// runStructuralGrid builds nFrames synthetic frames at (srIndex,
// bitrateIndex, mode, nch), amplitude-cycling through
// structuralGridAmplitudes, encodes
// each through enc.AppendFramePin (the production codeGranule + appendFrame
// pair), and validates the resulting stream with validateFrames.
func runStructuralGrid(t *testing.T, srIndex, bitrateIndex, wantKbps, mode, nch, nFrames int) {
	t.Helper()
	seed := uint64(srIndex)<<32 | uint64(bitrateIndex)<<8 | uint64(mode)

	var stream []byte
	for f := range nFrames {
		amp := structuralGridAmplitudes[f%len(structuralGridAmplitudes)]
		var xr [2][2][576]float64
		for g := range 2 {
			for ch := range nch {
				for i := range 576 {
					v := testsignal.LCG(&seed) * amp
					if testsignal.LCG(&seed) < 0.5 {
						v = -v
					}
					xr[g][ch][i] = v
				}
			}
		}
		stream = enc.AppendFramePin(stream, bitrateIndex, srIndex, 0, mode, &xr, nch)
	}

	validateFrames(t, stream, int(hdrHz[srIndex]), wantKbps, nch, nFrames, false)
}

// kbpsToIndex maps every MPEG-1 Layer III CBR bitrate to its side-info
// bitrate_index, ISO/IEC 11172-3 Table B.1 (hand-specified: the standard
// MPEG-1 Layer III bitrate list).
var kbpsToIndex = map[int]int{
	32: 1, 40: 2, 48: 3, 56: 4, 64: 5, 80: 6, 96: 7,
	112: 8, 128: 9, 160: 10, 192: 11, 224: 12, 256: 13, 320: 14,
}

// gridMode pairs a side-info channel-mode index (mode) with its channel
// count (nch): the (stereo, mono) combinations TestEncFrameStructuralGrid
// and TestEncoderStructuralGrid both grid over.
type gridMode struct {
	mode int
	nch  int
}

// gridSampleRates, gridBitratesKbps and gridModes are the shared coverage
// TestEncFrameStructuralGrid and TestEncoderStructuralGrid both sweep via
// forEachGridCase: every MPEG-1 sample rate x bitrate in {32,128,320} x
// (stereo, mono).
var gridSampleRates = [3]int{44100, 48000, 32000}

var gridBitratesKbps = []int{32, 128, 320}

var gridModes = []gridMode{
	{0, 2}, // stereo
	{3, 1}, // single_channel
}

// forEachGridCase drives the sample-rate x bitrate x mode grid shared by
// TestEncFrameStructuralGrid and TestEncoderStructuralGrid, invoking run as
// its own subtest for every case, named "sr<Hz>_kbps<kbps>_nch<nch>" (same
// naming and iteration order both tests used before this helper existed).
// sr is the MPEG-1 sample-rate index (0=44100, 1=48000, 2=32000, matching
// srIndex elsewhere in this package); m is the (mode, nch) pair.
func forEachGridCase(t *testing.T, run func(t *testing.T, sr, kbps int, m gridMode)) {
	t.Helper()
	for sr := range 3 {
		for _, kbps := range gridBitratesKbps {
			for _, m := range gridModes {
				t.Run(fmt.Sprintf("sr%d_kbps%d_nch%d", gridSampleRates[sr], kbps, m.nch), func(t *testing.T) {
					t.Parallel()
					run(t, sr, kbps, m)
				})
			}
		}
	}
}

// TestEncFrameStructuralGrid is the no-oracle centerpiece for Task 6: every
// (sample rate) x (bitrate in {32,128,320}) x (mono,stereo) combination,
// 20 frames each, run through the production codeGranule/appendFrame pair
// and validated structurally and by real decode. 32kHz/320kbps/mono
// (budget 5676, effBudget 4095) is included, as required, in this grid's
// bitrate-320 x mono coverage.
//
// Note on that specific coverage: a missing maxPart23Length cap would mask
// part_2_3_length on write (WriteBits truncates silently to the low 12
// bits) but, because main_data_begin is always 0 here, the resulting
// under-declared granule length never pushes the decoder's per-frame
// bits.Reader past its limit (that reader spans the whole frame's main-data
// area, sized to the frame's true, uncorrupted mainBits budget, and a
// 12-bit-masked declared length is by construction always < 4096, far
// inside it) - so this grid's decode leg does not, on its own, turn a
// missing cap into an observable n != 1152 or Overrun failure. Verified by
// temporarily removing the cap: TestCodeGranuleBudgetCap (frame_test.go)
// fails immediately, while this grid still passes. TestCodeGranuleBudgetCap
// is therefore the load-bearing regression guard for the cap itself; this
// grid's value for that requirement is the literal coverage the addendum
// asks for (32kHz/320kbps/mono exercised end to end) plus its broader job
// of decoder-proving region boundaries and table selects (see this file's
// validateGranules and the addendum's CF3 note) across real, varied
// content. A full 14-bitrate sweep at 44.1 kHz stereo is included
// separately.
func TestEncFrameStructuralGrid(t *testing.T) {
	forEachGridCase(t, func(t *testing.T, sr, kbps int, m gridMode) {
		t.Helper()
		runStructuralGrid(t, sr, kbpsToIndex[kbps], kbps, m.mode, m.nch, 20)
	})

	for kbps, idx := range kbpsToIndex {
		t.Run(fmt.Sprintf("sweep_44100_stereo_kbps%d", kbps), func(t *testing.T) {
			t.Parallel()
			runStructuralGrid(t, 0, idx, kbps, 0, 2, 20)
		})
	}
}

// TestEncHeaderSampleRateRowMapping closes the gap TestEncSfbWidthsMatchDec
// leaves open: that test hard-codes decoder rows {5,6,7} per encoder index,
// so a 44.1<->32kHz label swap consistently applied to BOTH
// enc.sfbWidthsLong's row order AND the encoder's header sampling_frequency
// packing would still pass it. This test instead builds a real header via
// enc.AppendFramePin, decodes the sample rate the decoder's own hdr_*
// accessors extract from it, and confirms the sfb-width row
// l3ReadSideInfo actually selects at runtime for that header (via
// hdrGetMySampleRate -> scfLongTable) equals enc.SfbWidthsLongRow at the
// SAME decoded rate (not the srIndex used to build the header): header
// bytes -> hdrGetMySampleRate -> scfLongTable row selection, so a label
// swap on either side breaks either the sample-rate check or the sfbTab
// check.
func TestEncHeaderSampleRateRowMapping(t *testing.T) {
	wantRate := [3]int{44100, 48000, 32000}
	indexOf := map[int]int{44100: 0, 48000: 1, 32000: 2}

	for srIndex := range 3 {
		var xr [2][2][576]float64 // zero spectra
		frame := enc.AppendFramePin(nil, 9, srIndex, 0, 3, &xr, 1)
		h := frame[:4]

		if !hdrValid(h) {
			t.Fatalf("srIndex %d: hdrValid = false", srIndex)
		}
		if got := int(hdrSampleRateHz(h)); got != wantRate[srIndex] {
			t.Fatalf("srIndex %d: hdrSampleRateHz = %d, want %d", srIndex, got, wantRate[srIndex])
		}

		rd := bits.NewReader(frame[4:])
		gr := make([]grInfo, 2)
		if got := l3ReadSideInfo(&rd, gr, h, len(frame)-4); got != 0 {
			t.Fatalf("srIndex %d: l3ReadSideInfo = %d, want 0", srIndex, got)
		}

		encRow := enc.SfbWidthsLongRow(indexOf[wantRate[srIndex]])
		for i := range 22 {
			if gr[0].sfbTab[i] != uint8(encRow[i]) {
				t.Fatalf("srIndex %d: gr[0].sfbTab[%d] = %d, want %d (enc.SfbWidthsLongRow at the decoded rate)",
					srIndex, i, gr[0].sfbTab[i], encRow[i])
			}
		}
	}
}

// encoderStructuralGridAmplitudes are the LCG-cycled amplitudes
// runEncoderStructuralGrid drives the real Encoder with, scaled into
// [-1,1] (the Encoder's documented input domain): silence, quiet, loud,
// and near-full-scale, so the rate loop exercises easy, moderate, and
// heavily-compressed granules exactly as structuralGridAmplitudes does for
// the synthetic-spectrum grid above.
var encoderStructuralGridAmplitudes = [4]float64{0, 0.02, 0.4, 0.95}

// runEncoderStructuralGrid drives the production enc.Encoder (Task 7)
// through nFrames real PCM frames plus one drain frame and validates the
// resulting stream with validateFrames, the same validator
// TestEncFrameStructuralGrid runs over the synthetic-spectrum
// AppendFramePin path above. This is the addendum's CF3 requirement
// (section d): the structural invariants must hold for the real end-to-end
// PCM-in/MP3-out pipeline, not only for hand-built spectra.
func runEncoderStructuralGrid(t *testing.T, sampleRate, kbps, nch, nFrames int) {
	t.Helper()

	e, err := enc.New(enc.Config{SampleRate: sampleRate, Channels: nch, BitrateKbps: kbps})
	if err != nil {
		t.Fatalf("enc.New: %v", err)
	}

	seed := uint64(sampleRate)<<32 | uint64(kbps)<<8 | uint64(nch)

	var stream []byte
	for f := range nFrames {
		amp := encoderStructuralGridAmplitudes[f%len(encoderStructuralGridAmplitudes)]
		samples := make([][]float32, nch)
		for ch := range nch {
			samples[ch] = make([]float32, 1152)
			for i := range 1152 {
				v := testsignal.LCG(&seed)*2 - 1
				samples[ch][i] = float32(v * amp)
			}
		}
		stream, err = e.EncodeFrame(stream, samples)
		if err != nil {
			t.Fatalf("frame %d: EncodeFrame: %v", f, err)
		}
	}
	stream, err = e.EncodeFrame(stream, nil) // drain: one extra frame
	if err != nil {
		t.Fatalf("drain: EncodeFrame: %v", err)
	}

	validateFrames(t, stream, sampleRate, kbps, nch, nFrames+1, true)
}

// TestEncoderStructuralGrid reruns TestEncFrameStructuralGrid's grid (every
// sample rate x bitrate in {32,128,320} x mono/stereo) through the real
// Task 7 Encoder instead of the synthetic AppendFramePin spectra, closing
// the addendum's CF3 gap: the structural validator must also see real
// PCM-in/MP3-out output, not just hand-built granule spectra.
func TestEncoderStructuralGrid(t *testing.T) {
	forEachGridCase(t, func(t *testing.T, sr, kbps int, m gridMode) {
		t.Helper()
		runEncoderStructuralGrid(t, gridSampleRates[sr], kbps, m.nch, 20)
	})
}
