package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tphakala/go-mp3/internal/quality"
)

// requireFFmpeg skips (or, under MP3_REQUIRE_FFMPEG=1, fails) when ffmpeg is
// missing, mirroring requireLame and the compat gate's convention.
func requireFFmpeg(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		if os.Getenv("MP3_REQUIRE_FFMPEG") != "" {
			t.Fatalf("ffmpeg not found on PATH and MP3_REQUIRE_FFMPEG=1: %v", err)
		}
		t.Skipf("ffmpeg not found on PATH (set MP3_REQUIRE_FFMPEG=1 to require it): %v", err)
	}
	return path
}

// TestCrossCheck drives the -crosscheck diagnostic both ways against a real
// go-mp3 stream decoded by ffmpeg: this project's own pcm decode of the same
// stream agrees and stays silent, while a deliberately divergent decode (the
// pcm samples phase-inverted, so they carry the same energy but score about
// -6 dB against ffmpeg's) trips the warning. It needs ffmpeg but not lame,
// since encodeGoMP3 produces the stream.
func TestCrossCheck(t *testing.T) {
	ffmpeg := requireFFmpeg(t)
	prog, ok := quality.ProgramByName(progMultitone)
	if !ok {
		t.Fatalf("program %q missing", progMultitone)
	}
	const sr, kbps = 44100, 128
	ref := genRef(prog, sr, 2)
	stream, err := encodeGoMP3(ref, sr, kbps)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	const mp3Name = "x.mp3"
	if err := os.WriteFile(filepath.Join(dir, mp3Name), stream, 0o644); err != nil {
		t.Fatal(err)
	}
	deg, err := decodeStream(stream)
	if err != nil {
		t.Fatal(err)
	}
	tl := tools{ffmpeg: ffmpeg}

	// Agreement: this project's pcm decode and ffmpeg's decode of the same
	// stream must not trip the divergence warning, so crossCheck stays silent.
	var buf bytes.Buffer
	crossCheck(t.Context(), tl, dir, "go-mp3", mp3Name, deg, sr, &buf)
	if s := buf.String(); s != "" {
		t.Fatalf("agreeing pcm and ffmpeg decodes must be silent, got: %q", s)
	}

	// Divergence: a phase-inverted copy of the pcm decode has the same energy
	// (so its SNR is defined, not NaN) but scores about -6 dB against ffmpeg's,
	// far below the floor, and must trip the warning.
	inv := make([][]float64, len(deg))
	for c := range deg {
		inv[c] = make([]float64, len(deg[c]))
		for i, v := range deg[c] {
			inv[c][i] = -v
		}
	}
	buf.Reset()
	crossCheck(t.Context(), tl, dir, "go-mp3", mp3Name, inv, sr, &buf)
	if !strings.Contains(buf.String(), "diverge") {
		t.Fatalf("phase-inverted decode must trip the divergence warning, got: %q", buf.String())
	}
}

// TestCrossCheckWorstChannel pins the worst-channel reducer against a stereo
// stream with one divergent channel, two ways. The AGREEING case pins the
// worst-over-mean choice: a finite, agreeing left channel next to a divergent
// right must still warn, where a mean of about (120 + -6)/2 = 57 dB would not.
// The SILENT case pins the NaN skip: a digitally silent left channel (SNR NaN)
// must be skipped rather than poison the min. Both must warn.
func TestCrossCheckWorstChannel(t *testing.T) {
	ffmpeg := requireFFmpeg(t)
	prog, ok := quality.ProgramByName("stereo-wide")
	if !ok {
		t.Fatal("stereo-wide program missing")
	}
	const sr, kbps = 44100, 128
	ref := genRef(prog, sr, 2)
	stream, err := encodeGoMP3(ref, sr, kbps)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	const mp3Name = "s.mp3"
	if err := os.WriteFile(filepath.Join(dir, mp3Name), stream, 0o644); err != nil {
		t.Fatal(err)
	}
	deg, err := decodeStream(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(deg) != 2 {
		t.Fatalf("expected a stereo decode, got %d channels", len(deg))
	}
	tl := tools{ffmpeg: ffmpeg}
	invRight := make([]float64, len(deg[1]))
	for i, v := range deg[1] {
		invRight[i] = -v
	}

	// Left agrees with ffmpeg (finite, high SNR), right diverges: the worst
	// channel is the -6 dB right one, so this warns; a mean would not.
	var buf bytes.Buffer
	crossCheck(t.Context(), tl, dir, "go-mp3", mp3Name, [][]float64{deg[0], invRight}, sr, &buf)
	if !strings.Contains(buf.String(), "diverge") {
		t.Fatalf("an agreeing left channel must not hide a divergent right (a mean would): %q", buf.String())
	}

	// Left is digital silence (SNR NaN, skipped), right diverges: the skip must
	// not turn the min into NaN and swallow the warning.
	buf.Reset()
	crossCheck(t.Context(), tl, dir, "go-mp3", mp3Name, [][]float64{make([]float64, len(deg[0])), invRight}, sr, &buf)
	if !strings.Contains(buf.String(), "diverge") {
		t.Fatalf("a silent left channel (NaN) must be skipped, not hide a divergent right: %q", buf.String())
	}
}
