package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

// cancelAfter reports no error for its first n Err calls and
// context.Canceled afterwards, which lets a test cancel at a chosen point
// inside the timing loop.
type cancelAfter struct {
	context.Context
	n int
}

func (c *cancelAfter) Err() error {
	if c.n > 0 {
		c.n--
		return nil
	}
	return context.Canceled
}

// A Ctrl-C during a case must stop it before its remaining runs, and an
// interrupted case is not a failed case. The first Err call is timeFile's
// bitrate check and the second is run 1 of 3 in timeEncode, so the third
// (before run 2) is where the cancel lands.
func TestTimeFileStopsBetweenRuns(t *testing.T) {
	path := sineWAV(t, t.TempDir(), fileA, 48000, 12000, 440, 0.5)
	tr := &timingRun{o: &options{secs: 1, runs: 3, bitrates: []int{64}}, stderr: io.Discard}
	tr.timeFile(&cancelAfter{Context: t.Context(), n: 2}, path)
	if len(tr.results) != 0 {
		t.Errorf("a cancelled case produced %d results, want none", len(tr.results))
	}
	if tr.failed != 0 {
		t.Errorf("a cancelled case was counted as %d failures", tr.failed)
	}
}

// -record stops before the next bitrate of an input when interrupted. The
// first Err call is the per-input check and the second is the first bitrate,
// so the cancel lands before the second bitrate.
func TestRecordStopsBetweenBitrates(t *testing.T) {
	path := sineWAV(t, t.TempDir(), fileA, 48000, 12000, 440, 0.5)
	var out, errb bytes.Buffer
	code := run(&cancelAfter{Context: t.Context(), n: 2}, []string{flagRecord, flagSecs, "1", flagBitrates, "64,96", path}, &out, &errb)
	if code != exitInterrupted {
		t.Errorf("exit %d, want %d", code, exitInterrupted)
	}
	if lines := strings.Count(out.String(), "\n"); lines != 1 {
		t.Errorf("recorded %d lines before the interrupt, want 1:\n%s", lines, out.String())
	}
}
