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

// config holds decoder options. No options are defined yet; WithF32 (native
// float32 passthrough) arrives in a later task and flips a field here.
type config struct{}

// Option configures a Decoder.
type Option func(*config)

// Decoder decodes an MP3 stream into interleaved little-endian S16 PCM. It
// implements io.Reader and io.WriterTo, mirroring the sibling pcm.Decoder
// shape (NewDecoder, Reset, Info, Read, WriteTo).
//
// A Decoder is not safe for concurrent use.
type Decoder struct {
	br   *bufio.Reader
	dec  *mp3.Decoder
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
	// once per Reset, on the very first Layer III frame encountered.
	xing        *xingHeader
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

	d.frameBuf = d.frameBuf[:0]
	d.pending = nil
	d.readErr = nil
	d.done = false
	d.err = nil
	d.info = Info{} // cleared up front so a failed Reset leaves no stale metadata
	d.xing = nil
	d.xingChecked = false
	d.gaplessStart = 0
	d.gaplessEnd = math.MaxUint64 // no tail trim until a LAME tag with a known total arms one
	d.producedSamples = 0
	d.initialOffset = 0
	d.audioStart = 0
	d.firstFrameBytes = 0

	// Anchor the CBR byte-span accounting on the source's true starting
	// position, captured before any read, so a pre-positioned file or an
	// io.SectionReader is measured correctly. A SectionReader reports 0 here
	// (its offsets are relative to the section), which is exactly right.
	if seeker, ok := r.(io.Seeker); ok {
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

	// No Xing tag frame count to derive TotalSamples from: fall back to a
	// CBR estimate from the audio byte length, when the source allows it.
	if d.xing == nil || d.xing.frames == 0 {
		d.estimateCBRDuration(r)
	}
	return nil
}

// Info returns the stream configuration. Valid after NewDecoder or Reset.
func (d *Decoder) Info() Info { return d.info }

// fill tops frameBuf up toward maxFrameBytes from the underlying reader, so a
// following mp3.DecodeFrame always sees a complete frame while the source
// still has bytes. It records the first read error (including io.EOF) in
// readErr and then does nothing on later calls.
func (d *Decoder) fill() {
	if d.readErr != nil || len(d.frameBuf) >= maxFrameBytes {
		return
	}
	if cap(d.frameBuf) < maxFrameBytes {
		grown := make([]byte, len(d.frameBuf), maxFrameBytes)
		copy(grown, d.frameBuf)
		d.frameBuf = grown
	}
	zeroReads := 0
	for len(d.frameBuf) < maxFrameBytes {
		n, err := d.br.Read(d.frameBuf[len(d.frameBuf):maxFrameBytes])
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
	for {
		d.fill()
		if len(d.frameBuf) == 0 {
			return d.finish() // reader exhausted, nothing buffered
		}

		n, fi, err := d.dec.DecodeFrame(d.frameBuf, d.sampleBuf)
		if err != nil {
			d.err = err // e.g. mp3.ErrUnsupported for a Layer I/II frame
			return d.err
		}
		if fi.FrameBytes == 0 {
			// No progress possible from the buffered bytes: with a full window
			// this is a partial/garbage frame at end of input. finish surfaces
			// a mid-stream read failure here too, rather than reporting the
			// truncated tail of a failed source as a clean end.
			return d.finish()
		}

		if !d.xingChecked && fi.Layer == 3 {
			d.xingChecked = true
			if xh, ok := parseXing(d.frameBuf[:fi.FrameBytes], fi.SampleRate, fi.Channels); ok {
				d.applyXing(xh, fi.SampleRate)
				// A LAME extension may follow the Xing fields; its encoder
				// delay/padding arm the gapless trim. applyXing has already set
				// the pre-trim TotalSamples applyLAME reads.
				if delay, padding, lok := parseLAME(d.frameBuf[:fi.FrameBytes], xh.lameStart); lok {
					d.applyLAME(delay, padding)
				}
				// The tag frame is not audio: exclude its bytes from the CBR
				// audio-byte span so the Step-3b estimate does not count it.
				d.audioStart += int64(fi.FrameBytes)
				d.consume(fi.FrameBytes)
				continue // tag frame: metadata only, never emitted as audio
			}
		}

		d.consume(fi.FrameBytes)
		if n == 0 {
			continue // valid-but-skippable frame; keep looking for audio
		}
		if d.info.SampleRate == 0 {
			// First audio frame establishes the stream configuration.
			d.info.SampleRate = fi.SampleRate
			d.info.Channels = fi.Channels
			d.firstFrameBytes = fi.FrameBytes
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
// The span is end minus audioStart, and audioStart is the absolute offset of
// the first audio frame (initialOffset + ID3v2 + any Xing tag frame), so the
// estimate is correct even when the source did not start at byte 0.
//
// The probe seeks r to measure its length and then restores r's exact prior
// read position, so it does not disturb decoding: bufio and frameBuf already
// hold everything read so far, and reading resumes from precisely where r
// was left.
func (d *Decoder) estimateCBRDuration(r io.Reader) {
	seeker, ok := r.(io.Seeker)
	if !ok || d.firstFrameBytes <= 0 {
		return
	}
	cur, err := seeker.Seek(0, io.SeekCurrent)
	if err != nil {
		return
	}
	end, err := seeker.Seek(0, io.SeekEnd)
	if err != nil {
		return
	}
	if _, err := seeker.Seek(cur, io.SeekStart); err != nil {
		return
	}

	audioBytes := end - d.audioStart
	if audioBytes <= 0 {
		return
	}
	frames := audioBytes / int64(d.firstFrameBytes)
	if frames <= 0 {
		return
	}
	d.info.TotalSamples = uint64(frames) * uint64(samplesPerFrame(d.info.SampleRate))
}

// packOutput quantizes the given interleaved float32 samples (a sub-slice of
// sampleBuf, already narrowed to the gapless keep-window) to S16 little-endian
// bytes and points pending at them. It is the single place that knows the
// output byte format, so a later task can branch here for native float32
// passthrough (WithF32) without touching the decode loop.
func (d *Decoder) packOutput(samples []float32) {
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

// Read fills p with interleaved little-endian S16 PCM. It returns (0, io.EOF)
// at a clean stream end and returns any latched terminal error on every later
// call. Short reads across a p that is not a whole number of samples are fine:
// pending is byte-granular, so a partial sample simply resumes on the next
// call.
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
