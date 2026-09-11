package pcm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	mp3 "github.com/tphakala/go-mp3"
)

// compatCmdTimeout bounds each external decoder invocation so a hung ffmpeg or
// mpg123 child cannot stall the whole test run.
const compatCmdTimeout = 30 * time.Second

// requirePCMCompatBinary resolves name on PATH, or skips (or, under
// MP3_REQUIRE_COMPAT=1, fails). Mirrors the root package's requireCompatBinary
// convention: a missing local binary skips cleanly, while CI, which installs
// both and sets the env, gets a hard failure instead of a silent skip.
func requirePCMCompatBinary(t *testing.T, name string) string {
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

// decodeRawS16LE runs an external decoder that emits interleaved little-endian
// S16 on stdout and returns the per-channel sample count together with the raw
// samples. Both current ffmpeg and mpg123 apply the LAME gapless trim.
func decodeRawS16LE(t *testing.T, name string, args []string, channels int) (perCh int, samples []int16) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), compatCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s decode failed: %v\nstderr:\n%s", name, err, stderr.String())
	}
	return len(out) / 2 / channels, bytesToS16(out)
}

// TestGaplessCompatFfmpegMpg123 is the external cross-check for issue #63: a
// stream carrying the encoder's Xing/Info + LAME tag must decode to EXACTLY the
// input length under both ffmpeg and mpg123 (they honor the encoder delay and
// padding), while the same audio encoded tagless decodes longer (the delay is
// not trimmed). The exact-count assertion is safe for the tagged case because a
// well-formed LAME tag pins the trim to delay+529 in both tools; the tagless
// trim is version-dependent, so it is only asserted to exceed the input.
func TestGaplessCompatFfmpegMpg123(t *testing.T) {
	ffmpeg := requirePCMCompatBinary(t, "ffmpeg")
	mpg123 := requirePCMCompatBinary(t, "mpg123")

	cases := []struct {
		sampleRate, channels, bitrate int
		n                             int // input samples per channel
	}{
		{44100, 2, 128000, mp3.FrameSize*10 + 517},
		{44100, 1, 128000, mp3.FrameSize*7 + 1},
		{48000, 2, 320000, mp3.FrameSize * 6},
		{32000, 1, 64000, mp3.FrameSize*5 + 900},
	}

	dir := t.TempDir()
	for _, tc := range cases {
		name := fmt.Sprintf("sr%d_ch%d_kbps%d", tc.sampleRate, tc.channels, tc.bitrate/1000)
		t.Run(name, func(t *testing.T) {
			cfg := Config{SampleRate: tc.sampleRate, Channels: tc.channels, Bitrate: tc.bitrate}
			pcm := genSineS16(tc.n, tc.channels, 1000, tc.sampleRate)

			taggedPath := filepath.Join(dir, name+"_tagged.mp3")
			taglessPath := filepath.Join(dir, name+"_tagless.mp3")
			writeEncoded(t, taggedPath, cfg, pcm)
			taglessCfg := cfg
			taglessCfg.OmitGaplessTag = true
			writeEncoded(t, taglessPath, taglessCfg, pcm)

			// ffmpeg: tagged decodes to exactly n; tagless decodes longer.
			ffTaggedN, ffSamples := decodeRawS16LE(t, ffmpeg,
				[]string{"-v", "error", "-i", taggedPath, "-f", "s16le", "-"}, tc.channels)
			if ffTaggedN != tc.n {
				t.Errorf("ffmpeg tagged decode = %d samples/ch, want exactly %d", ffTaggedN, tc.n)
			}
			ffTaglessN, _ := decodeRawS16LE(t, ffmpeg,
				[]string{"-v", "error", "-i", taglessPath, "-f", "s16le", "-"}, tc.channels)
			if ffTaglessN <= tc.n {
				t.Errorf("ffmpeg tagless decode = %d samples/ch, want more than %d (delay untrimmed)", ffTaglessN, tc.n)
			}

			// mpg123: same expectation. -s emits raw native-endian S16; only the
			// per-channel sample COUNT is compared here, which is endian-independent,
			// and gapless is applied by default.
			mpTaggedN, _ := decodeRawS16LE(t, mpg123, []string{"-q", "-s", taggedPath}, tc.channels)
			if mpTaggedN != tc.n {
				t.Errorf("mpg123 tagged decode = %d samples/ch, want exactly %d", mpTaggedN, tc.n)
			}
			mpTaglessN, _ := decodeRawS16LE(t, mpg123, []string{"-q", "-s", taglessPath}, tc.channels)
			if mpTaglessN <= tc.n {
				t.Errorf("mpg123 tagless decode = %d samples/ch, want more than %d (delay untrimmed)", mpTaglessN, tc.n)
			}

			// Phase alignment, not just length: the ffmpeg-decoded tagged output
			// must match the input over a guarded interior, which only holds when
			// the head trim landed on the right sample.
			if ffTaggedN == tc.n {
				ref := bytesToS16(pcm)
				guard := mp3.FrameSize * tc.channels
				if len(ref) > 2*guard && len(ffSamples) == len(ref) {
					n := len(ref) - 2*guard
					if snr := snrDB(ref[guard:guard+n], ffSamples[guard:guard+n]); snr < 15 {
						t.Errorf("ffmpeg tagged decode SNR vs input = %.1f dB, want >= 15 (alignment)", snr)
					}
				}
			}
		})
	}
}

// writeEncoded encodes pcm with cfg and writes the MP3 to path.
func writeEncoded(t *testing.T, path string, cfg Config, pcm []byte) {
	t.Helper()
	var buf bytes.Buffer
	if err := EncodeInterleaved(&buf, cfg, pcm); err != nil {
		t.Fatalf("EncodeInterleaved: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}
