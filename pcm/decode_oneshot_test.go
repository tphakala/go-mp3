package pcm

import (
	"bytes"
	"errors"
	"testing"

	mp3 "github.com/tphakala/go-mp3"
)

// encodeForDecode returns a CBR MP3 stream of nFramesPerCh full frames of a tone.
func encodeForDecode(t *testing.T, cfg Config, nSamplesPerCh int) []byte {
	t.Helper()
	pcm := genSineS16(nSamplesPerCh, cfg.Channels, 1000, cfg.SampleRate)
	var buf bytes.Buffer
	if err := EncodeInterleaved(&buf, cfg, pcm); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDecodeInterleavedBasic(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, Bitrate: 128000}
	stream := encodeForDecode(t, cfg, mp3.FrameSize*5)

	out, info, err := DecodeInterleaved(bytes.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if info.SampleRate != 44100 || info.Channels != 2 {
		t.Fatalf("Info mismatch: %+v", info)
	}
	if len(out) == 0 {
		t.Fatal("no PCM decoded")
	}
	if len(out)%2 != 0 {
		t.Fatalf("S16 output not a whole number of bytes: %d", len(out))
	}
}

func TestDecodeInterleavedLimitOverflow(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, Bitrate: 128000}
	stream := encodeForDecode(t, cfg, mp3.FrameSize*10)

	// A tiny ceiling must trip ErrDecodeLimit rather than return the whole decode.
	_, _, err := DecodeInterleavedLimit(bytes.NewReader(stream), 100)
	if !errors.Is(err, ErrDecodeLimit) {
		t.Fatalf("want ErrDecodeLimit, got %v", err)
	}
}

func TestDecodeInterleavedLimitFits(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 1, Bitrate: 128000}
	stream := encodeForDecode(t, cfg, mp3.FrameSize*2)

	out, _, err := DecodeInterleavedLimit(bytes.NewReader(stream), DefaultMaxDecodedBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("no PCM decoded")
	}
}

func TestDecodeInterleavedWithF32(t *testing.T) {
	cfg := Config{SampleRate: 48000, Channels: 2, Bitrate: 128000}
	stream := encodeForDecode(t, cfg, mp3.FrameSize*3)

	s16Out, _, err := DecodeInterleaved(bytes.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	f32Out, info, err := DecodeInterleaved(bytes.NewReader(stream), WithF32())
	if err != nil {
		t.Fatal(err)
	}
	if info.Channels != 2 {
		t.Fatalf("Info mismatch: %+v", info)
	}
	// float32 output is 4 bytes/sample vs 2 for S16, so it must be exactly twice
	// the length for the same decoded sample count.
	if len(f32Out) != len(s16Out)*2 {
		t.Fatalf("WithF32 length %d, want %d (2x S16)", len(f32Out), len(s16Out)*2)
	}
}

func TestDecodeInterleavedNoFrame(t *testing.T) {
	// Non-MP3 garbage must return a construction error, not a panic or empty ok.
	if _, _, err := DecodeInterleaved(bytes.NewReader([]byte("not an mp3 stream at all"))); err == nil {
		t.Fatal("expected error for non-MP3 input")
	}
}
