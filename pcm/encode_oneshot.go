package pcm

import (
	"fmt"
	"io"
	"sync"
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
func EncodeInterleaved(w io.Writer, cfg Config, pcm []byte) error {
	if err := cfg.validate(); err != nil {
		return err
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
	if err := e.Reset(w, cfg); err != nil {
		return err
	}
	if _, err := e.Write(pcm); err != nil {
		return err
	}
	return e.Close()
}
