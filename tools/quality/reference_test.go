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

// TestChooseReference pins the producer-selection policy: the lame binary is
// preferred when present, ffmpeg's libmp3lame is the fallback, and neither
// present is refNone (which run() turns into a setup error).
func TestChooseReference(t *testing.T) {
	if r := chooseReference("/bin/lame", "/bin/ffmpeg"); r.kind != refLameBinary || r.bin != "/bin/lame" {
		t.Fatalf("both present: got %+v, want lame binary preferred", r)
	}
	if r := chooseReference("", "/bin/ffmpeg"); r.kind != refFFmpegLAME || r.bin != "/bin/ffmpeg" {
		t.Fatalf("lame absent: got %+v, want ffmpeg fallback", r)
	}
	if r := chooseReference("/bin/lame", ""); r.kind != refLameBinary || r.bin != "/bin/lame" {
		t.Fatalf("ffmpeg absent: got %+v, want lame binary", r)
	}
	if r := chooseReference("", ""); r.kind != refNone {
		t.Fatalf("neither present: got %+v, want refNone", r)
	}
}

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
// standing in for the lame binary (no lame; the fallback choice itself is made
// in run() by resolveReference, so the test sets tools.ref directly): it
// exercises the refFFmpegLAME encode path end to end through runCase, decoding the
// produced stream with pcm and scoring it. It pins go-mp3's own alignment
// (mp3.TotalDelay) but deliberately does NOT pin the reference lag: alignTrim
// discovers it by cross-correlation, and ffmpeg's gapless tag may trim
// differently than the lame binary's across ffmpeg versions.
func TestQualityHarnessFFmpegLAME(t *testing.T) {
	ffmpeg := requireFFmpeg(t)
	tl := tools{ffmpeg: ffmpeg, ref: new(chooseReference("", ffmpeg))} // no lame: the fallback must carry the reference
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
	// A PATH holding only ffmpeg: detectTools resolves ffmpeg but not lame, so
	// chooseReference must fall back to ffmpeg's libmp3lame.
	binDir := t.TempDir()
	if err := os.Symlink(ffmpeg, filepath.Join(binDir, "ffmpeg")); err != nil {
		t.Fatalf("symlink ffmpeg: %v", err)
	}
	t.Setenv("PATH", binDir)
	outDir := t.TempDir()
	var errbuf bytes.Buffer
	// gridArgs uses the -flag=value single-token form so the flag names do not
	// add bare "-flag" string literals that goconst counts across the package.
	code := run(t.Context(), gridArgs("2", outDir), &errbuf)
	if code != exitOK {
		t.Fatalf("run() exit %d, want exitOK %d; stderr:\n%s", code, exitOK, errbuf.String())
	}
}

// TestRunNoReferenceEncoder pins the refNone setup path: with neither lame nor
// ffmpeg resolvable on PATH, run() reports a setup error and does not proceed.
// It needs no external binary (it fails before any encode), so it runs in every
// job, including the binary-less test job.
func TestRunNoReferenceEncoder(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // an empty dir: neither lame nor ffmpeg on PATH
	outDir := t.TempDir()
	var errbuf bytes.Buffer
	code := run(t.Context(), gridArgs("1", outDir), &errbuf)
	if code != exitSetup {
		t.Fatalf("run() exit %d, want exitSetup %d; stderr:\n%s", code, exitSetup, errbuf.String())
	}
	if !strings.Contains(errbuf.String(), "no reference encoder") {
		t.Fatalf("expected a 'no reference encoder' setup error, got: %q", errbuf.String())
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

// Verbatim pre-change auto setup messages (main.go before -reference existed);
// resolveReference must keep them byte for byte.
const (
	autoNoRefMsg  = "no reference encoder found: install lame (preferred), or ffmpeg for its libmp3lame, or pass -lame"
	autoNoLameMsg = "ffmpeg is present but its build lacks the libmp3lame encoder: install lame, or an ffmpeg built with --enable-libmp3lame"
)

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
		wantErr      string // exact message for auto, substring otherwise
		wantProbes   int
	}{
		{"auto both", refModeAuto, "/l", "/f", true, refLameBinary, "/l", "", 0},
		{"auto lame only", refModeAuto, "/l", "", false, refLameBinary, "/l", "", 0},
		{"auto ffmpeg only", refModeAuto, "", "/f", true, refFFmpegLAME, "/f", "", 1},
		{"auto ffmpeg no libmp3lame", refModeAuto, "", "/f", false, refNone, "", autoNoLameMsg, 1},
		{"auto neither", refModeAuto, "", "", true, refNone, "", autoNoRefMsg, 0},
		{"lame both", refModeLame, "/l", "/f", true, refLameBinary, "/l", "", 0},
		{"lame only", refModeLame, "/l", "", false, refLameBinary, "/l", "", 0},
		{"lame missing", refModeLame, "", "/f", true, refNone, "", "-reference lame: lame not found", 0},
		{"ffmpeg overrides lame", refModeFFmpeg, "/l", "/f", true, refFFmpegLAME, "/f", "", 1},
		{"ffmpeg only", refModeFFmpeg, "", "/f", true, refFFmpegLAME, "/f", "", 1},
		{"ffmpeg missing", refModeFFmpeg, "/l", "", true, refNone, "", "-reference ffmpeg: ffmpeg not found", 0},
		{"ffmpeg no libmp3lame", refModeFFmpeg, "/l", "/f", false, refNone, "", "lacks the libmp3lame encoder", 1},
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
				if err == nil {
					t.Fatalf("got %+v, want error %q", r, c.wantErr)
				}
				if c.mode == refModeAuto && err.Error() != c.wantErr {
					t.Fatalf("auto error %q, want verbatim %q", err, c.wantErr)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("error %q lacks %q", err, c.wantErr)
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

// TestRunReferenceUnavailable: an explicit -reference whose producer is absent
// is a setup error, never a silent fallback. Needs no binaries.
func TestRunReferenceUnavailable(t *testing.T) {
	for _, mode := range []string{"lame", "ffmpeg"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())
			outDir := t.TempDir()
			var errbuf bytes.Buffer
			code := run(t.Context(), gridArgs("1", outDir, "-reference="+mode), &errbuf)
			if code != exitSetup {
				t.Fatalf("run() exit %d, want exitSetup %d; stderr:\n%s", code, exitSetup, errbuf.String())
			}
			if want := "-reference " + mode; !strings.Contains(errbuf.String(), want) {
				t.Fatalf("stderr %q lacks %q", errbuf.String(), want)
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

// TestRunReferenceFFmpegWithoutLibmp3lame: run() wires the libmp3lame probe for
// the explicit ffmpeg mode, using an ffmpeg stand-in that lists no encoders.
func TestRunReferenceFFmpegWithoutLibmp3lame(t *testing.T) {
	binDir := t.TempDir()
	writeFakeBin(t, binDir, "ffmpeg", "exit 0")
	t.Setenv("PATH", binDir)
	outDir := t.TempDir()
	var errbuf bytes.Buffer
	code := run(t.Context(), gridArgs("1", outDir, "-reference=ffmpeg"), &errbuf)
	if code != exitSetup {
		t.Fatalf("run() exit %d, want exitSetup %d; stderr:\n%s", code, exitSetup, errbuf.String())
	}
	for _, want := range []string{"lacks the libmp3lame encoder", "-reference ffmpeg"} {
		if !strings.Contains(errbuf.String(), want) {
			t.Fatalf("stderr %q lacks %q", errbuf.String(), want)
		}
	}
}

// TestRunCaseUsesResolvedReference: runCase encodes the reference with tl.ref
// only. With both fake binaries present and ref pointing at ffmpeg, a per-case
// re-derivation (which prefers lame) would surface FAKE-LAME instead.
func TestRunCaseUsesResolvedReference(t *testing.T) {
	binDir := t.TempDir()
	fakeLame := writeFakeBin(t, binDir, "lame", "echo FAKE-LAME >&2; exit 1")
	fakeFFmpeg := writeFakeBin(t, binDir, "ffmpeg", "echo FAKE-FFMPEG >&2; exit 1")
	prog, ok := quality.ProgramByName("tone-click")
	if !ok {
		t.Fatal("tone-click program missing")
	}
	spec := caseSpec{Program: prog, SampleRate: 44100, Kbps: 128, Seconds: 1}
	tl := tools{lame: fakeLame, ffmpeg: fakeFFmpeg, ref: &refEncoder{kind: refFFmpegLAME, bin: fakeFFmpeg}}
	_, err := runCase(t.Context(), tl, t.TempDir(), spec, caseRef(spec), false, io.Discard)
	if err == nil {
		t.Fatal("expected the fake reference encoder to fail")
	}
	if !strings.Contains(err.Error(), "FAKE-FFMPEG") || strings.Contains(err.Error(), "FAKE-LAME") {
		t.Fatalf("runCase did not use tl.ref (ffmpeg): %v", err)
	}
	tl.ref = nil
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
	requireFFmpeg(t)
	runMode := func(mode string) string {
		outDir := t.TempDir()
		jsonPath := filepath.Join(outDir, jsonName)
		var errbuf bytes.Buffer
		code := run(t.Context(), gridArgs("2", outDir, "-reference="+mode), &errbuf)
		if code != exitOK {
			t.Fatalf("-reference=%s: run() exit %d, want exitOK; stderr:\n%s", mode, code, errbuf.String())
		}
		b, err := os.ReadFile(jsonPath)
		if err != nil {
			t.Fatal(err)
		}
		var rep struct {
			LAMEVersion string `json:"lame_version"`
		}
		if err := json.Unmarshal(b, &rep); err != nil {
			t.Fatal(err)
		}
		return rep.LAMEVersion
	}
	const ffmpegPrefix = "ffmpeg libmp3lame: "
	if v := runMode("ffmpeg"); !strings.HasPrefix(v, ffmpegPrefix) {
		t.Fatalf("-reference=ffmpeg provenance %q lacks prefix %q", v, ffmpegPrefix)
	}
	v := runMode("lame")
	if strings.HasPrefix(v, ffmpegPrefix) {
		t.Fatalf("-reference=lame provenance %q names ffmpeg", v)
	}
	if want := lameVersion(t.Context(), lame); v != want {
		t.Fatalf("-reference=lame provenance %q, want %q", v, want)
	}
}
