package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tphakala/go-mp3/internal/quality"
)

// mkResult builds one encoder's result. mos is NaN for a case the external
// tool did not measure, which is what makes the partial-NaN paths reachable.
func mkResult(snr, lsd, mos float64) encoderResult {
	return encoderResult{
		Lag: 0, Bytes: 1000, MOS: mos, ODG: math.NaN(),
		Metrics: quality.Metrics{SNR: snr, BandSNR: snr, SegSNR: snr, LSD: lsd, PreEcho: math.NaN(), Bandwidth: 16000},
	}
}

// sampleCases is deliberately ASYMMETRIC: at 128 kbps go-mp3 wins SNR on two
// of three programs and LSD on one, so a flipped win direction changes the
// counts. Exactly one program has a finite MOS, so a summary that divided by
// the program count instead of the compared count would be wrong. A second
// sample rate is present so the per-rate grouping is exercised.
func sampleCases() []caseResult {
	return []caseResult{
		{Program: "a", Channels: 1, SampleRate: 44100, Kbps: 128,
			GoMP3: mkResult(30, 2, 4.5), LAME: mkResult(28, 3, 4.1)},
		{Program: "b", Channels: 1, SampleRate: 44100, Kbps: 128,
			GoMP3: mkResult(20, 4, math.NaN()), LAME: mkResult(26, 2, math.NaN())},
		{Program: "c", Channels: 1, SampleRate: 44100, Kbps: 128,
			GoMP3: mkResult(35, 5, math.NaN()), LAME: mkResult(31, 1, math.NaN())},
		{Program: "a", Channels: 1, SampleRate: 44100, Kbps: 320,
			GoMP3: mkResult(50, 1, math.NaN()), LAME: mkResult(50, 1, math.NaN())},
		{Program: "a", Channels: 2, SampleRate: 48000, Kbps: 128,
			GoMP3: mkResult(40, 2, math.NaN()), LAME: mkResult(38, 2, math.NaN())},
	}
}

func TestSummarize(t *testing.T) {
	rows := summarize(sampleCases())
	if len(rows) != 3 {
		t.Fatalf("%d rows, want 3 (two bitrates at 44100 plus one at 48000)", len(rows))
	}
	if rows[0].SampleRate != 44100 || rows[0].Kbps != 128 ||
		rows[1].SampleRate != 44100 || rows[1].Kbps != 320 ||
		rows[2].SampleRate != 48000 || rows[2].Kbps != 128 {
		t.Fatalf("rows not grouped and sorted by (rate, kbps): %+v", rows)
	}

	r := rows[0]
	if r.Programs != 3 {
		t.Fatalf("programs = %d, want 3", r.Programs)
	}
	// (30-28 + 20-26 + 35-31)/3 = 0
	if got := r.MeanDelta[mSNR]; math.Abs(got) > 1e-9 {
		t.Fatalf("mean SNR delta = %v, want 0", got)
	}
	// Distinct counts, so an inverted direction cannot reproduce them.
	if r.Wins[mSNR] != 2 || r.Wins[mLSD] != 1 {
		t.Fatalf("wins SNR=%d LSD=%d, want 2 and 1", r.Wins[mSNR], r.Wins[mLSD])
	}
	// One program carried a finite MOS, so the mean is that single delta and
	// the denominator is 1, not 3.
	if got := r.MeanDelta[mMOS]; math.Abs(got-0.4) > 1e-9 {
		t.Fatalf("mean MOS delta = %v, want 0.4 (the one compared program)", got)
	}
	if r.Compared[mMOS] != 1 || r.Compared[mSNR] != 3 {
		t.Fatalf("compared MOS=%d SNR=%d, want 1 and 3", r.Compared[mMOS], r.Compared[mSNR])
	}
	if !math.IsNaN(r.MeanDelta[mPreEcho]) || !math.IsNaN(r.MeanDelta[mODG]) {
		t.Fatalf("all-NaN metrics must summarize to NaN: %+v", r.MeanDelta)
	}
	// Bandwidth is informational, so it is never counted as a win.
	if r.Wins[mBandwidth] != 0 {
		t.Fatalf("Bandwidth wins = %d, want 0 (it is not scored)", r.Wins[mBandwidth])
	}
	if rows[1].Wins[mSNR] != 0 || rows[1].MeanDelta[mSNR] != 0 {
		t.Fatalf("a tie is neither a win nor a delta: %+v", rows[1])
	}
}

func TestWriteMarkdownAndJSON(t *testing.T) {
	rep := &report{SchemaVersion: reportSchemaVersion, GeneratedUTC: "2026-09-02T00:00:00Z", GoMP3Rev: "abc1234",
		LAMEVersion: "LAME 64bits version 3.100", Seconds: 6, Attempted: 6, Failed: 1, Cases: sampleCases()}
	var md bytes.Buffer
	if err := writeMarkdown(&md, rep); err != nil {
		t.Fatal(err)
	}
	s := md.String()

	// A format-verb error corrupts the header without failing anything else,
	// which is exactly how the Summary table shipped unrenderable once.
	if strings.Contains(s, "%!") {
		t.Fatalf("format-verb error in the rendered markdown:\n%s", s)
	}
	if strings.Contains(s, "NaN") {
		t.Fatalf("markdown must render a non-finite value as n/a:\n%s", s)
	}
	// One fully rendered numeric row: this is what pins the delta's SIGN, the
	// go/LAME column order, and the kHz conversion of Bandwidth. A substring
	// check on the header alone catches none of the three.
	const wantRow = "| a | 128 | 1 | 0 | 0 | 30.00 | 28.00 | 2.00 |"
	if !strings.Contains(s, wantRow) {
		t.Fatalf("row a not rendered as %q:\n%s", wantRow, s)
	}
	if !strings.Contains(s, "16.00 | 16.00 | 0.00") {
		t.Fatalf("Bandwidth must render in kHz:\n%s", s)
	}
	for _, want := range []string{"LAME 64bits version 3.100", "abc1234", "## 44100 Hz", "## 48000 Hz",
		"## Summary", notMeasured, "External metrics: none", "6 attempted, 1 failed", "2/3"} {
		if !strings.Contains(s, want) {
			t.Fatalf("markdown missing %q:\n%s", want, s)
		}
	}
	// A metric nothing compared reports n/a wins, not a clean sweep of losses.
	if strings.Contains(s, "0/3") {
		t.Fatalf("an unmeasured metric must not report wins over the program count:\n%s", s)
	}
	// Per-rate grouping: the 48 kHz case belongs under its own heading.
	head44 := s[strings.Index(s, "## 44100 Hz"):strings.Index(s, "## 48000 Hz")]
	if strings.Contains(head44, "| a | 128 | 2 |") {
		t.Fatalf("the 48 kHz case leaked into the 44100 Hz table:\n%s", head44)
	}

	var js bytes.Buffer
	if err := writeJSON(&js, rep); err != nil {
		t.Fatal(err)
	}
	var back struct {
		SchemaVersion int    `json:"schema_version"`
		LAMEVersion   string `json:"lame_version"`
		Attempted     int    `json:"attempted"`
		Failed        int    `json:"failed"`
		Cases         []struct {
			Program string `json:"program"`
			GoMP3   struct {
				SNR     *float64 `json:"snr"`
				PreEcho *float64 `json:"pre_echo"`
				MOS     *float64 `json:"mos"`
			} `json:"gomp3"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(js.Bytes(), &back); err != nil {
		t.Fatalf("json: %v\n%s", err, js.String())
	}
	if back.LAMEVersion != rep.LAMEVersion || len(back.Cases) != 5 || back.Cases[0].Program != "a" {
		t.Fatalf("json round trip: %+v", back)
	}
	if back.Attempted != 6 || back.Failed != 1 {
		t.Fatalf("json case counts = %d/%d, want 6/1", back.Attempted, back.Failed)
	}
	if back.SchemaVersion != reportSchemaVersion {
		t.Fatalf("json schema_version = %d, want %d", back.SchemaVersion, reportSchemaVersion)
	}
	c := back.Cases[0].GoMP3
	if c.SNR == nil || *c.SNR != 30 || c.PreEcho != nil || c.MOS == nil || *c.MOS != 4.5 {
		t.Fatalf("json nullable floats: snr=%v pre_echo=%v mos=%v", c.SNR, c.PreEcho, c.MOS)
	}
}

// TestCellEscapes: a program name comes from a corpus file name, so a pipe or
// a newline in it must not split a cell or forge a row.
func TestCellEscapes(t *testing.T) {
	if got := cell("a|b"); got != `a\|b` {
		t.Fatalf("cell(%q) = %q", "a|b", got)
	}
	if got := cell("row\ninject"); got != "row inject" {
		t.Fatalf("cell with a newline = %q", got)
	}
}

func TestFmtMetric(t *testing.T) {
	if fmtMetric(math.NaN()) != notMeasured || fmtMetric(math.Inf(1)) != notMeasured ||
		fmtMetric(1.234) != "1.23" || fmtMetric(-0.5) != "-0.50" {
		t.Fatalf("fmtMetric: %q %q %q", fmtMetric(math.NaN()), fmtMetric(math.Inf(1)), fmtMetric(1.234))
	}
}

// TestMetricsTableComplete: every metric must be readable and have a
// direction. The single table makes a half-added metric impossible, and this
// pins that it stays a single table.
func TestMetricsTableComplete(t *testing.T) {
	seen := map[string]bool{}
	r := mkResult(1, 2, 3)
	for _, m := range metrics {
		if m.name == "" || m.get == nil {
			t.Fatalf("metric %+v is incomplete", m)
		}
		if seen[m.name] {
			t.Fatalf("duplicate metric %q", m.name)
		}
		seen[m.name] = true
		if m.better != 1 && m.better != -1 {
			t.Fatalf("metric %q has direction %d, want +1 or -1", m.name, m.better)
		}
		if math.IsNaN(m.get(&r)) && m.name != mPreEcho && m.name != mODG {
			t.Fatalf("metric %q reads NaN from a fully populated result", m.name)
		}
	}
	if len(metrics) != 8 {
		t.Fatalf("%d metrics, want 8", len(metrics))
	}
}

func TestParseInts(t *testing.T) {
	got, err := parseInts(" 128, 192 ,320")
	if err != nil || len(got) != 3 || got[0] != 128 || got[2] != 320 {
		t.Fatalf("parseInts: %v, %v", got, err)
	}
	if _, err := parseInts(","); err == nil {
		t.Fatal("empty list must error")
	}
	if _, err := parseInts("128,abc"); err == nil {
		t.Fatal("non-integer must error")
	}
}

// testRates is the full set of MPEG-1 rates, so a corpus fixture at any of
// them is accepted and the rate check is not what a test is measuring unless
// it says so.
var testRates = []int{32000, 44100, 48000}

func TestSelectPrograms(t *testing.T) {
	all, err := selectPrograms("", "", testRates)
	if err != nil || len(all) != len(quality.Programs()) {
		t.Fatalf("all programs: %d, %v", len(all), err)
	}
	two, err := selectPrograms("sweep, multitone", "", testRates)
	if err != nil || len(two) != 2 || two[0].Name != "sweep" {
		t.Fatalf("filtered programs: %+v, %v", two, err)
	}
	if _, err := selectPrograms("nope", "", testRates); err == nil {
		t.Fatal("unknown program must error")
	}
	if _, err := selectPrograms("", "/nonexistent-corpus-dir", testRates); err == nil {
		t.Fatal("an unreadable corpus directory must error")
	}
}

// TestWavProgramCorpus writes a WAV into a corpus dir and checks it becomes a
// program pinned to its own rate, that non-WAV and non-regular entries are
// skipped, and that an unparsable WAV is an error rather than a silent skip.
func TestWavProgramCorpus(t *testing.T) {
	dir := t.TempDir()
	ch := [][]float64{{0.1, -0.1, 0.2, -0.2}, {0, 0.5, 0, -0.5}}
	if err := writeWAVFile(filepath.Join(dir, "clip.wav"), 48000, ch); err != nil {
		t.Fatal(err)
	}
	// Both of these must be skipped, so the count assertion below is
	// contingent on the skip actually happening.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub.wav"), 0o755); err != nil {
		t.Fatal(err)
	}

	progs, err := selectPrograms("multitone", dir, testRates)
	if err != nil || len(progs) != 2 || progs[1].Name != "clip" || progs[1].Channels != 2 {
		t.Fatalf("corpus programs: %+v, %v", progs, err)
	}
	if progs[1].SampleRate != 48000 {
		t.Fatalf("clip.SampleRate = %d, want 48000", progs[1].SampleRate)
	}
	if !progs[1].RunsAt(48000) || progs[1].RunsAt(44100) {
		t.Fatal("a corpus program must run only at its own rate")
	}
	if !progs[0].RunsAt(44100) || !progs[0].RunsAt(48000) {
		t.Fatal("a synthetic program must run at any rate")
	}
	got := progs[1].Gen(48000, 999)
	if len(got) != 2 || len(got[0]) != 4 || got[1][1] != 0.5 {
		t.Fatalf("clip at 48 kHz: %v", got)
	}

	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "bad.wav"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := selectPrograms("", bad, testRates); err == nil {
		t.Fatal("an unparsable corpus WAV must error")
	}
}

// TestWavProgramRateRejected: a corpus file whose rate is not in the
// effective -rates set is an error at load, not a program that is loaded and
// then skipped at every case. Rate 0, which a malformed fmt chunk can
// declare and which Program.SampleRate reads as "any rate", goes the same way.
func TestWavProgramRateRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.wav")
	if err := writeWAVFile(path, 48000, [][]float64{{0.1, -0.1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := wavProgram(path, []int{44100}); err == nil {
		t.Fatal("a 48 kHz corpus file must be rejected when -rates is 44100")
	}
	if _, err := wavProgram(path, testRates); err != nil {
		t.Fatalf("a 48 kHz corpus file must load when -rates includes it: %v", err)
	}

	// A zero rate in the fmt chunk, patched in place: bytes 24-27 of a
	// canonical 44-byte header.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(raw[24:], 0)
	zero := filepath.Join(dir, "zero.wav")
	if err := os.WriteFile(zero, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wavProgram(zero, testRates); err == nil {
		t.Fatal("a corpus file declaring rate 0 must be rejected")
	}
}

// TestRunExitCodes drives run() itself, which nothing else does. Every case
// is a setup error, so none needs an external binary.
func TestRunExitCodes(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
	}{
		{"non-positive seconds", []string{"-seconds", "0"}},
		{"unsupported rate", []string{"-rates", "12345"}},
		{"negative rate", []string{"-rates", "-1"}},
		{"unknown program", []string{"-programs", "nope"}},
		{"missing explicit lame", []string{"-lame", "/nonexistent/lame"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := run(t.Context(), c.args, io.Discard); got != exitSetup {
				t.Fatalf("run(%v) = %d, want exitSetup %d", c.args, got, exitSetup)
			}
		})
	}
}
