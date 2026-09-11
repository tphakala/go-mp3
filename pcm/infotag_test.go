package pcm

import (
	"bytes"
	"testing"

	mp3 "github.com/tphakala/go-mp3"
)

// TestInfoFrameHeaderDecodes proves the synthesized tag frame is a valid MPEG-1
// Layer III frame of the intended rate/bitrate/channels, using the decoder as
// the oracle rather than re-deriving the header bits here. FrameBytes must equal
// the length infoFrameLen declares, so decoders walk over exactly one frame.
func TestInfoFrameHeaderDecodes(t *testing.T) {
	cases := []struct {
		sampleRate, kbps, channels int
	}{
		{44100, 128, 2}, {44100, 128, 1}, {48000, 320, 2},
		{32000, 64, 1}, {48000, 32, 2}, {44100, 320, 1},
	}
	out := make([]float32, mp3.FrameSize*2)
	for _, tc := range cases {
		frame := buildInfoFrame(tc.sampleRate, tc.kbps, tc.channels, 10, 1000, mp3.EncoderDelay, 500)
		// Append a duplicate so the decoder always sees a following sync word.
		buf := append(bytes.Clone(frame), frame...)
		_, fi, err := mp3.NewDecoder().DecodeFrame(buf, out)
		if err != nil {
			t.Fatalf("%dHz %dkbps ch%d: DecodeFrame: %v", tc.sampleRate, tc.kbps, tc.channels, err)
		}
		if fi.Layer != 3 {
			t.Errorf("%dHz %dkbps ch%d: Layer = %d, want 3", tc.sampleRate, tc.kbps, tc.channels, fi.Layer)
		}
		if fi.SampleRate != tc.sampleRate {
			t.Errorf("SampleRate = %d, want %d", fi.SampleRate, tc.sampleRate)
		}
		if fi.Channels != tc.channels {
			t.Errorf("%dHz %dkbps: Channels = %d, want %d", tc.sampleRate, tc.kbps, fi.Channels, tc.channels)
		}
		if fi.Bitrate != tc.kbps {
			t.Errorf("%dHz ch%d: Bitrate = %d kbps, want %d", tc.sampleRate, tc.channels, fi.Bitrate, tc.kbps)
		}
		if fi.FrameBytes != len(frame) || fi.FrameBytes != infoFrameLen(tc.sampleRate, tc.kbps) {
			t.Errorf("%dHz %dkbps ch%d: FrameBytes = %d, want %d", tc.sampleRate, tc.kbps, tc.channels, fi.FrameBytes, len(frame))
		}
	}
}

// TestBuildInfoFrameParses round-trips the write side through the parse side:
// a frame built with known fields must be detected as an Info tag with those
// exact frames/bytes, and its LAME extension must yield back the delay/padding.
// The delay and padding use nonzero low/high nibbles so a mis-shifted 12-bit
// pack fails loudly.
func TestBuildInfoFrameParses(t *testing.T) {
	const (
		sr, kbps, ch          = 44100, 128, 2
		frames                = 1234
		totalBytes            = 567890
		wantDelay, wantPadded = 1105, 699 // 0x451, 0x2bb
	)
	frame := buildInfoFrame(sr, kbps, ch, frames, totalBytes, wantDelay, wantPadded)

	xh, ok := parseXing(frame, sr, ch)
	if !ok {
		t.Fatal("parseXing returned ok=false for a built Info frame")
	}
	if !xh.isInfo {
		t.Error("parseXing: isInfo = false, want true (CBR Info tag)")
	}
	if xh.frames != frames {
		t.Errorf("parsed frames = %d, want %d", xh.frames, frames)
	}
	if xh.bytes != totalBytes {
		t.Errorf("parsed bytes = %d, want %d", xh.bytes, totalBytes)
	}

	delay, padding, ok := parseLAME(frame, xh.lameStart)
	if !ok {
		t.Fatal("parseLAME returned ok=false for a built LAME extension")
	}
	if delay != wantDelay || padding != wantPadded {
		t.Errorf("parsed (delay, padding) = (%d, %d), want (%d, %d)", delay, padding, wantDelay, wantPadded)
	}
}

func TestInfoFrameLen(t *testing.T) {
	cases := []struct {
		sampleRate, kbps, want int
	}{
		{44100, 128, 417},
		{48000, 320, 960},
		{32000, 32, 144},
		{48000, 32, 96}, // the smallest supported frame; must still hold the tag
	}
	for _, tc := range cases {
		if got := infoFrameLen(tc.sampleRate, tc.kbps); got != tc.want {
			t.Errorf("infoFrameLen(%d, %d) = %d, want %d", tc.sampleRate, tc.kbps, got, tc.want)
		}
	}
}

// TestLamePadding pins the write-side padding via the decoder's recovery
// contract, not by re-stating lamePadding's own formula: a gapless decode
// recovers exactly nInput as total - EncoderDelay - padding, where total =
// audioFrames*FrameSize. A wrong padding breaks that identity.
func TestLamePadding(t *testing.T) {
	cases := []struct {
		audioFrames, nInput int64
	}{
		{9, 8 * mp3.FrameSize},      // 8 full frames + drain, frame-aligned
		{9, 8*mp3.FrameSize - 1151}, // maximal partial tail
		{2, 1},                      // tiny stream
		{1, 0},                      // empty: drain flush frame only
	}
	for _, tc := range cases {
		got := lamePadding(tc.audioFrames, tc.nInput)
		if got < 0 || got > 4095 {
			t.Errorf("lamePadding(%d, %d) = %d, outside the 12-bit range", tc.audioFrames, tc.nInput, got)
		}
		total := tc.audioFrames * mp3.FrameSize
		if recovered := total - mp3.EncoderDelay - int64(got); recovered != tc.nInput {
			t.Errorf("lamePadding(%d, %d)=%d: total-delay-padding = %d, want nInput %d",
				tc.audioFrames, tc.nInput, got, recovered, tc.nInput)
		}
	}
	// Clamp: an impossibly large frame count saturates at the 12-bit max.
	if got := lamePadding(1<<20, 0); got != 4095 {
		t.Fatalf("clamped padding = %d, want 4095", got)
	}
	// Clamp: a negative arithmetic result floors at zero.
	if got := lamePadding(0, 1<<20); got != 0 {
		t.Fatalf("floored padding = %d, want 0", got)
	}
}

// TestToU32 covers the tag-field count clamp at the boundaries the int64 widening
// guards (negative and above the uint32 max), which the normal encode path never
// reaches.
func TestToU32(t *testing.T) {
	cases := []struct {
		in   int64
		want uint32
	}{
		{0, 0},
		{1234, 1234},
		{0xffffffff, 0xffffffff},
		{-1, 0},          // negative floors to 0
		{1 << 40, 0xffffffff}, // above uint32 max saturates
	}
	for _, tc := range cases {
		if got := toU32(tc.in); got != tc.want {
			t.Errorf("toU32(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
