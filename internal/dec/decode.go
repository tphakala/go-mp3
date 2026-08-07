package dec

import "github.com/tphakala/go-mp3/internal/bits"

// l3ChangeSign mirrors upstream L3_change_sign (tools/oracle/minimp3.h:1186-1192):
// negates every odd-indexed sample of every other 18-float subband block
// (subbands 1, 3, 5, ... within grbuf), the frequency-inversion the polyphase
// synthesis filterbank expects. It runs once per granule-channel immediately
// after l3ImdctGr, the step Task 9 deferred here.
func l3ChangeSign(grbuf []float32) {
	off := 18
	for b := 0; b < 32; b += 2 {
		for i := 1; i < 18; i += 2 {
			grbuf[off+i] = -grbuf[off+i]
		}
		off += 36
	}
}

// l3Decode mirrors upstream L3_decode (tools/oracle/minimp3.h:1238-1272): the
// per-granule decode from side info to time-domain samples. For each channel
// it reads scalefactors and Huffman-decodes the spectrum (using mainBS, the
// reservoir-assembled main-data reader threaded across both granules of the
// frame), applies stereo processing, then reorders, antialiases, runs the
// hybrid IMDCT against the persisted overlap state, and change-signs.
//
// gr is the two (or one, for mono) grInfo values for this granule; d.header
// is the current frame header (== the hdr slice, already copied in). The
// n_long_bands and aa_bands derivations match the pin exactly, including the
// short-block reorder that borrows s.syn as scratch before mp3dSynthGranule
// overwrites it.
func l3Decode(d *Decoder, s *mp3Scratch, gr []grInfo, nch int, mainBS *bits.Reader) {
	hdr := d.header[:]

	for ch := range nch {
		layer3grLimit := mainBS.Pos() + int(gr[ch].part23Length)
		l3ReadScalefactors(hdr, s.scf[:], s.istPos[ch][:], &gr[ch], mainBS, ch)
		l3Huffman(s.grbuf[576*ch:576*ch+576], mainBS, &gr[ch], s.scf[:], layer3grLimit)
	}

	// Intensity and MS stereo are two-channel operations: each combines
	// channel 0 with channel 1. A single-channel granule has no second
	// grInfo (gr has length nch), and l3IntensityStereo reads gr[1] for the
	// MPEG-2 shift bit (stereo.go), so running the stereo path on a mono
	// granule would index out of range. Upstream L3_decode calls the stereo
	// path unconditionally and survives only because its gr_info is a
	// fixed-size C array (gr_info[4]): gr_info[1] reads a stale/garbage struct
	// rather than overrunning. The Go port uses a length-nch slice, so the
	// same access panics; guard it here. A valid mono stream never signals
	// stereo (the I_STEREO/MS bits only appear on joint-stereo frames, which
	// are two-channel), so this guard is invisible to every valid stream and
	// only suppresses a malformed mono frame carrying a spurious stereo flag.
	if nch == 2 {
		l3StereoProcess(s.grbuf[0:576], s.grbuf[576:1152], hdr, s.istPos[1][:], gr)
	}

	for ch := range nch {
		gi := &gr[ch]
		aaBands := 31
		nLongBands := 0
		if gi.mixedBlockFlag != 0 {
			nLongBands = 2
		}
		if hdrGetMySampleRate(hdr) == 2 {
			nLongBands <<= 1
		}

		if gi.nShortSfb != 0 {
			aaBands = nLongBands - 1
			l3Reorder(s.grbuf[576*ch+nLongBands*18:576*ch+576], s.syn[:], gi)
		}

		l3Antialias(s.grbuf[576*ch:576*ch+576], aaBands)
		l3ImdctGr(s.grbuf[576*ch:576*ch+576], d.mdctOverlap[ch][:], gi.blockType, nLongBands)
		l3ChangeSign(s.grbuf[576*ch : 576*ch+576])
	}
}

// DecodeFrame mirrors upstream mp3dec_decode_frame (tools/oracle/minimp3.h:1713-1806).
// It decodes one MP3 frame from mp3 into interleaved float32 PCM in pcm and
// fills info, returning the number of samples per channel (0 when no frame
// decoded). mp3 must begin at or before a frame; the return and info follow
// upstream semantics exactly, so a caller advances by info.FrameBytes.
//
// The fast path reuses the cached header when it matches the next frame,
// avoiding a full resync; a miss zeroes all persistent state (reset) and
// resyncs via findFrame. Only Layer III is decoded (the library's scope); a
// non-Layer-III frame is recognized and sized but produces no samples.
//
//nolint:gocognit,gocyclo // faithful port of mp3dec_decode_frame's fast-path, resync, and granule loop; splitting it would obscure the 1:1 mapping to the pin.
func (d *Decoder) DecodeFrame(mp3 []byte, pcm []float32, info *FrameInfo) int {
	mp3Bytes := len(mp3)
	i := 0
	frameSize := 0

	if mp3Bytes > 4 && d.header[0] == 0xff && hdrCompare(d.header[:], mp3) {
		frameSize = hdrFrameBytes(mp3, d.freeFormatBytes) + hdrPadding(mp3)
		if frameSize != mp3Bytes && (frameSize+hdrSize > mp3Bytes || !hdrCompare(mp3, mp3[frameSize:])) {
			frameSize = 0
		}
	}
	if frameSize == 0 {
		d.reset()
		i = findFrame(mp3, &d.freeFormatBytes, &frameSize)
		if frameSize == 0 || i+frameSize > mp3Bytes {
			info.FrameBytes = i
			return 0
		}
	}

	hdr := mp3[i : i+hdrSize]
	copy(d.header[:], hdr)
	info.FrameBytes = i + frameSize
	info.FrameOffset = i
	info.Channels = 2
	if hdrIsMono(hdr) {
		info.Channels = 1
	}
	info.SampleRateHz = int(hdrSampleRateHz(hdr))
	info.Layer = 4 - hdrGetLayer(hdr)
	info.BitrateKbps = int(hdrBitrateKbps(hdr))

	if pcm == nil {
		return int(hdrFrameSamples(hdr))
	}

	if frameSize < hdrSize {
		// The fast path can size a repeat frame below the 4-byte header when a
		// crafted or free-format stream leaves free_format_bytes < HDR_SIZE (see
		// findFrame, which latches free_format_bytes = k - padding, as low as 3):
		// frameSize = hdrFrameBytes(mp3, freeFormatBytes) + hdrPadding(mp3) then
		// undershoots hdrSize, and mp3[i+hdrSize:i+frameSize] would invert its
		// bounds and panic. Upstream never panics here: bs_init takes a negative
		// limit (frame_size - HDR_SIZE) and get_bits, which advances pos before
		// checking it, returns 0 for every read so L3_read_side_info trips the
		// bs_frame->pos > bs_frame->limit overrun and returns 0 after mp3dec_init
		// (tools/oracle/minimp3.h:1753,1762-1766). Match that observable outcome
		// without the negative-limit dance: no samples, info.FrameBytes already
		// advanced (i+frameSize, set above) so the caller still makes progress,
		// and header[0] cleared via initState so the next call resyncs.
		d.initState()
		return 0
	}

	bsData := mp3[i+hdrSize : i+frameSize]
	bsFrame := bits.NewReader(bsData)
	if hdrIsCRC(hdr) {
		bsFrame.Bits(16)
	}

	if info.Layer != 3 {
		// Layer I/II are out of scope: the frame is sized (info is filled)
		// so the caller advances correctly, but no PCM is produced.
		return 0
	}

	s := &d.scratch
	mainDataBegin := l3ReadSideInfo(&bsFrame, s.grInfo[:], hdr, len(bsData))
	if mainDataBegin < 0 || bsFrame.Overrun() {
		d.initState()
		return 0
	}

	mainBS, mainData, ok := l3RestoreReservoir(&d.res, &bsFrame, bsData, mainDataBegin, s.maindata[:])
	success := 0
	if ok {
		success = 1
		nGranules := 1
		if hdrTestMPEG1(hdr) {
			nGranules = 2
		}
		for igr := range nGranules {
			clear(s.grbuf[:])
			base := igr * info.Channels
			l3Decode(d, s, s.grInfo[base:base+info.Channels], info.Channels, &mainBS)
			mp3dSynthGranule(d.qmfState[:], s.grbuf[:], 18, info.Channels, pcm[576*info.Channels*igr:], s.syn[:])
		}
	}
	l3SaveReservoir(&d.res, mainBS, mainData)

	return success * int(hdrFrameSamples(d.header[:]))
}
