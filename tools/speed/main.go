// Command speed measures the encoder's realtime factor on WAV files and
// prints a SHA-256 of every encoded stream, so a speed change can be shown to
// leave the output bytes alone.
//
// The method is the frame API: each input is cut to -secs seconds, passed to
// Encoder.EncodeFrame in 1152-sample frames (the short final frame as it is),
// then drained with a nil call, and the hash covers every byte returned. The
// timed section is the encode only: WAV parsing, sample conversion,
// NewEncoder and hashing sit outside it, and the output buffer is sized
// beforehand so appends do not grow it inside the timing.
//
// With -ref the same audio is also timed through ffmpeg's libmp3lame, run as
// a black box child process. ffmpeg's start-up would dominate a short input,
// so the cut segment is written to a temporary WAV and ffmpeg loops it
// (-stream_loop) to at least -ref-secs of audio, and a 50 ms input measures
// the start-up, which is subtracted from the wall and CPU times.
//
// -record prints reference hash lines for the inputs; -check re-encodes every
// case of such a list against a local corpus directory and fails on any
// difference.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/go-mp3"
	"github.com/tphakala/go-mp3/internal/quality"
)

// Exit codes. 0 to 3 mean what they mean in tools/quality (main.go:29-32);
// exitUnverified is specific to -check.
const (
	exitOK          = 0
	exitFailed      = 1
	exitSetup       = 2
	exitInterrupted = 3
	exitUnverified  = 4
)

// logf writes to a report or diagnostic sink; a failed write has nowhere
// else to be reported, so it is ignored (as in tools/quality/main.go:393).
func logf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func main() { os.Exit(realMain()) }

// realMain is separate from main so NotifyContext's stop runs on return: a
// Ctrl-C then cancels the run and the deferred temp-dir cleanup executes.
func realMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return run(ctx, os.Args[1:], os.Stdout, os.Stderr)
}

// options is the parsed command line.
type options struct {
	secs, runs, refSecs int
	bitrates            []int
	ref, record, check  bool
	ffmpeg, jsonOut     string
	cpuProfile          string
	list, corpus        string
	inputs              []string
}

// defaultList is relative to the repository root, where `go run ./tools/speed`
// and `task speed` start (the same convention as tools/quality/main.go).
const defaultList = "tools/speed/testdata/output-hashes.txt"

func parseOptions(args []string, stderr io.Writer) (*options, error) {
	o := &options{}
	flagSet := flag.NewFlagSet("speed", flag.ContinueOnError)
	flagSet.SetOutput(stderr)
	flagSet.Usage = func() {
		logf(stderr, "usage: speed [flags] file.wav...\n       speed -check -corpus DIR [-list FILE]\n")
		flagSet.PrintDefaults()
	}
	bitrates := flagSet.String("bitrates", "96,128", "comma-separated CBR bitrates in kbps")
	flagSet.IntVar(&o.secs, "secs", 30, "seconds of audio taken from the start of each input (0 means the whole file; a shorter file is used whole)")
	flagSet.IntVar(&o.runs, "runs", 3, "timed runs per case; the median is reported")
	flagSet.BoolVar(&o.ref, "ref", false, "also time ffmpeg's libmp3lame on the same audio")
	flagSet.StringVar(&o.ffmpeg, "ffmpeg", "", "ffmpeg binary for -ref (default: ffmpeg on PATH)")
	flagSet.IntVar(&o.refSecs, "ref-secs", 300, "the -ref input is looped to at least this many seconds of audio")
	flagSet.StringVar(&o.jsonOut, "json", "", "also write the timing results as JSON to this file")
	flagSet.StringVar(&o.cpuProfile, "cpuprofile", "", "write a CPU profile of the timing run to this file")
	flagSet.BoolVar(&o.record, "record", false, "print hash list lines for the inputs instead of timing")
	flagSet.BoolVar(&o.check, "check", false, "verify -list against the WAV files in -corpus")
	flagSet.StringVar(&o.list, "list", defaultList, "hash list for -check")
	flagSet.StringVar(&o.corpus, "corpus", "", "corpus directory for -check")
	if err := flagSet.Parse(args); err != nil {
		return nil, err // the flag package has already printed it
	}
	o.inputs = flagSet.Args()
	set := map[string]bool{}
	flagSet.Visit(func(f *flag.Flag) { set[f.Name] = true })

	var err error
	if o.bitrates, err = parseKbps(*bitrates); err != nil {
		return nil, setupError(stderr, fmt.Errorf("-bitrates: %w", err))
	}
	if err := o.validate(set); err != nil {
		return nil, setupError(stderr, err)
	}
	return o, nil
}

// setupError reports err on stderr and returns it, so every parseOptions
// failure is printed there once (the flag package prints its own).
func setupError(stderr io.Writer, err error) error {
	logf(stderr, "speed: %v\n", err)
	return err
}

// validate rejects flag combinations that have no meaning; set names the
// flags given explicitly on the command line.
func (o *options) validate(set map[string]bool) error {
	switch {
	case o.record && o.check:
		return errors.New("-record and -check are exclusive")
	case o.runs < 1:
		return errors.New("-runs must be at least 1")
	case o.secs < 0:
		return errors.New("-secs must not be negative")
	case o.refSecs < 1:
		return errors.New("-ref-secs must be at least 1")
	}
	if o.record || o.check {
		for _, name := range []string{"ref", "json", "cpuprofile", "ffmpeg"} {
			if set[name] {
				return fmt.Errorf("-%s only applies to timing runs", name)
			}
		}
	}
	if o.check {
		if len(o.inputs) > 0 {
			return errors.New("-check takes its files from the list and -corpus, not from arguments")
		}
		if o.corpus == "" {
			return errors.New("-check needs -corpus")
		}
	} else if len(o.inputs) == 0 {
		return errors.New("no input files")
	}
	return nil
}

// parseKbps parses a comma-separated list of positive, distinct integers.
func parseKbps(s string) ([]int, error) {
	var out []int
	for f := range strings.SplitSeq(s, ",") {
		v, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("%q is not a positive integer", f)
		}
		if slices.Contains(out, v) {
			return nil, fmt.Errorf("%d is repeated", v)
		}
		out = append(out, v)
	}
	return out, nil
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	o, err := parseOptions(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitSetup
	}
	switch {
	case o.check:
		return runCheck(ctx, o, stdout, stderr)
	case o.record:
		return runRecord(ctx, o, stdout, stderr)
	default:
		return runTiming(ctx, o, stdout, stderr)
	}
}

// wavFile is a decoded input and the digest of its bytes.
type wavFile struct {
	name  string // base name only: no directory reaches tracked files or output
	inSHA string // SHA-256 of the file bytes
	rate  int
	ch    [][]float64
}

// readInput reads a file and returns its bytes with their SHA-256.
func readInput(path string) (data []byte, sha string, err error) {
	data, err = os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	return data, hashHex(data), nil
}

// decodeWAV parses WAV bytes. It rejects an empty file, which EncodeFrame
// would reject anyway (encoder.go:314-318), and a missing sample rate.
func decodeWAV(name, sha string, data []byte) (*wavFile, error) {
	rate, ch, err := quality.ReadWAV(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if rate <= 0 || len(ch[0]) == 0 {
		return nil, fmt.Errorf("%s: no audio (rate %d, %d frames)", name, rate, len(ch[0]))
	}
	return &wavFile{name: name, inSHA: sha, rate: rate, ch: ch}, nil
}

// pathErr drops the directory from an *fs.PathError so error text carries the
// base name only. The result still satisfies errors.Is(err, fs.ErrNotExist).
func pathErr(err error) error {
	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		return pe.Err
	}
	return err
}

func loadWAV(path string) (*wavFile, error) {
	name := filepath.Base(path)
	data, sha, err := readInput(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, pathErr(err))
	}
	return decodeWAV(name, sha, data)
}

// cut returns the first secs seconds of the file (all of it when secs is 0 or
// the file is shorter) as the encoder's float32 input, and as the float64
// segment the reference input is written from. The encoder input goes through
// Quantize16 so it carries exactly the samples a reader of the 16-bit
// reference WAV sees: for a 16-bit source that is the identity, and other
// bit depths reach both encoders quantized to 16 bits.
func (w *wavFile) cut(secs int) (pcm [][]float32, seg [][]float64) {
	n := len(w.ch[0])
	if secs > 0 {
		if limit := int64(secs) * int64(w.rate); limit < int64(n) {
			n = int(limit)
		}
	}
	pcm = make([][]float32, len(w.ch))
	seg = make([][]float64, len(w.ch))
	for c := range w.ch {
		seg[c] = w.ch[c][:n]
		q := quality.Quantize16(seg[c])
		pcm[c] = make([]float32, n)
		for i, v := range q {
			pcm[c][i] = float32(v)
		}
	}
	return pcm, seg
}

// streamCap bounds the encoded size of frames sample frames at kbps: one
// 144*bitrate/rate byte frame (plus a padding byte) per 1152 samples, with
// slack for the partial frame and the drain.
func streamCap(frames, rate, kbps int) int {
	return (frames/mp3.FrameSize + 3) * (144000*kbps/rate + 1)
}

// encodeStream encodes pcm and appends the stream to out; a nil out is sized
// with streamCap once the encoder has accepted rate and kbps, so a rejected
// configuration allocates nothing. Only the encode loop and the drain are
// timed.
func encodeStream(pcm [][]float32, rate, kbps int, out []byte) (stream []byte, elapsed time.Duration, err error) {
	e, err := mp3.NewEncoder(mp3.EncoderConfig{SampleRate: rate, Channels: len(pcm), Bitrate: kbps * 1000})
	if err != nil {
		return out, 0, err
	}
	n := len(pcm[0])
	if out == nil {
		out = make([]byte, 0, streamCap(n, rate, kbps))
	}
	views := make([][]float32, len(pcm))
	start := time.Now()
	for pos := 0; pos < n; pos += mp3.FrameSize {
		end := min(pos+mp3.FrameSize, n)
		for c := range views {
			views[c] = pcm[c][pos:end]
		}
		if out, err = e.EncodeFrame(out, views); err != nil {
			return out, time.Since(start), err
		}
	}
	out, err = e.EncodeFrame(out, nil)
	return out, time.Since(start), err
}

func hashHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
