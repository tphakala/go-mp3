package pcm

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"testing"

	mp3 "github.com/tphakala/go-mp3"
)

// decodeAllFloat32ViaFrameAPI decodes raw with the low-level frame API,
// skipping the leading Xing/Info tag frame exactly as pcm.Decoder does, and
// returns every real audio sample in order, gapless-trimmed to
// [delay, total-padding) per channel. It never calls pcm.Decoder or
// packOutput, so it is an independent ground truth for TestDecoderF32Output:
// a bug in packOutput's float32 branch cannot also be present here.
func decodeAllFloat32ViaFrameAPI(t *testing.T, raw []byte, delay, padding int) []float32 {
	t.Helper()
	d := mp3.NewDecoder()
	scratch := make([]float32, 1152*2)
	var all []float32
	channels := 0
	for pos := 0; pos < len(raw); {
		n, fi, err := d.DecodeFrame(raw[pos:], scratch)
		if err != nil {
			t.Fatalf("DecodeFrame at %d: %v", pos, err)
		}
		if fi.FrameBytes == 0 {
			break
		}
		if n > 0 {
			if channels == 0 {
				channels = fi.Channels
			}
			if _, ok := parseXing(raw[pos:pos+fi.FrameBytes], fi.SampleRate, fi.Channels); !ok {
				all = append(all, scratch[:n*fi.Channels]...)
			}
		}
		pos += fi.FrameBytes
	}

	lo := delay * channels
	hi := len(all) - padding*channels
	if lo > hi {
		t.Fatalf("gapless trim window invalid: lo=%d hi=%d (len=%d)", lo, hi, len(all))
	}
	return all[lo:hi]
}

// float32BytesFromLE reinterprets interleaved little-endian float32 bytes
// (as WithF32 emits) back into a float32 slice.
func float32BytesFromLE(t *testing.T, b []byte) []float32 {
	t.Helper()
	if len(b)%bytesPerF32Sample != 0 {
		t.Fatalf("byte length %d is not a multiple of %d", len(b), bytesPerF32Sample)
	}
	out := make([]float32, len(b)/bytesPerF32Sample)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*bytesPerF32Sample:]))
	}
	return out
}

// TestDecoderF32Output proves WithF32 emits the decoder's native float32
// samples, bit-exact against an independent frame-API decode, and that the
// default (no option) decoder still emits S16, exactly convertF32toS16 of
// those same samples.
func TestDecoderF32Output(t *testing.T) {
	raw := readFixture(t, sine48mono128)
	want := decodeAllFloat32ViaFrameAPI(t, raw, sine48mDelay, sine48mPadding)

	f32d, err := NewDecoder(bytes.NewReader(raw), WithF32())
	if err != nil {
		t.Fatalf("NewDecoder(WithF32): %v", err)
	}
	f32Got, err := io.ReadAll(f32d)
	if err != nil {
		t.Fatalf("ReadAll (f32): %v", err)
	}

	wantBytes := len(want) * bytesPerF32Sample
	if len(f32Got) != wantBytes {
		t.Fatalf("WithF32 output = %d bytes, want %d (%d samples * %d bytes)",
			len(f32Got), wantBytes, len(want), bytesPerF32Sample)
	}

	assertF32Equal(t, float32BytesFromLE(t, f32Got), want, "WithF32 output vs frame-API decode")

	s16d, err := NewDecoder(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("NewDecoder (default): %v", err)
	}
	s16Got, err := io.ReadAll(s16d)
	if err != nil {
		t.Fatalf("ReadAll (default): %v", err)
	}

	if len(s16Got) != len(f32Got)/2 {
		t.Fatalf("default S16 output = %d bytes, want half of WithF32's %d bytes = %d",
			len(s16Got), len(f32Got), len(f32Got)/2)
	}

	wantS16 := make([]int16, len(want))
	convertF32toS16(wantS16, want)
	wantS16Bytes := make([]byte, len(wantS16)*bytesPerS16Sample)
	for i, v := range wantS16 {
		binary.LittleEndian.PutUint16(wantS16Bytes[i*bytesPerS16Sample:], uint16(v))
	}
	if !bytes.Equal(s16Got, wantS16Bytes) {
		t.Fatal("default S16 output does not equal convertF32toS16 of the same frame-API samples")
	}
}
