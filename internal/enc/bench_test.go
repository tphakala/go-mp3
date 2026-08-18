package enc

import "testing"

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
		b.Run(tc.name, func(b *testing.B) {
			e, err := New(tc.cfg)
			if err != nil {
				b.Fatalf("New: %v", err)
			}
			seed := uint64(1)
			samples := planarSamples(&seed, tc.cfg.Channels, 0.7)
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
		})
	}
}
