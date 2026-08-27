package pcm

import (
	"bytes"
	"errors"
	"path/filepath"
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

// TestDecodeInterleavedMatchesStreamingOracle ties the one-shot decode to the
// streaming pcm.Decoder, which CI verifies bit-exact against the pinned minimp3
// oracle (see the Oracle differential job and the streaming conformance suite).
// DecodeInterleaved adds no decode logic; it accumulates the streaming decoder's
// output under a byte ceiling. So decoding a real MP3 fixture through the one-shot
// must be byte-identical to draining the streaming decoder, which transitively
// gives the one-shot the same oracle guarantee without a redundant decoder dump
// hook (the coding guideline reserves those hooks for actual decoder units, and
// this is a thin wrapper over one). The fixtures are committed, so this runs in
// CI on both amd64 and arm64.
func TestDecodeInterleavedMatchesStreamingOracle(t *testing.T) {
	fixtures := []string{
		fixturesDir + "/sine48m_128.mp3",  // 48 kHz mono
		fixturesDir + "/sine44s_128.mp3",  // 44.1 kHz stereo
		fixturesDir + "/chirp44m_128.mp3", // 44.1 kHz mono chirp
	}
	for _, fx := range fixtures {
		t.Run(filepath.Base(fx), func(t *testing.T) {
			raw := readFixture(t, fx)
			// Reference: the streaming decoder's full S16 output, oracle-verified in CI.
			want := decodeAllBytes(t, bytes.NewReader(raw))
			got, info, err := DecodeInterleaved(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("one-shot S16 output differs from the streaming (oracle-verified) decoder: got %d bytes, want %d", len(got), len(want))
			}
			if info.SampleRate == 0 || info.Channels == 0 {
				t.Fatalf("Info not populated: %+v", info)
			}
		})
	}
}
