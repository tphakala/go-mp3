package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"slices"
	"text/tabwriter"
	"time"

	"github.com/tphakala/go-mp3/internal/quality"
)

// caseResult is one (file, bitrate) measurement.
type caseResult struct {
	file, inSHA string
	channels    int
	rate, kbps  int
	audioSecs   float64
	wall, min   time.Duration
	sha         string
	ref         *refResult
}

func (c *caseResult) xRT() float64 { return c.audioSecs / c.wall.Seconds() }

// timeEncode encodes pcm runs times into one reused buffer and returns
// the median and minimum encode time with the stream hash. Runs that produce
// different streams fail the case: the encoder is deterministic, so a
// difference means the measurement is not of one thing. A cancelled ctx stops
// it before the next run.
func timeEncode(ctx context.Context, pcm [][]float32, rate, kbps, runs int) (med, fastest time.Duration, sha string, err error) {
	var buf []byte // sized by the first run, outside its timed section, then reused
	times := make([]time.Duration, 0, runs)
	for i := range runs {
		if err := ctx.Err(); err != nil {
			return 0, 0, "", err
		}
		stream, d, err := encodeStream(pcm, rate, kbps, buf[:0])
		if err != nil {
			return 0, 0, "", err
		}
		sum := hashHex(stream)
		if i > 0 && sum != sha {
			return 0, 0, "", fmt.Errorf("run %d produced a different stream (%s, run 1 %s)", i+1, sum, sha)
		}
		sha = sum
		buf = stream
		times = append(times, d)
	}
	return median(times), slices.Min(times), sha, nil
}

// writeRefFiles writes the cut segment and its 50 ms start into dir.
func writeRefFiles(dir string, rate int, seg [][]float64) (*refFiles, error) {
	write := func(name string, ch [][]float64) (string, error) {
		path := filepath.Join(dir, name)
		f, err := os.Create(path)
		if err != nil {
			return "", err
		}
		if err := quality.WriteWAV16(f, rate, ch); err != nil {
			_ = f.Close()
			return "", err
		}
		return path, f.Close()
	}
	n := len(seg[0])
	head := make([][]float64, len(seg))
	for c := range seg {
		head[c] = seg[c][:min(n, rate/20)]
	}
	rf := &refFiles{frames: n, rate: rate}
	var err error
	if rf.seg, err = write("seg.wav", seg); err != nil {
		return nil, err
	}
	rf.start, err = write("start.wav", head)
	return rf, err
}

// timingRun is the state shared by the cases of one timing run.
type timingRun struct {
	o       *options
	stderr  io.Writer
	ffmpeg  string
	tmp     string
	results []caseResult
	failed  int
}

// timeFile times every bitrate of one input.
func (tr *timingRun) timeFile(ctx context.Context, path string) {
	w, err := loadWAV(path)
	if err != nil {
		logf(tr.stderr, "%v\n", err)
		tr.failed++
		return
	}
	pcm, seg := w.cut(tr.o.secs)
	var rf *refFiles
	if tr.o.ref {
		if rf, err = writeRefFiles(tr.tmp, w.rate, seg); err != nil {
			logf(tr.stderr, "%s: reference input: %v\n", w.name, err)
			tr.failed++
			return
		}
	}
	for _, kbps := range tr.o.bitrates {
		if ctx.Err() != nil {
			return
		}
		logf(tr.stderr, "%s at %d kbps\n", w.name, kbps)
		med, fastest, sha, err := timeEncode(ctx, pcm, w.rate, kbps, tr.o.runs)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			logf(tr.stderr, "%s at %d kbps: %v\n", w.name, kbps, err)
			tr.failed++
			continue
		}
		r := caseResult{
			file: w.name, inSHA: w.inSHA, channels: len(pcm), rate: w.rate, kbps: kbps,
			audioSecs: float64(len(pcm[0])) / float64(w.rate), wall: med, min: fastest, sha: sha,
		}
		if rf != nil {
			ref, err := timeRef(ctx, tr.ffmpeg, rf, kbps, tr.o.runs, tr.o.refSecs)
			if err != nil {
				logf(tr.stderr, "%s at %d kbps: reference: %v\n", w.name, kbps, err)
				tr.failed++
				continue
			}
			r.ref = &ref
		}
		tr.results = append(tr.results, r)
	}
}

// outputOverInput reports an output path that names one of the inputs, also
// through a link, so a mistyped -json or -cpuprofile cannot overwrite a
// recording. An output that does not exist yet cannot be an input.
func outputOverInput(o *options) error {
	for _, out := range []string{o.jsonOut, o.cpuProfile} {
		if out == "" {
			continue
		}
		outInfo, err := os.Stat(out)
		if err != nil {
			continue
		}
		for _, in := range o.inputs {
			if inInfo, err := os.Stat(in); err == nil && os.SameFile(outInfo, inInfo) {
				return fmt.Errorf("output %s is the input %s", filepath.Base(out), filepath.Base(in))
			}
		}
	}
	return nil
}

func runTiming(ctx context.Context, o *options, stdout, stderr io.Writer) int {
	if err := outputOverInput(o); err != nil {
		logf(stderr, "speed: %v\n", err)
		return exitSetup
	}
	tr := &timingRun{o: o, stderr: stderr}
	if o.ref {
		bin, err := findFFmpeg(ctx, o.ffmpeg)
		if err != nil {
			logf(stderr, "speed: -ref: %v\n", err)
			return exitSetup
		}
		tr.ffmpeg = bin
		if tr.tmp, err = os.MkdirTemp("", "speed-"); err != nil {
			logf(stderr, "speed: %v\n", err)
			return exitSetup
		}
		defer func() { _ = os.RemoveAll(tr.tmp) }()
	}
	if o.cpuProfile != "" {
		f, err := os.Create(o.cpuProfile)
		if err != nil {
			logf(stderr, "speed: %v\n", pathErr(err))
			return exitSetup
		}
		defer func() { _ = f.Close() }()
		if err := pprof.StartCPUProfile(f); err != nil {
			logf(stderr, "speed: %v\n", err)
			return exitSetup
		}
		defer pprof.StopCPUProfile()
	}
	for _, path := range o.inputs {
		if ctx.Err() != nil {
			return exitInterrupted
		}
		tr.timeFile(ctx, path)
	}
	if ctx.Err() != nil {
		return exitInterrupted
	}
	printTable(stdout, tr.results, o.ref)
	if o.jsonOut != "" {
		if err := writeJSON(o, tr); err != nil {
			logf(stderr, "speed: %v\n", pathErr(err))
			return exitFailed
		}
	}
	if tr.failed > 0 {
		logf(stderr, "speed: %d cases failed\n", tr.failed)
		return exitFailed
	}
	return exitOK
}

// fmtRatio prints a ratio or n/a.
func fmtRatio(v float64, ok bool, format string) string {
	if !ok {
		return "n/a"
	}
	return fmt.Sprintf(format, v)
}

func printTable(w io.Writer, results []caseResult, withRef bool) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	head := "file\tch\thz\tkbps\taudio_s\twall_s\twall_min_s\tx_rt"
	if withRef {
		head += "\tref_audio_s\tref_wall_s\tref_wall_corr_s\tref_cpu_s\tref_cpu_corr_s\tref_x_rt\tratio"
	}
	logf(tw, "%s\tsha256\n", head)
	for i := range results {
		c := &results[i]
		logf(tw, "%s\t%d\t%d\t%d\t%.3f\t%.3f\t%.3f\t%.1f", c.file, c.channels, c.rate, c.kbps,
			c.audioSecs, c.wall.Seconds(), c.min.Seconds(), c.xRT())
		if r := c.ref; r != nil {
			x, xok := r.xRT()
			ratio, rok := r.ratio(c.audioSecs, c.wall)
			logf(tw, "\t%.3f\t%.3f\t%.3f\t%.3f\t%.3f\t%s\t%s", r.audioSecs, r.wall.Seconds(),
				r.wallCorr().Seconds(), r.cpu.Seconds(), r.cpuCorr().Seconds(),
				fmtRatio(x, xok, "%.1f"), fmtRatio(ratio, rok, "%.2f"))
		}
		logf(tw, "\t%s\n", c.sha)
	}
	_ = tw.Flush()
	if withRef {
		logf(w, "ratio: libmp3lame realtime factor over go-mp3 realtime factor, wall clock, reference start-up subtracted (ref_wall_corr_s); above 1 means go-mp3 is slower\n")
	}
}

type jsonRef struct {
	AudioS        float64  `json:"audio_s"`
	WallS         float64  `json:"wall_s"`
	CPUS          float64  `json:"cpu_s"`
	StartupWallS  float64  `json:"startup_wall_s"`
	StartupCPUS   float64  `json:"startup_cpu_s"`
	WallCorrected float64  `json:"wall_corrected_s"`
	CPUCorrected  float64  `json:"cpu_corrected_s"`
	RealtimeX     *float64 `json:"realtime_x,omitempty"`
	Ratio         *float64 `json:"ratio,omitempty"`
}

type jsonCase struct {
	File      string   `json:"file"`
	InputSHA  string   `json:"input_sha256"`
	Channels  int      `json:"channels"`
	Rate      int      `json:"sample_rate"`
	Kbps      int      `json:"kbps"`
	AudioS    float64  `json:"audio_s"`
	WallS     float64  `json:"wall_s"`
	WallMinS  float64  `json:"wall_min_s"`
	RealtimeX float64  `json:"realtime_x"`
	SHA       string   `json:"sha256"`
	Ref       *jsonRef `json:"ref,omitempty"`
}

type jsonReport struct {
	SchemaVersion int        `json:"schema_version"`
	GoVersion     string     `json:"go_version"`
	GOOS          string     `json:"goos"`
	GOARCH        string     `json:"goarch"`
	Runs          int        `json:"runs"`
	Secs          int        `json:"secs"`
	RefMinSecs    int        `json:"ref_min_secs,omitempty"`
	Failed        int        `json:"failed"`
	Cases         []jsonCase `json:"cases"`
}

func ptr(v float64, ok bool) *float64 {
	if !ok {
		return nil
	}
	return &v
}

func writeJSON(o *options, tr *timingRun) error {
	rep := jsonReport{
		SchemaVersion: 1, GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Runs: o.runs, Secs: o.secs, Failed: tr.failed, Cases: make([]jsonCase, 0, len(tr.results)),
	}
	if o.ref {
		rep.RefMinSecs = o.refSecs
	}
	for i := range tr.results {
		c := &tr.results[i]
		jc := jsonCase{
			File: c.file, InputSHA: c.inSHA, Channels: c.channels, Rate: c.rate, Kbps: c.kbps,
			AudioS: c.audioSecs, WallS: c.wall.Seconds(), WallMinS: c.min.Seconds(), RealtimeX: c.xRT(), SHA: c.sha,
		}
		if r := c.ref; r != nil {
			x, xok := r.xRT()
			ratio, rok := r.ratio(c.audioSecs, c.wall)
			jc.Ref = &jsonRef{
				AudioS: r.audioSecs, WallS: r.wall.Seconds(), CPUS: r.cpu.Seconds(),
				StartupWallS: r.startWall.Seconds(), StartupCPUS: r.startCPU.Seconds(),
				WallCorrected: r.wallCorr().Seconds(), CPUCorrected: r.cpuCorr().Seconds(),
				RealtimeX: ptr(x, xok), Ratio: ptr(ratio, rok),
			}
		}
		rep.Cases = append(rep.Cases, jc)
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(o.jsonOut, append(b, '\n'), 0o600)
}
