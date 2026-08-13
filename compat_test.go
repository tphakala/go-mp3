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
