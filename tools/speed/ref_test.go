package main

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRefLoops(t *testing.T) {
	const rate, minSecs = 48000, 300
	tests := []struct {
		frames    int
		wantLoops int
		wantSecs  float64
	}{
		{1440000, 9, 300},
		{288000, 49, 300},
		{1439708, 10, 329.9}, // ten copies fall 0.06 s short of 300 s
		{2496000, 5, 312},
		{19200000, 0, 400},
	}
	for _, tt := range tests {
		loops, secs := refLoops(tt.frames, rate, minSecs)
		if loops != tt.wantLoops {
			t.Errorf("refLoops(%d): loops %d, want %d", tt.frames, loops, tt.wantLoops)
		}
		if math.Abs(secs-tt.wantSecs) > 0.1 {
			t.Errorf("refLoops(%d): %.3f s, want about %.1f", tt.frames, secs, tt.wantSecs)
		}
		if want := float64(loops+1) * float64(tt.frames) / rate; secs != want {
			t.Errorf("refLoops(%d): %v s, want (loops+1)*frames/rate = %v", tt.frames, secs, want)
		}
	}
}

// The ratio must use the reference wall time less its start-up, and must be
// the reference's realtime factor over go-mp3's.
func TestRefRatioUsesStartupCorrectedWall(t *testing.T) {
	r := &refResult{audioSecs: 300, wall: 2100 * time.Millisecond, startWall: 100 * time.Millisecond}
	got, ok := r.ratio(30, time.Second)
	if !ok || math.Abs(got-5.0) > 1e-9 {
		t.Errorf("ratio = %v, %v; want 5.0 (raw wall would give 4.76, inverted 0.2)", got, ok)
	}
	r = &refResult{audioSecs: 300, wall: time.Second, startWall: time.Second}
	if got, ok := r.ratio(30, time.Second); ok {
		t.Errorf("start-up equal to wall gave ratio %v, want not ok", got)
	}
}

// requireFFmpeg skips (or, under MP3_REQUIRE_FFMPEG=1, fails) when ffmpeg or
// its libmp3lame encoder is missing.
func requireFFmpeg(t *testing.T) string {
	t.Helper()
	path, err := findFFmpeg(t.Context(), "")
	if err != nil {
		if os.Getenv("MP3_REQUIRE_FFMPEG") != "" {
			t.Fatalf("MP3_REQUIRE_FFMPEG=1: %v", err)
		}
		t.Skipf("ffmpeg with libmp3lame not available (set MP3_REQUIRE_FFMPEG=1 to require it): %v", err)
	}
	return path
}

// TestRefLoopEncodesEveryCopy checks the one black-box assumption the ratio
// rests on: -stream_loop N makes ffmpeg encode N+1 copies of the input.
func TestRefLoopEncodesEveryCopy(t *testing.T) {
	bin := requireFFmpeg(t)
	dir := t.TempDir()
	wav := sineWAV(t, dir, "in.wav", 48000, 48000, 440, 0.5)
	size := func(loops int) int64 {
		out := filepath.Join(dir, "out.mp3")
		if _, _, err := runFFmpeg(t.Context(), bin, refArgs(wav, loops, 128, out)); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(out)
		if err != nil {
			t.Fatal(err)
		}
		return fi.Size()
	}
	one, four := size(0), size(3)
	// Four copies give about 3.8 times the bytes of one (tag overhead), three
	// copies about 2.9, five about 4.8.
	if r := float64(four) / float64(one); r < 3.4 || r > 4.3 {
		t.Errorf("-stream_loop 3 gave %d bytes against %d for one copy (ratio %.2f), want 4 copies (3.4 to 4.3)", four, one, r)
	}
}
