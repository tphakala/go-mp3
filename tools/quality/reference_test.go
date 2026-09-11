package main

import (
	"bytes"
	"io"
	"math"
	"os"
	"path/filepath"
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
// standing in for the lame binary (tools{ffmpeg: ...}, no lame): it exercises
// the refFFmpegLAME encode path end to end through runCase, decoding the
// produced stream with pcm and scoring it. It pins go-mp3's own alignment
// (mp3.TotalDelay) but deliberately does NOT pin the reference lag: alignTrim
// discovers it by cross-correlation, and ffmpeg's gapless tag may trim
// differently than the lame binary's across ffmpeg versions.
func TestQualityHarnessFFmpegLAME(t *testing.T) {
	ffmpeg := requireFFmpeg(t)
	tl := tools{ffmpeg: ffmpeg} // no lame: the fallback must carry the reference
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
	code := run(t.Context(), []string{
		// The four selection flags use the -flag=value single-token form so their
		// names do not add bare "-flag" string literals that goconst counts across
		// the package (the equivalent " -flag", "value" pairs tipped several over
		// its threshold); flag parsing treats the two forms identically.
		"-rates=44100", "-bitrates=128", "-programs=tone-click", "-seconds=2",
		"-out", filepath.Join(outDir, "report.md"), "-json", filepath.Join(outDir, "report.json"),
	}, &errbuf)
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
	code := run(t.Context(), []string{
		// -flag=value single-token form: see TestQualityHarnessRunFFmpegOnly for
		// why (avoids adding bare "-flag" literals goconst counts package-wide).
		"-rates=44100", "-bitrates=128", "-programs=tone-click", "-seconds=1",
		"-out", filepath.Join(outDir, "report.md"), "-json", filepath.Join(outDir, "report.json"),
	}, &errbuf)
	if code != exitSetup {
		t.Fatalf("run() exit %d, want exitSetup %d; stderr:\n%s", code, exitSetup, errbuf.String())
	}
	if !strings.Contains(errbuf.String(), "no reference encoder") {
		t.Fatalf("expected a 'no reference encoder' setup error, got: %q", errbuf.String())
	}
}
