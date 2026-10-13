package main

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	mp3 "github.com/tphakala/go-mp3"
	"github.com/tphakala/go-mp3/internal/quality"
)

// requireFFmpeg (defined in crosscheck_test.go) skips, or under
// MP3_REQUIRE_FFMPEG=1 fails, when ffmpeg is absent.

// TestFFmpegVersion: an empty or unresolvable ffmpeg path yields unknownVersion
// rather than an error or a crash, so the provenance line degrades gracefully.
func TestFFmpegVersion(t *testing.T) {
	if v := ffmpegVersion(t.Context(), ""); v != unknownVersion {
		t.Fatalf("empty ffmpeg path: %q, want %q", v, unknownVersion)
	}
	if v := ffmpegVersion(t.Context(), "/nonexistent-ffmpeg-binary-xyz"); v != unknownVersion {
		t.Fatalf("bogus ffmpeg path: %q, want %q", v, unknownVersion)
	}
}

// TestReferenceVersion: each producer kind reports through the matching version
// helper, and refNone (plus an unresolvable binary) degrades to unknownVersion
// while still naming the producer for the ffmpeg branch.
func TestReferenceVersion(t *testing.T) {
	if v := referenceVersion(t.Context(), refEncoder{kind: refNone}); v != unknownVersion {
		t.Fatalf("refNone: %q, want %q", v, unknownVersion)
	}
	if v := referenceVersion(t.Context(), refEncoder{kind: refLameBinary, bin: ""}); v != unknownVersion {
		t.Fatalf("lame with empty bin: %q, want %q", v, unknownVersion)
	}
	v := referenceVersion(t.Context(), refEncoder{kind: refFFmpegLAME, bin: "/nonexistent-ffmpeg-binary-xyz"})
	if v != "ffmpeg libmp3lame: "+unknownVersion {
		t.Fatalf("ffmpeg branch: %q, want prefixed unknown", v)
	}
}

// TestQualityHarnessFFmpegLAME runs one real case with ffmpeg's libmp3lame
// standing in for the lame binary: it exercises the refFFmpegLAME encode path end to end through runCase, decoding the
// produced stream with pcm and scoring it. It pins go-mp3's own alignment
// (mp3.TotalDelay) but deliberately does NOT pin the reference lag: alignTrim
// discovers it by cross-correlation, and ffmpeg's gapless tag may trim
// differently than the lame binary's across ffmpeg versions.
func TestQualityHarnessFFmpegLAME(t *testing.T) {
	ffmpeg := requireFFmpeg(t)
	tl := tools{ffmpeg: ffmpeg, ref: refEncoder{kind: refFFmpegLAME, bin: ffmpeg}}
	prog, ok := quality.ProgramByName("tone-click")
	if !ok {
		t.Fatal("tone-click program missing")
	}
	spec := caseSpec{Program: prog, SampleRate: 44100, Kbps: 128, Seconds: 2}
	res, err := runCase(t.Context(), tl, t.TempDir(), spec, caseRef(spec), false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if res.GoMP3.Lag != mp3.TotalDelay {
		t.Fatalf("go-mp3 lag = %d, want mp3.TotalDelay %d", res.GoMP3.Lag, mp3.TotalDelay)
	}
	// The reference column is ffmpeg's libmp3lame here; sanity-bound it the way
	// the lame smoke does, without pinning its exact lag.
	ref := res.LAME
	if ref.Bytes < 1000 {
		t.Fatalf("reference stream only %d bytes", ref.Bytes)
	}
	if math.IsNaN(ref.Metrics.SNR) || ref.Metrics.SNR < 5 || ref.Metrics.SNR > quality.SNRCap {
		t.Fatalf("reference SNR %v out of sane range", ref.Metrics.SNR)
	}
	if math.IsNaN(ref.Metrics.LSD) || ref.Metrics.LSD <= 0 {
		t.Fatalf("reference LSD %v", ref.Metrics.LSD)
	}
}

// TestQualityHarnessRunFFmpegOnly drives the whole run() entry point with only
// ffmpeg on PATH (no lame), pinning the headline behavior of this change: a
// comparison run no longer requires the lame binary. A regression that
// reinstated a hard lame requirement in run() would make this exit non-zero.
// Named "TestQualityHarness*" so the CI compat job's -run filter selects it,
// where ffmpeg is installed and MP3_REQUIRE_FFMPEG=1 makes requireFFmpeg fail
// rather than skip.
func TestQualityHarnessRunFFmpegOnly(t *testing.T) {
	ffmpeg := requireFFmpeg(t)
	// A PATH holding only ffmpeg: -reference auto must fall back to ffmpeg's
	// libmp3lame.
	binDir := t.TempDir()
	if err := os.Symlink(ffmpeg, filepath.Join(binDir, "ffmpeg")); err != nil {
		t.Fatalf("symlink ffmpeg: %v", err)
	}
	t.Setenv("PATH", binDir)
	outDir := t.TempDir()
	var errbuf bytes.Buffer
	code := run(t.Context(), gridArgs("2", outDir), &errbuf)
	if code != exitOK {
		t.Fatalf("run() exit %d, want exitOK %d; stderr:\n%s", code, exitOK, errbuf.String())
	}
}

// Test fixtures shared by the reference tests.
const (
	forceFFmpeg  = "-reference=ffmpeg"
	noLibmp3lame = "lacks the libmp3lame encoder"
)

// TestRunReferenceSetupErrors: run() reports an unavailable reference as a
// setup error before any encode, and an explicit -reference never falls back to
// the other producer. It needs no real binary, so it runs in every job.
func TestRunReferenceSetupErrors(t *testing.T) {
	cases := []struct {
		name       string
		fakeFFmpeg bool // an ffmpeg stand-in that lists no encoders
		args       []string
		want       []string
	}{
		{"auto, nothing on PATH", false, nil, []string{"no reference encoder"}},
		{"lame missing", false, []string{"-reference=" + string(refModeLame)}, []string{"-reference lame"}},
		{"ffmpeg missing", false, []string{forceFFmpeg}, []string{"-reference ffmpeg"}},
		{"ffmpeg without libmp3lame", true, []string{forceFFmpeg}, []string{"-reference ffmpeg", noLibmp3lame}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			binDir := t.TempDir()
			if c.fakeFFmpeg {
				writeFakeBin(t, binDir, "ffmpeg", "exit 0")
			}
			t.Setenv("PATH", binDir)
			var errbuf bytes.Buffer
			code := run(t.Context(), gridArgs("1", t.TempDir(), c.args...), &errbuf)
			if code != exitSetup {
				t.Fatalf("run() exit %d, want exitSetup %d; stderr:\n%s", code, exitSetup, errbuf.String())
			}
			for _, w := range c.want {
				if !strings.Contains(errbuf.String(), w) {
					t.Fatalf("stderr %q lacks %q", errbuf.String(), w)
				}
			}
		})
	}
}

// TestQualityHarnessFFmpegLibmp3lame pins the libmp3lame capability probe: an
// empty or unresolvable ffmpeg reports the encoder absent (so run() degrades to
// a clear setup error rather than failing every case cryptically), and a real
// ffmpeg build reports it present. Named "TestQualityHarness*" so the CI compat
// job (which installs ffmpeg) exercises the positive case; the empty and bogus
// cases run first and need no binary.
func TestQualityHarnessFFmpegLibmp3lame(t *testing.T) {
	if ffmpegHasLibmp3lame(t.Context(), "") {
		t.Fatal("empty ffmpeg path must report libmp3lame absent")
	}
	if ffmpegHasLibmp3lame(t.Context(), "/nonexistent-ffmpeg-binary-xyz") {
		t.Fatal("bogus ffmpeg path must report libmp3lame absent")
	}
	ffmpeg := requireFFmpeg(t) // the positive case needs a real ffmpeg build
	if !ffmpegHasLibmp3lame(t.Context(), ffmpeg) {
		t.Fatal("system ffmpeg should report libmp3lame present")
	}
}

// TestResolveReference covers every mode against every availability
// combination, including how often the libmp3lame probe runs.
func TestResolveReference(t *testing.T) {
	cases := []struct {
		name         string
		mode         refMode
		lame, ffmpeg string
		probeOK      bool
		wantKind     refKind
		wantBin      string
		wantErr      string // substring
		wantProbes   int
	}{
		{"auto both", refModeAuto, "/l", "/f", true, refLameBinary, "/l", "", 0},
		{"auto lame only", refModeAuto, "/l", "", false, refLameBinary, "/l", "", 0},
		{"auto ffmpeg only", refModeAuto, "", "/f", true, refFFmpegLAME, "/f", "", 1},
		{"auto ffmpeg no libmp3lame", refModeAuto, "", "/f", false, refNone, "", noLibmp3lame, 1},
		{"auto neither", refModeAuto, "", "", true, refNone, "", "no reference encoder found", 0},
		{"lame both", refModeLame, "/l", "/f", true, refLameBinary, "/l", "", 0},
		{"lame only", refModeLame, "/l", "", false, refLameBinary, "/l", "", 0},
		{"lame missing", refModeLame, "", "/f", true, refNone, "", "-reference lame: lame not found", 0},
		{"ffmpeg overrides lame", refModeFFmpeg, "/l", "/f", true, refFFmpegLAME, "/f", "", 1},
		{"ffmpeg only", refModeFFmpeg, "", "/f", true, refFFmpegLAME, "/f", "", 1},
		{"ffmpeg missing", refModeFFmpeg, "/l", "", true, refNone, "", "-reference ffmpeg: ffmpeg not found", 0},
		{"ffmpeg no libmp3lame", refModeFFmpeg, "/l", "/f", false, refNone, "", noLibmp3lame, 1},
		{"unknown mode", refMode("bogus"), "/l", "/f", true, refNone, "", "bogus", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			probes := 0
			r, err := resolveReference(c.mode, c.lame, c.ffmpeg, func(string) bool { probes++; return c.probeOK })
			if probes != c.wantProbes {
				t.Fatalf("probe called %d times, want %d", probes, c.wantProbes)
			}
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("got %+v, %v; want error containing %q", r, err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if r.kind != c.wantKind || r.bin != c.wantBin {
				t.Fatalf("got %+v, want kind %v bin %q", r, c.wantKind, c.wantBin)
			}
		})
	}
}

const jsonName = "report.json"

// gridArgs is a one-case grid writing both reports under outDir, preceded by
// extra flags (for example -reference=lame).
func gridArgs(seconds, outDir string, extra ...string) []string {
	return append(extra, "-rates=44100", "-bitrates=128", "-programs=tone-click", "-seconds="+seconds,
		"-out", filepath.Join(outDir, "report.md"), "-json", filepath.Join(outDir, jsonName))
}

// writeFakeBin writes an executable /bin/sh script named name into dir.
func writeFakeBin(t *testing.T, dir, name, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake shell-script binaries need a POSIX shell")
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestRunUsesExplicitLame: an explicit -lame path that is not on PATH is the
// binary run() encodes the reference with, under -reference auto and lame. The
// fake fails with a marker, so the case error in stderr shows which binary ran;
// with the path dropped, an empty PATH would make it a setup error instead.
func TestRunUsesExplicitLame(t *testing.T) {
	fakeLame := writeFakeBin(t, t.TempDir(), "lame", "echo FAKE-EXPLICIT-LAME >&2; exit 1")
	t.Setenv("PATH", t.TempDir())
	for _, mode := range []refMode{refModeAuto, refModeLame} {
		t.Run(string(mode), func(t *testing.T) {
			var errbuf bytes.Buffer
			code := run(t.Context(), gridArgs("1", t.TempDir(), "-reference="+string(mode), "-lame="+fakeLame), &errbuf)
			if code != exitCases {
				t.Fatalf("run() exit %d, want exitCases %d; stderr:\n%s", code, exitCases, errbuf.String())
			}
			if !strings.Contains(errbuf.String(), "FAKE-EXPLICIT-LAME") {
				t.Fatalf("reference was not encoded with the -lame binary; stderr:\n%s", errbuf.String())
			}
		})
	}
}

// TestRunCaseUsesResolvedReference: runCase encodes the reference with tl.ref
// only. With both fake binaries on PATH and ref pointing at ffmpeg, a per-case
// re-derivation (which prefers lame) would surface FAKE-LAME instead.
func TestRunCaseUsesResolvedReference(t *testing.T) {
	binDir := t.TempDir()
	writeFakeBin(t, binDir, "lame", "echo FAKE-LAME >&2; exit 1")
	t.Setenv("PATH", binDir) // a per-case lookup on PATH would find the fake lame
	fakeFFmpeg := writeFakeBin(t, binDir, "ffmpeg", "echo FAKE-FFMPEG >&2; exit 1")
	prog, ok := quality.ProgramByName("tone-click")
	if !ok {
		t.Fatal("tone-click program missing")
	}
	spec := caseSpec{Program: prog, SampleRate: 44100, Kbps: 128, Seconds: 1}
	tl := tools{ffmpeg: fakeFFmpeg, ref: refEncoder{kind: refFFmpegLAME, bin: fakeFFmpeg}}
	_, err := runCase(t.Context(), tl, t.TempDir(), spec, caseRef(spec), false, io.Discard)
	if err == nil {
		t.Fatal("expected the fake reference encoder to fail")
	}
	if !strings.Contains(err.Error(), "FAKE-FFMPEG") || strings.Contains(err.Error(), "FAKE-LAME") {
		t.Fatalf("runCase did not use tl.ref (ffmpeg): %v", err)
	}
	tl.ref = refEncoder{}
	_, err = runCase(t.Context(), tl, t.TempDir(), spec, caseRef(spec), false, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "no reference encoder configured") {
		t.Fatalf("unset ref must fail loudly, got: %v", err)
	}
}

// TestQualityHarnessRunReferenceExplicit drives run() with each explicit mode
// while both producers are on PATH; the provenance line must name the forced
// one. Named TestQualityHarness* so the compat job runs it.
func TestQualityHarnessRunReferenceExplicit(t *testing.T) {
	lame := requireLame(t)
	ffmpeg := requireFFmpeg(t)
	// Only lame and ffmpeg on PATH: optional scorers would add minutes and are
	// irrelevant to which reference is chosen.
	binDir := t.TempDir()
	for name, bin := range map[string]string{string(refModeLame): lame, string(refModeFFmpeg): ffmpeg} {
		if err := os.Symlink(bin, filepath.Join(binDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir)
	for _, c := range []struct {
		mode refMode
		want refEncoder
	}{
		{refModeFFmpeg, refEncoder{kind: refFFmpegLAME, bin: ffmpeg}},
		{refModeLame, refEncoder{kind: refLameBinary, bin: lame}},
	} {
		t.Run(string(c.mode), func(t *testing.T) {
			outDir := t.TempDir()
			var errbuf bytes.Buffer
			if code := run(t.Context(), gridArgs("1", outDir, "-reference="+string(c.mode)), &errbuf); code != exitOK {
				t.Fatalf("run() exit %d, want exitOK; stderr:\n%s", code, errbuf.String())
			}
			b, err := os.ReadFile(filepath.Join(outDir, jsonName))
			if err != nil {
				t.Fatal(err)
			}
			var rep struct {
				LAMEVersion string `json:"lame_version"`
			}
			if err := json.Unmarshal(b, &rep); err != nil {
				t.Fatal(err)
			}
			if want := referenceVersion(t.Context(), c.want); rep.LAMEVersion != want {
				t.Fatalf("provenance %q, want %q", rep.LAMEVersion, want)
			}
		})
	}
}
