package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	hexA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hexB = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

func TestParseHashList(t *testing.T) {
	t.Run("round trip", func(t *testing.T) {
		want := []hashEntry{
			{Name: "a.wav", InSHA: hexA, Secs: 30, Kbps: 128, OutSHA: hexB},
			{Name: "tone+noise-stereo-44k.wav", InSHA: hexB, Secs: 0, Kbps: 64, OutSHA: hexA},
		}
		text := "# header\n\n" + want[0].String() + "\n# note\n" + want[1].String() + "\n\n"
		got, err := parseHashList(strings.NewReader(text))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("parsed %+v, want %+v", got, want)
		}
	})

	good := hexA + " 30 128 " + hexB
	rejects := []struct{ name, text string }{
		{"four fields", "a.wav " + hexA + " 30 128\n"},
		{"six fields", "a.wav " + hexA + " 30 128 " + hexB + " extra\n"},
		{"short hash", "a.wav " + hexA[:63] + " 30 128 " + hexB + "\n"},
		{"uppercase hash", "a.wav " + strings.ToUpper(hexB) + " 30 128 " + hexB + "\n"},
		{"non-numeric secs", "a.wav " + hexA + " x 128 " + hexB + "\n"},
		{"negative secs", "a.wav " + hexA + " -1 128 " + hexB + "\n"},
		{"zero kbps", "a.wav " + hexA + " 30 0 " + hexB + "\n"},
		{"duplicate case", "a.wav " + good + "\na.wav " + good + "\n"},
		{"path in name", "dir/a.wav " + good + "\n"},
		{"no entries", "# only a comment\n\n"},
	}
	for _, tt := range rejects {
		t.Run("reject "+tt.name, func(t *testing.T) {
			if got, err := parseHashList(strings.NewReader(tt.text)); err == nil {
				t.Errorf("parsed %+v, want an error", got)
			}
		})
	}

	t.Run("same name at another bitrate is not a duplicate", func(t *testing.T) {
		text := "a.wav " + hexA + " 30 96 " + hexB + "\na.wav " + hexA + " 30 128 " + hexB + "\n"
		if _, err := parseHashList(strings.NewReader(text)); err != nil {
			t.Error(err)
		}
	})
}

// checkFixture is three short mono files and a list recorded from them.
type checkFixture struct {
	dir, list string
	names     []string
}

func newCheckFixture(t *testing.T) *checkFixture {
	t.Helper()
	dir := t.TempDir()
	f := &checkFixture{dir: dir, list: filepath.Join(dir, "list.txt"), names: []string{"a.wav", "b.wav", "c.wav"}}
	args := make([]string, 0, 5+len(f.names))
	args = append(args, "-record", "-secs", "1", "-bitrates", "64")
	for i, name := range f.names {
		args = append(args, sineWAV(t, dir, name, 48000, 12000, 440*float64(i+1), 0.5))
	}
	var out, errb bytes.Buffer
	if code := run(t.Context(), args, &out, &errb); code != exitOK {
		t.Fatalf("record exited %d: %s", code, errb.String())
	}
	if strings.Contains(out.String(), dir) {
		t.Errorf("record output carries the directory path:\n%s", out.String())
	}
	if err := os.WriteFile(f.list, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

// check runs -check and returns the exit code and stdout.
func (f *checkFixture) check(t *testing.T) (code int, stdout string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(t.Context(), []string{"-check", "-corpus", f.dir, "-list", f.list}, &out, &errb)
	if strings.Contains(out.String(), f.dir) {
		t.Errorf("check output carries the directory path:\n%s", out.String())
	}
	return code, out.String()
}

// breakOutput replaces the output hash of name's entry in the list.
func (f *checkFixture) breakOutput(t *testing.T, name string) {
	t.Helper()
	b, err := os.ReadFile(f.list)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, name+" ") {
			lines[i] = l[:len(l)-64] + strings.Repeat("0", 64)
		}
	}
	if err := os.WriteFile(f.list, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func hasLine(out, prefix string) bool {
	for l := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

func TestCheckOutcomes(t *testing.T) {
	t.Run("unchanged", func(t *testing.T) {
		f := newCheckFixture(t)
		code, out := f.check(t)
		if code != exitOK {
			t.Errorf("exit %d, want 0:\n%s", code, out)
		}
		for _, name := range f.names {
			if !hasLine(out, "ok "+name+" 1 64") {
				t.Errorf("no ok line for %s:\n%s", name, out)
			}
		}
		if !hasLine(out, "checked 3 cases: 3 ok, 0 mismatch, 0 error, 0 missing, 0 input-changed") {
			t.Errorf("summary line wrong:\n%s", out)
		}
	})

	t.Run("output hash differs", func(t *testing.T) {
		f := newCheckFixture(t)
		f.breakOutput(t, "b.wav")
		code, out := f.check(t)
		if code != exitFailed {
			t.Errorf("exit %d, want 1:\n%s", code, out)
		}
		if !hasLine(out, "mismatch b.wav 1 64 got ") || !hasLine(out, "ok a.wav") || !hasLine(out, "ok c.wav") {
			t.Errorf("want mismatch for b.wav and ok for the others:\n%s", out)
		}
	})

	// A partial corpus must never pass, and a replaced recording must not be
	// reported as an encoder regression.
	t.Run("missing input", func(t *testing.T) {
		f := newCheckFixture(t)
		if err := os.Remove(filepath.Join(f.dir, "b.wav")); err != nil {
			t.Fatal(err)
		}
		code, out := f.check(t)
		if code != exitUnverified {
			t.Errorf("exit %d, want 4:\n%s", code, out)
		}
		if !hasLine(out, "missing b.wav 1 64") || !hasLine(out, "ok a.wav") || !hasLine(out, "ok c.wav") || hasLine(out, "mismatch") {
			t.Errorf("want missing for b.wav and ok for the others:\n%s", out)
		}
	})

	t.Run("changed input", func(t *testing.T) {
		f := newCheckFixture(t)
		sineWAV(t, f.dir, "c.wav", 48000, 12000, 1234, 0.25)
		code, out := f.check(t)
		if code != exitUnverified {
			t.Errorf("exit %d, want 4:\n%s", code, out)
		}
		if !hasLine(out, "input-changed c.wav 1 64 got ") || !hasLine(out, "ok a.wav") || hasLine(out, "mismatch") {
			t.Errorf("want input-changed for c.wav, not a mismatch:\n%s", out)
		}
	})

	t.Run("mismatch outranks unverified inputs", func(t *testing.T) {
		f := newCheckFixture(t)
		if err := os.Remove(filepath.Join(f.dir, "b.wav")); err != nil {
			t.Fatal(err)
		}
		f.breakOutput(t, "a.wav")
		if code, out := f.check(t); code != exitFailed {
			t.Errorf("exit %d, want 1:\n%s", code, out)
		}
	})
}

// TestCommittedHashListParses catches a committed list damaged by a merge or
// written in a format the parser no longer reads.
func TestCommittedHashListParses(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "output-hashes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := parseHashList(f); err != nil {
		t.Fatal(err)
	}
}
