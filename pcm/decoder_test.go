package pcm

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	mp3 "github.com/tphakala/go-mp3"
)

const (
	fixturesDir   = "../testdata/fixtures"
	sine48mono128 = fixturesDir + "/sine48m_128.mp3"

	// sine48m_128.mp3 is 48 kHz mono: 86 frames of 1152 samples/channel =
	// 99072 samples/channel (the count Phase 0+1's mp3 test established).
	sine48mSamples  = 99072
	sine48mS16Bytes = sine48mSamples * 1 * 2 // mono, 2 bytes/sample (S16)
)

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return b
}

// decodeAllBytes returns the full S16 output of decoding r.
func decodeAllBytes(t *testing.T, r io.Reader) []byte {
	t.Helper()
	d, err := NewDecoder(r)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	b, err := io.ReadAll(d)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return b
}

func TestDecoderInfoAndRead(t *testing.T) {
	raw := readFixture(t, sine48mono128)
	d, err := NewDecoder(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	if got := d.Info().SampleRate; got != 48000 {
		t.Errorf("Info().SampleRate = %d, want 48000", got)
	}
	if got := d.Info().Channels; got != 1 {
		t.Errorf("Info().Channels = %d, want 1", got)
	}
	if got := d.Info().TotalSamples; got != 0 {
		t.Errorf("Info().TotalSamples = %d, want 0 (set by a later task)", got)
	}
	if got := d.Info().Duration(); got != 0 {
		t.Errorf("Info().Duration() = %v, want 0 when TotalSamples is unknown", got)
	}

	out, err := io.ReadAll(d)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(out) != sine48mS16Bytes {
		t.Errorf("decoded %d bytes, want %d (%d samples * 2 bytes S16)", len(out), sine48mS16Bytes, sine48mSamples)
	}

	// The stream is exhausted: a further read must report the clean end.
	var scratch [16]byte
	if n, err := d.Read(scratch[:]); n != 0 || !errors.Is(err, io.EOF) {
		t.Errorf("post-EOF Read = (%d, %v), want (0, io.EOF)", n, err)
	}
}

func TestDecoderSmallReads(t *testing.T) {
	raw := readFixture(t, sine48mono128)
	want := decodeAllBytes(t, bytes.NewReader(raw))

	d, err := NewDecoder(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	var got []byte
	buf := make([]byte, 1)
	for {
		n, err := d.Read(buf)
		got = append(got, buf[:n]...)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("byte-at-a-time output differs from ReadAll (%d vs %d bytes)", len(got), len(want))
	}
}

func TestDecoderWriteTo(t *testing.T) {
	raw := readFixture(t, sine48mono128)
	want := decodeAllBytes(t, bytes.NewReader(raw))

	d, err := NewDecoder(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	var buf bytes.Buffer
	n, err := d.WriteTo(&buf)
	if err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	if n != int64(len(want)) {
		t.Errorf("WriteTo returned %d, want %d", n, len(want))
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("WriteTo output differs from ReadAll (%d vs %d bytes)", buf.Len(), len(want))
	}
}

func TestNewDecoderNilReader(t *testing.T) {
	if _, err := NewDecoder(nil); err == nil {
		t.Fatal("NewDecoder(nil) = nil error, want non-nil")
	}
}

func TestInfoDuration(t *testing.T) {
	i := Info{SampleRate: 48000, TotalSamples: 96000}
	if got, want := i.Duration(), 2*time.Second; got != want {
		t.Errorf("Duration() = %v, want %v", got, want)
	}
	if got := (Info{SampleRate: 48000}).Duration(); got != 0 {
		t.Errorf("Duration() with unknown TotalSamples = %v, want 0", got)
	}
}

// mp3FrameTotals decodes data with the low-level mp3 frame API, returning the
// total samples per channel and the channel count, so a pcm byte count can be
// checked against an independent ground truth rather than a hard-coded number.
func mp3FrameTotals(t *testing.T, data []byte) (samplesPerChannel, channels int) {
	t.Helper()
	d := mp3.NewDecoder()
	pcm := make([]float32, 1152*2)
	for pos := 0; pos < len(data); {
		n, fi, err := d.DecodeFrame(data[pos:], pcm)
		if err != nil {
			t.Fatalf("DecodeFrame at %d: %v", pos, err)
		}
		if n > 0 {
			samplesPerChannel += n
			channels = fi.Channels
		}
		if fi.FrameBytes == 0 {
			break
		}
		pos += fi.FrameBytes
	}
	return samplesPerChannel, channels
}

// TestDecoderStereoByteCount exercises the packOutput n*Channels interleave
// path, which the mono fixtures do not. The expected byte count is derived
// independently from the low-level frame API, so a double-count or dropped
// channel would show up as an inequality.
func TestDecoderStereoByteCount(t *testing.T) {
	const path = fixturesDir + "/sine44s_128.mp3"
	raw := readFixture(t, path)

	perChannel, channels := mp3FrameTotals(t, raw)
	if channels != 2 {
		t.Fatalf("fixture channel count = %d, want a stereo (2ch) fixture", channels)
	}

	d, err := NewDecoder(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if got := d.Info().Channels; got != 2 {
		t.Errorf("Info().Channels = %d, want 2", got)
	}
	if got := d.Info().SampleRate; got != 44100 {
		t.Errorf("Info().SampleRate = %d, want 44100", got)
	}

	out, err := io.ReadAll(d)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	want := perChannel * channels * bytesPerS16Sample
	if len(out) != want {
		t.Errorf("stereo decoded %d bytes, want %d (%d samples/ch * %d ch * %d bytes)",
			len(out), want, perChannel, channels, bytesPerS16Sample)
	}
}

// TestDecodeNextFrameNoAllocSteadyState pins fix for the per-frame frameBuf
// re-allocation: once the buffers are warm, decoding a frame (fill + compact +
// DecodeFrame + pack) must not allocate.
func TestDecodeNextFrameNoAllocSteadyState(t *testing.T) {
	raw := readFixture(t, sine48mono128)
	d, err := NewDecoder(bytes.NewReader(raw)) // decodes frame 1, allocates buffers
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	// Warm the reused buffers and the underlying fast-path header cache.
	for range 3 {
		if err := d.decodeNextFrame(); err != nil {
			t.Fatalf("warmup decodeNextFrame: %v", err)
		}
	}

	avg := testing.AllocsPerRun(20, func() {
		if err := d.decodeNextFrame(); err != nil && !errors.Is(err, io.EOF) {
			t.Fatalf("decodeNextFrame: %v", err)
		}
	})
	if avg != 0 {
		t.Errorf("steady-state decodeNextFrame allocs = %v, want 0", avg)
	}
}
