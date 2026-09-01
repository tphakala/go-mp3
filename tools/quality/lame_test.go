package main

import (
	"math"
	"os"
	"os/exec"
	"testing"

	mp3 "github.com/tphakala/go-mp3"
	"github.com/tphakala/go-mp3/internal/quality"
)

// requireLame skips (or, under MP3_REQUIRE_LAME=1, fails) when the lame
// binary is missing: the MP3_REQUIRE_COMPAT convention of the compat gate.
func requireLame(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("lame")
	if err != nil {
		if os.Getenv("MP3_REQUIRE_LAME") != "" {
			t.Fatalf("lame not found on PATH and MP3_REQUIRE_LAME=1: %v", err)
		}
		t.Skipf("lame not found on PATH (set MP3_REQUIRE_LAME=1 to require it): %v", err)
	}
	return path
}

// TestQualityHarnessLame runs one real case end to end: both encoders, pcm
// decode, alignment, metrics. It pins the two alignment facts the harness
// relies on (go-mp3's tagless stream lands at mp3.TotalDelay; LAME's tagged
// stream is gapless-trimmed by pcm to lag 0) and sanity-bounds the metrics.
func TestQualityHarnessLame(t *testing.T) {
	lame := requireLame(t)
	tl := tools{lame: lame} // no external perceptual tools in the smoke test
	prog, ok := quality.ProgramByName("tone-click")
	if !ok {
		t.Fatal("tone-click program missing")
	}
	spec := caseSpec{Program: prog, SampleRate: 44100, Kbps: 128, Seconds: 2}
	res, err := runCase(t.Context(), tl, t.TempDir(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if res.GoMP3.Lag != mp3.TotalDelay {
		t.Fatalf("go-mp3 lag = %d, want mp3.TotalDelay %d", res.GoMP3.Lag, mp3.TotalDelay)
	}
	if res.LAME.Lag != 0 {
		t.Fatalf("lame lag = %d, want 0 (pcm gapless trim of the LAME tag)", res.LAME.Lag)
	}
	for _, r := range []encoderResult{res.GoMP3, res.LAME} {
		if r.Bytes < 1000 {
			t.Fatalf("%s: stream only %d bytes", r.Name, r.Bytes)
		}
		if r.Metrics.SNR < 5 || r.Metrics.SNR > quality.SNRCap {
			t.Fatalf("%s: SNR %v out of sane range", r.Name, r.Metrics.SNR)
		}
		if math.IsNaN(r.Metrics.LSD) || r.Metrics.LSD <= 0 {
			t.Fatalf("%s: LSD %v", r.Name, r.Metrics.LSD)
		}
		if r.Metrics.PreEchoN == 0 || math.IsNaN(r.Metrics.PreEcho) {
			t.Fatalf("%s: tone-click produced no pre-echo events", r.Name)
		}
		if r.Metrics.Bandwidth < 10000 {
			t.Fatalf("%s: bandwidth %v Hz, want the tone-click bursts' full band", r.Name, r.Metrics.Bandwidth)
		}
		if !math.IsNaN(r.MOS) || !math.IsNaN(r.ODG) {
			t.Fatalf("%s: external metrics must be NaN without tools", r.Name)
		}
	}
	if res.Channels != 1 || res.SampleRate != 44100 || res.Kbps != 128 {
		t.Fatalf("case metadata mismatch: %+v", res)
	}
}

// TestQualityHarnessStereoShortTail: a stereo program whose length is not a
// whole number of frames exercises the short final frame and the two-channel
// alignment path.
func TestQualityHarnessStereoShortTail(t *testing.T) {
	lame := requireLame(t)
	prog, _ := quality.ProgramByName("stereo-decorrelated")
	// 1 s at 44.1 kHz is 38.28 frames: the last go-mp3 frame is short.
	res, err := runCase(t.Context(), tools{lame: lame}, t.TempDir(), caseSpec{Program: prog, SampleRate: 44100, Kbps: 192, Seconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Channels != 2 || res.GoMP3.Lag != mp3.TotalDelay || res.LAME.Lag != 0 {
		t.Fatalf("stereo case: channels=%d lags=%d/%d", res.Channels, res.GoMP3.Lag, res.LAME.Lag)
	}
	if res.GoMP3.Metrics.SNR < 5 || res.LAME.Metrics.SNR < 5 {
		t.Fatalf("stereo SNRs %v / %v implausibly low", res.GoMP3.Metrics.SNR, res.LAME.Metrics.SNR)
	}
}

// TestDetectToolsMissing: bogus explicit paths are reported as absent
// rather than crashing later.
func TestDetectToolsMissing(t *testing.T) {
	tl := detectTools("/nonexistent/lame", "/nonexistent/visqol", "/nonexistent/peaq")
	if tl.lame != "" || tl.visqol != "" || tl.peaq != "" {
		t.Fatalf("expected empty paths, got %+v", tl)
	}
}

func TestParseLast(t *testing.T) {
	v, err := parseLast(mosRe, "ViSQOL conformance version: 300\nAudio mode\nMOS-LQO:\t\t4.44909\n", "visqol")
	if err != nil || math.Abs(v-4.44909) > 1e-9 {
		t.Fatalf("MOS parse: %v, %v", v, err)
	}
	v, err = parseLast(odgRe, "Objective Difference Grade: -1.113\nDistortion Index: 0.766\n", "peaq")
	if err != nil || math.Abs(v-(-1.113)) > 1e-9 {
		t.Fatalf("ODG parse: %v, %v", v, err)
	}
	v, err = parseLast(odgRe, "Objective Difference Grade: -nan\nDistortion Index: nan\n", "peaq")
	if err != nil || !math.IsNaN(v) {
		t.Fatalf("ODG -nan must parse to NaN without error: %v, %v", v, err)
	}
	if _, err := parseLast(odgRe, "something went wrong", "peaq"); err == nil {
		t.Fatal("missing result line must error")
	}
}

func TestAlignTrim(t *testing.T) {
	ref := [][]float64{make([]float64, 5000), make([]float64, 5000)}
	seed := uint64(9)
	for i := range ref[0] {
		seed = seed*6364136223846793005 + 1442695040888963407
		ref[0][i] = float64(seed>>11)/float64(1<<53) - 0.5
		ref[1][i] = -ref[0][i]
	}
	deg := [][]float64{make([]float64, 6200), make([]float64, 6200)}
	copy(deg[0][1057:], ref[0])
	copy(deg[1][1057:], ref[1])
	r, d, lag := alignTrim(ref, deg)
	if lag != 1057 || len(r[0]) != 5000 || len(d[1]) != 5000 {
		t.Fatalf("lag=%d lens=%d/%d", lag, len(r[0]), len(d[1]))
	}
	if quality.SNR(r[1], d[1]) != quality.SNRCap {
		t.Fatal("aligned second channel must be identical")
	}
	lead := [][]float64{ref[0][20:], ref[1][20:]}
	r, d, lag = alignTrim(ref, lead)
	if lag != -20 || len(r[0]) != 4980 || quality.SNR(r[0], d[0]) != quality.SNRCap {
		t.Fatalf("negative lag: lag=%d len=%d", lag, len(r[0]))
	}
}
