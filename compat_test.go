// TestCompatFfmpegMpg123 is the black-box cross-decoder compatibility gate
// for the public mp3.Encoder: streams this package produces must decode
// cleanly under two independent, widely deployed MP3 decoders neither of
// whose source this project ever consults (PROVENANCE.md's Encoder
// section). ffmpeg and mpg123 are used strictly as black-box binaries here.

package mp3_test

import (
	"bytes"
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	mp3 "github.com/tphakala/go-mp3"
	"github.com/tphakala/go-mp3/internal/testsignal"
)

// compatCmdTimeout bounds every compat-gate subprocess call, so a hung
// ffmpeg/mpg123/ffprobe child cannot hang the whole test run.
const compatCmdTimeout = 60 * time.Second

// compatMultiTone returns nSamples samples of a deterministic multi-tone
// program (a 440 Hz fundamental plus overtones at -6 dB and -12 dB), scaled
// so the signal stays within [-1, 1] regardless of phase alignment.
// chPhase offsets a second channel's phase so a stereo stream is
// decorrelated rather than the left channel duplicated. Delegates to
// testsignal.MultiTone (peak 0.85), the shared generator internal/dec's
// dec_test package's buildMultiTone also uses, converting each float64
// sample to float32 at the call site.
func compatMultiTone(sampleRate, nSamples int, chPhase float64) []float32 {
	v := testsignal.MultiTone(sampleRate, nSamples, chPhase, 0.85)
	x := make([]float32, nSamples)
	for i, s := range v {
		x[i] = float32(s)
	}
	return x
}

// compatFramesForOneSecond returns the smallest number of FrameSize-sample
// MP3 frames covering at least one second of audio at sampleRate.
func compatFramesForOneSecond(sampleRate int) int {
	return testsignal.FramesForOneSecond(sampleRate)
}

// compatEncodeStream encodes about one second of compatMultiTone at
// (sampleRate, nch, kbps) through the public mp3.Encoder, drains it, and
// returns the resulting stream plus the encoder's cumulative Stats.
func compatEncodeStream(t *testing.T, sampleRate, nch, kbps int) ([]byte, mp3.Stats) {
	t.Helper()

	e, err := mp3.NewEncoder(mp3.EncoderConfig{SampleRate: sampleRate, Channels: nch, Bitrate: kbps * 1000})
	if err != nil {
		t.Fatalf("NewEncoder(sr=%d nch=%d kbps=%d): %v", sampleRate, nch, kbps, err)
	}

	nFrames := compatFramesForOneSecond(sampleRate)
	totalSamples := nFrames * mp3.FrameSize

	input := make([][]float32, nch)
	for ch := range nch {
		phase := 0.0
		if ch == 1 {
			phase = 0.37 // arbitrary offset: decorrelates the right channel
		}
		input[ch] = compatMultiTone(sampleRate, totalSamples, phase)
	}

	var stream []byte
	for f := range nFrames {
		samples := make([][]float32, nch)
		for ch := range nch {
			samples[ch] = input[ch][f*mp3.FrameSize : (f+1)*mp3.FrameSize]
		}
		stream, err = e.EncodeFrame(stream, samples)
		if err != nil {
			t.Fatalf("EncodeFrame at frame %d: %v", f, err)
		}
	}
	stream, err = e.EncodeFrame(stream, nil) // drain
	if err != nil {
		t.Fatalf("drain EncodeFrame: %v", err)
	}

	return stream, e.Stats()
}

// compatIdenticalNoise returns nSamples of low-level LCG noise intended to
// be duplicated on both channels: identical L/R forces M/S every frame
// (the side channel goes near-silent before quantization), matching
// TestMsIdenticalChannels' construction (internal/dec/encx_mstereo_test.go),
// reproduced here since that file's helper lives in a different,
// non-importable package.
func compatIdenticalNoise(nSamples int) []float32 {
	seed := uint64(0xC0FFEE)
	x := make([]float32, nSamples)
	for i := range x {
		x[i] = float32(testsignal.LCGSigned(&seed) * 0.3)
	}
	return x
}

// compatDecorrelatedNoise returns two independent LCG noise channels with
// no shared structure at all, matching TestMsChannelSeparation's
// construction (internal/dec/encx_mstereo_test.go), reproduced here for the
// same reason compatIdenticalNoise is.
func compatDecorrelatedNoise(nSamples int) (x, y []float32) {
	seedX, seedY := uint64(0x5EED1), uint64(0x5EED2)
	x = make([]float32, nSamples)
	y = make([]float32, nSamples)
	for i := range x {
		x[i] = float32(testsignal.LCGSigned(&seedX) * 0.5)
		y[i] = float32(testsignal.LCGSigned(&seedY) * 0.5)
	}
	return x, y
}

// compatEncodeStereoStream encodes one second of precomputed stereo input
// (left, right, each exactly compatFramesForOneSecond(sampleRate)*
// mp3.FrameSize samples) through the public mp3.Encoder at kbps, drains it,
// and returns the resulting stream plus the encoder's cumulative Stats.
// Shared by the identical-channels and decorrelated-noise M/S compat
// programs below, which need explicit per-channel content rather than
// compatMultiTone's phase-offset tones.
func compatEncodeStereoStream(t *testing.T, sampleRate, kbps int, left, right []float32) ([]byte, mp3.Stats) {
	t.Helper()

	e, err := mp3.NewEncoder(mp3.EncoderConfig{SampleRate: sampleRate, Channels: 2, Bitrate: kbps * 1000})
	if err != nil {
		t.Fatalf("NewEncoder(sr=%d kbps=%d): %v", sampleRate, kbps, err)
	}

	nFrames := compatFramesForOneSecond(sampleRate)

	var stream []byte
	for f := range nFrames {
		samples := [][]float32{
			left[f*mp3.FrameSize : (f+1)*mp3.FrameSize],
			right[f*mp3.FrameSize : (f+1)*mp3.FrameSize],
		}
		stream, err = e.EncodeFrame(stream, samples)
		if err != nil {
			t.Fatalf("EncodeFrame at frame %d: %v", f, err)
		}
	}
	stream, err = e.EncodeFrame(stream, nil) // drain
	if err != nil {
		t.Fatalf("drain EncodeFrame: %v", err)
	}

	return stream, e.Stats()
}

// mpegBitrateKbps and mpegSampleRateHz are the ISO/IEC 11172-3 MPEG-1 Layer
// III header field tables (Table B.1 and the sampling_frequency field),
// reproduced here so countMSFrames can walk a stream's frame headers
// standalone: package mp3_test cannot reach internal/enc's own copies of
// these tables (frame.go's bitrateKbpsTable, sampleRateHzTable), and this
// package has no exported frame-header-parsing API of its own.
var (
	mpegBitrateKbps  = [15]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
	mpegSampleRateHz = [3]int{44100, 48000, 32000}
)

// countMSFrames walks stream as consecutive MPEG-1 Layer III frames and
// returns how many carry mode==1 (joint stereo) and mode_extension==2 (M/S
// on, intensity stereo off): the exact header bits encoder.go's codeFrame
// writes when msDecide selects M/S for a frame (internal/enc/encoder.go's
// mode, modeExt assignment). Used to confirm the "identical" M/S compat
// program actually produced M/S-coded frames rather than the base L/R grid
// this package's encoder always falls back to.
func countMSFrames(t *testing.T, stream []byte) int {
	t.Helper()

	count := 0
	for off := 0; off+4 <= len(stream); {
		h := stream[off : off+4]
		if h[0] != 0xFF || h[1]&0xE0 != 0xE0 {
			t.Fatalf("countMSFrames: bad frame sync at offset %d: % x", off, h[:2])
		}

		bitrateIndex := int(h[2] >> 4)
		srIndex := int(h[2] >> 2 & 3)
		padding := int(h[2] >> 1 & 1)
		mode := int(h[3] >> 6 & 3)
		modeExt := int(h[3] >> 4 & 3)

		if bitrateIndex < 1 || bitrateIndex > 14 || srIndex > 2 {
			t.Fatalf("countMSFrames: unsupported bitrate/sample-rate index at offset %d: br=%d sr=%d", off, bitrateIndex, srIndex)
		}

		if mode == 1 && modeExt == 2 {
			count++
		}

		off += 144000*mpegBitrateKbps[bitrateIndex]/mpegSampleRateHz[srIndex] + padding
	}
	return count
}

// requireCompatBinary resolves name on PATH, or skips (or, under
// MP3_REQUIRE_COMPAT=1, fails) the test: the same convention
// MP3_REQUIRE_DUMPS uses (internal/dec/dumps_test.go), so a missing local
// binary skips cleanly while CI, which installs both, gets a hard failure
// instead of a silent skip.
func requireCompatBinary(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		if os.Getenv("MP3_REQUIRE_COMPAT") != "" {
			t.Fatalf("%s not found on PATH and MP3_REQUIRE_COMPAT=1: %v", name, err)
		}
		t.Skipf("%s not found on PATH (set MP3_REQUIRE_COMPAT=1 to require it): %v", name, err)
	}
	return path
}

// compatCheckFfmpeg requires ffmpeg to decode path to exit 0 with empty
// stderr, in strict mode (-xerror turns any decode warning into an error).
func compatCheckFfmpeg(t *testing.T, ffmpeg, path string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), compatCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-xerror", "-i", path, "-f", "null", "-")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("ffmpeg failed on %s: %v\nstderr:\n%s", path, err, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("ffmpeg produced stderr output on %s, want empty:\n%s", path, stderr.String())
	}
}

// compatCheckMpg123 requires mpg123 to accept path in test (decode-only)
// mode with exit code 0.
func compatCheckMpg123(t *testing.T, mpg123, path string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), compatCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, mpg123, "-q", "--test", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mpg123 --test failed on %s: %v\noutput:\n%s", path, err, out)
	}
}

// compatCheckDuration asserts ffprobe's reported duration for path is
// within 2 frames (2*FrameSize/sampleRate seconds) of stats.Frames worth of
// audio at sampleRate. R-D-5: this stays the required quantitative gate
// because it is robust to ffmpeg's version-dependent tagless
// decoder-delay trimming, unlike an exact decoded-byte-count comparison
// would be.
func compatCheckDuration(t *testing.T, ffprobe, path string, sampleRate int, stats mp3.Stats) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), compatCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffprobe, "-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", path)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffprobe failed on %s: %v", path, err)
	}

	got, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		t.Fatalf("ffprobe duration %q: parse error: %v", strings.TrimSpace(string(out)), err)
	}

	want := float64(stats.Frames*mp3.FrameSize) / float64(sampleRate)
	tolerance := 2 * float64(mp3.FrameSize) / float64(sampleRate)
	if diff := math.Abs(got - want); diff > tolerance {
		t.Fatalf("ffprobe duration = %.6fs, want %.6fs +/- %.6fs (diff %.6fs)", got, want, tolerance, diff)
	}
}

// TestCompatFfmpegMpg123 encodes every (sample rate) x (mono, stereo) x
// (32, 128, 320 kbps) combination with the pure-Go public mp3.Encoder and
// requires both ffmpeg and mpg123 to accept the result cleanly. See this
// file's header comment for the provenance rationale.
func TestCompatFfmpegMpg123(t *testing.T) {
	ffmpeg := requireCompatBinary(t, "ffmpeg")
	mpg123 := requireCompatBinary(t, "mpg123")
	ffprobe := requireCompatBinary(t, "ffprobe")

	sampleRates := []int{44100, 48000, 32000}
	channelCounts := []int{1, 2}
	kbpsList := []int{32, 128, 320}

	dir := t.TempDir()

	for _, sr := range sampleRates {
		for _, nch := range channelCounts {
			for _, kbps := range kbpsList {
				name := "sr" + strconv.Itoa(sr) + "_nch" + strconv.Itoa(nch) + "_kbps" + strconv.Itoa(kbps)
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					stream, stats := compatEncodeStream(t, sr, nch, kbps)

					path := filepath.Join(dir, name+".mp3")
					if err := os.WriteFile(path, stream, 0o644); err != nil {
						t.Fatalf("WriteFile: %v", err)
					}

					compatCheckFfmpeg(t, ffmpeg, path)
					compatCheckMpg123(t, mpg123, path)
					compatCheckDuration(t, ffprobe, path, sr, stats)
				})
			}
		}
	}
}

// TestCompatFfmpegMpg123MS extends the compat gate with two M/S-forcing
// stereo programs the base grid's phase-offset multitone never reaches:
// identical channels (near-silent side channel, M/S selected every frame)
// and fully decorrelated noise (independent per-channel content, the
// channel-separation gate's construction). Both must decode cleanly under
// ffmpeg and mpg123 across every sample rate, at 128 and 320 kbps: the
// bitrates where M/S's bit-reservoir interplay is most exercised (32kbps
// M/S coverage already exists via the base grid's stereo case, whose two
// channels are already phase-decorrelated).
func TestCompatFfmpegMpg123MS(t *testing.T) {
	ffmpeg := requireCompatBinary(t, "ffmpeg")
	mpg123 := requireCompatBinary(t, "mpg123")
	ffprobe := requireCompatBinary(t, "ffprobe")

	sampleRates := []int{44100, 48000, 32000}
	kbpsList := []int{128, 320}

	dir := t.TempDir()

	for _, sr := range sampleRates {
		nSamples := compatFramesForOneSecond(sr) * mp3.FrameSize

		identical := compatIdenticalNoise(nSamples)
		decX, decY := compatDecorrelatedNoise(nSamples)

		programs := []struct {
			name        string
			left, right []float32
		}{
			{"identical", identical, identical},
			{"decorrelated", decX, decY},
		}

		for _, kbps := range kbpsList {
			for _, p := range programs {
				name := "sr" + strconv.Itoa(sr) + "_kbps" + strconv.Itoa(kbps) + "_" + p.name
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					stream, stats := compatEncodeStereoStream(t, sr, kbps, p.left, p.right)

					// The "identical" program is constructed so the side
					// channel goes near-silent every frame
					// (compatIdenticalNoise's doc comment), which forces
					// msDecide to select M/S every frame. If msDecide
					// regressed to always returning false, this test
					// would otherwise stay green while exercising nothing
					// beyond the base L/R grid. "decorrelated" gets no
					// such requirement: it may legitimately stay mostly
					// L/R.
					if p.name == "identical" {
						if n := countMSFrames(t, stream); n == 0 {
							t.Fatalf("%s: no M/S-coded frames found, want at least one", name)
						}
					}

					path := filepath.Join(dir, name+".mp3")
					if err := os.WriteFile(path, stream, 0o644); err != nil {
						t.Fatalf("WriteFile: %v", err)
					}

					compatCheckFfmpeg(t, ffmpeg, path)
					compatCheckMpg123(t, mpg123, path)
					compatCheckDuration(t, ffprobe, path, sr, stats)
				})
			}
		}
	}
}
