package pcm

import (
	"errors"
	"fmt"
	"io"
	"math"

	mp3 "github.com/tphakala/go-mp3"
)

// ErrSeekUnsupported is returned by SeekToSample when the source does not
// implement io.Seeker, or cannot be positioned by a frame-header walk (a
// free-format stream). It is a capability error: the decoder is left usable.
var ErrSeekUnsupported = errors.New("go-mp3/pcm: seek unsupported")

// ErrInvalidSeek is returned by SeekToSample for a negative sample index. It is
// an argument error: the decoder is left usable.
var ErrInvalidSeek = errors.New("go-mp3/pcm: invalid seek")

// seekPrimeFrames is the number of frames decoded and discarded ahead of the
// landing frame so its samples come out bit-exact. A frame's main_data can live
// in up to the previous ~511 bytes of bit-reservoir, and the MDCT overlap-add
// needs the preceding frame, so decoding straight at the target would yield
// wrong samples for the first frame or two. Ten lead-in frames already
// reproduce every fixture's landing frame bit-exact against a decode from the
// start; 16 keeps a comfortable margin.
const seekPrimeFrames = 16

// SeekToSample positions the decoder so the next Read or WriteTo yields audio
// starting at sampleIndex, counted in the playable (gapless-trimmed) per-channel
// timeline that Read emits, where sample 0 is the first playable sample. It
// returns the sample actually positioned at, which equals sampleIndex on
// success. Seeking to or past the known total lands at the stream end and
// returns the total (the next read is io.EOF).
//
// It returns ErrSeekUnsupported when the source is not an io.Seeker, or when the
// stream cannot be positioned by the frame-header walk (a free-format stream,
// whose frames carry no size in their headers). A negative index returns
// ErrInvalidSeek. Both are argument/capability errors that leave the decoder
// usable. A trailing non-frame header met after at least one real frame (an
// ID3v1 tag or junk past the last audio frame) is treated like the stream end,
// landing there rather than failing. Any other mid-seek failure (a Seek or read
// error, or a landing frame that decodes to no samples) latches sticky, so a
// later Read returns it rather than resuming from an indeterminate position; a
// subsequent successful seek clears it.
//
// Landing is exact rather than merely frame-accurate: SeekToSample recovers the
// target frame's exact index by an inexpensive frame-header walk from the first
// audio frame (a byte-based Xing/VBRI TOC is only approximate for a real VBR
// stream and cannot place a sample exactly), primes the bit reservoir and MDCT
// overlap by decoding seekPrimeFrames lead-in frames, then drops the leading
// samples inside the landing frame to hit sampleIndex precisely.
func (d *Decoder) SeekToSample(sampleIndex int64) (int64, error) {
	if d.seeker == nil {
		return 0, ErrSeekUnsupported
	}
	if sampleIndex < 0 {
		return 0, ErrInvalidSeek
	}

	// Clamp a seek at or past the known playable length to the stream end.
	if total := d.info.TotalSamples; total != 0 && total <= math.MaxInt64 &&
		sampleIndex >= int64(total) { //nolint:gosec // G115: guarded total <= math.MaxInt64.
		return d.seekToEnd(int64(total)) //nolint:gosec // G115: same guard.
	}

	// Work in the raw (pre-gapless-trim) timeline: playable sample s maps to raw
	// sample s+gaplessStart, and every Layer III frame holds exactly spf samples
	// per channel, so the landing frame index and the intra-frame drop follow
	// directly. rawTarget >= gaplessStart (sampleIndex >= 0), so the head trim is
	// already satisfied at the landing sample.
	spf := int64(samplesPerFrame(d.info.SampleRate))
	rawTarget := sampleIndex + int64(d.gaplessStart) //nolint:gosec // G115: gaplessStart is the small LAME delay plus the 529-sample decoder delay.
	if rawTarget < sampleIndex {
		// int64 overflow: sampleIndex plus the (small) gapless delay wrapped past
		// math.MaxInt64. Only an unknown length (TotalSamples == 0) reaches here
		// for such an index, since a known length clamps above. Saturate rather
		// than let a negative rawTarget drive a negative slice index during
		// priming: targetFrame is then huge-positive, the frame walk runs to EOF,
		// and the !reached branch lands at the true end.
		rawTarget = math.MaxInt64
	}
	targetFrame := rawTarget / spf
	intra := rawTarget % spf
	primeFrame := targetFrame - seekPrimeFrames
	if primeFrame < 0 {
		primeFrame = 0
	}

	// Capture the source position so the non-poisoning ErrSeekUnsupported return
	// below can restore it: frameOffsets seeks the raw source directly, and
	// leaving it moved would desync the bufio reader for the reads that follow a
	// failed seek. The normal (reached) path rebinds via reseek, so it needs no
	// restore. d.seeker is non-nil here, guaranteed by the check above.
	srcPos, posErr := d.seeker.Seek(0, io.SeekCurrent)

	primeOff, avail, reached, err := d.frameOffsets(primeFrame, targetFrame)
	if err != nil {
		if errors.Is(err, ErrSeekUnsupported) {
			// A free-format stream cannot be header-walked. Leave the decoder
			// usable rather than latching: restore the source and return the
			// capability error, exactly like the non-seekable pre-flight check.
			if posErr != nil {
				// The prior position could not be captured, so it cannot be
				// restored; latch rather than resume from an indeterminate offset.
				return d.seekFailed(posErr)
			}
			_, _ = d.seeker.Seek(srcPos, io.SeekStart)
			return 0, err
		}
		return d.seekFailed(err)
	}
	if !reached {
		// The stream ends before the target frame: a seek past the true end of an
		// unknown-length stream (a known length would have clamped above). Land at
		// the end, reporting the playable samples that do exist.
		end := avail*spf - int64(d.gaplessStart) //nolint:gosec // G115: the small LAME delay plus the 529-sample decoder delay.
		if end < 0 {
			end = 0
		}
		return d.seekToEnd(end)
	}
	if err := d.reseek(primeOff); err != nil {
		return d.seekFailed(err)
	}
	landed, err := d.primeAndLand(primeFrame, targetFrame, intra, spf)
	if err != nil {
		return d.seekFailed(err)
	}
	return landed, nil
}

// frameOffsets walks frame headers from audioStart and returns the absolute
// byte offset of frame prime (0-based over real audio frames, frame 0 at
// audioStart), reporting whether the stream actually reaches frame target. It
// reads only each frame's four header bytes and derives the frame length from
// the MPEG header fields, seeking over the body, so it is far cheaper than
// decoding. When the stream ends before frame target, reached is false and
// avail is the number of frames present. The walk is exact for CBR and VBR
// alike, which is what lets the landing be sample-accurate.
//
// Offsets established along the way are cached in frameOff, so a later seek
// resumes from the highest frame already walked instead of re-walking from
// audioStart: a repeat or backward seek costs a single header probe, and a
// forward seek pays only the frames past the cached range. The returns are
// byte-identical either way, because a cached entry is the very pos the
// uncached walk would have computed at that index and the cache never extends
// past the frame the walk stopped on.
func (d *Decoder) frameOffsets(prime, target int64) (primeOff, avail int64, reached bool, err error) {
	// The caller guarantees d.seeker != nil (SeekToSample's pre-flight check).
	//
	// Seed the cache with frame 0's offset. audioStart is final by the time
	// SeekToSample can run: Reset's eager first-frame decode has already folded
	// in any tag frames and resync offset, and its guards fire once per Reset.
	if len(d.frameOff) == 0 {
		d.frameOff = append(d.frameOff, d.audioStart)
	}
	// Resume from the highest cached frame at or below target. Every cached
	// offset was computed by this same walk over this same source, so skipping
	// the seek and header read for frames below the resume point changes no
	// return value; the walk re-probes the resume frame itself and everything
	// after it exactly as a cold walk would. prime < start means frames 0..prime
	// were all sized on an earlier walk, so the cold walk would have reached
	// iteration prime and set primeOff to this same cached offset.
	start := int64(len(d.frameOff)) - 1
	if start > target {
		start = target
	}
	if prime < start {
		primeOff = d.frameOff[prime]
	}
	pos := d.frameOff[start]
	var hdr [4]byte
	for i := start; i <= target; i++ {
		if i == prime {
			primeOff = pos
		}
		if _, serr := d.seeker.Seek(pos, io.SeekStart); serr != nil {
			return 0, i, false, serr
		}
		if _, rerr := io.ReadFull(d.src, hdr[:]); rerr != nil {
			if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
				return primeOff, i, false, nil // frame i absent: stream shorter than target
			}
			return 0, i, false, rerr
		}
		if i == target {
			return primeOff, target, true, nil // frame target present
		}
		length, lok := frameLength(hdr[:])
		if !lok {
			if i == 0 {
				// The very first audio frame cannot be sized from its header (a
				// free-format stream, whose bitrate index frameLength cannot size).
				// This stream is not seekable by header walk: report a
				// non-poisoning capability error rather than latching d.err.
				return 0, 0, false, ErrSeekUnsupported
			}
			// i > 0: at least one real frame was already walked, so this is a
			// non-frame header past the last audio frame (an ID3v1 "TAG" trailer or
			// trailing junk). Treat it like EOF and land at the stream end, mirroring
			// the streaming path's tolerance of trailing junk as a clean end.
			return primeOff, i, false, nil
		}
		pos += int64(length)
		// Frame i is sized, so pos is now frame i+1's confirmed start. Extend the
		// cache only on new ground: a re-walk over already-cached frames returns at
		// the i == target check above, before reaching here, and this guard keeps
		// frameOff dense and append-only. Nothing is appended past the frame a
		// failing walk stopped on, since every exit above skips it, so the
		// terminating frame is always re-probed live rather than served from cache.
		if int64(len(d.frameOff)) == i+1 {
			d.frameOff = append(d.frameOff, pos)
		}
	}
	return primeOff, target, true, nil
}

// reseek repositions the source at absolute offset pos and rebinds the decode
// path there: it resets the wrapped mp3.Decoder (dropping stale reservoir and
// overlap), drops the bufio buffer and the frame buffer, and clears the
// end/error state so decoding resumes cleanly from pos.
func (d *Decoder) reseek(pos int64) error {
	// The caller guarantees d.seeker != nil (SeekToSample's pre-flight check).
	if _, err := d.seeker.Seek(pos, io.SeekStart); err != nil {
		return err
	}
	d.dec.Reset()
	d.br.Reset(d.src)
	d.frameBuf = d.frameBuf[:0]
	d.pending = nil
	d.readErr = nil
	d.done = false
	d.err = nil
	return nil
}

// decodeRawFrame decodes exactly one frame from the buffered bytes, advancing
// past it, and returns its per-channel sample count n and frame info without any
// gapless trim or output packing. SeekToSample uses it to prime decoder state
// and to fetch the landing frame. It returns io.EOF at a clean end of the
// buffered stream.
func (d *Decoder) decodeRawFrame() (int, mp3.FrameInfo, error) {
	d.fill()
	if d.readErr != nil && !errors.Is(d.readErr, io.EOF) {
		// A real source failure during priming must not be reported as a clean
		// end: SeekToSample latches whatever this returns, and io.EOF would be
		// mistaken for a benign stream end.
		return 0, mp3.FrameInfo{}, d.readErr
	}
	if len(d.frameBuf) == 0 {
		return 0, mp3.FrameInfo{}, io.EOF
	}
	n, fi, err := d.dec.DecodeFrame(d.frameBuf, d.sampleBuf)
	if err != nil {
		return 0, fi, err
	}
	if fi.FrameBytes == 0 {
		return 0, fi, io.EOF
	}
	d.consume(fi.FrameBytes)
	return n, fi, nil
}

// primeAndLand decodes and discards the frames from primeFrame (where reseek
// positioned the decoder) up to targetFrame to converge the bit reservoir and
// MDCT/synthesis state, then decodes the landing frame and points pending at
// its samples from intra onward, intersected with the gapless tail bound. It
// sets producedSamples to the landing frame's raw end so ordinary decoding
// continues in the correct timeline and the tail trim still fires. It returns
// the playable sample landed on (== the seek's sampleIndex).
func (d *Decoder) primeAndLand(primeFrame, targetFrame, intra, spf int64) (int64, error) {
	// Decode and discard the frames from primeFrame up to targetFrame, counting
	// frames rather than samples. reseek landed on an exact frame boundary, so
	// each decodeRawFrame consumes exactly one frame; the first lead-in frames
	// decode with an empty bit reservoir and yield no samples (n == 0), and
	// counting samples would skip over them and over-consume past the target.
	for i := primeFrame; i < targetFrame; i++ {
		if _, _, err := d.decodeRawFrame(); err != nil {
			return 0, err
		}
	}

	// The landing frame. With seekPrimeFrames of lead-in the bit reservoir and
	// MDCT overlap have converged, so it decodes to a full frame of samples.
	n, fi, err := d.decodeRawFrame()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, fmt.Errorf("%w: seek landing frame at %d produced no samples", mp3.ErrCorruptStream, targetFrame)
	}

	frameStart := targetFrame * spf
	frameEnd := frameStart + int64(n)
	d.producedSamples = uint64(frameEnd) //nolint:gosec // G115: frameEnd is a non-negative sample position.

	// Emit [rawTarget, min(frameEnd, gaplessEnd)). rawTarget lies inside the
	// frame and below gaplessEnd (sampleIndex < the trimmed total), so the first
	// emitted sample is exactly the requested playable sample.
	rawTarget := frameStart + intra
	hi := frameEnd
	if d.gaplessEnd != math.MaxUint64 {
		if ge := int64(d.gaplessEnd); ge < hi { //nolint:gosec // G115: a set gaplessEnd is a real total <= math.MaxInt64.
			hi = ge
		}
	}
	if rawTarget < hi {
		skip := int(rawTarget-frameStart) * fi.Channels
		count := int(hi-rawTarget) * fi.Channels
		d.packOutput(d.sampleBuf[skip : skip+count])
	} else {
		d.pending = nil
	}

	return rawTarget - int64(d.gaplessStart), nil //nolint:gosec // G115: gaplessStart is the small LAME delay plus the 529-sample decoder delay.
}

// seekToEnd positions the decoder at end-of-stream so the next read is io.EOF,
// and returns total (the sample count to report as landed). It clears any
// latched error, since reaching a valid end is a successful seek.
func (d *Decoder) seekToEnd(total int64) (int64, error) {
	d.done = true
	d.pending = nil
	d.err = nil
	return total, nil
}

// seekFailed latches err and returns it, so a Read or WriteTo after a seek that
// failed part-way surfaces the error rather than resuming from an indeterminate
// position. The state is recoverable: a later successful reseek clears d.err.
func (d *Decoder) seekFailed(err error) (int64, error) {
	d.err = err
	d.pending = nil
	return 0, err
}

// MPEG audio Layer III bitrate tables, in bits per second, indexed by the
// header's 4-bit bitrate field. Index 0 (free format) and 15 (reserved) are 0
// and reported undecodable by frameLength. (ISO/IEC 11172-3 for MPEG1,
// 13818-3 for MPEG2/2.5.)
var (
	bitrateV1L3 = [16]int{0, 32000, 40000, 48000, 56000, 64000, 80000, 96000, 112000, 128000, 160000, 192000, 224000, 256000, 320000, 0}
	bitrateV2L3 = [16]int{0, 8000, 16000, 24000, 32000, 40000, 48000, 56000, 64000, 80000, 96000, 112000, 128000, 144000, 160000, 0}
	// sampleRates is indexed by [version][sample-rate field]; version 1 is
	// reserved (all zero). Versions: 0=MPEG2.5, 2=MPEG2, 3=MPEG1.
	sampleRates = [4][4]int{
		{11025, 12000, 8000, 0},
		{0, 0, 0, 0},
		{22050, 24000, 16000, 0},
		{44100, 48000, 32000, 0},
	}
)

// frameLength derives the total byte length of the MPEG-1/2/2.5 Layer III frame
// whose four-byte header begins h, from the version, bitrate, sample-rate, and
// padding fields. It reports ok=false for a bad sync word, a non-Layer-III or
// reserved-version frame, or the free-format/reserved bitrate or sample-rate
// index, none of which can be sized from the header alone. Only the four header
// bytes are read; the frame body is not needed, which is what makes the seek
// frame walk far cheaper than decoding.
func frameLength(h []byte) (int, bool) {
	if len(h) < 4 || h[0] != 0xFF || h[1]&0xE0 != 0xE0 {
		return 0, false
	}
	version := (h[1] >> 3) & 0x03 // 0=MPEG2.5, 1=reserved, 2=MPEG2, 3=MPEG1
	layer := (h[1] >> 1) & 0x03   // 1=Layer III
	if layer != 0x01 || version == 0x01 {
		return 0, false
	}
	bitrateIdx := (h[2] >> 4) & 0x0F
	sampleIdx := (h[2] >> 2) & 0x03
	padding := int((h[2] >> 1) & 0x01)

	mpeg1 := version == 0x03
	var bitrate int
	if mpeg1 {
		bitrate = bitrateV1L3[bitrateIdx]
	} else {
		bitrate = bitrateV2L3[bitrateIdx]
	}
	sampleRate := sampleRates[version][sampleIdx]
	if bitrate == 0 || sampleRate == 0 {
		return 0, false
	}

	// Layer III frame length in bytes: (samples-per-frame / 8) * bitrate /
	// sampleRate + padding, i.e. 144*bitrate/sampleRate for MPEG1 (1152 samples)
	// and 72*bitrate/sampleRate for MPEG2/2.5 (576), with bitrate in bits/sec.
	var length int
	if mpeg1 {
		length = 144*bitrate/sampleRate + padding
	} else {
		length = 72*bitrate/sampleRate + padding
	}
	if length <= 4 {
		return 0, false
	}
	return length, true
}
