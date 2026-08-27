package pcm

import (
	"errors"
	"fmt"
	"io"

	mp3 "github.com/tphakala/go-mp3"
)

// ErrEncoderClosed is returned by Encoder.Write after Close, mirroring
// flac.ErrEncoderClosed and aac.ErrEncoderClosed in the sibling pcm packages.
// The go-mp3 root package has no such sentinel (its frame encoder uses
// ErrEncoderFinalized), so the streaming wrapper defines its own.
var ErrEncoderClosed = errors.New("go-mp3/pcm: encoder is closed")

// Encoder streams interleaved little-endian signed 16-bit PCM (as []byte) to a
// CBR MPEG-1 Layer III (MP3) stream on an io.Writer. It is shaped like the
// sibling pcm.Encoders in go-flac, go-aac and go-opus: a flat Config,
// NewEncoder(w, cfg), Reset(w, cfg) for pooling, and Write/Close.
//
// MP3 frames are self-framing, so the Encoder needs no seeking and no stream
// finalization beyond Close, which flushes the final partial frame and drains
// the root encoder's one-frame attack-detection lookahead. Because the encoder
// emits a tagless CBR stream (no LAME gapless tag), the decoded output carries
// mp3.TotalDelay leading samples of algorithmic delay; a caller aligning decoded
// audio back to the original input must drop them (see the root mp3.TotalDelay).
//
// An Encoder is not safe for concurrent use.
type Encoder struct {
	enc *mp3.Encoder
	w   io.Writer
	cfg Config

	stride     int // bytes per inter-channel sample: 2 * Channels
	frameBytes int // bytes in one full frame: stride * mp3.FrameSize

	planarBuf  [2][mp3.FrameSize]float32 // embedded per-channel scratch, no per-frame alloc
	planarView [2][]float32              // slice headers into planarBuf
	planar     [][]float32               // planarView sliced to Channels

	carry []byte // buffered interleaved bytes, always < frameBytes, reused
	out   []byte // reused EncodeFrame append target

	closed bool
}

var _ io.WriteCloser = (*Encoder)(nil)

// NewEncoder validates cfg and returns an Encoder writing a CBR MP3 stream to w.
// A config error returns immediately, before any byte is written.
func NewEncoder(w io.Writer, cfg Config) (*Encoder, error) {
	e := &Encoder{}
	if err := e.Reset(w, cfg); err != nil {
		return nil, err
	}
	return e, nil
}

// Reset rebinds the Encoder to a new sink w and reconfigures it with cfg so one
// Encoder can encode many independent streams without re-allocating, the pooling
// path for a many-short-clips workload. It re-validates cfg, discards buffered
// input, and resets all per-stream state (the root encoder's filterbank history,
// lookahead, and drain latch). After a successful Reset the encoder is ready for
// Write/Close as if freshly constructed; on error it must not be used. Reset may
// be called on a closed encoder, the usual pooling pattern (Reset, Write, Close,
// repeat).
func (e *Encoder) Reset(w io.Writer, cfg Config) error {
	// Latch closed until every step below succeeds, so a failed Reset leaves the
	// encoder safely closed (Write returns ErrEncoderClosed) rather than holding a
	// nil sink that a contract-violating Write would nil-panic on. Detach from any
	// previous stream too, so no failure path keeps a reference to the old sink.
	e.closed = true
	e.w = nil
	if w == nil {
		return fmt.Errorf("go-mp3/pcm: nil writer")
	}
	encCfg, err := cfg.toEncoderConfig()
	if err != nil {
		return err
	}
	if e.enc == nil {
		enc, nerr := mp3.NewEncoder(encCfg)
		if nerr != nil {
			return nerr
		}
		e.enc = enc
	} else if rerr := e.enc.Reset(encCfg); rerr != nil {
		return rerr
	}

	e.cfg = cfg
	e.w = w
	e.stride = 2 * cfg.Channels
	e.frameBytes = e.stride * mp3.FrameSize
	e.planarView[0] = e.planarBuf[0][:]
	e.planarView[1] = e.planarBuf[1][:]
	e.planar = e.planarView[:cfg.Channels]
	e.carry = e.carry[:0]
	e.closed = false
	return nil
}

// Write consumes interleaved little-endian S16 PCM in arbitrary chunk sizes.
// Bytes that do not yet complete a full frame are buffered until the next Write
// or Close, so io.Copy works with any buffer size, including one not divisible
// by the sample stride. The produced stream depends only on the byte sequence,
// never on how it was chunked. Write returns len(p) on success; on a mid-write
// sink error it returns the number of bytes of p that were durably consumed
// (io.Writer contract). A sink error is terminal for the stream: the frame that
// failed to write has already advanced the encoder's internal state, so the
// caller must abandon the stream rather than resume it by retrying the same
// bytes. After Close, Write returns ErrEncoderClosed.
func (e *Encoder) Write(p []byte) (int, error) {
	if e.closed {
		return 0, ErrEncoderClosed
	}
	n := len(p) // captured before p is resliced; Write must report this on success
	written := 0

	// 1. Complete one frame from carry + the head of p, if we now have enough.
	if len(e.carry) > 0 {
		need := e.frameBytes - len(e.carry) // >= 1: carry is always < frameBytes
		if len(p) < need {                  // still short of a full frame
			e.carry = append(e.carry, p...)
			return n, nil
		}
		origLen := len(e.carry)
		e.carry = append(e.carry, p[:need]...) // carry is now exactly one frame
		if err := e.emitFrame(e.carry, mp3.FrameSize); err != nil {
			e.carry = e.carry[:origLen] // revert: uphold the carry < frameBytes invariant
			return 0, err               // boundary frame failed: no bytes of p durably consumed
		}
		e.carry = e.carry[:0]
		p = p[need:]
		written = need
	}

	// 2. Emit whole frames straight from p (no copy).
	off := 0
	for len(p)-off >= e.frameBytes {
		if err := e.emitFrame(p[off:off+e.frameBytes], mp3.FrameSize); err != nil {
			return written + off, err
		}
		off += e.frameBytes
	}

	// 3. Stash the remainder (< one frame) as carry.
	e.carry = append(e.carry[:0], p[off:]...)
	return n, nil
}

// emitFrame deinterleaves chunk (exactly n inter-channel samples) into the
// per-channel float32 scratch, encodes one MP3 frame's worth of input, and
// writes whatever the root encoder appended to the sink. n may be less than
// mp3.FrameSize only for the final frame (Close), which the root encoder
// zero-pads and treats as the stream terminator.
func (e *Encoder) emitFrame(chunk []byte, n int) error {
	for c := range e.planar {
		e.planar[c] = e.planar[c][:n]
	}
	deinterleaveS16(e.planar, chunk, n, e.cfg.Channels)

	var err error
	e.out, err = e.enc.EncodeFrame(e.out[:0], e.planar)
	if err != nil {
		return err
	}
	if len(e.out) > 0 {
		if _, werr := e.w.Write(e.out); werr != nil {
			return werr
		}
	}
	// No need to restore scratch length: the next emitFrame re-slices e.planar[c]
	// to its new n from the preserved FrameSize capacity, so the narrow view here
	// does not leak into the following frame.
	return nil
}

// Close flushes the final partial frame (zero-padded to a whole frame by the
// root encoder) and drains the encoder's one-frame lookahead plus the final
// flush frame. The drain is mandatory: without it the last real frame of audio,
// held in the encoder's lookahead, is never emitted. Close is idempotent; a
// Write after Close returns ErrEncoderClosed. It errors if the buffered trailing
// bytes are not a whole number of inter-channel samples.
func (e *Encoder) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true

	if len(e.carry) > 0 {
		if len(e.carry)%e.stride != 0 {
			return fmt.Errorf("go-mp3/pcm: Close: %d trailing bytes are not a whole sample", len(e.carry))
		}
		n := len(e.carry) / e.stride
		if err := e.emitFrame(e.carry, n); err != nil {
			return err
		}
	}
	e.carry = e.carry[:0]

	// Drain: a nil EncodeFrame flushes the held frame plus the final flush frame.
	var err error
	e.out, err = e.enc.EncodeFrame(e.out[:0], nil)
	if err != nil {
		return err
	}
	if len(e.out) > 0 {
		if _, werr := e.w.Write(e.out); werr != nil {
			return werr
		}
	}
	return nil
}
