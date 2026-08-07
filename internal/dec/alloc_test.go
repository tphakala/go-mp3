package dec

import "testing"

// TestDecodeSteadyStateAllocs pins the decoder to zero heap allocations per
// DecodeFrame in steady state. Upstream keeps its scratch (mp3dec_scratch_t) on
// the caller's stack and its persistent state (mp3dec_t) caller-owned, so a
// faithful port must not allocate per frame either: Decoder holds the scratch
// and all persistent buffers as fields (see decoder.go), and DecodeFrame writes
// into caller-provided pcm/info, so the only bitstream readers it builds
// (bits.Reader values over slices of mp3 and the reused scratch.maindata) stay
// on the stack.
//
// A warmup call primes the fast-path header cache so the measured runs take the
// steady-state path (fast-path hit, no reset/resync). Any nonzero result means
// the hot path grew a per-call allocation and must be moved back into a reused
// Decoder field; go build -gcflags=-m localizes the escape.
func TestDecodeSteadyStateAllocs(t *testing.T) {
	d := NewDecoder()
	data := readFile(t, "../../testdata/fixtures/sine44s_128.mp3")
	pcm := make([]float32, maxSamplesPerFrame)
	var info FrameInfo

	d.DecodeFrame(data, pcm, &info) // warmup: prime the fast-path header cache

	avg := testing.AllocsPerRun(50, func() {
		d.DecodeFrame(data, pcm, &info)
	})
	if avg != 0 {
		t.Fatalf("steady-state allocs = %v, want 0", avg)
	}
}
