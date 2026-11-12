package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tphakala/go-mp3/internal/quality"
)

// TestRecordCheckCutAndCache records one file at two cut lengths and several
// bitrates, compares every recorded hash with an independent frame-API encode
// of the matching number of frames, and checks the list back. The entries run
// (secs 1 at 64 and 128, then secs 0 at 64) so the check's one-file cache must
// reuse the file across bitrates and re-cut it when secs changes.
func TestRecordCheckCutAndCache(t *testing.T) {
	const rate, total, cutFrames = 48000, 72000, 48000
	prog, ok := quality.ProgramByName("stereo-wide")
	if !ok {
		t.Fatal("program stereo-wide missing")
	}
	dir := t.TempDir()
	path := writeTestWAV(t, dir, "wide.wav", rate, prog.Gen(rate, total))

	var list strings.Builder
	for _, rec := range []struct {
		secs, frames int
		bitrates     string
	}{{1, cutFrames, "64,128"}, {0, total, "64"}} {
		var out, errb bytes.Buffer
		args := []string{flagRecord, flagSecs, strconv.Itoa(rec.secs), flagBitrates, rec.bitrates, path}
		if code := run(t.Context(), args, &out, &errb); code != exitOK {
			t.Fatalf("record exited %d: %s", code, errb.String())
		}
		entries, err := parseHashList(strings.NewReader(out.String()))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if want := independentHash(t, path, rate, 2, rec.frames, e.Kbps); e.OutSHA != want {
				t.Errorf("recorded %d kbps at secs %d: %s, want %s", e.Kbps, rec.secs, e.OutSHA, want)
			}
		}
		list.WriteString(out.String())
	}

	listPath := filepath.Join(dir, "list.txt")
	if err := os.WriteFile(listPath, []byte(list.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run(t.Context(), []string{flagCheck, flagCorpus, dir, flagList, listPath}, &out, &errb); code != exitOK {
		t.Errorf("check exited %d:\n%s%s", code, out.String(), errb.String())
	}
	if !hasLine(out.String(), "checked 3 cases: 3 ok,") {
		t.Errorf("want 3 ok cases:\n%s", out.String())
	}
}

// TestRunSetupErrors pins exit code 2 for the rejected flag combinations in
// its table. Cases that name a valid list, corpus and input fail on the flag
// itself.
func TestRunSetupErrors(t *testing.T) {
	f := newCheckFixture(t)
	wav := filepath.Join(f.dir, fileA)
	emptyList := filepath.Join(f.dir, "empty.txt")
	if err := os.WriteFile(emptyList, []byte("# nothing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
	}{
		{"record with check", []string{flagRecord, flagCheck, flagCorpus, f.dir, flagList, f.list}},
		{"check without corpus", []string{flagCheck, flagList, f.list}},
		{"check with an input argument", []string{flagCheck, flagCorpus, f.dir, flagList, f.list, wav}},
		{"ref with record", []string{flagRecord, "-ref", wav}},
		{"ref with check", []string{flagCheck, "-ref", flagCorpus, f.dir, flagList, f.list}},
		{"json with check", []string{flagCheck, flagJSON, filepath.Join(f.dir, "x.json"), flagCorpus, f.dir, flagList, f.list}},
		{"no inputs", nil},
		{"zero runs", []string{flagRuns, "0", wav}},
		{"negative secs", []string{flagSecs, "-1", wav}},
		{"zero ref-secs", []string{"-ref-secs", "0", wav}},
		{"ref-secs past the range", []string{"-ref-secs", strconv.Itoa(math.MaxInt), wav}},
		{"zero bitrate", []string{flagBitrates, "0", wav}},
		{"bitrate that would wrap to a legal one", []string{flagBitrates, "2305843009213694080", wav}},
		{"bitrate outside the legal set", []string{flagBitrates, "100", wav}},
		{"repeated bitrate", []string{flagBitrates, "64,64", wav}},
		{"unknown flag", []string{"-nope"}},
		{"missing list", []string{flagCheck, flagCorpus, f.dir, flagList, filepath.Join(f.dir, "none.txt")}},
		{"list without entries", []string{flagCheck, flagCorpus, f.dir, flagList, emptyList}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run(t.Context(), tt.args, &out, &errb); code != exitSetup {
				t.Errorf("exit %d, want 2\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
			}
		})
	}
}

// A listed file that cannot be decoded is an error that fails the check,
// never a pass, and -record refuses names the list cannot hold while still
// recording the others.
func TestCheckCorruptInputFails(t *testing.T) {
	dir := t.TempDir()
	junk := []byte("not a wav file")
	if err := os.WriteFile(filepath.Join(dir, "bad.wav"), junk, 0o600); err != nil {
		t.Fatal(err)
	}
	list := "bad.wav " + hashHex(junk) + " 1 64 " + hashHex(nil) + "\n"
	listPath := filepath.Join(dir, "list.txt")
	if err := os.WriteFile(listPath, []byte(list), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := run(t.Context(), []string{flagCheck, flagCorpus, dir, flagList, listPath}, &out, &errb)
	if code != exitFailed || !hasLine(out.String(), "error bad.wav 1 64: ") {
		t.Errorf("corrupt input: exit %d, want 1 with an error line:\n%s", code, out.String())
	}

	other := t.TempDir()
	good := sineWAV(t, dir, fileA, 48000, 12000, 440, 0.5)
	dup := sineWAV(t, other, fileA, 48000, 12000, 880, 0.5)
	hidden := sineWAV(t, dir, "#c.wav", 48000, 12000, 220, 0.5)
	out.Reset()
	errb.Reset()
	code = run(t.Context(), []string{flagRecord, flagSecs, "1", flagBitrates, "64", good, dup, hidden}, &out, &errb)
	if code != exitFailed {
		t.Errorf("record with a repeated base name and a name starting with #: exit %d, want 1", code)
	}
	if n := strings.Count(out.String(), "\n"); n != 1 || !hasLine(out.String(), "a.wav ") {
		t.Errorf("want only the first a.wav recorded:\n%s", out.String())
	}
}

// TestTimingModeOutput runs the default mode end to end: the table's hash is
// the independent frame-API hash, a missing input is counted and exits 1
// while the readable input is still reported, and the JSON carries base names
// only and no reference block without -ref.
func TestTimingModeOutput(t *testing.T) {
	dir := t.TempDir()
	path := sineWAV(t, dir, fileA, 48000, 12000, 440, 0.5)
	want := independentHash(t, path, 48000, 1, 12000, 64)
	missing := filepath.Join(dir, "missing.wav")
	jsonPath := filepath.Join(dir, "out.json")

	args := []string{flagRuns, "2", flagSecs, "1", flagBitrates, "64"}
	var out, errb bytes.Buffer
	if code := run(t.Context(), append(args, path), &out, &errb); code != exitOK {
		t.Fatalf("exit %d, want 0: %s", code, errb.String())
	}
	var sha string
	for l := range strings.SplitSeq(out.String(), "\n") {
		if f := strings.Fields(l); len(f) > 0 && f[0] == fileA {
			sha = f[len(f)-1]
		}
	}
	if sha != want {
		t.Errorf("table hash %q, want %s:\n%s", sha, want, out.String())
	}

	out.Reset()
	errb.Reset()
	if code := run(t.Context(), append(args, flagJSON, jsonPath, missing, path), &out, &errb); code != exitFailed {
		t.Errorf("with a missing input: exit %d, want 1", code)
	}
	if !strings.Contains(out.String(), fileA) {
		t.Errorf("the readable input is missing from the table:\n%s", out.String())
	}
	b, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	var rep struct {
		SchemaVersion int `json:"schema_version"`
		Failed        int
		Cases         []struct{ File string }
	}
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.SchemaVersion != 1 || rep.Failed != 1 || len(rep.Cases) != 1 || rep.Cases[0].File != fileA {
		t.Errorf("JSON report %+v", rep)
	}
	if strings.Contains(string(b), dir) || strings.Contains(string(b), `"ref"`) {
		t.Errorf("JSON carries a directory or a reference block:\n%s", b)
	}
}

// failWriter fails every write, like a full disk behind a redirect.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

// A list line that cannot be written fails -record: exit 0 would hand back a
// shorter list that still parses.
func TestRecordFailsWhenListWriteFails(t *testing.T) {
	path := sineWAV(t, t.TempDir(), fileA, 48000, 12000, 440, 0.5)
	var errb bytes.Buffer
	code := run(t.Context(), []string{flagRecord, flagSecs, "1", flagBitrates, "64", path}, failWriter{}, &errb)
	if code != exitFailed {
		t.Errorf("exit %d, want %d\nstderr: %s", code, exitFailed, errb.String())
	}
}

// An output flag that names an input, directly or through a link, is refused
// before anything is written, and the input keeps its bytes.
func TestOutputOverInputIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := sineWAV(t, dir, fileA, 48000, 12000, 440, 0.5)
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The link cases skip on their own where the file system has no links;
	// the direct cases always run.
	symlink := filepath.Join(dir, "symlink.json")
	symErr := os.Symlink(path, symlink)
	hardlink := filepath.Join(dir, "hardlink.json")
	hardErr := os.Link(path, hardlink)
	for _, tt := range []struct {
		name, flag, out string
		linkErr         error
	}{
		{"json", flagJSON, path, nil},
		{"cpuprofile", "-cpuprofile", path, nil},
		{"json through a symlink", flagJSON, symlink, symErr},
		{"json through a hard link", flagJSON, hardlink, hardErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.linkErr != nil {
				t.Skipf("cannot make the link: %v", tt.linkErr)
			}
			var out, errb bytes.Buffer
			args := []string{flagSecs, "1", flagBitrates, "64", flagRuns, "1", tt.flag, tt.out, path}
			if code := run(t.Context(), args, &out, &errb); code != exitSetup {
				t.Errorf("exit %d, want %d\nstderr: %s", code, exitSetup, errb.String())
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Error("the input file was overwritten")
			}
		})
	}
}

// -json and -cpuprofile naming one file is refused before either is created:
// the same path, the same path with a ./ segment in it, and an existing file.
func TestOutputsMustDiffer(t *testing.T) {
	dir := t.TempDir()
	path := sineWAV(t, dir, fileA, 48000, 12000, 440, 0.5)
	fresh := filepath.Join(dir, "fresh.out")
	respelled := dir + string(filepath.Separator) + "." + string(filepath.Separator) + "fresh.out"
	existing := filepath.Join(dir, "existing.out")
	if err := os.WriteFile(existing, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ name, json, profile string }{
		{"same new path", fresh, fresh},
		{"same new path spelled differently", fresh, respelled},
		{"same existing file", existing, existing},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			args := []string{flagSecs, "1", flagBitrates, "64", flagRuns, "1", flagJSON, tt.json, "-cpuprofile", tt.profile, path}
			if code := run(t.Context(), args, &out, &errb); code != exitSetup {
				t.Errorf("exit %d, want %d\nstderr: %s", code, exitSetup, errb.String())
			}
			if _, err := os.Stat(fresh); err == nil {
				t.Error("an output file was created")
			}
			if got, err := os.ReadFile(existing); err != nil || string(got) != "keep" {
				t.Errorf("the existing file was touched: %q, %v", got, err)
			}
		})
	}
}
