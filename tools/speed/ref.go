package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ffmpegTimeout bounds one reference run: a 300 s mono 48 kHz input at
// 96 kbps took 1.14 s in one measured run.
const ffmpegTimeout = 10 * time.Minute

// findFFmpeg resolves the ffmpeg binary and checks that it was built with
// libmp3lame. ffmpeg can be present without it, and the failure would then
// surface as an "Unknown encoder" on every case instead of one setup error.
func findFFmpeg(ctx context.Context, flagValue string) (string, error) {
	name := flagValue
	if name == "" {
		name = "ffmpeg"
	}
	bin, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("ffmpeg not found: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-hide_banner", "-encoders").Output()
	if err != nil {
		return "", fmt.Errorf("ffmpeg -encoders: %w", err)
	}
	if !bytes.Contains(out, []byte("libmp3lame")) {
		return "", errors.New("this ffmpeg was built without libmp3lame")
	}
	return bin, nil
}

// refLoops returns the -stream_loop argument and the resulting seconds of
// audio for an input of frames sample frames: ffmpeg encodes loops+1 copies
// (TestRefLoopEncodesEveryCopy checks it where ffmpeg with libmp3lame is installed), and the number of
// copies is the smallest that reaches minSecs.
func refLoops(frames, rate, minSecs int) (loops int, audioSecs float64) {
	need := int64(minSecs) * int64(rate)
	copies := max(1, int((need+int64(frames)-1)/int64(frames)))
	return copies - 1, float64(copies) * float64(frames) / float64(rate)
}

// refArgs is the reference command line. Both -threads options pin ffmpeg to
// one thread per side; -stream_loop N plays the input N+1 times; output goes
// to out (os.DevNull in the tool).
func refArgs(wav string, loops, kbps int, out string) []string {
	return []string{
		"-nostdin", "-v", "error", "-threads", "1",
		"-stream_loop", strconv.Itoa(loops), "-i", wav,
		"-c:a", "libmp3lame", "-b:a", strconv.Itoa(kbps) + "k", "-threads", "1",
		"-f", "mp3", "-y", out,
	}
}

// runFFmpeg runs one reference encode and returns its wall time and the CPU
// time (user plus system) of the child.
func runFFmpeg(ctx context.Context, bin string, args []string) (wall, cpu time.Duration, err error) {
	ctx, cancel := context.WithTimeout(ctx, ffmpegTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// WaitDelay so a killed ffmpeg that leaks a child holding the stderr pipe
	// cannot wedge Run after a cancellation (same reason as runTool in
	// tools/quality/external.go).
	cmd.WaitDelay = 3 * time.Second
	start := time.Now()
	err = cmd.Run()
	wall = time.Since(start)
	if err != nil {
		return wall, 0, fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return wall, cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime(), nil
}

// refFiles are the two temporary inputs of one case: the cut segment and a
// 50 ms start of it that measures ffmpeg's start-up.
type refFiles struct {
	seg, start string
	frames     int
	rate       int
}

// refResult is the reference measurement of one case at one bitrate.
type refResult struct {
	audioSecs           float64
	wall, cpu           time.Duration
	startWall, startCPU time.Duration
}

func (r *refResult) wallCorr() time.Duration { return r.wall - r.startWall }
func (r *refResult) cpuCorr() time.Duration  { return r.cpu - r.startCPU }

// xRT is the reference's realtime factor on the start-up corrected wall time.
func (r *refResult) xRT() (x float64, ok bool) {
	w := r.wallCorr()
	if w <= 0 {
		return 0, false
	}
	return r.audioSecs / w.Seconds(), true
}

// ratio is the reference's realtime factor over go-mp3's, which encoded
// audioSecs seconds in wall. Above 1 means go-mp3 is slower. It is not ok
// when either time leaves no positive duration to divide by.
func (r *refResult) ratio(audioSecs float64, wall time.Duration) (ratio float64, ok bool) {
	ref, ok := r.xRT()
	if !ok || wall <= 0 {
		return 0, false
	}
	return ref / (audioSecs / wall.Seconds()), true
}

// median returns the upper median, d[len/2] of the sorted values.
func median(d []time.Duration) time.Duration {
	s := slices.Clone(d)
	slices.Sort(s)
	return s[len(s)/2]
}

// timeRef measures the reference for one case: runs encodes of the start-up
// input, then runs of the looped segment, each median taken separately for
// wall and CPU.
func timeRef(ctx context.Context, bin string, f *refFiles, kbps, runs, minSecs int) (refResult, error) {
	loops, audio := refLoops(f.frames, f.rate, minSecs)
	measure := func(wav string, loops int) (wall, cpu time.Duration, err error) {
		var ws, cs []time.Duration
		for range runs {
			w, c, err := runFFmpeg(ctx, bin, refArgs(wav, loops, kbps, os.DevNull))
			if err != nil {
				return 0, 0, err
			}
			ws, cs = append(ws, w), append(cs, c)
		}
		return median(ws), median(cs), nil
	}
	r := refResult{audioSecs: audio}
	var err error
	if r.startWall, r.startCPU, err = measure(f.start, 0); err != nil {
		return r, err
	}
	r.wall, r.cpu, err = measure(f.seg, loops)
	return r, err
}
