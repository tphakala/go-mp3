package enc

import (
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// BenchmarkEscalationFrame measures per-frame encode wall time on the
// escalation-heavy configurations issue #37 targets. Low-bitrate broadband
// stereo pins the masking-driven budget escalation at its call cap, where the
// per-frame outerLoop memoization (escTryBudget's memo) removes the most
// redundant work (the repeated flat-budget probe and recurring hiB offers).
// ns/op is per-frame time. Each frame can cost hundreds of ms on these
// configs, so run with a low benchtime, for example:
//
//	go test ./internal/enc -run x -bench BenchmarkEscalationFrame -benchtime 20x
//
// Broadband noise (planarSamples) keeps escalation deep every frame; two
// warmup frames prime dst's backing array so the timed loop never grows it.
func BenchmarkEscalationFrame(b *testing.B) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"stereo-128k-44100", Config{SampleRate: 44100, Channels: 2, BitrateKbps: 128}},
		{"stereo-32k-48000", Config{SampleRate: 48000, Channels: 2, BitrateKbps: 32}},
	}
	for _, tc := range cases {
		for _, rc := range rateControlModes {
			cfg := tc.cfg
			cfg.FastRateControl = rc.fast
			b.Run(tc.name+"/"+rc.name, func(b *testing.B) {
				seed := uint64(1)
				benchFrames(b, cfg, planarSamples(&seed, cfg.Channels, 0.7))
			})
		}
	}
}

// benchFrames encodes the same frame repeatedly with a fresh encoder; two
// warmup frames prime dst's backing array so the timed loop never grows it.
func benchFrames(b *testing.B, cfg Config, samples [][]float32) {
	b.Helper()
	e, err := New(cfg)
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	dst := make([]byte, 0, 4096)
	for range 2 {
		if dst, err = e.EncodeFrame(dst[:0], samples); err != nil {
			b.Fatalf("warmup EncodeFrame: %v", err)
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		if dst, err = e.EncodeFrame(dst[:0], samples); err != nil {
			b.Fatalf("EncodeFrame: %v", err)
		}
	}
}

// rateControlModes drives the frame benchmarks in both global_gain search
// modes so benchstat can read the fast path's speedup directly off one run.
var rateControlModes = []struct {
	name string
	fast bool
}{
	{"exact", false},
	{"fast", true},
}

// transientSamples builds an nch x 1152 planar float32 buffer with a loud
// LCG-noise burst confined to the first 192 samples (one attack-detector
// sub-block, blockswitch.go's attackDetect) and silence elsewhere. Fed
// back-to-back via b.Loop(), the SAME frame content retriggers the attack
// detector's energy-ratio test on every call: each call's own silent tail
// carries a near-zero energy history into the next call's loud opening
// sub-block, exactly the transition attackDetect compares against.
func transientSamples(seed *uint64, nch int, amp float32) [][]float32 {
	out := make([][]float32, nch)
	for ch := range nch {
		out[ch] = make([]float32, 1152)
		for i := range 192 {
			v := float32(testsignal.LCG(seed))*2 - 1
			out[ch][i] = v * amp
		}
	}
	return out
}

// BenchmarkTransientFrame measures per-frame encode wall time on the
// short-block configurations issue #37's Inc7 re-measurement targets: a
// high-bitrate mono config, the existing broadband stereo baseline (for
// direct comparison against BenchmarkEscalationFrame's own
// stereo-128k-44100 case), and the same 128kbps stereo config driven with
// transient content instead, isolating the added cost of block switching
// (attack detection, short MDCT, subblock_gain escalation) from ordinary
// masking-driven escalation. ns/op is per-frame time; run with a low
// benchtime as BenchmarkEscalationFrame's doc comment recommends.
func BenchmarkTransientFrame(b *testing.B) {
	cases := []struct {
		name      string
		cfg       Config
		transient bool
	}{
		{"mono-320k-44100", Config{SampleRate: 44100, Channels: 1, BitrateKbps: 320}, false},
		{"stereo-128k-broadband-44100", Config{SampleRate: 44100, Channels: 2, BitrateKbps: 128}, false},
		{"stereo-128k-transient-44100", Config{SampleRate: 44100, Channels: 2, BitrateKbps: 128}, true},
	}
	for _, tc := range cases {
		for _, rc := range rateControlModes {
			cfg := tc.cfg
			cfg.FastRateControl = rc.fast
			b.Run(tc.name+"/"+rc.name, func(b *testing.B) {
				seed := uint64(1)
				var samples [][]float32
				if tc.transient {
					samples = transientSamples(&seed, cfg.Channels, 0.8)
				} else {
					samples = planarSamples(&seed, cfg.Channels, 0.7)
				}
				benchFrames(b, cfg, samples)
			})
		}
	}
}

// tonalPartialBins are the partials of tonalSamples, in cycles per 1152-sample
// frame (about 421, 880, 1799 and 3713 Hz at 44.1 kHz), with their weights.
var (
	tonalPartialBins    = [...]int{11, 23, 47, 97}
	tonalPartialWeights = [...]float64{1, 0.5, 0.25, 0.125}
)

// tonalSamples builds an nch x 1152 planar float32 buffer holding four
// harmonically unrelated partials, each an integer number of cycles per frame,
// so replaying the frame back to back is a continuous stationary tone with no
// frame-boundary click. Channel ch is phase shifted so stereo channels differ.
// The peak is at most amp. The products feeding sums are wrapped in float64()
// so arm64 cannot fuse them into different inputs than amd64.
func tonalSamples(nch int, amp float32) [][]float32 {
	var sum float64
	for _, w := range tonalPartialWeights {
		sum += w
	}
	out := make([][]float32, nch)
	for ch := range nch {
		out[ch] = make([]float32, 1152)
		for i := range out[ch] {
			var acc float64
			for k, bin := range tonalPartialBins {
				phase := float64(2*math.Pi*float64(bin)*float64(i)/1152) + float64(0.7*float64(ch))
				acc += float64(tonalPartialWeights[k] * math.Sin(phase))
			}
			out[ch][i] = float32(float64(float64(amp)*acc) / sum)
		}
	}
	return out
}

// BenchmarkTonalFrame measures per-frame encode wall time on ordinary tonal
// content: a stationary multi-partial tone (tonalSamples), the opposite of
// BenchmarkEscalationFrame's broadband noise. Stereo 128 kbps 44.1 kHz, so it
// compares directly against that benchmark's stereo-128k-44100 cases.
//
// It exists because the other frame benchmarks are escape-heavy: the share of
// big-values pairs that take the prefix-cost kernels' escape branch (a
// magnitude above 15, prefixcost_kernel_amd64.s) is, measured on amd64
// over 20 frames after the warmup, exact/fast:
//
//	BenchmarkEscalationFrame stereo-128k-44100        73.0% / 47.9%
//	BenchmarkEscalationFrame stereo-32k-48000         99.6% / 65.1%
//	BenchmarkTransientFrame  mono-320k-44100          79.7% / 63.5%
//	BenchmarkTransientFrame  stereo-128k-broadband    73.0% / 47.9%
//	BenchmarkTransientFrame  stereo-128k-transient    16.6% / 21.1%
//	BenchmarkTonalFrame      stereo-128k-44100 (here) 16.3% / 15.1%
func BenchmarkTonalFrame(b *testing.B) {
	for _, rc := range rateControlModes {
		cfg := Config{SampleRate: 44100, Channels: 2, BitrateKbps: 128, FastRateControl: rc.fast}
		b.Run("stereo-128k-44100/"+rc.name, func(b *testing.B) {
			benchFrames(b, cfg, tonalSamples(cfg.Channels, 0.5))
		})
	}
}
