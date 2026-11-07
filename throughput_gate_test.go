package mp3_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	mp3 "github.com/tphakala/go-mp3"
	"github.com/tphakala/go-mp3/internal/testsignal"
)

// The throughput gate measures encode speed as a realtime factor (seconds of
// audio per second of wall time) on a few deterministic programs and fails
// when one falls under its floor. An encoder slower than realtime cannot keep
// up with live capture, and nothing else in CI measures speed: the test suite
// uses short clips under a long timeout and the quality gate measures quality
// only. It runs in its own CI step (MP3_THROUGHPUT_GATE=1, `go test . -run
// TestThroughputGate -p 1`) so no other package's tests compete for CPU.

const (
	throughputAudioSeconds = 3
	// throughputAttempts is the number of tries per program; a program passes
	// when any attempt meets its floor, which damps shared-runner noise.
	throughputAttempts = 2
)

// Realtime-factor floors, chosen from measurements on real hardware at
// main (95f79ed), default RateControlExact, 3 s programs, run one at a
// time (nuc7: amd64; rpi5: Cortex-A76 arm64):
//
//	program      nuc7    rpi5
//	tonal        21.9x   12.0x
//	tone+noise    0.38x   0.16x
//	transient     1.21x   0.54x
//	noise 32k     0.33x   0.13x
//
// A healthy encoder should keep at least 2x margin over a floor on a noisy
// 2-vCPU runner, and the known-slow programs should miss their floor by at
// least 2x:
//   - floorTonal: 3x leaves 4x margin to rpi5 (12x) and 7x to nuc7.
//   - floorDense: 1x is the live-capture bound (slower than realtime cannot
//     keep up). Tone+noise and noise miss it by 2.6x or more on nuc7 and 6x
//     or more on rpi5.
//   - floorTransient: 2.5x. Transient is the lightest dense program (1.21x on
//     nuc7), so a 1x floor would pass there. 2.5x misses by 2.1x on nuc7 and
//     4.6x on rpi5. The healthy-encoder margin for it is NOT MEASURED, since
//     no healthy build exists to measure; tonal is the only program known to
//     run well above its floor.
const (
	floorTonal     = 3.0
	floorDense     = 1.0
	floorTransient = 2.5
)

type throughputProgram struct {
	name string
	cfg  mp3.EncoderConfig
	// gen returns planar samples for n frames per channel.
	gen   func(sampleRate, nSamples int) [][]float32
	floor float64
}

func throughputPrograms() []throughputProgram {
	stereo128k := mp3.EncoderConfig{SampleRate: 44100, Channels: 2, Bitrate: 128000}
	return []throughputProgram{
		{
			name:  "tonal_stereo_128k_44100",
			cfg:   stereo128k,
			gen:   genTonal,
			floor: floorTonal,
		},
		{
			name:  "tone_noise_stereo_128k_44100",
			cfg:   stereo128k,
			gen:   genToneNoise,
			floor: floorDense,
		},
		{
			name:  "transient_stereo_128k_44100",
			cfg:   stereo128k,
			gen:   genTransient,
			floor: floorTransient,
		},
		{
			name:  "noise_stereo_32k_48000",
			cfg:   mp3.EncoderConfig{SampleRate: 48000, Channels: 2, Bitrate: 32000},
			gen:   genNoise,
			floor: floorDense,
		},
	}
}

func f64to32(x []float64) []float32 {
	out := make([]float32, len(x))
	for i, v := range x {
		out[i] = float32(v)
	}
	return out
}

func genTonal(sr, n int) [][]float32 {
	return [][]float32{
		f64to32(testsignal.MultiTone(sr, n, 0, 0.5)),
		f64to32(testsignal.MultiTone(sr, n, 0.7, 0.5)),
	}
}

func genToneNoise(sr, n int) [][]float32 {
	out := genTonal(sr, n)
	seed := uint64(0x70E01E)
	for _, ch := range out {
		for i := range ch {
			ch[i] += float32(testsignal.LCGSigned(&seed) * 0.02)
		}
	}
	return out
}

func genTransient(sr, n int) [][]float32 {
	l := testsignal.ToneClick(sr, n, testsignal.ClickPeriodFrames, testsignal.ClickBurstFrames)
	r := testsignal.ToneClick(sr, n, testsignal.ClickPeriodFrames, testsignal.ClickBurstFrames)
	return [][]float32{l, r}
}

func genNoise(_, n int) [][]float32 {
	l, r := testsignal.DecorrelatedNoise(n)
	return [][]float32{l, r}
}

// encodeWithin encodes planar samples frame by frame and aborts as soon as
// budget is exceeded. It returns the frames finished within budget, the total
// and the elapsed time; done equals total only when every frame finished
// within budget.
func encodeWithin(cfg mp3.EncoderConfig, samples [][]float32, budget time.Duration) (done, total int, elapsed time.Duration, err error) {
	enc, err := mp3.NewEncoder(cfg)
	if err != nil {
		return 0, 0, 0, err
	}
	n := len(samples[0])
	total = (n + mp3.FrameSize - 1) / mp3.FrameSize
	var dst []byte
	chunk := make([][]float32, len(samples))
	start := time.Now()
	for f := range total {
		lo, hi := f*mp3.FrameSize, min((f+1)*mp3.FrameSize, n)
		for c := range samples {
			chunk[c] = samples[c][lo:hi]
		}
		if dst, err = enc.EncodeFrame(dst[:0], chunk); err != nil {
			return done, total, time.Since(start), err
		}
		if elapsed = time.Since(start); elapsed > budget {
			return done, total, elapsed, nil
		}
		done++
	}
	return done, total, time.Since(start), nil
}

func TestThroughputGate(t *testing.T) {
	if os.Getenv("MP3_THROUGHPUT_GATE") == "" {
		t.Skip("set MP3_THROUGHPUT_GATE=1 to run the encoder throughput gate")
	}
	for _, p := range throughputPrograms() {
		t.Run(p.name, func(t *testing.T) {
			n := throughputAudioSeconds * p.cfg.SampleRate
			samples := p.gen(p.cfg.SampleRate, n)
			// Budget is the wall time a floor-speed encoder would need, so a
			// failing program costs about that long instead of its full run.
			budget := time.Duration(float64(throughputAudioSeconds) / p.floor * float64(time.Second))
			var last string
			for attempt := 1; attempt <= throughputAttempts; attempt++ {
				done, total, elapsed, err := encodeWithin(p.cfg, samples, budget)
				if err != nil {
					t.Fatalf("encode: %v", err)
				}
				if done == total {
					t.Logf("%s: attempt %d: %d frames in %v, realtime factor %.2fx (floor %.2fx)",
						p.name, attempt, total, elapsed.Round(time.Millisecond),
						throughputAudioSeconds/elapsed.Seconds(), p.floor)
					return
				}
				last = fmt.Sprintf("%s: attempt %d aborted: %d of %d frames encoded in %v, over the %v budget for the %.2fx realtime floor",
					p.name, attempt, done, total, elapsed.Round(time.Millisecond), budget.Round(time.Millisecond), p.floor)
				t.Log(last)
			}
			t.Errorf("encoder below the realtime floor on every attempt: %s", last)
		})
	}
}

// throughputGateActivationRe matches the ci.yml env line that turns the
// throughput gate on. Anchored to a whole YAML mapping entry, so the comment
// that spells the variable with an equals sign does not count.
var throughputGateActivationRe = regexp.MustCompile(`(?m)^\s*MP3_THROUGHPUT_GATE:\s*['"]?1['"]?\s*$`)

// TestThroughputGateActivatedInCI fails when ci.yml stops setting
// MP3_THROUGHPUT_GATE=1, which would leave TestThroughputGate skipping in CI.
func TestThroughputGateActivatedInCI(t *testing.T) {
	path := filepath.Join(".github", "workflows", "ci.yml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !throughputGateActivationRe.Match(b) {
		t.Fatalf("%s no longer sets MP3_THROUGHPUT_GATE=1 on a job step: the encoder throughput gate "+
			"(TestThroughputGate) would silently skip in CI. Restore the env, or update "+
			"throughputGateActivationRe if the activation intentionally moved.", path)
	}
}

// TestEncodeWithinCountsOverBudgetFrameAsAbort pins that a frame finishing
// after the budget is not counted as done, so a run whose final frame crosses
// the budget is reported as aborted rather than complete.
func TestEncodeWithinCountsOverBudgetFrameAsAbort(t *testing.T) {
	cfg := mp3.EncoderConfig{SampleRate: 44100, Channels: 2, Bitrate: 128000}
	samples := genTonal(cfg.SampleRate, mp3.FrameSize) // one frame, so the first frame is the last
	done, total, _, err := encodeWithin(cfg, samples, time.Nanosecond)
	if err != nil {
		t.Fatalf("encodeWithin: %v", err)
	}
	if total != 1 || done != 0 {
		t.Fatalf("done/total = %d/%d for a single frame over a 1ns budget, want 0/1", done, total)
	}
}
