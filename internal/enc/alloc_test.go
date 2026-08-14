package enc

import "testing"

// TestEncodeSteadyStateAllocs pins the Encoder to zero heap allocations per
// EncodeFrame in steady state, mirroring internal/dec/alloc_test.go's
// TestDecodeSteadyStateAllocs pattern: all per-frame state (filterbank
// shift registers, MDCT overlap history, padding accumulator, the
// granuleCoding pair) lives in Encoder fields, so a warm call should
// allocate nothing beyond growing the caller-provided dst on the first
// call. Two warmup frames prime dst's backing array (so later calls append
// within capacity, not grow it) and any first-call-only lazy state; the
// measured 5 runs reuse the same pre-grown dst and sample buffers. Alloc
// count is deterministic per call (not a statistical measurement), so a
// small run count is enough to catch a regression while keeping this test
// affordable: each run pays the masking-driven escalation's outer-loop cost
// at 128k stereo broadband content.
//
// A nonzero result means the hot path grew a per-call allocation and must
// be moved back into a reused Encoder field; go build -gcflags=-m
// localizes the escape (see the addendum's note to watch &w escaping in
// renderFrameInto/renderMainData, and by extension any local composite
// literal passed by pointer into the pipeline).
func TestEncodeSteadyStateAllocs(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, BitrateKbps: 128}
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	seed := uint64(1)
	samples := planarSamples(&seed, 2, 0.7)

	dst := make([]byte, 0, 4096)
	for range 2 {
		dst, err = e.EncodeFrame(dst[:0], samples)
		if err != nil {
			t.Fatalf("warmup EncodeFrame: %v", err)
		}
	}

	avg := testing.AllocsPerRun(5, func() {
		var encErr error
		dst, encErr = e.EncodeFrame(dst[:0], samples)
		if encErr != nil {
			t.Fatalf("EncodeFrame: %v", encErr)
		}
	})
	if avg != 0 {
		t.Fatalf("steady-state allocs = %v, want 0", avg)
	}
}

// TestEncodeMonoHighRateAllocs guards zero-alloc for a config where the
// frame's main-data area exceeds the coded Huffman ceiling, so renderMainData
// pads up to a spendMin larger than that ceiling. Mono 320kbps/32kHz has area
// 1419 > huffCap 1024; a low-entropy tone banks the reservoir to its cap so
// the anti-overflow floor (hence spendMin) reaches the full area. Before
// mainScratch was sized by max(huffCap, area) this grew the buffer on every
// frame, a per-call heap allocation the 128kbps stereo case above (area 381 <
// huffCap 2047) never exercised.
func TestEncodeMonoHighRateAllocs(t *testing.T) {
	e, err := New(Config{SampleRate: 32000, Channels: 1, BitrateKbps: 320})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// A low-amplitude fs/4 tone: low perceptual entropy, so the reservoir
	// banks toward its cap and the anti-overflow floor drives spendMin up to
	// the frame's main-data area.
	samples := [][]float32{make([]float32, 1152)}
	for i := range samples[0] {
		samples[0][i] = []float32{0.02, 0, -0.02, 0}[i&3]
	}
	dst := make([]byte, 0, 4096)
	for range 8 { // saturate reservoir occupancy so lo == area
		if dst, err = e.EncodeFrame(dst[:0], samples); err != nil {
			t.Fatalf("warmup EncodeFrame: %v", err)
		}
	}
	avg := testing.AllocsPerRun(5, func() {
		var encErr error
		dst, encErr = e.EncodeFrame(dst[:0], samples)
		if encErr != nil {
			t.Fatalf("EncodeFrame: %v", encErr)
		}
	})
	if avg != 0 {
		t.Fatalf("steady-state allocs = %v, want 0", avg)
	}
}
