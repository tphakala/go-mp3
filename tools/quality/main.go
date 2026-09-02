package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
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

func main() { os.Exit(realMain()) }

// realMain wires up signal handling and runs. It is separate from main so the
// os.Exit lives alone: NotifyContext's stop must be deferred (and so must run
// on the normal return here), which cannot sit in a function that also calls
// os.Exit.
func realMain() int {
	// NotifyContext so a Ctrl-C cancels the run rather than hard-killing the
	// process: run's deferred work-directory cleanup then executes on the
	// normal return, instead of being skipped by an uncaught signal.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// After the first signal has cancelled ctx, restore the default handler so
	// a second Ctrl-C hard-kills: otherwise NotifyContext keeps swallowing
	// signals until stop() runs, and a run stuck draining a wedged external
	// tool could not be force-quit.
	go func() {
		<-ctx.Done()
		stop()
	}()
	return run(ctx, os.Args[1:], os.Stderr)
}

// options is the parsed command line.
type options struct {
	rates, bitrates          []int
	programs                 []quality.Program
	seconds                  int
	jobs                     int
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
	fs.IntVar(&o.jobs, "jobs", runtime.GOMAXPROCS(0), "number of cases to run concurrently (each forks CPU-heavy external tools)")
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
	if o.jobs < 1 {
		return nil, errors.New("-jobs must be positive")
	}
	// Duplicates would run every case for a repeated value again and, worse,
	// double it in the per-bitrate summary (which counts programs per row),
	// so -bitrates 128,128 reported 22 programs where there were 11.
	o.rates = dedupInts(o.rates)
	o.bitrates = dedupInts(o.bitrates)
	// Validate at setup, not per case. An unvalidated rate reaches
	// Program.Gen, where a negative one panics in make and 0 makes the
	// chirp generator's zero-length period loop forever.
	for _, r := range o.rates {
		if r != 32000 && r != 44100 && r != 48000 {
			return nil, fmt.Errorf("-rates: unsupported sample rate %d (want 32000, 44100, or 48000)", r)
		}
	}
	for _, kbps := range o.bitrates {
		if kbps <= 0 {
			return nil, fmt.Errorf("-bitrates: %d is not a positive bitrate", kbps)
		}
	}
	if o.programs, err = selectPrograms(*programs, *corpus, o.rates); err != nil {
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
	// An explicitly named tool that does not resolve is a setup error, not a
	// reason to quietly omit its column: the user asked for that measurement.
	for _, t := range []struct{ flag, name, got string }{
		{o.lame, "-lame", tl.lame}, {o.visqol, "-visqol", tl.visqol}, {o.peaq, "-peaq", tl.peaq},
	} {
		if t.flag != "" && t.got == "" {
			return fail(errw, fmt.Errorf("%s %q not found", t.name, t.flag))
		}
	}
	// Both report directories, before the grid: creating only one meant a
	// completed run could discard its JSON at the final syscall.
	for _, p := range []string{o.out, o.jsonOut} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return fail(errw, err)
		}
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
		SchemaVersion: reportSchemaVersion,
		GeneratedUTC:  time.Now().UTC().Format(time.RFC3339),
		GoMP3Rev:      vcsRevision(),
		LAMEVersion:   lameVersion(ctx, tl.lame),
		Seconds:       o.seconds,
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
	rep.Failed = failed
	rep.Attempted = len(rep.Cases) + failed
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

// caseJob is one dispatched comparison. ref is the quantized reference,
// generated once per (sample rate, program) and shared read-only across that
// program's bitrate cases (and across the workers running them).
type caseJob struct {
	idx  int // 1-based, dense over dispatched jobs; also names the case dir
	spec caseSpec
	ref  [][]float64
}

// buildJobs expands the (rate, program, bitrate) grid into the dispatch list,
// generating and quantizing each program's reference once per sample rate and
// reusing it across bitrates. Rates a program cannot serve, and empty
// programs, are logged and dropped here so the job list holds only real cases.
func buildJobs(o *options, errw io.Writer) []caseJob {
	var jobs []caseJob
	for _, sr := range o.rates {
		for i := range o.programs {
			p := o.programs[i]
			if !p.RunsAt(sr) {
				logf(errw, "skip: %s has no data at %d Hz\n", p.Name, sr)
				continue
			}
			// Generate and quantize once per (rate, program) and share the
			// result read-only across this program's bitrate cases.
			ref := genRef(p, sr, o.seconds)
			if len(ref) == 0 {
				logf(errw, "skip: %s is empty at %d Hz\n", p.Name, sr)
				continue
			}
			for _, kbps := range o.bitrates {
				jobs = append(jobs, caseJob{
					idx:  len(jobs) + 1,
					spec: caseSpec{Program: p, SampleRate: sr, Kbps: kbps, Seconds: o.seconds},
					ref:  ref,
				})
			}
		}
	}
	return jobs
}

// genRef generates program p at sampleRate for seconds and quantizes it to the
// 16-bit signal both encoders compare against, returning nil for an empty
// program (a generator yielding no channels or no samples). Building the
// reference once here is what lets a program's bitrate cases share it.
func genRef(p quality.Program, sampleRate, seconds int) [][]float64 {
	raw := p.Gen(sampleRate, sampleRate*seconds)
	if len(raw) == 0 || len(raw[0]) == 0 {
		return nil
	}
	ref := make([][]float64, len(raw))
	for c := range raw {
		ref[c] = quality.Quantize16(raw[c])
	}
	return ref
}

// runGrid runs every case concurrently (up to o.jobs at a time), appending
// results to rep in deterministic grid order and returning how many cases
// failed. Only setup-class errors (a work directory that cannot be created)
// are returned; a per-case failure is counted, not fatal. A cancelled context
// (Ctrl-C) stops dispatch and leaves a partial report.
func runGrid(ctx context.Context, tl tools, o *options, workDir string, rep *report, errw io.Writer) (int, error) {
	jobs := buildJobs(o, errw)
	total := len(jobs)

	// Cancel the shared context on a setup error so in-flight workers stop.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make([]*caseResult, total) // nil = not completed
	failedFlags := make([]bool, total)    // true = ran and failed
	var (
		wg        sync.WaitGroup
		setupOnce sync.Once
		setupErr  error
	)
	// Serialize every log write for the run: workers emit progress lines here
	// AND, through runCase, per-tool warnings from measure, all to one sink
	// concurrently. os.Stderr locks internally, but a non-*os.File writer (a
	// test buffer) does not, so guard the sink itself rather than one call site.
	logw := &syncWriter{w: errw}
	// Reclaim each finished case's dir only when the whole work tree is a
	// throwaway temp dir the run deletes at the end anyway. An explicit -work
	// dir (or -keep) means the user wants to inspect artifacts, so leave those.
	reclaim := o.work == "" && !o.keep
	sem := make(chan struct{}, o.jobs)

	for ji := range jobs {
		if ctx.Err() != nil {
			break // stop dispatching once cancelled
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(ji int) {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			j := &jobs[ji]
			// Index-prefixed: a corpus file may share a synthetic program's
			// name, and two cases writing one directory would collide.
			dir := filepath.Join(workDir, fmt.Sprintf("%03d-%s-%d-%d", j.idx, j.spec.Program.Name, j.spec.SampleRate, j.spec.Kbps))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				setupOnce.Do(func() { setupErr = err })
				cancel()
				return
			}
			start := time.Now()
			res, err := runCase(ctx, tl, dir, j.spec, j.ref, logw)
			if err != nil {
				failedFlags[ji] = true
				logf(logw, "[%d/%d] %s %d Hz %d kbps: FAILED: %v\n", j.idx, total, j.spec.Program.Name, j.spec.SampleRate, j.spec.Kbps, err)
				return // leave the dir in place for inspection
			}
			results[ji] = &res
			if reclaim {
				_ = os.RemoveAll(dir) // reclaim as we go; nothing reads it now
			}
			logf(logw, "[%d/%d] %s %d Hz %d kbps: go-mp3 SNR %s LSD %s MOS %s | LAME SNR %s LSD %s MOS %s (%.1fs)\n",
				j.idx, total, j.spec.Program.Name, j.spec.SampleRate, j.spec.Kbps,
				fmtMetric(res.GoMP3.Metrics.SNR), fmtMetric(res.GoMP3.Metrics.LSD), fmtMetric(res.GoMP3.MOS),
				fmtMetric(res.LAME.Metrics.SNR), fmtMetric(res.LAME.Metrics.LSD), fmtMetric(res.LAME.MOS),
				time.Since(start).Seconds())
		}(ji)
	}
	wg.Wait()
	if setupErr != nil {
		return 0, setupErr
	}
	// Append in index order: the report stays byte-stable regardless of the
	// order workers happened to finish in.
	failed := 0
	for ji := range jobs {
		switch {
		case results[ji] != nil:
			rep.Cases = append(rep.Cases, *results[ji])
		case failedFlags[ji]:
			failed++
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

// syncWriter serializes concurrent writes to an underlying writer, so the
// parallel grid's log lines (worker progress plus per-tool warnings from
// measure) neither corrupt nor tear. os.Stderr locks internally, but a test
// buffer does not, so the guard lives on the shared sink, not one call site.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
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

// dedupInts returns xs with later duplicates removed, preserving first-seen
// order. It allocates a fresh slice, leaving the input untouched.
func dedupInts(xs []int) []int {
	seen := make(map[int]bool, len(xs))
	out := make([]int, 0, len(xs))
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// selectPrograms returns the synthetic programs named in filter (all when
// empty) plus one program per WAV file in corpus. rates is the effective
// -rates set, which every corpus file must declare one of.
func selectPrograms(filter, corpus string, rates []int) ([]quality.Program, error) {
	var progs []quality.Program
	switch filter {
	case "":
		progs = quality.Programs()
	case "none":
		// Explicitly no synthetic programs, for a corpus-only run. Left as an
		// empty list; the guard below rejects a run with nothing to compare.
		progs = nil
	default:
		// Deduplicate names the way -rates/-bitrates are deduped: a repeat
		// (-programs a,a) would otherwise run that program twice per (rate,
		// bitrate) and double-weight it in the summary's per-program mean.
		seen := make(map[string]bool)
		for name := range strings.SplitSeq(filter, ",") {
			name = strings.TrimSpace(name)
			if seen[name] {
				continue
			}
			seen[name] = true
			p, ok := quality.ProgramByName(name)
			if !ok {
				return nil, fmt.Errorf("unknown program %q", name)
			}
			progs = append(progs, p)
		}
	}
	if corpus == "" {
		if len(progs) == 0 {
			return nil, errors.New("no programs selected (-programs none needs a -corpus)")
		}
		return progs, nil
	}
	entries, err := os.ReadDir(corpus)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		// Regular files only: opening a fifo named *.wav blocks forever.
		if !e.Type().IsRegular() || !strings.EqualFold(filepath.Ext(e.Name()), ".wav") {
			continue
		}
		p, err := wavProgram(filepath.Join(corpus, e.Name()), rates)
		if err != nil {
			return nil, err
		}
		progs = append(progs, p)
	}
	if len(progs) == 0 {
		return nil, fmt.Errorf("no programs selected (corpus %q has no usable WAV files)", corpus)
	}
	return progs, nil
}

// wavProgram wraps a WAV file as a Program. Its Gen ignores nSamples in
// favor of the file's own length and returns empty channels (which runCase
// reports as a failed case) when asked for a different sample rate than the
// file's, since resampling would change what is being measured.
func wavProgram(path string, rates []int) (quality.Program, error) {
	f, err := os.Open(path)
	if err != nil {
		return quality.Program{}, err
	}
	sr, ch, err := quality.ReadWAV(f)
	_ = f.Close() // read-only handle; the parse result is what matters
	if err != nil {
		return quality.Program{}, fmt.Errorf("%s: %w", path, err)
	}
	// A corpus program runs only at its own rate, so one outside the -rates
	// set contributes no case at all: without this it would load, be skipped
	// at every rate, and go missing from the report with only a log line.
	// This also rejects the 0 a malformed fmt chunk can declare, which
	// Program.SampleRate reads as the "any rate" sentinel and which would
	// otherwise be measured under all three wrong labels.
	if !slices.Contains(rates, sr) {
		return quality.Program{}, fmt.Errorf("%s: sample rate %d is not one of -rates %v", path, sr, rates)
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return quality.Program{
		Name:       name,
		Channels:   len(ch),
		SampleRate: sr, // a corpus file is measured only at its own rate
		Gen:        func(_, _ int) [][]float64 { return ch },
	}, nil
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
