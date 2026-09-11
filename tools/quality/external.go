package main

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// tools holds the resolved paths of the black-box binaries; "" means absent.
type tools struct {
	lame, ffmpeg, visqol, peaq string
}

// detectTools resolves each binary: an explicit non-empty flag is looked up
// as given (a path or a name on PATH), otherwise the default name is looked
// up on PATH. Absence is not an error here; callers decide.
func detectTools(lameFlag, visqolFlag, peaqFlag string) tools {
	look := func(flag, def string) string {
		name := def
		if flag != "" {
			name = flag
		}
		p, err := exec.LookPath(name)
		if err != nil {
			return ""
		}
		return p
	}
	return tools{
		lame:   look(lameFlag, "lame"),
		ffmpeg: look("", "ffmpeg"),
		visqol: look(visqolFlag, "visqol"),
		peaq:   look(peaqFlag, "peaq-odg"),
	}
}

// firstVersionLine returns the first line of `bin flag`, or unknownVersion when
// bin is empty, cannot be run, or prints nothing. Shared by lameVersion and
// ffmpegVersion, which differ only in the version flag.
func firstVersionLine(ctx context.Context, bin, flag string) string {
	if bin == "" {
		return unknownVersion
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, flag).Output()
	if err != nil {
		return unknownVersion
	}
	line, _, _ := strings.Cut(string(out), "\n")
	if line = strings.TrimSpace(line); line == "" {
		return unknownVersion // a build that prints its banner elsewhere
	}
	return line
}

// lameVersion returns the first line of `lame --version`, or unknownVersion.
func lameVersion(ctx context.Context, lame string) string {
	return firstVersionLine(ctx, lame, "--version")
}

// refKind names how the LAME-family reference MP3 is produced. libmp3lame is
// the LAME library either way; the difference is only which binary drives it.
type refKind int

const (
	refNone       refKind = iota // no reference producer available
	refLameBinary                // the standalone `lame` binary
	refFFmpegLAME                // ffmpeg's libmp3lame encoder
)

// refEncoder is the resolved reference producer: which kind, and the path of
// the binary that runs it.
type refEncoder struct {
	kind refKind
	bin  string
}

// chooseReference picks the reference producer from the resolved binaries: the
// standalone `lame` binary when present (the canonical CLI, and what the
// committed comparisons were taken against), otherwise ffmpeg's libmp3lame, so
// a run is not tied to the `lame` binary being installed. refNone when neither
// is available; the caller turns that into a setup error.
func chooseReference(lame, ffmpeg string) refEncoder {
	switch {
	case lame != "":
		return refEncoder{kind: refLameBinary, bin: lame}
	case ffmpeg != "":
		return refEncoder{kind: refFFmpegLAME, bin: ffmpeg}
	default:
		return refEncoder{kind: refNone}
	}
}

// ffmpegVersion returns the first line of `ffmpeg -version`, or unknownVersion.
func ffmpegVersion(ctx context.Context, ffmpeg string) string {
	return firstVersionLine(ctx, ffmpeg, "-version")
}

// referenceVersion describes the resolved reference producer for the report's
// provenance line: the lame binary's version, or ffmpeg's when its libmp3lame
// stands in, so a reader can tell which encoder produced the reference column.
func referenceVersion(ctx context.Context, r refEncoder) string {
	switch r.kind {
	case refLameBinary:
		return lameVersion(ctx, r.bin)
	case refFFmpegLAME:
		return "ffmpeg libmp3lame: " + ffmpegVersion(ctx, r.bin)
	default:
		return unknownVersion
	}
}

// Result-line patterns of the external tools. Both can print nan (PEAQ
// prints "-nan" when a basic-model MOV has no active frames, for instance a
// reference with no content above 8.1 kHz), so the capture admits it.
var (
	mosRe = regexp.MustCompile(`(?i)MOS-LQO:\s*([-+]?(?:nan|inf|[0-9.]+))`)
	odgRe = regexp.MustCompile(`(?i)Objective Difference Grade:\s*([-+]?(?:nan|inf|[0-9.]+))`)
)

// runTool runs an external binary inside dir and returns its combined
// output, bounded by cmdTimeout.
func runTool(ctx context.Context, dir, bin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	// WaitDelay so a tool that is killed on cancellation but leaks a child
	// still holding the output pipe cannot wedge cmd.Run forever: after the
	// delay Go closes the pipe and returns. Without it a Ctrl-C during a
	// wedged external tool could not free the worker.
	cmd.WaitDelay = 3 * time.Second
	err := cmd.Run()
	return out.String(), err
}

// to48k returns the name of a 48 kHz copy of wav inside dir, resampling
// with ffmpeg when needed. It fails when the rate is not 48 kHz and ffmpeg
// is absent.
func to48k(ctx context.Context, tl tools, dir, wav string, sampleRate int) (string, error) {
	if sampleRate == 48000 {
		return wav, nil
	}
	if tl.ffmpeg == "" {
		return "", fmt.Errorf("%s: %d Hz needs ffmpeg to resample to 48 kHz", wav, sampleRate)
	}
	out := strings.TrimSuffix(wav, ".wav") + "-48k.wav"
	if txt, err := runTool(ctx, dir, tl.ffmpeg, "-v", "error", "-y", "-i", wav, "-ar", "48000", out); err != nil {
		return "", fmt.Errorf("ffmpeg resample: %w: %s", err, strings.TrimSpace(txt))
	}
	return out, nil
}

// perceptualTool describes one external 48 kHz reference-versus-degraded
// scorer: how to build its argument list and how to read its result line.
type perceptualTool struct {
	name string
	bin  string
	args func(ref, deg string) []string
	re   *regexp.Regexp
}

// runPerceptual resamples both WAVs to 48 kHz when needed, runs the tool
// inside dir with relative file names (the visqol wrapper mounts the working
// directory into its container), and parses the last result line.
func runPerceptual(ctx context.Context, tl tools, dir, refWav, degWav string, sampleRate int, pt perceptualTool) (float64, error) {
	ref48, err := to48k(ctx, tl, dir, refWav, sampleRate)
	if err != nil {
		return 0, err
	}
	deg48, err := to48k(ctx, tl, dir, degWav, sampleRate)
	if err != nil {
		return 0, err
	}
	out, err := runTool(ctx, dir, pt.bin, pt.args(ref48, deg48)...)
	if err != nil {
		return 0, fmt.Errorf("%s: %w: %s", pt.name, err, strings.TrimSpace(out))
	}
	return parseLast(pt.re, out, pt.name)
}

// runVisqol scores degWav against refWav with ViSQOL in audio mode and
// returns MOS-LQO.
func runVisqol(ctx context.Context, tl tools, dir, refWav, degWav string, sampleRate int) (float64, error) {
	return runPerceptual(ctx, tl, dir, refWav, degWav, sampleRate, perceptualTool{
		name: "visqol",
		bin:  tl.visqol,
		args: func(ref, deg string) []string { return []string{"--reference_file", ref, "--degraded_file", deg} },
		re:   mosRe,
	})
}

// runPEAQ scores degWav against refWav with the PEAQ basic model and
// returns the Objective Difference Grade.
func runPEAQ(ctx context.Context, tl tools, dir, refWav, degWav string, sampleRate int) (float64, error) {
	return runPerceptual(ctx, tl, dir, refWav, degWav, sampleRate, perceptualTool{
		name: "peaq",
		bin:  tl.peaq,
		args: func(ref, deg string) []string { return []string{"--basic", ref, deg} },
		re:   odgRe,
	})
}

// parseLast returns the last numeric capture of re in out. A nan or inf
// capture parses to NaN (the tool ran but had nothing to say), not an error.
func parseLast(re *regexp.Regexp, out, what string) (float64, error) {
	m := re.FindAllStringSubmatch(out, -1)
	if len(m) == 0 {
		return 0, fmt.Errorf("no %s result line in output: %s", what, strings.TrimSpace(out))
	}
	v := m[len(m)-1][1]
	bare := strings.ToLower(strings.TrimLeft(v, "+-"))
	if bare == "nan" || bare == "inf" {
		return math.NaN(), nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: unparsable result %q: %w", what, v, err)
	}
	return f, nil
}
