package pcm

import (
	"bufio"
	"bytes"
	"io"
	"testing"
)

// TestGaplessTrim is the Task 3 conformance gate. It decodes a real
// LAME-encoded fixture (a Xing/Info tag frame carrying a LAME extension with
// encoder delay and padding, followed by 1 kHz-tone audio) through the full
// pcm.Decoder and requires the emitted sample count, per channel, to equal the
// ISO reference .pcm exactly. Phase 0+1's frame-API conformance documented a
// 4608-sample (2 stereo frame) gap on this vector: 1 tag frame the raw walk
// emitted plus the LAME gapless delay+padding it did not trim. pcm.Decoder
// already excludes the tag frame (Task 2); gapless trim (this task) closes the
// remaining delay+padding, so the gap must be zero.
//
// The .bit and its .pcm reference are gitignored conformance material, fetched
// by scripts/fetch-vectors.sh; both honor the MP3_REQUIRE_DUMPS convention
// (skip when absent locally, hard-fail under MP3_REQUIRE_DUMPS in CI).
func TestGaplessTrim(t *testing.T) {
	const stem = "l3-nonstandard-sin1k0db_lame_vbrtag"
	raw := readVectorFixture(t, stem+".bit")
	ref := readVectorFixture(t, stem+".pcm")

	d, err := NewDecoder(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	channels := d.Info().Channels
	if channels != 2 {
		t.Fatalf("fixture channels = %d, want 2", channels)
	}

	out, err := io.ReadAll(d)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	// The reference is interleaved 16-bit signed PCM: 2 bytes/sample.
	const bytesPerSample = 2
	refPerChannel := len(ref) / bytesPerSample / channels
	gotPerChannel := len(out) / bytesPerSample / channels

	if gotPerChannel != refPerChannel {
		t.Errorf("emitted %d samples/channel, want %d (reference); discrepancy %d",
			gotPerChannel, refPerChannel, gotPerChannel-refPerChannel)
	}

	// The T2 invariant survives gapless: Info().TotalSamples equals the emitted
	// per-channel count (both now post-gapless).
	if got := d.Info().TotalSamples; got != uint64(gotPerChannel) {
		t.Errorf("Info().TotalSamples = %d, want %d (== emitted samples/channel)", got, gotPerChannel)
	}
}

// TestGaplessDelayAndPadding pins the delay+padding math on a fixture whose
// total IS known (its Info tag carries a frame count), so both the head trim
// and the tail trim apply. sine48m_128.mp3: 85 audio frames * 1152 = 97920
// samples/channel pre-trim; LAME delay 576, padding 1344; emitted = 97920 -
// 576 - 1344 = 96000. The padding (1344) exceeds one frame (1152), so the tail
// trim spans the last two frames, exercising the multi-frame tail boundary.
func TestGaplessDelayAndPadding(t *testing.T) {
	raw := readFixture(t, sine48mono128)
	d, err := NewDecoder(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	if got := d.Info().EncoderDelay; got != sine48mDelay {
		t.Errorf("Info().EncoderDelay = %d, want %d", got, sine48mDelay)
	}
	if got := d.Info().EncoderPadding; got != sine48mPadding {
		t.Errorf("Info().EncoderPadding = %d, want %d", got, sine48mPadding)
	}
	if got := d.Info().TotalSamples; got != uint64(sine48mSamples) {
		t.Errorf("Info().TotalSamples = %d, want %d", got, sine48mSamples)
	}

	out, err := io.ReadAll(d)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	gotSamples := len(out) / bytesPerS16Sample // mono
	if gotSamples != sine48mSamples {
		t.Errorf("emitted %d samples/channel, want %d (97920 - %d delay - %d padding)",
			gotSamples, sine48mSamples, sine48mDelay, sine48mPadding)
	}
	if uint64(gotSamples) != d.Info().TotalSamples {
		t.Errorf("emitted %d != Info().TotalSamples %d (post-gapless invariant)", gotSamples, d.Info().TotalSamples)
	}
}

// TestGaplessHeadOnlyTrim covers the branch where the total length is unknown:
// only the head is trimmed, the tail is left intact, and TotalSamples stays 0.
// It takes the sine48m fixture, zeroes the Info tag's frame count (so no total
// can be derived), and feeds it through a non-seeker (a bufio.Reader, which is
// not an io.Seeker) so the CBR byte-length fallback cannot supply one either.
// The LAME delay is still parsed and head-trimmed; all 85 audio frames decode,
// so emitted = 85*1152 - 576 = 97344 samples/channel.
func TestGaplessHeadOnlyTrim(t *testing.T) {
	raw := readFixture(t, sine48mono128)
	mod := bytes.Clone(raw)

	// Zero the Info tag's 4-byte frame-count field (the first field after the
	// 4-byte magic and 4-byte flags word) so parseXing reports frames = 0 while
	// the flag stays set and every later field keeps its position.
	idx := bytes.Index(mod, []byte("Info"))
	if idx < 0 {
		t.Fatal("fixture has no Info tag; test assumption is stale")
	}
	framesOff := idx + xingMagicLen + xingFlagsLen
	for i := range xingFieldLen {
		mod[framesOff+i] = 0
	}

	// bufio.Reader is not an io.Seeker, so the decoder cannot measure the
	// stream length: TotalSamples stays unknown (0), and only the head trims.
	r := bufio.NewReader(bytes.NewReader(mod))
	d, err := NewDecoder(r)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if got := d.Info().EncoderDelay; got != sine48mDelay {
		t.Errorf("Info().EncoderDelay = %d, want %d", got, sine48mDelay)
	}
	if got := d.Info().TotalSamples; got != 0 {
		t.Errorf("Info().TotalSamples = %d, want 0 (unknown: no frame count, non-seekable)", got)
	}

	out, err := io.ReadAll(d)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	const wantSamples = 85*1152 - sine48mDelay // head trim only
	gotSamples := len(out) / bytesPerS16Sample // mono
	if gotSamples != wantSamples {
		t.Errorf("emitted %d samples/channel, want %d (head trim only)", gotSamples, wantSamples)
	}
}
