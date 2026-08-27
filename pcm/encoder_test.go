package pcm

import (
	"bytes"
	"errors"
	"io"
	"testing"

	mp3 "github.com/tphakala/go-mp3"
)

func TestNewEncoderRejectsBadConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"bad sample rate", Config{SampleRate: 22050, Channels: 2, Bitrate: 128000}},
		{"zero sample rate", Config{Channels: 2, Bitrate: 128000}},
		{"bad channels", Config{SampleRate: 44100, Channels: 3, Bitrate: 128000}},
		{"zero channels", Config{SampleRate: 44100, Bitrate: 128000}},
		{"negative bitrate", Config{SampleRate: 44100, Channels: 2, Bitrate: -1}},
		{"non-rate bitrate", Config{SampleRate: 44100, Channels: 2, Bitrate: 123000}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewEncoder(io.Discard, tc.cfg); err == nil {
				t.Fatalf("expected error for %+v", tc.cfg)
			}
		})
	}
}

func TestNewEncoderNilWriter(t *testing.T) {
	if _, err := NewEncoder(nil, Config{SampleRate: 44100, Channels: 2, Bitrate: 128000}); err == nil {
		t.Fatal("expected error for nil writer")
	}
}

func TestEncoderZeroBitrateDefaults(t *testing.T) {
	// A zero Bitrate must be accepted (maps to mp3.DefaultBitrate), not rejected.
	var buf bytes.Buffer
	e, err := NewEncoder(&buf, Config{SampleRate: 44100, Channels: 2})
	if err != nil {
		t.Fatalf("zero bitrate rejected: %v", err)
	}
	pcm := genSineS16(mp3.FrameSize*2, 2, 1000, 44100)
	if _, err := e.Write(pcm); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		t.Fatal("no output produced")
	}
}

// TestEncoderChunkBoundaryInvariance proves the produced stream depends only on
// the byte sequence, not on how Write is chunked: feeding the same PCM in one
// Write, in tiny odd-sized Writes, and in exact-frame Writes yields identical
// output bytes.
func TestEncoderChunkBoundaryInvariance(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, Bitrate: 128000}
	pcm := genSineS16(mp3.FrameSize*5+37, 2, 1000, 44100)

	encodeChunked := func(chunk int) []byte {
		var buf bytes.Buffer
		e, err := NewEncoder(&buf, cfg)
		if err != nil {
			t.Fatal(err)
		}
		for off := 0; off < len(pcm); off += chunk {
			end := off + chunk
			if end > len(pcm) {
				end = len(pcm)
			}
			if _, err := e.Write(pcm[off:end]); err != nil {
				t.Fatal(err)
			}
		}
		if err := e.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}

	whole := encodeChunked(len(pcm))
	for _, chunk := range []int{1, 3, 7, 100, mp3.FrameSize * 4, mp3.FrameSize*4 + 1} {
		got := encodeChunked(chunk)
		if !bytes.Equal(whole, got) {
			t.Fatalf("chunk=%d produced different bytes (%d vs %d)", chunk, len(got), len(whole))
		}
	}
}

func TestEncoderWriteAfterCloseErrors(t *testing.T) {
	var buf bytes.Buffer
	e, err := NewEncoder(&buf, Config{SampleRate: 44100, Channels: 1, Bitrate: 128000})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Write(genSineS16(10, 1, 1000, 44100)); !errors.Is(err, ErrEncoderClosed) {
		t.Fatalf("want ErrEncoderClosed, got %v", err)
	}
}

func TestEncoderCloseIdempotent(t *testing.T) {
	var buf bytes.Buffer
	e, err := NewEncoder(&buf, Config{SampleRate: 44100, Channels: 1, Bitrate: 128000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Write(genSineS16(mp3.FrameSize, 1, 1000, 44100)); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	n := buf.Len()
	if err := e.Close(); err != nil {
		t.Fatalf("second Close errored: %v", err)
	}
	if buf.Len() != n {
		t.Fatalf("second Close wrote more bytes: %d -> %d", n, buf.Len())
	}
}

func TestEncoderClosePartialSampleErrors(t *testing.T) {
	// A trailing odd byte (not a whole inter-channel sample) must fail on Close.
	var buf bytes.Buffer
	e, err := NewEncoder(&buf, Config{SampleRate: 44100, Channels: 2, Bitrate: 128000})
	if err != nil {
		t.Fatal(err)
	}
	// Stereo stride is 4 bytes; write 6 bytes (1 sample + 2 leftover).
	if _, err := e.Write([]byte{1, 2, 3, 4, 5, 6}); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err == nil {
		t.Fatal("expected error for partial trailing sample")
	}
}

func TestEncoderEmptyInputProducesFlushFrame(t *testing.T) {
	// N=0 non-nil calls plus the drain yields exactly one flush frame: empty in,
	// one valid frame out (the documented root-encoder contract).
	var buf bytes.Buffer
	e, err := NewEncoder(&buf, Config{SampleRate: 44100, Channels: 1, Bitrate: 128000})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		t.Fatal("empty input produced no output; expected one flush frame")
	}
}

// errWriter fails every Write after the first failAfter successful writes, used
// to exercise the sink-error paths in Encoder.Write and Close.
type errWriter struct {
	ok  int // number of successful writes before failing
	n   int
	err error
}

func (w *errWriter) Write(p []byte) (int, error) {
	if w.n >= w.ok {
		return 0, w.err
	}
	w.n++
	return len(p), nil
}

// TestEncoderResetFailureLeavesClosed proves a failed Reset latches the encoder
// closed rather than leaving a nil sink that a later Write would nil-panic on. A
// non-CBR-rate bitrate passes the pcm-level validate (sign only) but is rejected
// by the root encoder's Reset, which is the failure path that must latch closed.
func TestEncoderResetFailureLeavesClosed(t *testing.T) {
	e, err := NewEncoder(io.Discard, Config{SampleRate: 44100, Channels: 2, Bitrate: 128000})
	if err != nil {
		t.Fatal(err)
	}
	if rerr := e.Reset(io.Discard, Config{SampleRate: 44100, Channels: 2, Bitrate: 123000}); rerr == nil {
		t.Fatal("expected Reset to reject a non-CBR-rate bitrate")
	}
	// Must return ErrEncoderClosed, not panic on a nil sink.
	if _, werr := e.Write(genSineS16(10, 2, 1000, 44100)); !errors.Is(werr, ErrEncoderClosed) {
		t.Fatalf("want ErrEncoderClosed after failed Reset, got %v", werr)
	}
}

// TestEncoderWriteSinkErrorPreservesCarryInvariant proves a sink error while
// emitting a straddling boundary frame reverts carry to its pre-Write length, so
// the carry-below-one-frame invariant holds and no bytes of p are silently
// absorbed while Write reports 0 consumed. The root encoder's bit reservoir can
// emit zero bytes on any given frame, so which frame first reaches the sink is
// not fixed; the test drives paired sub-frame + frame-completing Writes against
// an always-failing sink until one straddling emit actually produces output and
// errors, then asserts the invariant on that call. Each completing Write supplies
// exactly the bytes needed to finish one frame, so the only emit per iteration is
// the straddling (carry-merge) emit, never a step-2 whole-frame emit.
func TestEncoderWriteSinkErrorPreservesCarryInvariant(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, Bitrate: 128000}
	ew := &errWriter{ok: 0, err: errors.New("sink boom")}
	e, err := NewEncoder(ew, cfg)
	if err != nil {
		t.Fatal(err)
	}
	half := e.frameBytes / 2
	half -= half % e.stride // whole samples
	sub := genSineS16(half/e.stride, 2, 1000, 44100)
	complete := genSineS16((e.frameBytes-half)/e.stride, 2, 1000, 44100)

	const maxIter = 256
	errored := false
	for range maxIter {
		// Build a sub-frame carry: this path never emits, so it never errors.
		if _, werr := e.Write(sub); werr != nil {
			t.Fatalf("sub-frame Write should buffer without error, got %v", werr)
		}
		before := len(e.carry)
		if before == 0 || before >= e.frameBytes {
			t.Fatalf("carry not a partial frame after sub-frame Write: %d (frameBytes %d)", before, e.frameBytes)
		}
		// Complete exactly one frame, triggering only the straddling emit.
		_, werr := e.Write(complete)
		if werr == nil {
			continue // reservoir held this frame (0-length output); try again
		}
		if len(e.carry) != before {
			t.Fatalf("carry not reverted after sink error: got %d want %d", len(e.carry), before)
		}
		if len(e.carry) >= e.frameBytes {
			t.Fatalf("carry invariant violated: %d >= frameBytes %d", len(e.carry), e.frameBytes)
		}
		errored = true
		break
	}
	if !errored {
		t.Fatalf("no straddling emit produced non-empty output within %d iterations", maxIter)
	}
}

// TestEncoderResetPooling proves Reset rebinds to a new sink and clears state, so
// one Encoder value encodes two independent streams identically to two fresh
// encoders.
func TestEncoderResetPooling(t *testing.T) {
	cfg := Config{SampleRate: 48000, Channels: 2, Bitrate: 96000}
	pcm := genSineS16(mp3.FrameSize*3, 2, 1000, 48000)

	fresh := func() []byte {
		var b bytes.Buffer
		e, err := NewEncoder(&b, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Write(pcm); err != nil {
			t.Fatal(err)
		}
		if err := e.Close(); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	want := fresh()

	e, err := NewEncoder(io.Discard, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		var b bytes.Buffer
		if err := e.Reset(&b, cfg); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Write(pcm); err != nil {
			t.Fatal(err)
		}
		if err := e.Close(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(want, b.Bytes()) {
			t.Fatal("Reset-reused encoder produced different bytes than a fresh one")
		}
	}
}
