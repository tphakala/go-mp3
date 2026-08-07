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
	// mp3.DecodeFrame call and reslices it forward by the bytes consumed.
	// Task 6 adds the sliding-window cap and retained-tail resync on top of
	// this same field.
	frameBuf []byte
	readErr  error // sticky error from filling frameBuf (io.EOF at a clean end)

	sampleBuf []float32 // reused mp3.DecodeFrame output (interleaved float32)
	s16Buf    []int16   // reused S16 quantization scratch
	outBuf    []byte    // reused packed output bytes; pending is a window into it
	pending   []byte    // decoded output not yet handed to Read/WriteTo

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

	if _, err := skipID3v2(d.br); err != nil {
		d.err = err
		return d.err
	}

	// Eagerly decode the first audio frame so Info() is valid immediately and
	// its samples are queued for the first Read.
	if err := d.decodeNextFrame(); err != nil {
		if errors.Is(err, io.EOF) {
			d.err = fmt.Errorf("%w: no MP3 frame found", mp3.ErrCorruptStream)
			return d.err
		}
		return d.err // decodeNextFrame already latched d.err
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
	for len(d.frameBuf) < maxFrameBytes {
		n, err := d.br.Read(d.frameBuf[len(d.frameBuf):maxFrameBytes])
		d.frameBuf = d.frameBuf[:len(d.frameBuf)+n]
		if err != nil {
			d.readErr = err
			return
		}
	}
}

// decodeNextFrame advances the stream to the next frame that yields audio,
// packs its samples into the output buffer, and points pending at them.
// Valid-but-skippable frames (resync, non-audio, trailing junk) are consumed
// and skipped. It returns io.EOF at a clean end and latches d.err on a decode
// error (e.g. an unsupported layer).
func (d *Decoder) decodeNextFrame() error {
	if d.done {
		return io.EOF
	}
	for {
		d.fill()
		if len(d.frameBuf) == 0 {
			d.done = true
			return io.EOF // reader exhausted, nothing buffered
		}

		n, fi, err := d.dec.DecodeFrame(d.frameBuf, d.sampleBuf)
		if err != nil {
			d.err = err // e.g. mp3.ErrUnsupported for a Layer I/II frame
			return d.err
		}
		if fi.FrameBytes == 0 {
			// No progress possible from the buffered bytes. With a full window
			// this means the tail is a partial/garbage frame at end of input;
			// treat it as a clean end rather than spinning.
			d.done = true
			return io.EOF
		}

		d.frameBuf = d.frameBuf[fi.FrameBytes:]
		if n == 0 {
			continue // valid-but-skippable frame; keep looking for audio
		}
		if d.info.SampleRate == 0 {
			// First audio frame establishes the stream configuration.
			d.info.SampleRate = fi.SampleRate
			d.info.Channels = fi.Channels
		}
		d.packOutput(n * fi.Channels)
		return nil
	}
}

// packOutput quantizes the first ns interleaved samples of sampleBuf to S16
// little-endian bytes and points pending at them. It is the single place that
// knows the output byte format, so a later task can branch here for native
// float32 passthrough (WithF32) without touching the decode loop.
func (d *Decoder) packOutput(ns int) {
	if cap(d.s16Buf) < ns {
		d.s16Buf = make([]int16, ns)
	}
	d.s16Buf = d.s16Buf[:ns]
	convertF32toS16(d.s16Buf, d.sampleBuf[:ns])

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
