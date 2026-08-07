package pcm

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	mp3 "github.com/tphakala/go-mp3"
)

var (
	_ io.Reader   = (*Decoder)(nil)
	_ io.WriterTo = (*Decoder)(nil)
)

const (
	// maxFrameBytes is a safe upper bound on a single MP3 frame. A real
	// MPEG-1/2/2.5 Layer III frame tops out near 1441 bytes and minimp3 caps a
	// free-format frame at 2304; 2880 keeps a comfortable margin above both.
	// The decode loop keeps at least this many bytes buffered (when the source
	// still has them) so mp3.DecodeFrame always sees a complete frame. Task 6
	// turns this into the retained-tail size of the bounded resync window.
	maxFrameBytes = 2880
	// maxSamplesPerFrame is the Layer III samples-per-channel ceiling.
	maxSamplesPerFrame = 1152
	// maxChannels is the largest channel count this decoder produces.
	maxChannels = 2
	// bytesPerS16Sample is the size of one interleaved S16 sample.
	bytesPerS16Sample = 2
	// bytesPerF32Sample is the size of one interleaved float32 sample
	// (WithF32 output).
	bytesPerF32Sample = 4
	// readerBufSize backs the bufio.Reader; large enough that the 10-byte
	// ID3v2 and 4-byte frame-header peeks never hit bufio.ErrBufferFull.
	readerBufSize = 1 << 16
	// maxZeroReads bounds a run of (0, nil) reads inside fill. io.Reader
	// permits "nothing happened" returns, and a reader stuck returning them
	// would otherwise spin fill forever; after this many in a row fill latches
	// io.ErrNoProgress, exactly as bufio.Reader does. Surfacing a real error
	// (rather than leaving readErr nil) is essential: a nil readErr would let
	// finish mistake the give-up for a clean end and silently truncate a
	// merely-bursty source.
	maxZeroReads = 100

	// resyncWindowBytes is the search window mp3.DecodeFrame is handed while
	// recovering from a run of mid-stream garbage. It is far larger than a
	// single frame (maxFrameBytes) so DecodeFrame can skip the junk AND confirm
	// the next real frame within one window: confirmation needs the recovered
	// frame's body plus a following header, so a one-frame window could never
	// resync across garbage. The normal, non-garbage decode stays on the small
	// maxFrameBytes window, so this larger buffer is allocated only when a
	// stream actually contains garbage.
	resyncWindowBytes = 16 * 1024
	// frameHeaderSize is the MPEG frame header length in bytes: the sync word
	// plus the version/layer/bitrate/sample-rate fields frameLength reads.
	frameHeaderSize = 4
	// resyncRetainBytes is the tail kept when a full resync window turns out to
	// be all garbage. An unconfirmable real frame (one DecodeFrame could not
	// confirm because its body, plus the following header matchFrame needs to
	// confirm it, overran the window) can have its own header as low as
	// resyncWindowBytes - maxFrameBytes - frameHeaderSize: a max-size frame plus
	// that following header just reaches the window end. Retaining
	// maxFrameBytes + frameHeaderSize keeps the whole of any such header across
	// the discard boundary; retaining only maxFrameBytes could chop a header in
	// the last frameHeaderSize bytes and drop the frame during recovery. It stays
	// strictly below resyncWindowBytes, so discarding (window - retain) always
	// makes forward progress and the resync can never spin.
	resyncRetainBytes = maxFrameBytes + frameHeaderSize
	// resyncBudgetBytes bounds the total bytes skipped while hunting for the
	// next frame sync. Past it the stream is declared corrupt rather than
	// scanned without limit, so a pure-garbage or badly-damaged source fails
	// fast instead of streaming megabytes of junk.
	resyncBudgetBytes = 128 * 1024
)

// Info describes the decoded stream. SampleRate and Channels are populated at
// construction from the first audio frame; the remaining fields are filled by
// later tasks (Xing/VBRI/LAME parsing) and stay at their zero values here.
type Info struct {
	SampleRate     int    // samples per second
	Channels       int    // number of channels (1 or 2)
	TotalSamples   uint64 // per channel; 0 when unknown (no length tag)
	EncoderDelay   int    // LAME encoder delay in samples; 0 if absent
	EncoderPadding int    // LAME encoder padding in samples; 0 if absent
}

// Duration reports the stream's playing time, or 0 when TotalSamples is
// unknown. The whole-seconds-plus-remainder split keeps the arithmetic exact
// and overflow-safe for stream lengths far beyond any real file.
func (i Info) Duration() time.Duration {
	const nsPerSecond = int64(time.Second)
	if i.TotalSamples == 0 || i.TotalSamples > math.MaxInt64 ||
		i.SampleRate <= 0 || int64(i.SampleRate) > math.MaxInt64/nsPerSecond {
		return 0
	}
	//nolint:gosec // G115: the guard above rejects every TotalSamples above math.MaxInt64.
	samples, rate := int64(i.TotalSamples), int64(i.SampleRate)
	whole, rem := samples/rate, samples%rate
	if whole > math.MaxInt64/nsPerSecond {
		return 0
	}
	ns, remNs := whole*nsPerSecond, rem*nsPerSecond/rate
	if ns > math.MaxInt64-remNs {
		return 0
	}
	return time.Duration(ns + remNs)
}

// config holds decoder options. f32 selects native float32 output
// (WithF32) over the default S16 conversion.
type config struct {
	f32 bool
}

// Option configures a Decoder.
type Option func(*config)

// Decoder decodes an MP3 stream into interleaved little-endian PCM: S16 (2
// bytes/sample) by default, or native float32 (4 bytes/sample) with
// WithF32. It implements io.Reader and io.WriterTo, mirroring the sibling
// pcm.Decoder shape (NewDecoder, Reset, Info, Read, WriteTo).
//
// A Decoder is not safe for concurrent use.
type Decoder struct {
	br   *bufio.Reader
	dec  *mp3.Decoder
	cfg  config
	info Info

	// frameBuf holds raw MP3 bytes read ahead of the decoder but not yet
	// consumed. The decode loop tops it up toward maxFrameBytes before each
	// mp3.DecodeFrame call (fill) and compacts the unconsumed tail forward in
	// place after each frame (consume), so its backing array is allocated once
	// and reused. Task 6 adds the sliding-window cap and retained-tail resync
	// on top of this same field.
	frameBuf []byte
	readErr  error // sticky error from filling frameBuf (io.EOF at a clean end)

	sampleBuf []float32 // reused mp3.DecodeFrame output (interleaved float32)
	s16Buf    []int16   // reused S16 quantization scratch
	outBuf    []byte    // reused packed output bytes; pending is a window into it
	pending   []byte    // decoded output not yet handed to Read/WriteTo

	// xing is the parsed Xing/Info tag from the stream's first frame, or nil
	// when that frame carried audio instead (or the tag was malformed).
	// xingChecked marks that the check has already run, so it fires at most
	// once per Reset, on the very first Layer III frame encountered. vbri is the
	// Fraunhofer VBRI tag parsed from that same first frame when it was not a
	// Xing/Info tag; like xing it is metadata, never emitted as audio, and it
	// supplies the frame count for a VBRI-tagged stream. (SeekToSample lands via
	// the frameOffsets header walk and never reads the VBRI TOC.)
	xing        *xingHeader
	vbri        *vbriHeader
	xingChecked bool

	// Gapless-trim window, in per-channel sample positions of the raw decoded
	// stream (before any trim). gaplessStart is the LAME encoder delay: samples
	// before it are dropped from the head (this already covers the decoder's own
	// ~528-sample filterbank delay, which LAME folds into its delay value, so no
	// separate offset is added). gaplessEnd is the first sample to drop from the
	// tail (pre-trim total minus the LAME padding); it stays math.MaxUint64 when
	// the total is unknown, meaning no tail trim. producedSamples counts the
	// per-channel samples produced by real audio frames so far, giving each new
	// frame its position in that raw timeline. All three are set by applyLAME
	// and reset per Reset.
	gaplessStart    uint64
	gaplessEnd      uint64
	producedSamples uint64

	// initialOffset is the source's absolute byte position when Reset ran,
	// captured before any read when the source is an io.Seeker (0 otherwise).
	// audioStart is the absolute offset where the first real audio frame
	// begins: initialOffset plus the ID3v2 tag and any consumed Xing/Info tag
	// frame. firstFrameBytes is the FrameBytes of the first frame emitted as
	// audio. All three feed the CBR duration fallback (Step 3b) when no Xing
	// tag supplies a frame count; anchoring on initialOffset makes the byte
	// span correct for a pre-positioned file or an io.SectionReader, not just
	// a source that starts at 0.
	initialOffset   int64
	audioStart      int64
	firstFrameBytes int

	// firstFrameSeen marks that the first confirmed (non-tag) frame has been
	// accounted for, so audioStart and firstFrameBytes are captured from it
	// exactly once, whether or not it yielded samples (a reservoir-dependent
	// first frame decodes with n == 0). See decodeNextFrame.
	firstFrameSeen bool

	// src is the raw source retained from Reset so SeekToSample can re-seek it
	// (bufio.Reader alone cannot). seekable records whether src implements
	// io.Seeker; SeekToSample returns ErrSeekUnsupported when it does not.
	src      io.Reader
	seekable bool

	done bool
	err  error // latched terminal error; cleared only by Reset
}

// NewDecoder reads enough of r to establish the stream configuration and
// returns a Decoder with Info populated. A nil reader, or a stream with no
// decodable MP3 frame, returns an error.
func NewDecoder(r io.Reader, opts ...Option) (*Decoder, error) {
	d := &Decoder{}
	if err := d.Reset(r, opts...); err != nil {
		return nil, err
	}
	return d, nil
}

// Reset rebinds the Decoder to a new source, reusing the internal buffers and
// the wrapped mp3.Decoder so a caller decoding many streams pays no per-stream
// allocation after warm-up. It clears any latched error and re-establishes
// Info from the new stream's first audio frame.
func (d *Decoder) Reset(r io.Reader, opts ...Option) error {
	if r == nil {
		return fmt.Errorf("go-mp3/pcm: nil reader")
	}
	var c config
	for _, o := range opts {
		o(&c)
	}
	d.cfg = c

	if d.br == nil {
		d.br = bufio.NewReaderSize(r, readerBufSize)
	} else {
		d.br.Reset(r)
	}
	if d.dec == nil {
		d.dec = mp3.NewDecoder()
	} else {
		d.dec.Reset()
	}
	if d.sampleBuf == nil {
		d.sampleBuf = make([]float32, maxSamplesPerFrame*maxChannels)
	}

	d.src = r
	d.seekable = false
	d.frameBuf = d.frameBuf[:0]
	d.pending = nil
	d.readErr = nil
	d.done = false
	d.err = nil
	d.info = Info{} // cleared up front so a failed Reset leaves no stale metadata
	d.xing = nil
	d.vbri = nil
	d.xingChecked = false
	d.gaplessStart = 0
	d.gaplessEnd = math.MaxUint64 // no tail trim until a LAME tag with a known total arms one
	d.producedSamples = 0
	d.initialOffset = 0
	d.audioStart = 0
	d.firstFrameBytes = 0
	d.firstFrameSeen = false

	// Anchor the CBR byte-span accounting on the source's true starting
	// position, captured before any read, so a pre-positioned file or an
	// io.SectionReader is measured correctly. A SectionReader reports 0 here
	// (its offsets are relative to the section), which is exactly right.
	if seeker, ok := r.(io.Seeker); ok {
		d.seekable = true
		if cur, serr := seeker.Seek(0, io.SeekCurrent); serr == nil {
			d.initialOffset = cur
		}
	}

	skipped, err := skipID3v2(d.br)
	if err != nil {
		d.err = err
		return d.err
	}
	d.audioStart = d.initialOffset + skipped

	// Eagerly decode the first audio frame so Info() is valid immediately and
	// its samples are queued for the first Read. decodeNextFrame checks the
	// very first Layer III frame for a Xing/Info tag and, if found, skips it
	// (never emitting its samples) and continues to the first real frame.
	if err := d.decodeNextFrame(); err != nil {
		if errors.Is(err, io.EOF) {
			d.err = fmt.Errorf("%w: no MP3 frame found", mp3.ErrCorruptStream)
			return d.err
		}
		return d.err // decodeNextFrame already latched d.err
	}

	// No Xing or VBRI tag frame count to derive TotalSamples from: fall back to
	// a CBR estimate from the audio byte length, when the source allows it.
	if (d.xing == nil || d.xing.frames == 0) && (d.vbri == nil || d.vbri.frames == 0) {
		d.estimateCBRDuration(r)
	}
	return nil
}

// Info returns the stream configuration. Valid after NewDecoder or Reset.
func (d *Decoder) Info() Info { return d.info }

// fill tops frameBuf up toward maxFrameBytes, the small window a steady decode
// needs. See fillTo.
func (d *Decoder) fill() { d.fillTo(maxFrameBytes) }

// fillTo tops frameBuf up toward target bytes from the underlying reader, so a
// following mp3.DecodeFrame always sees a complete frame while the source
// still has bytes. The steady decode passes maxFrameBytes; the mid-stream
// resync passes the larger resyncWindowBytes so DecodeFrame has room to skip a
// garbage run and still confirm the next frame. It records the first read error
// (including io.EOF) in readErr and then does nothing on later calls.
func (d *Decoder) fillTo(target int) {
	if d.readErr != nil || len(d.frameBuf) >= target {
		return
	}
	if cap(d.frameBuf) < target {
		grown := make([]byte, len(d.frameBuf), target)
		copy(grown, d.frameBuf)
		d.frameBuf = grown
	}
	zeroReads := 0
	for len(d.frameBuf) < target {
		n, err := d.br.Read(d.frameBuf[len(d.frameBuf):target])
		d.frameBuf = d.frameBuf[:len(d.frameBuf)+n]
		if err != nil {
			d.readErr = err
			return
		}
		if n == 0 {
			// A (0, nil) read made no progress. Bound a run of them so a
			// pathological reader cannot spin this loop forever, and latch
			// io.ErrNoProgress (as bufio.Reader does) so the give-up surfaces
			// as a real error via finish. Leaving readErr nil here would let
			// finish report it as a clean io.EOF, silently truncating a source
			// that had only stalled in a burst.
			zeroReads++
			if zeroReads >= maxZeroReads {
				d.readErr = io.ErrNoProgress
				return
			}
			continue
		}
		zeroReads = 0
	}
}

// consume drops the first n bytes of frameBuf by compacting the unconsumed
// tail to the front of the same backing array, preserving its full capacity.
// Reslicing forward instead (frameBuf = frameBuf[n:]) would shrink the
// capacity below maxFrameBytes, forcing the next fill to re-allocate the whole
// buffer every frame; compaction keeps the steady-state decode allocation-free
// and is the same retain-the-tail operation Task 6's resync window needs.
func (d *Decoder) consume(n int) {
	remaining := copy(d.frameBuf, d.frameBuf[n:])
	d.frameBuf = d.frameBuf[:remaining]
}

// finish reports the terminal outcome once no further frames can be produced
// from the buffered bytes. A source that failed mid-stream (a read error other
// than a clean io.EOF) surfaces that error, latched, so a genuine I/O failure
// is never silently reported as a clean end. A clean exhaustion marks the
// decoder done and returns io.EOF.
func (d *Decoder) finish() error {
	if d.readErr != nil && !errors.Is(d.readErr, io.EOF) {
		d.err = d.readErr
		return d.err
	}
	d.done = true
	return io.EOF
}

// decodeNextFrame advances the stream to the next frame that yields audio,
// packs its samples into the output buffer, and points pending at them.
// Valid-but-skippable frames (resync, non-audio, trailing junk) are consumed
// and skipped. It returns io.EOF at a clean end and latches d.err on a decode
// error (e.g. an unsupported layer).
//
// The very first Layer III frame it sees (per Reset call) is checked for a
// Xing/Info tag before being consumed. A tag frame is metadata, not audio:
// applyXing records it and the loop continues to the next frame without ever
// packing its (meaningless) decoded samples into pending.
func (d *Decoder) decodeNextFrame() error {
	if d.done {
		return io.EOF
	}
	// window is the byte span handed to DecodeFrame: the small maxFrameBytes for
	// a steady decode, widened to resyncWindowBytes while skipping garbage.
	// garbageSkipped is the running total of bytes discarded during a resync run,
	// bounded by resyncBudgetBytes. Both reset the moment a real frame is found.
	window, garbageSkipped := maxFrameBytes, 0
	for {
		d.fillTo(window)
		if len(d.frameBuf) == 0 {
			return d.finish() // reader exhausted, nothing buffered
		}

		n, fi, err := d.dec.DecodeFrame(d.frameBuf, d.sampleBuf)
		if err != nil {
			// A Layer I/II frame yields mp3.ErrUnsupported. This is terminal by
			// choice: skipping and continuing would mask a malformed or mixed
			// stream, so the decoder stops here rather than dropping frames.
			d.err = err
			return d.err
		}
		if fi.FrameBytes == 0 || (n == 0 && fi.Layer == 0) {
			// No confirmable frame in the buffered window: mid-stream garbage, a
			// truncated final frame, or a clean end of trailing non-frame bytes.
			var proceed bool
			if window, garbageSkipped, proceed, err = d.handleNoFrame(window, garbageSkipped); err != nil {
				return err
			}
			if proceed {
				continue // buffer advanced; retry the (possibly widened) window
			}
			return d.finish() // clean end (or a latched read error)
		}

		// A confirmable frame (audio, or a resync frame that yields no samples):
		// any garbage run is over, so return to the steady, small window.
		window, garbageSkipped = maxFrameBytes, 0

		if !d.xingChecked && fi.Layer == 3 {
			d.xingChecked = true
			// The frame's own bytes start at fi.FrameOffset, not index 0: with
			// leading garbage (or bytes DecodeFrame skipped to resync) the header
			// is offset, so every tag parse must slice from there.
			frame := d.frameBuf[fi.FrameOffset:fi.FrameBytes]
			if xh, ok := parseXing(frame, fi.SampleRate, fi.Channels); ok {
				d.applyXing(xh, fi.SampleRate)
				// A LAME extension may follow the Xing fields; its encoder
				// delay/padding arm the gapless trim. applyXing has already set
				// the pre-trim TotalSamples applyLAME reads.
				if delay, padding, lok := parseLAME(frame, xh.lameStart); lok {
					d.applyLAME(delay, padding)
				}
				// The tag frame is not audio: exclude its bytes from the CBR
				// audio-byte span so the Step-3b estimate does not count it.
				d.audioStart += int64(fi.FrameBytes)
				d.consume(fi.FrameBytes)
				continue // tag frame: metadata only, never emitted as audio
			}
			// A Fraunhofer VBRI tag sits at a fixed offset (36) rather than after
			// the side info, so it is a distinct check. Like a Xing tag frame it
			// carries metadata, not audio, and is excluded the same way.
			if vh, ok := parseVBRI(frame); ok {
				d.applyVBRI(vh, fi.SampleRate)
				d.audioStart += int64(fi.FrameBytes)
				d.consume(fi.FrameBytes)
				continue // VBRI tag frame: metadata only, never emitted as audio
			}
		}

		// Account for the first confirmed (non-tag) frame exactly once, BEFORE the
		// n == 0 skip below consumes it. fi.FrameOffset is the leading garbage
		// DecodeFrame skipped to resync onto this frame, and fi.FrameBytes -
		// fi.FrameOffset is the frame's own size (a valid CBR divisor). audioStart
		// must point at this frame's start so the T4 seek header-walk begins on a
		// real sync word, not in the garbage where frameLength fails. This must run
		// here, not in the SampleRate block, because a reservoir-dependent first
		// frame decodes with n == 0 (common when frame 0 was stripped, or right
		// after a resync): the n == 0 continue would otherwise consume it and drop
		// its offset before SampleRate is ever established, leaving audioStart in
		// the garbage. Such a frame still counts as a frame in the stream, so
		// audioStart points at it and later frames are contiguous (FrameOffset ==
		// 0). Tag frames continue above; normal streams have FrameOffset == 0, so
		// both updates are no-ops.
		if !d.firstFrameSeen {
			d.firstFrameSeen = true
			d.audioStart += int64(fi.FrameOffset)
			d.firstFrameBytes = fi.FrameBytes - fi.FrameOffset
		}

		d.consume(fi.FrameBytes)
		if n == 0 {
			continue // valid-but-skippable frame; keep looking for audio
		}
		if d.info.SampleRate == 0 {
			// First audio frame establishes the stream configuration.
			d.info.SampleRate = fi.SampleRate
			d.info.Channels = fi.Channels
		}

		// Place this frame in the raw (pre-trim) per-channel timeline and
		// intersect it with the gapless keep-window [gaplessStart, gaplessEnd).
		frameStart := d.producedSamples
		frameEnd := frameStart + uint64(n)
		d.producedSamples = frameEnd

		if frameEnd <= d.gaplessStart {
			continue // entirely within the head trim: emit nothing, keep decoding
		}
		if frameStart >= d.gaplessEnd {
			// Entirely past the tail-trim boundary. No later frame can survive
			// either, so end the stream cleanly.
			d.done = true
			return io.EOF
		}
		lo, hi := frameStart, frameEnd
		if lo < d.gaplessStart {
			lo = d.gaplessStart
		}
		if hi > d.gaplessEnd {
			hi = d.gaplessEnd
		}
		if lo >= hi {
			// Degenerate window (e.g. a malformed tag with delay >= end): no
			// playable audio remains from here on.
			d.done = true
			return io.EOF
		}
		skip := int(lo-frameStart) * fi.Channels
		count := int(hi-lo) * fi.Channels
		d.packOutput(d.sampleBuf[skip : skip+count])
		return nil
	}
}

// handleNoFrame decides what to do when mp3.DecodeFrame found no confirmable
// frame in the buffered window (mid-stream garbage, a truncated final frame, or
// a clean end). It returns the possibly-widened resync window, the running total
// of garbage bytes skipped, whether the caller should retry, and a terminal
// error.
//
// While the source still has bytes, the miss is treated as recoverable garbage:
// the search window widens to resyncWindowBytes (giving DecodeFrame room to skip
// the junk and confirm the next frame in one pass), and once a full window is
// still all garbage the confirmed-garbage head is discarded while a frame-sized
// tail is retained, so a frame bridging the discard boundary is not lost. It
// gives up with mp3.ErrCorruptStream only after skipping more than
// resyncBudgetBytes, so a pure-garbage source fails fast rather than scanning
// without limit. At true EOF a leftover whose frame header overruns the
// remaining bytes is a truncated frame (mp3.ErrCorruptStream); any other
// leftover (an ID3v1 tag, trailing padding) is a clean end, left for finish.
func (d *Decoder) handleNoFrame(window, skipped int) (newWindow, newSkipped int, proceed bool, err error) {
	if d.readErr == nil {
		window = resyncWindowBytes
		if len(d.frameBuf) < resyncWindowBytes {
			return window, skipped, true, nil // read more before judging this garbage
		}
		discard := len(d.frameBuf) - resyncRetainBytes
		skipped += discard
		if skipped > resyncBudgetBytes {
			d.err = fmt.Errorf("%w: no frame sync within %d bytes", mp3.ErrCorruptStream, skipped)
			return window, skipped, false, d.err
		}
		d.consume(discard)
		return window, skipped, true, nil
	}
	if errors.Is(d.readErr, io.EOF) && truncatedFrame(d.frameBuf) {
		d.err = wrapTruncation()
		return window, skipped, false, d.err
	}
	d.frameBuf = d.frameBuf[:0]
	return window, skipped, false, nil
}

// truncatedFrame reports whether buf holds a valid MPEG frame header whose
// declared length runs past the bytes present, i.e. a frame cut short at end of
// input. It returns the verdict at the first header frameLength accepts, so
// trailing non-frame bytes (an ID3v1 tag, padding) report false and a clean end
// is never mistaken for corruption, while a complete-but-unconfirmable final
// frame (its bytes all present) likewise reports false.
func truncatedFrame(buf []byte) bool {
	for i := range len(buf) {
		if length, ok := frameLength(buf[i:]); ok {
			return i+length > len(buf)
		}
	}
	return false
}

// wrapTruncation reports a stream that ends inside a frame it promised as
// mp3.ErrCorruptStream. It is only ever called at a confirmed truncation (the
// io.EOF branch of handleNoFrame), so it takes no argument: the outcome is
// always the corrupt-stream sentinel wrapping a fixed message.
func wrapTruncation() error {
	return fmt.Errorf("%w: truncated frame at end of stream", mp3.ErrCorruptStream)
}

// applyXing records a detected Xing/Info tag's metadata and, when its frame
// count is present, derives TotalSamples from it. The LAME/Xing de-facto
// convention counts real audio frames only, excluding the tag frame itself
// (verified against several fixtures' raw header bytes: the field exactly
// equals the on-disk frame count minus the tag frame, with no further
// adjustment needed), so no further arithmetic is needed here beyond the
// samples-per-frame multiply. When the flag is absent (or present but
// zero), TotalSamples is left for Reset's CBR fallback.
func (d *Decoder) applyXing(xh *xingHeader, sampleRate int) {
	d.xing = xh
	if xh.frames == 0 {
		return
	}
	d.info.TotalSamples = uint64(xh.frames) * uint64(samplesPerFrame(sampleRate))
}

// applyVBRI records a detected Fraunhofer VBRI tag and, when its frame count is
// present and no length was established yet, derives TotalSamples from it (audio
// frames times samples-per-frame, the tag frame already excluded). Only the
// frame count is used; SeekToSample lands via the frameOffsets header walk and
// never consults the VBRI TOC. A VBRI stream carries no LAME gapless extension,
// so no head/tail trim is armed here.
func (d *Decoder) applyVBRI(vh *vbriHeader, sampleRate int) {
	d.vbri = vh
	if d.info.TotalSamples == 0 && vh.frames > 0 {
		d.info.TotalSamples = uint64(vh.frames) * uint64(samplesPerFrame(sampleRate))
	}
}

// applyLAME records a detected LAME extension's encoder delay and padding and
// arms the gapless-trim window. The head trim drops the first delay
// samples-per-channel; the tail trim drops the last padding samples-per-channel
// at the true end, which needs the stream's total length. It reads the pre-trim
// total from d.info.TotalSamples as applyXing set it (frames * samplesPerFrame).
//
// When that total is known, applyLAME sets gaplessEnd = total - padding and
// reduces TotalSamples by delay+padding, so Duration reflects the playable
// length and the emitted count equals TotalSamples. When the total is unknown
// (no frame count), only the head is trimmed (gaplessEnd stays open) and
// TotalSamples is left at 0 for Reset's CBR fallback to decide. Negative inputs
// (a malformed field) are ignored.
func (d *Decoder) applyLAME(delay, padding int) {
	if delay < 0 || padding < 0 {
		return
	}
	d.info.EncoderDelay = delay
	d.info.EncoderPadding = padding
	d.gaplessStart = uint64(delay)

	preTrim := d.info.TotalSamples
	if preTrim == 0 {
		return // total unknown: head-only trim, tail left open, TotalSamples 0
	}

	pad := uint64(padding)
	if pad > preTrim {
		pad = preTrim
	}
	d.gaplessEnd = preTrim - pad

	trim := uint64(delay) + uint64(padding)
	if trim > preTrim {
		d.info.TotalSamples = 0
	} else {
		d.info.TotalSamples = preTrim - trim
	}
}

// estimateCBRDuration fills Info.TotalSamples from the audio byte length
// when no Xing/VBRI tag supplied a frame count. It requires the original
// source r to be an io.Seeker; anything else leaves TotalSamples at 0
// (unknowable without a full scan).
//
// This is a CBR ESTIMATE: it assumes every audio frame is the same size as
// the first, so the audio byte span divides evenly by firstFrameBytes into a
// frame count. That holds for CBR streams and is only a guess for VBR without
// a tag; it is used solely as the fallback when no usable frame count exists.
// The span excludes non-audio bytes at both ends: it is end, less any trailing
// ID3v1 tag, minus audioStart, where audioStart is the absolute offset of the
// first audio frame past the ID3v2 header, any Xing/VBRI tag frame, and any
// leading garbage skipped to resync. So the estimate is correct even when the
// source did not start at byte 0 and even with metadata wrapped around the
// audio.
//
// The probe seeks r to measure its length and then restores r's exact prior
// read position, so it does not disturb decoding: bufio and frameBuf already
// hold everything read so far, and reading resumes from precisely where r
// was left.
func (d *Decoder) estimateCBRDuration(r io.Reader) {
	// r is typed io.Reader, so implementing io.Seeker is equivalent to
	// implementing io.ReadSeeker; assert the latter once and reuse it for both
	// the length probe and the ID3v1 trailer read.
	rs, ok := r.(io.ReadSeeker)
	if !ok || d.firstFrameBytes <= 0 || d.info.TotalSamples != 0 {
		// A VBRI (or any) tag may already have supplied the length; never let the
		// CBR estimate overwrite a total derived from a real frame count.
		return
	}
	cur, err := rs.Seek(0, io.SeekCurrent)
	if err != nil {
		return
	}
	end, err := rs.Seek(0, io.SeekEnd)
	if err != nil {
		return
	}
	// A trailing 128-byte ID3v1 tag is metadata, not audio; exclude it so the
	// CBR frame count is not inflated by roughly one frame. The probe reads at
	// end-128 and the SeekStart below restores the read position regardless.
	trailer := id3v1TrailerBytes(rs, end)
	if _, err := rs.Seek(cur, io.SeekStart); err != nil {
		return
	}

	// A false-positive "TAG" match on a very short file (the last 128 bytes are
	// real audio that happens to start with "TAG") must not zero out the span.
	// Only exclude the trailer when audio still remains after doing so.
	if end-trailer <= d.audioStart {
		trailer = 0
	}
	audioBytes := end - trailer - d.audioStart
	if audioBytes <= 0 {
		return
	}
	frames := audioBytes / int64(d.firstFrameBytes)
	if frames <= 0 {
		return
	}
	d.info.TotalSamples = uint64(frames) * uint64(samplesPerFrame(d.info.SampleRate))
}

// packOutput packs the given interleaved float32 samples (a sub-slice of
// sampleBuf, already narrowed to the gapless keep-window) into pending, in
// the byte format d.cfg selects. It is the single place that knows the
// output byte format: everything upstream (the gapless trim, the decode
// loop, pending buffering) is unaware of the pack width or format.
func (d *Decoder) packOutput(samples []float32) {
	if d.cfg.f32 {
		d.packOutputF32(samples)
		return
	}
	d.packOutputS16(samples)
}

// packOutputS16 quantizes samples to S16 little-endian bytes (the default
// output) and points pending at them.
func (d *Decoder) packOutputS16(samples []float32) {
	ns := len(samples)
	if cap(d.s16Buf) < ns {
		d.s16Buf = make([]int16, ns)
	}
	d.s16Buf = d.s16Buf[:ns]
	convertF32toS16(d.s16Buf, samples)

	need := ns * bytesPerS16Sample
	if cap(d.outBuf) < need {
		d.outBuf = make([]byte, need)
	}
	d.outBuf = d.outBuf[:need]
	for i, v := range d.s16Buf {
		binary.LittleEndian.PutUint16(d.outBuf[i*bytesPerS16Sample:], uint16(v))
	}
	d.pending = d.outBuf
}

// packOutputF32 packs samples as native little-endian float32 bytes (the
// WithF32 output, bypassing S16 conversion entirely) and points pending at
// them.
func (d *Decoder) packOutputF32(samples []float32) {
	need := len(samples) * bytesPerF32Sample
	if cap(d.outBuf) < need {
		d.outBuf = make([]byte, need)
	}
	d.outBuf = d.outBuf[:need]
	for i, v := range samples {
		binary.LittleEndian.PutUint32(d.outBuf[i*bytesPerF32Sample:], math.Float32bits(v))
	}
	d.pending = d.outBuf
}

// Read fills p with interleaved little-endian PCM (S16 by default, or
// float32 with WithF32). It returns (0, io.EOF) at a clean stream end and
// returns any latched terminal error on every later call. Short reads across
// a p that is not a whole number of samples are fine: pending is
// byte-granular, so a partial sample simply resumes on the next call.
func (d *Decoder) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if d.err != nil {
		return 0, d.err
	}
	for len(d.pending) == 0 {
		if err := d.decodeNextFrame(); err != nil {
			return 0, err
		}
	}
	n := copy(p, d.pending)
	d.pending = d.pending[n:]
	return n, nil
}

// WriteTo drains all decoded PCM into w, implementing io.WriterTo so
// io.Copy(w, decoder) streams the whole decode in one call. A clean end
// returns (total, nil); io.EOF is swallowed per the io.WriterTo convention.
func (d *Decoder) WriteTo(w io.Writer) (int64, error) {
	if d.err != nil {
		return 0, d.err
	}
	var total int64
	if len(d.pending) > 0 {
		n, err := w.Write(d.pending)
		total += int64(n)
		d.pending = d.pending[n:]
		if err != nil {
			return total, err
		}
	}
	for {
		err := d.decodeNextFrame()
		if errors.Is(err, io.EOF) {
			return total, nil
		}
		if err != nil {
			return total, err
		}
		n, werr := w.Write(d.pending)
		total += int64(n)
		d.pending = d.pending[n:]
		if werr != nil {
			return total, werr
		}
	}
}
