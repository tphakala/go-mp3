package main

import (
	"context"
	"io"
	"slices"
	"testing"

	"github.com/tphakala/go-mp3/internal/quality"
)

func TestDedupInts(t *testing.T) {
	cases := []struct {
		in, want []int
	}{
		{[]int{128, 192, 256, 320}, []int{128, 192, 256, 320}},
		{[]int{128, 128}, []int{128}},
		{[]int{320, 128, 320, 192, 128}, []int{320, 128, 192}}, // first-seen order
		{[]int{44100}, []int{44100}},
	}
	for _, c := range cases {
		got := dedupInts(c.in)
		if !slices.Equal(got, c.want) {
			t.Errorf("dedupInts(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSelectProgramsNone(t *testing.T) {
	rates := []int{44100}
	// -programs none with no corpus has nothing to compare: an error, not an
	// empty run that writes a header and no rows.
	if _, err := selectPrograms("none", "", rates); err == nil {
		t.Fatal("selectPrograms(none, no corpus) = nil error, want a no-programs error")
	}
	// Default still yields the full synthetic corpus.
	all, err := selectPrograms("", "", rates)
	if err != nil {
		t.Fatalf("selectPrograms(all): %v", err)
	}
	if len(all) != len(quality.Programs()) {
		t.Fatalf("selectPrograms(all) returned %d programs, want %d", len(all), len(quality.Programs()))
	}
	// An unknown name is still rejected (none is the only special token).
	if _, err := selectPrograms("does-not-exist", "", rates); err == nil {
		t.Fatal("selectPrograms(unknown) = nil error, want unknown-program error")
	}
}

// progMultitone and progToneClick name synthetic programs reused across tests.
const (
	progMultitone = "multitone"
	progToneClick = "tone-click"
)

func TestSelectProgramsDedup(t *testing.T) {
	// A repeated -programs name is collapsed to one, first-seen order kept, so
	// it is not run (and summary-weighted) twice, matching -rates/-bitrates.
	progs, err := selectPrograms("pink-noise,"+progMultitone+",pink-noise", "", []int{44100})
	if err != nil {
		t.Fatal(err)
	}
	if len(progs) != 2 || progs[0].Name != "pink-noise" || progs[1].Name != progMultitone {
		t.Fatalf("dedup produced %d programs %v, want [pink-noise multitone]", len(progs), names(progs))
	}
}

func names(progs []quality.Program) []string {
	out := make([]string, len(progs))
	for i, p := range progs {
		out[i] = p.Name
	}
	return out
}

func TestBuildJobsGridOrderAndSharedRef(t *testing.T) {
	o := &options{
		rates:    []int{44100},
		bitrates: []int{128, 320},
		programs: []quality.Program{
			{Name: "a", Channels: 1, Gen: func(_, n int) [][]float64 { return [][]float64{make([]float64, n)} }},
			{Name: "b", Channels: 1, Gen: func(_, n int) [][]float64 { return [][]float64{make([]float64, n)} }},
		},
		seconds: 1,
	}
	jobs := buildJobs(t.Context(), o, io.Discard)
	if len(jobs) != 4 {
		t.Fatalf("built %d jobs, want 4 (2 programs x 2 bitrates)", len(jobs))
	}
	// Dense 1-based indices in program-major, then bitrate, order.
	want := []struct {
		idx  int
		name string
		kbps int
	}{
		{1, "a", 128}, {2, "a", 320}, {3, "b", 128}, {4, "b", 320},
	}
	for i, w := range want {
		j := jobs[i]
		if j.idx != w.idx || j.spec.Program.Name != w.name || j.spec.Kbps != w.kbps {
			t.Errorf("job %d = {idx %d %s %d}, want {idx %d %s %d}", i, j.idx, j.spec.Program.Name, j.spec.Kbps, w.idx, w.name, w.kbps)
		}
	}
	// The two bitrate cases of program "a" share one reference (generated once
	// per (rate, program)), which is what lets parallel workers read it safely.
	if &jobs[0].ref[0][0] != &jobs[1].ref[0][0] {
		t.Error("program a's two bitrate jobs do not share the same reference backing array")
	}
	if &jobs[0].ref[0][0] == &jobs[2].ref[0][0] {
		t.Error("programs a and b unexpectedly share a reference")
	}
}

func TestBuildJobsCancelled(t *testing.T) {
	// An already-cancelled context stops buildJobs before it generates any
	// reference, so a Ctrl-C during a long -seconds generation phase is prompt.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	o := &options{
		rates:    []int{44100},
		bitrates: []int{128},
		seconds:  1,
		programs: []quality.Program{{Name: "a", Channels: 1, Gen: func(_, n int) [][]float64 { return [][]float64{make([]float64, n)} }}},
	}
	if jobs := buildJobs(ctx, o, io.Discard); len(jobs) != 0 {
		t.Fatalf("cancelled buildJobs built %d jobs, want 0", len(jobs))
	}
}

func TestBuildJobsSkipsRateMismatchAndEmpty(t *testing.T) {
	o := &options{
		rates:    []int{44100},
		bitrates: []int{128},
		programs: []quality.Program{
			// Pinned to a different rate: skipped, no case.
			{Name: "pinned", Channels: 1, SampleRate: 48000, Gen: func(_, n int) [][]float64 { return [][]float64{make([]float64, n)} }},
			// Empty at this rate: skipped, no case.
			{Name: "empty", Channels: 1, Gen: func(_, _ int) [][]float64 { return [][]float64{{}} }},
			// Real one.
			{Name: "ok", Channels: 1, Gen: func(_, n int) [][]float64 { return [][]float64{make([]float64, n)} }},
		},
		seconds: 1,
	}
	jobs := buildJobs(t.Context(), o, io.Discard)
	if len(jobs) != 1 || jobs[0].spec.Program.Name != "ok" || jobs[0].idx != 1 {
		t.Fatalf("buildJobs skipped incorrectly: got %d jobs %+v", len(jobs), jobs)
	}
}

// TestParseFlagsRejectsIllegalBitrate: an out-of-set bitrate is rejected at
// setup by the encoder's own legality source, rather than passing setup and
// failing every case at encode time. A legal set parses.
func TestParseFlagsRejectsIllegalBitrate(t *testing.T) {
	if _, err := parseFlags([]string{"-bitrates", "100"}); err == nil {
		t.Fatal("an out-of-set bitrate (100) must be rejected at setup")
	}
	o, err := parseFlags([]string{"-bitrates", "128,320"})
	if err != nil {
		t.Fatalf("legal bitrates rejected: %v", err)
	}
	if len(o.bitrates) != 2 {
		t.Fatalf("got %d bitrates, want 2", len(o.bitrates))
	}
}
