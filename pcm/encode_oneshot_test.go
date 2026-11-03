package pcm

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	mp3 "github.com/tphakala/go-mp3"
)

// TestEncodeInterleavedMatchesStreaming proves the one-shot is exactly
// Reset+Write+Close: it must produce byte-identical output to the streaming
// Encoder fed the whole buffer in one Write. The streaming side uses a seekable
// sink so it writes the same gapless tag the one-shot prepends; a plain
// io.Writer would produce a tagless stream (covered by TestStreamingTaglessOnPlainWriter).
func TestEncodeInterleavedMatchesStreaming(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, Bitrate: 128000}
	pcm := genSineS16(mp3.FrameSize*4+123, 2, 1000, 44100)

	var stream memWriteSeeker
	e, err := NewEncoder(&stream, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Write(pcm); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	var oneshot bytes.Buffer
	if err := EncodeInterleaved(&oneshot, cfg, pcm); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stream.Bytes(), oneshot.Bytes()) {
		t.Fatalf("one-shot and seekable-streaming differ: %d vs %d bytes", oneshot.Len(), len(stream.Bytes()))
	}
}

func TestEncodeInterleavedRejectsPartialSample(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, Bitrate: 128000}
	// Stereo stride is 4 bytes; 6 is not a whole number of samples.
	if err := EncodeInterleaved(io.Discard, cfg, make([]byte, 6)); err == nil {
		t.Fatal("expected error for partial trailing sample")
	}
}

func TestEncodeInterleavedRejectsBadConfig(t *testing.T) {
	if err := EncodeInterleaved(io.Discard, Config{SampleRate: 12345, Channels: 2}, nil); err == nil {
		t.Fatal("expected error for bad sample rate")
	}
}

// TestEncodeInterleavedRejectsNilWriter pins that a nil sink returns a clean
// error rather than panicking. The default (tagged) path encodes into an
// internal buffer, so it never validates w via Reset; without an up-front check
// a nil w would panic at the final tag write instead.
func TestEncodeInterleavedRejectsNilWriter(t *testing.T) {
	pcm := genSineS16(mp3.FrameSize, 2, 1000, 44100)
	cfg := Config{SampleRate: 44100, Channels: 2, Bitrate: 128000}
	if err := EncodeInterleaved(nil, cfg, pcm); err == nil {
		t.Fatal("expected an error for a nil writer (default tagged path)")
	}
	cfg.OmitGaplessTag = true
	if err := EncodeInterleaved(nil, cfg, pcm); err == nil {
		t.Fatal("expected an error for a nil writer (tagless path)")
	}
}

func TestEncodeInterleavedEmpty(t *testing.T) {
	// Empty but valid: whole-sample check passes (0 % stride == 0); the drain
	// still emits the flush frame, so output is non-empty and decodable.
	var buf bytes.Buffer
	if err := EncodeInterleaved(&buf, Config{SampleRate: 44100, Channels: 1, Bitrate: 128000}, nil); err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		t.Fatal("empty one-shot produced no output")
	}
}

// TestEncodeInterleavedPoolReuse runs many back-to-back one-shots to exercise the
// sync.Pool Get/Reset/Put path; every call must succeed and produce identical
// bytes for identical input.
func TestEncodeInterleavedPoolReuse(t *testing.T) {
	cfg := Config{SampleRate: 48000, Channels: 1, Bitrate: 64000}
	pcm := genSineS16(mp3.FrameSize*2, 1, 1000, 48000)
	var first []byte
	for i := range 16 {
		var buf bytes.Buffer
		if err := EncodeInterleaved(&buf, cfg, pcm); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = bytes.Clone(buf.Bytes())
			continue
		}
		if !bytes.Equal(first, buf.Bytes()) {
			t.Fatalf("pool reuse iteration %d produced different bytes", i)
		}
	}
}

// countingCtx is a deterministic context whose Err returns cancelErr from its
// k-th call on (0-based), counting every call, so a test can cancel at an exact
// check point of the one-shot loop.
type countingCtx struct {
	context.Context
	k         int
	cancelErr error
	calls     int
}

func (c *countingCtx) Err() error {
	n := c.calls
	c.calls++
	if n >= c.k {
		return c.cancelErr
	}
	return nil
}

// TestEncodeInterleavedContextCancel cancels at each check point of the loop.
// Input is 3 whole frames plus 100 samples, so the checks are: 0..2 before each
// whole-frame Write, 3 before the partial chunk (which lands in carry), 4 before
// Close, 5 (tagged path only) before the tag and body are written.
func TestEncodeInterleavedContextCancel(t *testing.T) {
	base := Config{SampleRate: 44100, Channels: 2, Bitrate: 128000}
	pcm := genSineS16(3*mp3.FrameSize+100, 2, 1000, 44100)

	var fullTagged, fullOmit bytes.Buffer
	if err := EncodeInterleaved(&fullTagged, base, pcm); err != nil {
		t.Fatal(err)
	}
	omitCfg := base
	omitCfg.OmitGaplessTag = true
	if err := EncodeInterleaved(&fullOmit, omitCfg, pcm); err != nil {
		t.Fatal(err)
	}

	rows := []struct {
		name string
		omit bool
		k    int
	}{
		{"tagged pre-canceled", false, 0},
		{"tagged mid-stream", false, 2},
		{"tagged before carry chunk", false, 3},
		{"tagged before Close with carry", false, 4},
		{"tagged after Close before write", false, 5},
		{"omit pre-canceled", true, 0},
		{"omit mid-stream", true, 2},
		{"omit before Close with carry", true, 4},
	}
	for _, tc := range rows {
		cause := context.Canceled
		if tc.k == 2 && !tc.omit {
			cause = context.DeadlineExceeded // one row covers the deadline error
		}
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			cfg.OmitGaplessTag = tc.omit
			ctx := &countingCtx{Context: t.Context(), k: tc.k, cancelErr: cause}
			e := new(Encoder)
			var w bytes.Buffer
			err := encodeInterleaved(ctx, e, &w, cfg, pcm)
			if !errors.Is(err, cause) {
				t.Fatalf("err = %v, want it to wrap %v", err, cause)
			}
			if ctx.calls != tc.k+1 {
				t.Fatalf("Err called %d times, want %d (stop at the first failing check)", ctx.calls, tc.k+1)
			}
			if tc.omit {
				if w.Len() >= fullOmit.Len() {
					t.Fatalf("canceled omit stream wrote %d bytes, full is %d", w.Len(), fullOmit.Len())
				}
				if !bytes.HasPrefix(fullOmit.Bytes(), w.Bytes()) {
					t.Fatal("canceled omit stream is not a prefix of the full stream")
				}
			} else if w.Len() != 0 {
				t.Fatalf("tagged path wrote %d bytes after cancellation", w.Len())
			}

			// The same Encoder, reused, must not carry state (including the
			// stale carry of the k=4 row) from the canceled call.
			var again bytes.Buffer
			if err := encodeInterleaved(t.Context(), e, &again, cfg, pcm); err != nil {
				t.Fatal(err)
			}
			want := fullTagged.Bytes()
			if tc.omit {
				want = fullOmit.Bytes()
			}
			if !bytes.Equal(again.Bytes(), want) {
				t.Fatal("reused encoder output differs from a fresh encode")
			}
		})
	}
}

// TestEncodeInterleavedContextCancelAfterClose pins that on the OmitGaplessTag
// path no check follows Close: the stream on w is complete by then, so a cancel
// that would land after Close is not reported.
func TestEncodeInterleavedContextCancelAfterClose(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, Bitrate: 128000, OmitGaplessTag: true}
	pcm := genSineS16(3*mp3.FrameSize+100, 2, 1000, 44100)
	ctx := &countingCtx{Context: t.Context(), k: 5, cancelErr: context.Canceled}
	var w bytes.Buffer
	if err := encodeInterleaved(ctx, new(Encoder), &w, cfg, pcm); err != nil {
		t.Fatalf("omit path returned %v, want nil (stream complete)", err)
	}
}

// TestEncodeInterleavedContextRealContext covers the exported pooled path with a
// real cancelled context: nothing is written and the pool stays usable.
func TestEncodeInterleavedContextRealContext(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, Bitrate: 128000}
	pcm := genSineS16(2*mp3.FrameSize, 2, 1000, 44100)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var w bytes.Buffer
	err := EncodeInterleavedContext(ctx, &w, cfg, pcm)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if w.Len() != 0 {
		t.Fatalf("wrote %d bytes after cancellation", w.Len())
	}
	var ok, want bytes.Buffer
	if err := EncodeInterleavedContext(t.Context(), &ok, cfg, pcm); err != nil {
		t.Fatal(err)
	}
	if err := EncodeInterleaved(&want, cfg, pcm); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ok.Bytes(), want.Bytes()) {
		t.Fatal("EncodeInterleavedContext with Background differs from EncodeInterleaved")
	}
}

// TestEncodeInterleavedContextSinkError pins that a sink failure during the
// chunked write loop (OmitGaplessTag streams straight to w) is returned.
func TestEncodeInterleavedContextSinkError(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, Bitrate: 128000, OmitGaplessTag: true}
	pcm := genSineS16(4*mp3.FrameSize, 2, 1000, 44100)
	want := errors.New("sink failed")
	err := EncodeInterleavedContext(t.Context(), &errWriter{ok: 0, err: want}, cfg, pcm)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}
