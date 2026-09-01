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

// lameVersion returns the first line of `lame --version`, or unknownVersion.
func lameVersion(ctx context.Context, lame string) string {
	if lame == "" {
		return unknownVersion
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, lame, "--version").Output()
	if err != nil {
		return unknownVersion
	}
	line, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(line)
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
	return strconv.ParseFloat(v, 64)
}
