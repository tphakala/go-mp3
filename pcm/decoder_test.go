package pcm

import (
	"bytes"
	"encoding/binary"
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

	// sine48m_128.mp3 is 48 kHz mono: 86 frames of 1152 samples/channel on
	// disk (the count Phase 0+1's mp3 test established), but its first
	// frame is a LAME Info tag, not audio: pcm.Decoder excludes it, so 85
	// real audio frames are actually emitted.
	sine48mSamples  = 85 * 1152              // 97920: real audio frames only, tag excluded
	sine48mS16Bytes = sine48mSamples * 1 * 2 // mono, 2 bytes/sample (S16)

	// sine48mXingTotalSamples is Info().TotalSamples as the fixture's own
	// Info tag derives it: frames field = 85 (0x55, verified against the
	// fixture bytes at offset 0x1d: "00 00 00 55"). The LAME/Xing frames
	// field counts real audio frames only, excluding the tag frame itself,
	// so 85*1152 = 97920, exactly equal to sine48mSamples above (before
	// gapless trim, which Task 3 introduces, the two must match exactly).
	sine48mXingTotalSamples = 85 * 1152
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
	if got := d.Info().TotalSamples; got != sine48mXingTotalSamples {
		t.Errorf("Info().TotalSamples = %d, want %d (from the fixture's Info tag)", got, sine48mXingTotalSamples)
	}
	wantDur := time.Duration(sine48mXingTotalSamples) * time.Second / time.Duration(48000)
	if got := d.Info().Duration(); got != wantDur {
		t.Errorf("Info().Duration() = %v, want %v", got, wantDur)
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
// channel would show up as an inequality. sine44s_128.mp3's first frame is a
// Xing/Info tag, not audio: mp3FrameTotals's raw frame-API walk decodes it
// like any other frame, so its sample contribution is subtracted here to
// match what pcm.Decoder actually emits (the tag frame excluded).
func TestDecoderStereoByteCount(t *testing.T) {
	const path = fixturesDir + "/sine44s_128.mp3"
	raw := readFixture(t, path)

	perChannel, channels := mp3FrameTotals(t, raw)
	if channels != 2 {
		t.Fatalf("fixture channel count = %d, want a stereo (2ch) fixture", channels)
	}

	tagOnly := mp3.NewDecoder()
	tagSamples, tagInfo, err := tagOnly.DecodeFrame(raw, make([]float32, 1152*2))
	if err != nil {
		t.Fatalf("decode frame 0: %v", err)
	}
	if _, ok := parseXing(raw[:tagInfo.FrameBytes], tagInfo.SampleRate, tagInfo.Channels); !ok {
		t.Fatalf("fixture %s: frame 0 is not a Xing/Info tag; test assumption is stale", path)
	}
	perChannel -= tagSamples

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

// TestDecoderXingDurationAndFirstFrame decodes a fixture whose first frame
// is a LAME Xing/Info tag (l3-nonstandard-sin1k0db_lame_vbrtag.bit) and
// checks two things Task 1 could not: TotalSamples is derived from the tag,
// and the tag frame's own (non-audio) samples are never emitted.
func TestDecoderXingDurationAndFirstFrame(t *testing.T) {
	const vector = "l3-nonstandard-sin1k0db_lame_vbrtag.bit"
	raw := readVectorFixture(t, vector)

	// The Xing/Info header sits at the fixed MPEG1-stereo offset (36) and
	// declares frames = 0x0000013c = 316 (verified directly against the
	// fixture bytes at offset 0x2c: "00 00 01 3c"; this expectation does not
	// call parseXing itself, so it is an independent check). The LAME/Xing
	// frames field counts real audio frames only, excluding the tag frame
	// itself, so 316 real audio frames follow the tag with no further
	// arithmetic needed.
	const declaredFrames = 316
	const wantSamples = uint64(declaredFrames) * 1152 // MPEG1 Layer III: 1152 samples/frame

	d, err := NewDecoder(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if got := d.Info().SampleRate; got != 44100 {
		t.Errorf("Info().SampleRate = %d, want 44100", got)
	}
	if got := d.Info().Channels; got != 2 {
		t.Errorf("Info().Channels = %d, want 2", got)
	}
	if got := d.Info().TotalSamples; got != wantSamples {
		t.Errorf("Info().TotalSamples = %d, want %d (%d*1152)", got, wantSamples, declaredFrames)
	}

	// Frame 0 is the tag; decode it and the frame that follows independently
	// with the frame API (same order, same Decoder instance, so bit
	// reservoir state matches what pcm.Decoder itself does internally) to
	// know what the FIRST emitted sample should be if the tag frame is
	// correctly excluded from the output.
	independent := mp3.NewDecoder()
	scratch := make([]float32, 1152*2)
	n0, fi0, err := independent.DecodeFrame(raw, scratch)
	if err != nil {
		t.Fatalf("independent decode of tag frame: %v", err)
	}
	if n0 == 0 {
		t.Fatal("tag frame decoded 0 samples; test assumption (it decodes like ordinary audio) is stale")
	}
	n1, fi1, err := independent.DecodeFrame(raw[fi0.FrameBytes:], scratch)
	if err != nil {
		t.Fatalf("independent decode of first real frame: %v", err)
	}
	if n1 == 0 {
		t.Fatal("first real frame decoded 0 samples; test assumption is stale")
	}
	wantS16 := make([]int16, n1*fi1.Channels)
	convertF32toS16(wantS16, scratch[:n1*fi1.Channels])
	wantBytes := make([]byte, len(wantS16)*bytesPerS16Sample)
	for i, v := range wantS16 {
		binary.LittleEndian.PutUint16(wantBytes[i*bytesPerS16Sample:], uint16(v))
	}

	got := make([]byte, len(wantBytes))
	if _, err := io.ReadFull(d, got); err != nil {
		t.Fatalf("Read first frame from pcm.Decoder: %v", err)
	}
	if !bytes.Equal(got, wantBytes) {
		t.Fatal("first emitted samples do not match the first real audio frame; the tag frame may have been emitted instead")
	}

	// Regression invariant: before gapless trim (Task 3), the count of
	// samples pcm.Decoder actually emits must equal Info().TotalSamples
	// exactly. This is the check that would have caught the (frames-1) bug:
	// a future off-by-a-frame in the TotalSamples derivation now fails
	// loudly here instead of only surfacing downstream.
	rest, err := io.ReadAll(d)
	if err != nil {
		t.Fatalf("ReadAll (stream remainder): %v", err)
	}
	totalBytes := len(got) + len(rest)
	channels := d.Info().Channels
	if totalBytes%(channels*bytesPerS16Sample) != 0 {
		t.Fatalf("total emitted bytes %d is not a whole number of samples (channels=%d)", totalBytes, channels)
	}
	gotSamples := uint64(totalBytes / (channels * bytesPerS16Sample))
	if gotSamples != wantSamples {
		t.Errorf("emitted samples/channel = %d, want %d (must equal Info().TotalSamples)", gotSamples, wantSamples)
	}
}

// TestDecoderCBRDurationNoTag exercises the Step-3b fallback: a tag-less
// stream opened as an io.Seeker gets TotalSamples estimated from the audio
// byte length under a CBR assumption. sine44s_32.mp3 carries no Xing/Info
// tag (verified: no "Xing"/"Info" magic at the MPEG2-stereo offset 21) and
// is exactly CBR: 58 frames of 144 bytes each, filling the 8352-byte file
// with no ID3v2 header and no remainder, 576 samples/frame (MPEG2/2.5 Layer
// III, this fixture decodes at 16000 Hz). The CBR estimate is therefore
// exact, not merely plausible.
func TestDecoderCBRDurationNoTag(t *testing.T) {
	const path = fixturesDir + "/sine44s_32.mp3"
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("close %s: %v", path, err)
		}
	}()

	d, err := NewDecoder(f)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	const wantFrames = 58
	const wantSamples = uint64(wantFrames) * 576

	if got := d.Info().TotalSamples; got != wantSamples {
		t.Errorf("Info().TotalSamples = %d, want %d (CBR estimate: %d frames * 576)", got, wantSamples, wantFrames)
	}
	if got := d.Info().Duration(); got <= 0 {
		t.Errorf("Info().Duration() = %v, want > 0", got)
	}
	// audioBytes(8352)/frameBytes(144) = 58 exactly; 58*576/16000 = 2.088s.
	const wantDur = 2088 * time.Millisecond
	if got := d.Info().Duration(); got != wantDur {
		t.Errorf("Info().Duration() = %v, want %v", got, wantDur)
	}

	// The seek probe used to measure the file must restore the reader
	// position: decoding should proceed unaffected.
	out, err := io.ReadAll(d)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(out) == 0 {
		t.Error("ReadAll returned no bytes after the CBR duration probe; reader position not restored?")
	}
}
