package pcm

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"slices"
	"testing"

	mp3 "github.com/tphakala/go-mp3"
	"github.com/tphakala/go-mp3/internal/testsignal"
)

// TestGaplessTrimAlignsToEncoderInput is the ground-truth gate for the LAME
// gapless convention: a decoder must skip the tag's encoder delay PLUS the
// standard 529-sample synthesis delay at the head (and that much less of the
// padding at the tail), so the emitted audio lines up sample for sample with
// what went into the encoder. It builds that situation from this project's
// own parts, with no external file: a known program is encoded through the
// root mp3.Encoder (whose EncoderDelay/TotalDelay split is exactly the LAME
// tag's convention), a synthetic Info frame carrying a LAME extension with
// delay = mp3.EncoderDelay and the matching padding is prepended, and the
// stream is decoded through pcm.Decoder. The output must have exactly the
// input's length and a high SNR against the input, which only holds when
// the alignment is exact (a 529-sample slip of this multi-tone program
// scores below 0 dB). Before lameDecoderDelay was applied, pcm trimmed only
// the tag's 576 and this test's SNR would have failed.
func TestGaplessTrimAlignsToEncoderInput(t *testing.T) {
	const (
		sampleRate = 44100
		kbps       = 192
		nFrames    = 60
		nInput     = nFrames * mp3.FrameSize
	)
	input := testsignal.MultiTone(sampleRate, nInput, 0, 0.7)

	e, err := mp3.NewEncoder(mp3.EncoderConfig{SampleRate: sampleRate, Channels: 1, Bitrate: kbps * 1000})
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]float32, mp3.FrameSize)
	var stream []byte
	for f := range nFrames {
		for i := range frame {
			frame[i] = float32(input[f*mp3.FrameSize+i])
		}
		if stream, err = e.EncodeFrame(stream, [][]float32{frame}); err != nil {
			t.Fatal(err)
		}
	}
	if stream, err = e.EncodeFrame(stream, nil); err != nil {
		t.Fatal(err)
	}

	// Size the tag frame off the stream's own first header so it is a legal
	// frame of the same rate/bitrate the frame API can walk over.
	var fi mp3.FrameInfo
	if _, fi, err = mp3.NewDecoder().DecodeFrame(stream, make([]float32, mp3.FrameSize*2)); err != nil || fi.FrameBytes == 0 {
		t.Fatalf("sizing the first frame: n/a, %v", err)
	}
	audioFrames := nFrames + 1 // N input frames plus the drain frame
	tag := make([]byte, fi.FrameBytes)
	copy(tag, stream[:4])
	const off = 4 + 17 // MPEG-1 mono side info ends here (no CRC)
	body := append([]byte("Info"), 0, 0, 0, xingFlagFrames|xingFlagBytes)
	body = put32(body, uint32(audioFrames))
	body = put32(body, uint32(len(tag)+len(stream)))
	padding := audioFrames*mp3.FrameSize - nInput - mp3.EncoderDelay
	body = append(body, buildLAMETag(mp3.EncoderDelay, padding)...)
	copy(tag[off:], body)
	tagged := slices.Concat(tag, stream)

	d, err := NewDecoder(bytes.NewReader(tagged), WithF32())
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if d.Info().EncoderDelay != mp3.EncoderDelay || d.Info().EncoderPadding != padding {
		t.Fatalf("parsed (delay, padding) = (%d, %d), want (%d, %d)", d.Info().EncoderDelay, d.Info().EncoderPadding, mp3.EncoderDelay, padding)
	}
	if got := d.Info().TotalSamples; got != nInput {
		t.Fatalf("Info().TotalSamples = %d, want the encoder input length %d", got, nInput)
	}
	raw, err := io.ReadAll(d)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if got := len(raw) / bytesPerF32Sample; got != nInput {
		t.Fatalf("emitted %d samples, want exactly the input length %d", got, nInput)
	}

	var sig, noise float64
	for i := range nInput {
		v := float64(math.Float32frombits(binary.LittleEndian.Uint32(raw[i*bytesPerF32Sample:])))
		d := input[i] - v
		sig += input[i] * input[i]
		noise += d * d
	}
	snr := 10 * math.Log10(sig/noise)
	if snr < 20 {
		t.Fatalf("SNR of gapless output against the encoder input = %.1f dB, want >= 20 dB (a misaligned head trim scores near or below 0 dB)", snr)
	}
}
