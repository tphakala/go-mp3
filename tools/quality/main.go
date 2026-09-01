package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/go-mp3/internal/quality"
)

// Exit codes: setup errors (flags, missing lame) are 2, a run where some
// case failed is 1, success is 0.
const (
	exitOK    = 0
	exitCases = 1
	exitSetup = 2
)

// unknownVersion is reported when a provenance value cannot be determined.
const unknownVersion = "unknown"

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stderr))
}

// options is the parsed command line.
type options struct {
	rates, bitrates          []int
	programs                 []quality.Program
	seconds                  int
	lame, visqol, peaq, work string
	keep                     bool
	out, jsonOut             string
}

// parseFlags parses args into options, resolving the program list.
func parseFlags(args []string) (*options, error) {
	o := &options{}
	fs := flag.NewFlagSet("quality", flag.ContinueOnError)
	rates := fs.String("rates", "44100", "comma-separated sample rates (32000, 44100, 48000)")
	bitrates := fs.String("bitrates", "128,192,256,320", "comma-separated CBR bitrates in kbps")
	programs := fs.String("programs", "", "comma-separated program names to run (default: all synthetic programs)")
	corpus := fs.String("corpus", "", "directory of WAV files to add as programs (named by file name)")
	fs.IntVar(&o.seconds, "seconds", 6, "program length in seconds for synthetic programs")
	fs.StringVar(&o.lame, "lame", "", "lame binary (default: lame on PATH)")
	fs.StringVar(&o.visqol, "visqol", "", "visqol binary (default: visqol on PATH; skipped when absent)")
	fs.StringVar(&o.peaq, "peaq", "", "PEAQ binary printing 'Objective Difference Grade:' (default: peaq-odg on PATH; skipped when absent)")
	fs.StringVar(&o.work, "work", "", "work directory for intermediate files (default: a temp dir next to -out)")
	fs.BoolVar(&o.keep, "keep", false, "keep the work directory")
	fs.StringVar(&o.out, "out", "tools/quality/out/report.md", "markdown report path")
	fs.StringVar(&o.jsonOut, "json", "tools/quality/out/report.json", "JSON report path")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	var err error
	if o.rates, err = parseInts(*rates); err != nil {
		return nil, fmt.Errorf("-rates: %w", err)
	}
	if o.bitrates, err = parseInts(*bitrates); err != nil {
		return nil, fmt.Errorf("-bitrates: %w", err)
	}
	if o.seconds <= 0 {
		return nil, errors.New("-seconds must be positive")
	}
	if o.programs, err = selectPrograms(*programs, *corpus); err != nil {
		return nil, err
	}
	return o, nil
}

// run is the whole program: parse, execute the grid, write the reports,
// return the exit code. Diagnostics and progress go to errw.
func run(ctx context.Context, args []string, errw io.Writer) int {
	o, err := parseFlags(args)
	if err != nil {
		return fail(errw, err)
	}
	tl := detectTools(o.lame, o.visqol, o.peaq)
	if tl.lame == "" {
		return fail(errw, errors.New("lame binary not found (install lame or pass -lame)"))
	}
	if err := os.MkdirAll(filepath.Dir(o.out), 0o755); err != nil {
		return fail(errw, err)
	}
	workDir := o.work
	if workDir == "" {
		workDir, err = os.MkdirTemp(filepath.Dir(o.out), "work-")
		if err != nil {
			return fail(errw, err)
		}
		if !o.keep {
			defer func() { _ = os.RemoveAll(workDir) }()
		}
	}

	rep := &report{
		GeneratedUTC: time.Now().UTC().Format(time.RFC3339),
		GoMP3Rev:     vcsRevision(),
		LAMEVersion:  lameVersion(ctx, tl.lame),
		Seconds:      o.seconds,
	}
	if tl.visqol != "" {
		rep.Tools = append(rep.Tools, "visqol")
	}
	if tl.peaq != "" {
		rep.Tools = append(rep.Tools, "peaq-odg")
	}

	failed, err := runGrid(ctx, tl, o, workDir, rep, errw)
	if err != nil {
		return fail(errw, err)
	}
	if err := writeFile(o.out, func(f *os.File) error { return writeMarkdown(f, rep) }); err != nil {
		return fail(errw, err)
	}
	if err := writeFile(o.jsonOut, func(f *os.File) error { return writeJSON(f, rep) }); err != nil {
		return fail(errw, err)
	}
	logf(errw, "wrote %s and %s (%d cases, %d failed)\n", o.out, o.jsonOut, len(rep.Cases), failed)
	if failed > 0 {
		return exitCases
	}
	return exitOK
}

// runGrid executes every (rate, program, bitrate) case, appending results to
// rep and returning how many cases failed. Only setup-class errors (a work
// directory that cannot be created) are returned.
func runGrid(ctx context.Context, tl tools, o *options, workDir string, rep *report, errw io.Writer) (int, error) {
	total := len(o.programs) * len(o.rates) * len(o.bitrates)
	failed, i := 0, 0
	for _, sr := range o.rates {
		for _, p := range o.programs {
			for _, kbps := range o.bitrates {
				i++
				spec := caseSpec{Program: p, SampleRate: sr, Kbps: kbps, Seconds: o.seconds}
				dir := filepath.Join(workDir, fmt.Sprintf("%s-%d-%d", p.Name, sr, kbps))
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return failed, err
				}
				start := time.Now()
				res, err := runCase(ctx, tl, dir, spec)
				if err != nil {
					failed++
					logf(errw, "[%d/%d] %s %d Hz %d kbps: FAILED: %v\n", i, total, p.Name, sr, kbps, err)
					continue
				}
				rep.Cases = append(rep.Cases, res)
				logf(errw, "[%d/%d] %s %d Hz %d kbps: go-mp3 SNR %s LSD %s MOS %s | LAME SNR %s LSD %s MOS %s (%.1fs)\n",
					i, total, p.Name, sr, kbps,
					fmtMetric(res.GoMP3.Metrics.SNR), fmtMetric(res.GoMP3.Metrics.LSD), fmtMetric(res.GoMP3.MOS),
					fmtMetric(res.LAME.Metrics.SNR), fmtMetric(res.LAME.Metrics.LSD), fmtMetric(res.LAME.MOS),
					time.Since(start).Seconds())
			}
		}
	}
	return failed, nil
}

// logf writes a progress or diagnostic line; a failed write to the log sink
// is deliberately ignored (there is nowhere else to report it).
func logf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

// fail prints a setup error and returns the setup exit code.
func fail(errw io.Writer, err error) int {
	logf(errw, "quality: %v\n", err)
	return exitSetup
}

// writeFile creates path and hands it to fn, closing it afterwards.
func writeFile(path string, fn func(*os.File) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := fn(f); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// parseInts parses a non-empty comma-separated integer list.
func parseInts(s string) ([]int, error) {
	var out []int
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		v, err := strconv.Atoi(part)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, errors.New("empty list")
	}
	return out, nil
}

// selectPrograms returns the synthetic programs named in filter (all when
// empty) plus one program per WAV file in corpus.
func selectPrograms(filter, corpus string) ([]quality.Program, error) {
	var progs []quality.Program
	if filter == "" {
		progs = quality.Programs()
	} else {
		for name := range strings.SplitSeq(filter, ",") {
			name = strings.TrimSpace(name)
			p, ok := quality.ProgramByName(name)
			if !ok {
				return nil, fmt.Errorf("unknown program %q", name)
			}
			progs = append(progs, p)
		}
	}
	if corpus == "" {
		return progs, nil
	}
	entries, err := os.ReadDir(corpus)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".wav") {
			continue
		}
		p, err := wavProgram(filepath.Join(corpus, e.Name()))
		if err != nil {
			return nil, err
		}
		progs = append(progs, p)
	}
	return progs, nil
}

// wavProgram wraps a WAV file as a Program. Its Gen ignores nSamples in
// favor of the file's own length and returns empty channels (which runCase
// reports as a failed case) when asked for a different sample rate than the
// file's, since resampling would change what is being measured.
func wavProgram(path string) (quality.Program, error) {
	f, err := os.Open(path)
	if err != nil {
		return quality.Program{}, err
	}
	sr, ch, err := quality.ReadWAV(f)
	_ = f.Close() // read-only handle; the parse result is what matters
	if err != nil {
		return quality.Program{}, fmt.Errorf("%s: %w", path, err)
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return quality.Program{Name: name, Channels: len(ch), Gen: func(rate, _ int) [][]float64 {
		if rate != sr {
			out := make([][]float64, len(ch))
			for c := range out {
				out[c] = []float64{}
			}
			return out
		}
		return ch
	}}, nil
}

// vcsRevision returns the short VCS revision embedded by the Go toolchain,
// or unknownVersion (a build without VCS stamping, or a non-git checkout).
func vcsRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return unknownVersion
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			return s.Value[:7]
		}
	}
	return unknownVersion
}
