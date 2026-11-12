package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/tphakala/go-mp3/internal/enc"
)

// hashEntry is one case of the reference list. Its identity is (Name, Secs,
// Kbps); nothing else in the list or the corpus directory affects it.
type hashEntry struct {
	Name   string
	InSHA  string // SHA-256 of the WAV file
	Secs   int    // the -secs value; 0 means the whole file
	Kbps   int
	OutSHA string // SHA-256 of the encoded stream
}

func (e hashEntry) String() string {
	return fmt.Sprintf("%s %s %d %d %s", e.Name, e.InSHA, e.Secs, e.Kbps, e.OutSHA)
}

type entryKey struct {
	name       string
	secs, kbps int
}

// validName reports whether name can be a list entry's first field: a plain
// base name with no whitespace, which would split the field, and no leading
// '#', which would turn the line into a comment.
func validName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.HasPrefix(name, "#") &&
		!strings.ContainsAny(name, `/\`) && strings.IndexFunc(name, unicode.IsSpace) < 0
}

func isHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// parseHashList reads a reference list: blank lines and lines starting with
// '#' are skipped, every other line is a hashEntry.String() line. Errors name
// the line.
func parseHashList(r io.Reader) ([]hashEntry, error) {
	var entries []hashEntry
	seen := map[entryKey]bool{}
	sc := bufio.NewScanner(r)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		e, err := parseEntry(text)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		k := entryKey{e.Name, e.Secs, e.Kbps}
		if seen[k] {
			return nil, fmt.Errorf("line %d: duplicate case %s %d %d", line, e.Name, e.Secs, e.Kbps)
		}
		seen[k] = true
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, errors.New("no entries")
	}
	return entries, nil
}

func parseEntry(text string) (hashEntry, error) {
	f := strings.Fields(text)
	if len(f) != 5 {
		return hashEntry{}, fmt.Errorf("want 5 fields, got %d", len(f))
	}
	e := hashEntry{Name: f[0], InSHA: f[1], OutSHA: f[4]}
	if !validName(e.Name) {
		return hashEntry{}, fmt.Errorf("%q is not a plain file name", e.Name)
	}
	if !isHash(e.InSHA) || !isHash(e.OutSHA) {
		return hashEntry{}, errors.New("a hash is not 64 lowercase hex characters")
	}
	var err error
	if e.Secs, err = strconv.Atoi(f[2]); err != nil || e.Secs < 0 {
		return hashEntry{}, fmt.Errorf("secs %q is not a non-negative integer", f[2])
	}
	if e.Kbps, err = strconv.Atoi(f[3]); err != nil || !enc.ValidBitrateKbps(e.Kbps) {
		return hashEntry{}, fmt.Errorf("kbps %q is not %s", f[3], legalKbps)
	}
	return e, nil
}

// encodeHash encodes pcm at kbps and returns the stream's hash.
func encodeHash(pcm [][]float32, rate, kbps int) (string, error) {
	stream, _, err := encodeStream(pcm, rate, kbps, nil)
	if err != nil {
		return "", err
	}
	return hashHex(stream), nil
}

// runRecord prints one list line per input and bitrate. The output is meant
// to be appended to a list, so it carries no header; the entries' identity is
// the base name, hence two inputs sharing one are an error. An interrupt stops
// it before the next bitrate with exit 3, after the lines already printed, and
// a line that cannot be written stops it with exit 1.
func runRecord(ctx context.Context, o *options, stdout, stderr io.Writer) int {
	failed := 0
	names := map[string]bool{}
	for _, path := range o.inputs {
		if ctx.Err() != nil {
			return exitInterrupted
		}
		name := filepath.Base(path)
		switch {
		case !validName(name):
			logf(stderr, "%s: not usable as a list name (whitespace, leading #, or a path separator)\n", name)
			failed++
			continue
		case names[name]:
			logf(stderr, "%s: two inputs share this base name\n", name)
			failed++
			continue
		}
		names[name] = true
		w, err := loadWAV(path)
		if err != nil {
			logf(stderr, "%v\n", err)
			failed++
			continue
		}
		pcm, _ := w.cut(o.secs)
		for _, kbps := range o.bitrates {
			if ctx.Err() != nil {
				return exitInterrupted
			}
			sum, err := encodeHash(pcm, w.rate, kbps)
			if err != nil {
				logf(stderr, "%s at %d kbps: %v\n", name, kbps, err)
				failed++
				continue
			}
			// The list is the product of this mode: a write that fails must
			// not end in exit 0 with a shorter list that still parses.
			if _, err := fmt.Fprintln(stdout, hashEntry{name, w.inSHA, o.secs, kbps, sum}); err != nil {
				logf(stderr, "speed: writing the list: %v\n", err)
				return exitFailed
			}
		}
	}
	if failed > 0 {
		return exitFailed
	}
	return exitOK
}

// Case statuses printed by -check.
const (
	statusOK       = "ok"
	statusMismatch = "mismatch"
	statusError    = "error"
	statusMissing  = "missing"
	statusChanged  = "input-changed"
)

// inputCache holds the last file read. A recorded list keeps a file's
// bitrates on adjacent lines, so one read serves all of them.
type inputCache struct {
	loaded  bool
	name    string
	sha     string
	data    []byte // dropped once decoded
	readErr error
	wav     *wavFile
	decErr  error
	pcm     [][]float32 // wav cut to pcmSecs, reused across a file's bitrates
	pcmSecs int
}

// load reads corpus/name unless it is the cached file.
func (c *inputCache) load(corpus, name string) {
	if c.loaded && c.name == name {
		return
	}
	*c = inputCache{name: name, loaded: true}
	c.data, c.sha, c.readErr = readInput(filepath.Join(corpus, name))
}

// decoded returns the parsed file, decoding on first use.
func (c *inputCache) decoded() (*wavFile, error) {
	if c.wav == nil && c.decErr == nil {
		c.wav, c.decErr = decodeWAV(c.name, c.sha, c.data)
		c.data = nil
	}
	return c.wav, c.decErr
}

// cutPCM returns the encoder input for secs, cutting once per file and secs.
func (c *inputCache) cutPCM(w *wavFile, secs int) [][]float32 {
	if c.pcm == nil || c.pcmSecs != secs {
		c.pcm, _ = w.cut(secs)
		c.pcmSecs = secs
	}
	return c.pcm
}

// verify returns the status of one entry and the text to append to its line.
// A case counts as verified only when the input hash and the output hash both
// match: a missing or replaced input is neither ok nor a mismatch.
func (c *inputCache) verify(corpus string, e *hashEntry) (status, detail string) {
	c.load(corpus, e.Name)
	switch {
	case errors.Is(c.readErr, fs.ErrNotExist):
		return statusMissing, ""
	case c.readErr != nil:
		return statusError, ": " + pathErr(c.readErr).Error()
	case c.sha != e.InSHA:
		return statusChanged, " got " + c.sha + " want " + e.InSHA
	}
	w, err := c.decoded()
	if err != nil {
		return statusError, ": " + err.Error()
	}
	got, err := encodeHash(c.cutPCM(w, e.Secs), w.rate, e.Kbps)
	switch {
	case err != nil:
		return statusError, ": " + err.Error()
	case got != e.OutSHA:
		return statusMismatch, " got " + got + " want " + e.OutSHA
	}
	return statusOK, ""
}

// runCheck verifies every case of the list against the corpus. It writes
// nothing but its report. Exit 1 when an output differs or a case failed, 4
// when only inputs are missing or changed, else 0.
func runCheck(ctx context.Context, o *options, stdout, stderr io.Writer) int {
	f, err := os.Open(o.list)
	if err != nil {
		logf(stderr, "speed: %v\n", pathErr(err))
		return exitSetup
	}
	entries, err := parseHashList(f)
	_ = f.Close()
	if err != nil {
		logf(stderr, "speed: %s: %v\n", filepath.Base(o.list), err)
		return exitSetup
	}
	counts := map[string]int{}
	var cache inputCache
	for i := range entries {
		if ctx.Err() != nil {
			return exitInterrupted
		}
		e := &entries[i]
		status, detail := cache.verify(o.corpus, e)
		counts[status]++
		logf(stdout, "%s %s %d %d%s\n", status, e.Name, e.Secs, e.Kbps, detail)
	}
	logf(stdout, "checked %d cases: %d %s, %d %s, %d %s, %d %s, %d %s\n", len(entries),
		counts[statusOK], statusOK, counts[statusMismatch], statusMismatch, counts[statusError], statusError,
		counts[statusMissing], statusMissing, counts[statusChanged], statusChanged)
	switch {
	case counts[statusMismatch]+counts[statusError] > 0:
		return exitFailed
	case counts[statusMissing]+counts[statusChanged] > 0:
		return exitUnverified
	}
	return exitOK
}
