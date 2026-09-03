package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tphakala/go-mp3/internal/quality"
)

// TestRunGridSetupError needs no external tools: a work directory that is
// actually a regular file makes every per-case MkdirAll fail (ENOTDIR) before
// any encoding, which runGrid must surface as a setup-class error, not a
// per-case failure count.
func TestRunGridSetupError(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "iamafile")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := &options{
		rates:    []int{44100},
		bitrates: []int{128},
		jobs:     2,
		seconds:  1,
		programs: []quality.Program{{Name: "z", Channels: 1, Gen: func(_, n int) [][]float64 { return [][]float64{make([]float64, n)} }}},
	}
	rep := &report{}
	failed, err := runGrid(t.Context(), tools{}, o, notADir, rep, io.Discard)
	if err == nil {
		t.Fatal("runGrid returned nil error when the work directory is a file")
	}
	if failed != 0 || len(rep.Cases) != 0 {
		t.Fatalf("a setup error must not report cases: failed=%d cases=%d", failed, len(rep.Cases))
	}
}

// TestRunGridCancelled needs no external tools: an already-cancelled context
// makes runGrid dispatch nothing and produce no cases, which is what lets run()
// skip the report write and exit non-zero instead of overwriting a prior report
// with a truncated one.
func TestRunGridCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	o := &options{
		rates:    []int{44100},
		bitrates: []int{128},
		jobs:     2,
		seconds:  1,
		programs: []quality.Program{{Name: "a", Channels: 1, Gen: func(_, n int) [][]float64 { return [][]float64{make([]float64, n)} }}},
	}
	rep := &report{}
	failed, err := runGrid(ctx, tools{}, o, t.TempDir(), rep, io.Discard)
	if err != nil {
		t.Fatalf("a cancelled grid is not a setup error: %v", err)
	}
	if failed != 0 || len(rep.Cases) != 0 {
		t.Fatalf("a cancelled grid must produce no cases: failed=%d cases=%d", failed, len(rep.Cases))
	}
}

// TestRunGridEmptyIsSetupError: a run where every program is skipped (here the
// only program is pinned to a rate the run does not request) must be a setup
// error, not a silent 0-case success that writes empty reports and exits 0.
func TestRunGridEmptyIsSetupError(t *testing.T) {
	o := &options{
		rates:    []int{44100},
		bitrates: []int{128},
		jobs:     2,
		seconds:  1,
		programs: []quality.Program{{Name: "p48", Channels: 1, SampleRate: 48000, Gen: func(_, n int) [][]float64 { return [][]float64{make([]float64, n)} }}},
	}
	rep := &report{}
	if _, err := runGrid(t.Context(), tools{}, o, t.TempDir(), rep, io.Discard); err == nil {
		t.Fatal("an all-skipped grid must be a setup error, not a silent 0-case success")
	}
	if len(rep.Cases) != 0 {
		t.Fatalf("empty grid must produce no cases, got %d", len(rep.Cases))
	}
}

// TestRunGridOrderAndReclaim exercises the concurrent path end to end: it pins
// that rep.Cases lands in deterministic grid order (program-major, then
// bitrate) regardless of which worker finishes first, that all cases succeed,
// and that the per-case dir reclaim honors the -work/-keep gate. Needs lame.
func TestRunGridOrderAndReclaim(t *testing.T) {
	lame := requireLame(t)
	progs := make([]quality.Program, 0, 2)
	for _, name := range []string{progMultitone, progToneClick} {
		p, ok := quality.ProgramByName(name)
		if !ok {
			t.Fatalf("program %q missing", name)
		}
		progs = append(progs, p)
	}
	newOpts := func(jobs int, work string) *options {
		return &options{rates: []int{44100}, bitrates: []int{128, 192}, programs: progs, seconds: 1, jobs: jobs, work: work}
	}
	runIt := func(o *options, workDir string) []caseResult {
		rep := &report{}
		failed, err := runGrid(t.Context(), tools{lame: lame}, o, workDir, rep, io.Discard)
		if err != nil {
			t.Fatalf("runGrid: %v", err)
		}
		if failed != 0 {
			t.Fatalf("runGrid reported %d failed cases", failed)
		}
		return rep.Cases
	}

	// A throwaway temp tree (o.work == "") at -jobs 4: order must be the grid
	// order even though workers finish out of order, and each case dir must be
	// reclaimed as it completes.
	autoDir := t.TempDir()
	cases := runIt(newOpts(4, ""), autoDir)
	want := []struct {
		program string
		kbps    int
	}{{progMultitone, 128}, {progMultitone, 192}, {progToneClick, 128}, {progToneClick, 192}}
	if len(cases) != len(want) {
		t.Fatalf("got %d cases, want %d", len(cases), len(want))
	}
	for i, w := range want {
		if cases[i].Program != w.program || cases[i].Kbps != w.kbps {
			t.Fatalf("case %d = %s/%d, want %s/%d (order must be deterministic under -jobs>1)", i, cases[i].Program, cases[i].Kbps, w.program, w.kbps)
		}
	}
	if entries, err := os.ReadDir(autoDir); err != nil || len(entries) != 0 {
		t.Fatalf("reclaim: temp work dir should be empty after the run, has %d entries (err %v)", len(entries), err)
	}

	// An explicit -work dir must keep its artifacts for inspection (the
	// backward-compatible behavior): reclaim is gated on o.work == "".
	keepDir := t.TempDir()
	_ = runIt(newOpts(2, keepDir), keepDir)
	kept, err := os.ReadDir(keepDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != len(want) {
		t.Fatalf("explicit -work must keep %d case dirs, found %d", len(want), len(kept))
	}
}
