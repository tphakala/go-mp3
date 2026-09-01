package main

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/tphakala/go-mp3/internal/quality"
)

func sampleCases() []caseResult {
	mk := func(snr, lsd float64) encoderResult {
		return encoderResult{
			Lag: 0, Bytes: 1000, MOS: math.NaN(), ODG: math.NaN(),
			Metrics: quality.Metrics{SNR: snr, BandSNR: snr, SegSNR: snr, LSD: lsd, PreEcho: math.NaN(), Bandwidth: 16000},
		}
	}
	return []caseResult{
		{Program: "a", Channels: 1, SampleRate: 44100, Kbps: 128, GoMP3: mk(30, 2), LAME: mk(28, 3)},
		{Program: "b", Channels: 1, SampleRate: 44100, Kbps: 128, GoMP3: mk(20, 4), LAME: mk(26, 2)},
		{Program: "a", Channels: 1, SampleRate: 44100, Kbps: 320, GoMP3: mk(50, 1), LAME: mk(50, 1)},
	}
}

func TestSummarize(t *testing.T) {
	rows := summarize(sampleCases())
	if len(rows) != 2 || rows[0].Kbps != 128 || rows[1].Kbps != 320 {
		t.Fatalf("rows = %+v", rows)
	}
	r := rows[0]
	if r.Programs != 2 {
		t.Fatalf("programs = %d, want 2", r.Programs)
	}
	if got := r.MeanDelta["SNR"]; math.Abs(got-(-2)) > 1e-9 { // (30-28 + 20-26)/2
		t.Fatalf("mean SNR delta = %v, want -2", got)
	}
	if r.Wins["SNR"] != 1 || r.Wins["LSD"] != 1 {
		t.Fatalf("wins = %+v, want 1 SNR win and 1 LSD win", r.Wins)
	}
	if !math.IsNaN(r.MeanDelta["PreEcho"]) || !math.IsNaN(r.MeanDelta["MOS"]) {
		t.Fatalf("all-NaN metrics must summarize to NaN: %+v", r.MeanDelta)
	}
	if rows[1].Wins["SNR"] != 0 || rows[1].MeanDelta["SNR"] != 0 {
		t.Fatalf("tie must be neither a win nor a delta: %+v", rows[1])
	}
}

func TestWriteMarkdownAndJSON(t *testing.T) {
	rep := &report{GeneratedUTC: "2026-09-01T00:00:00Z", GoMP3Rev: "abc1234", LAMEVersion: "LAME 64bits version 3.100", Seconds: 6, Cases: sampleCases()}
	var md bytes.Buffer
	if err := writeMarkdown(&md, rep); err != nil {
		t.Fatal(err)
	}
	s := md.String()
	for _, want := range []string{"LAME 64bits version 3.100", "abc1234", "| a | 128 | 1 |", "| b | 128 | 1 |", "## 44100 Hz", "## Summary", "n/a", "External metrics: none"} {
		if !strings.Contains(s, want) {
			t.Fatalf("markdown missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "NaN") {
		t.Fatalf("markdown must render NaN as n/a:\n%s", s)
	}
	var js bytes.Buffer
	if err := writeJSON(&js, rep); err != nil {
		t.Fatal(err)
	}
	var back struct {
		LAMEVersion string `json:"lame_version"`
		Cases       []struct {
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
	if back.LAMEVersion != rep.LAMEVersion || len(back.Cases) != 3 || back.Cases[0].Program != "a" {
		t.Fatalf("json round trip: %+v", back)
	}
	c := back.Cases[0].GoMP3
	if c.SNR == nil || *c.SNR != 30 || c.PreEcho != nil || c.MOS != nil {
		t.Fatalf("json nullable floats: snr=%v pre_echo=%v mos=%v", c.SNR, c.PreEcho, c.MOS)
	}
}

func TestFmtMetric(t *testing.T) {
	if fmtMetric(math.NaN()) != "n/a" || fmtMetric(1.234) != "1.23" || fmtMetric(-0.5) != "-0.50" {
		t.Fatalf("fmtMetric: %q %q %q", fmtMetric(math.NaN()), fmtMetric(1.234), fmtMetric(-0.5))
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

func TestSelectPrograms(t *testing.T) {
	all, err := selectPrograms("", "")
	if err != nil || len(all) != len(quality.Programs()) {
		t.Fatalf("all programs: %d, %v", len(all), err)
	}
	two, err := selectPrograms("sweep, multitone", "")
	if err != nil || len(two) != 2 || two[0].Name != "sweep" {
		t.Fatalf("filtered programs: %+v, %v", two, err)
	}
	if _, err := selectPrograms("nope", ""); err == nil {
		t.Fatal("unknown program must error")
	}
}

// TestWavProgramCorpus writes a WAV into a corpus dir and checks it becomes a
// program at its own rate and an empty one at another rate.
func TestWavProgramCorpus(t *testing.T) {
	dir := t.TempDir()
	ch := [][]float64{{0.1, -0.1, 0.2, -0.2}, {0, 0.5, 0, -0.5}}
	if err := writeWAVFile(dir+"/clip.wav", 48000, ch); err != nil {
		t.Fatal(err)
	}
	progs, err := selectPrograms("multitone", dir)
	if err != nil || len(progs) != 2 || progs[1].Name != "clip" || progs[1].Channels != 2 {
		t.Fatalf("corpus programs: %+v, %v", progs, err)
	}
	got := progs[1].Gen(48000, 999)
	if len(got) != 2 || len(got[0]) != 4 || got[1][1] != 0.5 {
		t.Fatalf("clip at 48 kHz: %v", got)
	}
	if other := progs[1].Gen(44100, 999); len(other) != 2 || len(other[0]) != 0 {
		t.Fatalf("clip at a foreign rate must be empty: %v", other)
	}
}
