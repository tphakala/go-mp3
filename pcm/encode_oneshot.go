package pcm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"

	mp3 "github.com/tphakala/go-mp3"
)

// encoderPool recycles Encoders for EncodeInterleaved so back-to-back one-shot
// encodes reuse the large internal state (the root encoder's filterbank and
// buffers) instead of re-allocating it. Every Get is paired with a Reset before
// any encoding, so a recycled encoder never carries state from a prior call.
// Mirrors go-flac and go-aac pcm.EncodeInterleaved.
var encoderPool = sync.Pool{New: func() any { return new(Encoder) }}

// EncodeInterleaved encodes a complete interleaved little-endian S16 PCM buffer
// to a CBR MP3 stream on w in a single call, centralizing the
// NewEncoder/Write/Close sequence. It draws an Encoder from an internal
// sync.Pool, so repeated calls are allocation-light, and it is safe for
// concurrent use.
//
// The buffer must hold a whole number of inter-channel samples for cfg; a
// trailing partial sample is an error before any sink write. A final partial
// frame (fewer than mp3.FrameSize samples) is zero-padded internally, exactly as
// the streaming Close path does, so the buffer need not be a whole number of
// frames.
//
// By default it writes a leading Xing/Info + LAME gapless tag (see the package
// docs). The tag's frame count and byte total are known only after the last
// frame, so the default path buffers the whole encoded stream in memory and
// writes nothing to w until encoding completes (relevant for a slow sink). The
// buffered stream is smaller than the PCM input this call already holds. Set
// Config.OmitGaplessTag to stream frames to w as they are produced, with no tag
// and no buffering.
//
// EncodeInterleaved cannot be canceled; EncodeInterleavedContext is the
// cancellable form.
func EncodeInterleaved(w io.Writer, cfg Config, pcm []byte) error {
	return EncodeInterleavedContext(context.Background(), w, cfg, pcm)
}

// EncodeInterleavedContext is EncodeInterleaved with cancellation. It checks
// ctx.Err() before each input frame (mp3.FrameSize samples per channel) is
// encoded, before the final drain, and on the default (tagged) path before
// anything is written to w. Because the encoder holds a one-frame lookahead,
// the final drain still codes a few frames after the last per-frame check, so
// the latency bound is a few frames of work, not one.
//
// On cancellation it returns an error wrapping ctx.Err(), so
// errors.Is(err, context.Canceled) (or context.DeadlineExceeded) holds. On the
// default path nothing is written to w once cancellation is seen, including a
// cancel during the final frames. With Config.OmitGaplessTag the frames encoded
// so far are already on w when the call returns: a truncated, frame-aligned,
// tagless stream the caller should discard. A cancel that lands during the final
// drain on that path is not reported, because the stream on w is then complete.
// Validation errors are returned before ctx is consulted.
func EncodeInterleavedContext(ctx context.Context, w io.Writer, cfg Config, pcm []byte) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	if w == nil {
		// Both paths below would otherwise reach a nil w only late (the default
		// path validates the internal buffer, not w), so reject it up front for
		// the same clean error the streaming Reset returns.
		return fmt.Errorf("go-mp3/pcm: nil writer")
	}
	stride := 2 * cfg.Channels
	if len(pcm)%stride != 0 {
		return fmt.Errorf("go-mp3/pcm: %d bytes is not a whole number of %d-byte samples", len(pcm), stride)
	}
	e, _ := encoderPool.Get().(*Encoder)
	defer func() {
		// Never hand a pooled encoder back holding the caller's sink; the next
		// Reset rebinds w before any use.
		e.w = nil
		encoderPool.Put(e)
	}()
	return encodeInterleaved(ctx, e, w, cfg, pcm)
}

// canceled wraps a context error for the one-shot's cancellation return.
func canceled(err error) error {
	return fmt.Errorf("go-mp3/pcm: encode canceled: %w", err)
}

// encodeInterleaved runs the one-shot on e, which the caller owns (a pooled
// Encoder in production, a known one in tests). It checks ctx before every input
// frame, before Close, and on the tagged path before any write to w. Whole-frame
// chunks take Write's no-copy path and the final partial chunk lands in carry
// for Close, so the output equals a single Write of pcm.
func encodeInterleaved(ctx context.Context, e *Encoder, w io.Writer, cfg Config, pcm []byte) error {
	// Default: prepend a Xing/Info + LAME gapless tag. Encode the audio into an
	// in-memory buffer first (a bytes.Buffer is not an io.WriteSeeker, so the
	// Encoder writes a tagless body), then build the tag from the final counts
	// and write it ahead of the body. The bytes are identical to streaming the
	// same input into an io.WriteSeeker. With OmitGaplessTag the bare tagless
	// stream goes straight to w.
	sink := w
	var body *bytes.Buffer
	if !cfg.OmitGaplessTag {
		body = new(bytes.Buffer)
		sink = body
	}
	if err := e.Reset(sink, cfg); err != nil {
		return err
	}
	for len(pcm) > 0 {
		if err := ctx.Err(); err != nil {
			return canceled(err)
		}
		n := min(len(pcm), e.frameBytes)
		if _, err := e.Write(pcm[:n]); err != nil {
			return err
		}
		pcm = pcm[n:]
	}
	if err := ctx.Err(); err != nil {
		return canceled(err)
	}
	if err := e.Close(); err != nil {
		return err
	}
	if cfg.OmitGaplessTag {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return canceled(err)
	}

	kbps := cfg.resolvedKbps()
	audioFrames := e.enc.Stats().Frames
	padding := lamePadding(audioFrames, e.samplesIn)
	totalBytes := int64(infoFrameLen(cfg.SampleRate, kbps)) + int64(body.Len())
	tag := buildInfoFrame(cfg.SampleRate, kbps, cfg.Channels, audioFrames, totalBytes, mp3.EncoderDelay, padding)

	if _, err := w.Write(tag); err != nil {
		return err
	}
	_, err := w.Write(body.Bytes())
	return err
}
