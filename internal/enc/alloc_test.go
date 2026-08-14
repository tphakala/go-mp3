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
// measured 50 runs reuse the same pre-grown dst and sample buffers.
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

	avg := testing.AllocsPerRun(50, func() {
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
