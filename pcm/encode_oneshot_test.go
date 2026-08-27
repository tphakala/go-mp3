package pcm

import (
	"bytes"
	"io"
	"testing"

	mp3 "github.com/tphakala/go-mp3"
)

// TestEncodeInterleavedMatchesStreaming proves the one-shot is exactly
// Reset+Write+Close: it must produce byte-identical output to the streaming
// Encoder fed the whole buffer in one Write.
func TestEncodeInterleavedMatchesStreaming(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, Bitrate: 128000}
	pcm := genSineS16(mp3.FrameSize*4+123, 2, 1000, 44100)

	var stream bytes.Buffer
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
		t.Fatalf("one-shot and streaming differ: %d vs %d bytes", oneshot.Len(), stream.Len())
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
