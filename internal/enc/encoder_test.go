package enc

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// planarSamples builds an nch x 1152 planar float32 buffer of LCG noise at
// the given amplitude (kept within [-1,1] so it needs no clamp to exercise
// the ordinary path).
func planarSamples(seed *uint64, nch int, amp float32) [][]float32 {
	out := make([][]float32, nch)
	for ch := range nch {
		out[ch] = make([]float32, 1152)
		for i := range out[ch] {
			v := float32(testsignal.LCG(seed))*2 - 1
			out[ch][i] = v * amp
		}
	}
	return out
}

// mustEncoder returns New(cfg) or fails the test: a small helper so the
// reservoir tests below (which each need a fresh Encoder) do not repeat the
// same three-line error check.
func mustEncoder(t *testing.T, cfg Config) *Encoder {
	t.Helper()
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New(%+v): %v", cfg, err)
	}
	return e
}

// quietThenBurstProgram builds n frames of mono planar samples that
// alternate 8 frames of silence with 8 frames of stored-LCG noise at
// amplitude 0.7 (golden-input discipline: integer/stored construction
// only, no libm cos/sin, no non-dyadic float product feeding a +/-). The
// silence/burst alternation is what exercises the reservoir's swing: 8
// quiet frames bank main-data bytes, the following 8 loud frames draw them
// back down.
func quietThenBurstProgram(t *testing.T, n int) [][][]float32 {
	t.Helper()
	seed := uint64(0xA5A5)
	out := make([][][]float32, n)
	for f := range n {
		if f%16 < 8 {
			out[f] = [][]float32{make([]float32, 1152)} // silence
		} else {
			out[f] = planarSamples(&seed, 1, 0.7)
		}
	}
	return out
}

// TestEncoderConfigValidate requires New/Reset to reject the illegal grid
// points called out in the brief (sample rate 22050, channels 3, bitrate
// 129) and accept the full legal grid (3 rates x 2 channel counts x 14
// bitrates).
func TestEncoderConfigValidate(t *testing.T) {
	t.Run("rejects", func(t *testing.T) {
		cases := []Config{
			{SampleRate: 22050, Channels: 2, BitrateKbps: 128},
			{SampleRate: 44100, Channels: 3, BitrateKbps: 128},
			{SampleRate: 44100, Channels: 2, BitrateKbps: 129},
		}
		for _, c := range cases {
			if _, err := New(c); err == nil {
				t.Fatalf("New(%+v): want error, got nil", c)
			}
		}
	})

	t.Run("accepts full legal grid", func(t *testing.T) {
		rates := []int{44100, 48000, 32000}
		kbps := []int{32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
		for _, sr := range rates {
			for _, nch := range []int{1, 2} {
				for _, kb := range kbps {
					cfg := Config{SampleRate: sr, Channels: nch, BitrateKbps: kb}
					e, err := New(cfg)
					if err != nil {
						t.Fatalf("New(%+v): unexpected error %v", cfg, err)
					}
					if err := e.Reset(cfg); err != nil {
						t.Fatalf("Reset(%+v): unexpected error %v", cfg, err)
					}
				}
			}
		}
	})
}

// bitrateIndex128kbps is the ISO/IEC 11172-3 Table B.1 header bitrate_index
// for 128 kbps, spelled as an independent literal (not bitrateIndexForKbps[128])
// so TestEncoderFrameSizes cross-checks the production frame-length math
// against a hand-pinned index instead of the encoder's own map. Using the map
// would be tautological: a wrong entry would shift want and len(dst) together
// and pass.
const bitrateIndex128kbps = 9

// TestEncoderFrameSizes checks that, after draining, the concatenated
// stream's frames match the padding accumulator's exact per-frame pattern:
// 8 frames at 44.1kHz/128kbps/stereo produce lengths in {417,418}, and
// 48kHz frames (which never pad, per paddingState's doc comment) are all a
// constant length. The reservoir (Task 3) can hold a frame across
// EncodeFrame calls, so an individual call's dst growth is no longer one
// frame's length; walking the drained, concatenated stream frame-by-frame
// is the whole-stream equivalent, and it is exact rather than approximate
// because the FIFO is a strict FIFO: frame i in the stream is always
// codeFrame's i-th padding decision, in order.
func TestEncoderFrameSizes(t *testing.T) {
	t.Run("44100 128 stereo", func(t *testing.T) {
		cfg := Config{SampleRate: 44100, Channels: 2, BitrateKbps: 128}
		e := mustEncoder(t, cfg)
		seed := uint64(1)
		var dst []byte
		const nAudioFrames = 8
		for f := range nAudioFrames {
			var err error
			dst, err = e.EncodeFrame(dst, planarSamples(&seed, 2, 0.5))
			if err != nil {
				t.Fatalf("frame %d: EncodeFrame: %v", f, err)
			}
		}
		dst, err := e.EncodeFrame(dst, nil) // drain
		if err != nil {
			t.Fatalf("drain: EncodeFrame: %v", err)
		}

		var pad paddingState
		pos := 0
		for f := range nAudioFrames + 1 {
			wantPad := pad.next(128, 44100)
			want := frameLength(bitrateIndex128kbps, 0, wantPad)
			if want != 417 && want != 418 {
				t.Fatalf("frame %d: computed length = %d, want 417 or 418", f, want)
			}
			if pos+want > len(dst) {
				t.Fatalf("frame %d: only %d bytes remain in the stream, want at least %d", f, len(dst)-pos, want)
			}
			pos += want
		}
		if pos != len(dst) {
			t.Fatalf("stream length = %d, want %d (sum of %d paddingState-exact frame lengths)", len(dst), pos, nAudioFrames+1)
		}
	})

	t.Run("48000 never pads", func(t *testing.T) {
		cfg := Config{SampleRate: 48000, Channels: 2, BitrateKbps: 128}
		e := mustEncoder(t, cfg)
		want := frameLength(bitrateIndex128kbps, 1, 0)
		seed := uint64(2)
		var dst []byte
		const nAudioFrames = 6
		for f := range nAudioFrames {
			var err error
			dst, err = e.EncodeFrame(dst, planarSamples(&seed, 2, 0.5))
			if err != nil {
				t.Fatalf("frame %d: EncodeFrame: %v", f, err)
			}
		}
		dst, err := e.EncodeFrame(dst, nil) // drain
		if err != nil {
			t.Fatalf("drain: EncodeFrame: %v", err)
		}
		if wantTotal := want * (nAudioFrames + 1); len(dst) != wantTotal {
			t.Fatalf("stream length = %d, want constant %d * %d frames = %d", len(dst), want, nAudioFrames+1, wantTotal)
		}
	})
}

// TestEncoderDrain requires that after N audio frames, one nil call appends
// at least the drain frame itself (the reservoir, Task 3, may also flush a
// backlog of earlier frames still held in the FIFO, so the exact count is
// not pinned here; TestEncoderDrainFlushesAll below checks the FIFO is
// fully empty afterward) and flips Drained() true, and further nil calls
// append nothing.
func TestEncoderDrain(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, BitrateKbps: 128}
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	seed := uint64(3)
	if _, err := e.EncodeFrame(nil, planarSamples(&seed, 2, 0.5)); err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	if e.Drained() {
		t.Fatalf("Drained() = true before any drain call")
	}

	dst, err := e.EncodeFrame(nil, nil)
	if err != nil {
		t.Fatalf("drain EncodeFrame: %v", err)
	}
	if len(dst) == 0 {
		t.Fatalf("drain call appended nothing, want one frame")
	}
	if !e.Drained() {
		t.Fatalf("Drained() = false after drain call")
	}

	dst2, err := e.EncodeFrame(dst, nil)
	if err != nil {
		t.Fatalf("second drain EncodeFrame: %v", err)
	}
	if len(dst2) != len(dst) {
		t.Fatalf("second nil call appended %d bytes, want 0", len(dst2)-len(dst))
	}
}

// TestEncoderHeldFrameContract is design decision 11's held-frame
// lookahead contract, pinned directly (TestEncoderStatsCount and
// TestEncoderDrain already establish the N-calls-plus-drain-yields-N+1-
// frames invariant generically; this test isolates the specific new
// behavior that invariant depends on): EncodeFrame call 1 stashes its
// samples and returns dst UNCHANGED (no frame emitted yet, since held
// granule 1's wantNext needs a next frame's granule 0 that does not exist
// until call 2), call 2 emits exactly the first coded frame, and N calls
// plus drain still total exactly N+1 emitted frames.
func TestEncoderHeldFrameContract(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 1, BitrateKbps: 128}
	e := mustEncoder(t, cfg)
	seed := uint64(42)

	dst, err := e.EncodeFrame(nil, planarSamples(&seed, 1, 0.5))
	if err != nil {
		t.Fatalf("call 1: EncodeFrame: %v", err)
	}
	if len(dst) != 0 {
		t.Fatalf("call 1 (the first ever, before any lookahead exists): appended %d bytes, want 0", len(dst))
	}

	const nCalls = 6
	for range nCalls - 1 {
		var err error
		dst, err = e.EncodeFrame(dst, planarSamples(&seed, 1, 0.5))
		if err != nil {
			t.Fatalf("EncodeFrame: %v", err)
		}
	}
	dst, err = e.EncodeFrame(dst, nil) // drain
	if err != nil {
		t.Fatalf("drain: EncodeFrame: %v", err)
	}
	if got := e.Stats().Frames; got != nCalls+1 {
		t.Fatalf("Stats().Frames = %d after %d calls + drain, want %d (N calls + drain = N+1 frames)", got, nCalls, nCalls+1)
	}
	if len(dst) == 0 {
		t.Fatal("drained stream is empty")
	}
}

// TestEncoderNaN requires a NaN OR an Inf (+Inf and -Inf both checked)
// anywhere in samples to return ErrInvalidAudio, append nothing, and
// poison the encoder until Reset. Each bad value sits at an interior
// channel/sample index (not the first or last of either), matching how a
// real corrupt-upstream sample would arrive. Without dedicated Inf
// coverage, deleting the `|| math.IsInf(f, 0)` half of EncodeFrame's guard
// would leave this suite green while an Inf silently clamped to 1.0
// instead of poisoning; each subtest below exercises the full contract
// (reject, append nothing, poison persists across a subsequent clean call
// and a nil drain call, Reset clears it) for its own bad value.
func TestEncoderNaN(t *testing.T) {
	cases := []struct {
		name    string
		ch, idx int
		bad     float32
	}{
		{"NaN", 1, 500, float32(math.NaN())},
		{"PositiveInf", 0, 300, float32(math.Inf(1))},
		{"NegativeInf", 1, 800, float32(math.Inf(-1))},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := Config{SampleRate: 44100, Channels: 2, BitrateKbps: 128}
			e, err := New(cfg)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			seed := uint64(4)
			samples := planarSamples(&seed, 2, 0.5)
			samples[c.ch][c.idx] = c.bad

			dst, err := e.EncodeFrame(nil, samples)
			if !errors.Is(err, ErrInvalidAudio) {
				t.Fatalf("EncodeFrame with %s: err = %v, want ErrInvalidAudio", c.name, err)
			}
			if len(dst) != 0 {
				t.Fatalf("EncodeFrame with %s: appended %d bytes, want 0", c.name, len(dst))
			}

			// Poisoned: even clean input now fails.
			clean := planarSamples(&seed, 2, 0.5)
			if _, err := e.EncodeFrame(nil, clean); !errors.Is(err, ErrInvalidAudio) {
				t.Fatalf("EncodeFrame after %s poison: err = %v, want ErrInvalidAudio", c.name, err)
			}
			// Poisoned: nil drain call also fails.
			if _, err := e.EncodeFrame(nil, nil); !errors.Is(err, ErrInvalidAudio) {
				t.Fatalf("drain EncodeFrame after %s poison: err = %v, want ErrInvalidAudio", c.name, err)
			}

			if err := e.Reset(cfg); err != nil {
				t.Fatalf("Reset: %v", err)
			}
			if _, err := e.EncodeFrame(nil, clean); err != nil {
				t.Fatalf("EncodeFrame after Reset (%s): %v", c.name, err)
			}
		})
	}
}

// TestEncoderClampsLoudInput requires a granule of +-1e9 samples to encode
// without error: err == nil across 5 consecutive frames is what this test
// actually checks. It does not itself decode or otherwise verify frame
// structure (that is TestEncoderStructuralGrid's and
// TestEncoderRoundTripSNR's job, in internal/dec, over real Encoder
// output); what err == nil here proves is that the ingest clamp to [-1,1]
// plus the Task 4 maxQuant=8206 clamp both hold, so no Huffman-table
// lookup can ever fail (which would otherwise be the only way
// EncodeFrame could return a non-nil error or panic on finite input),
// regardless of how loud the finite input is.
func TestEncoderClampsLoudInput(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, BitrateKbps: 320}
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	samples := make([][]float32, 2)
	for ch := range 2 {
		samples[ch] = make([]float32, 1152)
		for i := range 1152 {
			v := float32(1e9)
			if (i+ch)%2 == 0 {
				v = -v
			}
			samples[ch][i] = v
		}
	}
	for f := range 5 {
		if _, err := e.EncodeFrame(nil, samples); err != nil {
			t.Fatalf("frame %d: EncodeFrame: %v", f, err)
		}
	}
}

// TestEncoderStatsCount requires Frames/Bytes/PaddedFrames to match the
// emitted stream exactly. Unlike TestEncoderFrameSizes, none of these
// counters assume one frame per EncodeFrame call: Frames and PaddedFrames
// count codeFrame invocations (unaffected by when the reservoir's FIFO,
// Task 3, actually flushes a frame out), and Bytes accumulates every byte
// EncodeFrame appends across the whole call sequence, including the
// drain's extra FIFO force-flush, so it stays exactly len(stream)
// regardless of how many frames any single call happened to flush.
func TestEncoderStatsCount(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, BitrateKbps: 128}
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	seed := uint64(5)
	var stream []byte
	const nFrames = 5
	for range nFrames {
		var err error
		stream, err = e.EncodeFrame(stream, planarSamples(&seed, 2, 0.5))
		if err != nil {
			t.Fatalf("EncodeFrame: %v", err)
		}
	}
	stream, err = e.EncodeFrame(stream, nil) // drain
	if err != nil {
		t.Fatalf("drain EncodeFrame: %v", err)
	}

	st := e.Stats()
	if st.Frames != nFrames+1 {
		t.Fatalf("Frames = %d, want %d", st.Frames, nFrames+1)
	}
	if st.Bytes != int64(len(stream)) {
		t.Fatalf("Bytes = %d, want %d (len(stream))", st.Bytes, len(stream))
	}

	// PaddedFrames is exactly derivable: codeFrame calls pad.next once per
	// EncodeFrame call, including the drain call (codeFrame does not
	// special-case samples == nil for padding), so an independent
	// paddingState run over the same nFrames+1 calls at the same
	// bitrate/sample rate reproduces the encoder's own padding decisions
	// exactly, not just a plausible range.
	var pad paddingState
	wantPadded := int64(0)
	for range nFrames + 1 {
		if pad.next(cfg.BitrateKbps, cfg.SampleRate) != 0 {
			wantPadded++
		}
	}
	if st.PaddedFrames != wantPadded {
		t.Fatalf("PaddedFrames = %d, want exactly %d (reference paddingState over %d frames)", st.PaddedFrames, wantPadded, nFrames+1)
	}

	// MeanGlobalGain is not exactly derivable here without re-running the
	// full rate loop per granule-channel (codeGranule's search path
	// depends on the LCG noise content, not just frame count/bitrate), so
	// this stays a range sanity check rather than an exact assertion.
	if st.MeanGlobalGain < 0 || st.MeanGlobalGain > 255 {
		t.Fatalf("MeanGlobalGain = %v, out of [0,255]", st.MeanGlobalGain)
	}
}

// TestEncoderPanicsOnWrongChannelCount requires a samples slice whose
// length disagrees with Config.Channels to panic (caller-bug class,
// distinct from ErrInvalidAudio).
func TestEncoderPanicsOnWrongChannelCount(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, BitrateKbps: 128}
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		if recover() == nil {
			t.Fatalf("EncodeFrame with 1 channel against a 2-channel Config: want panic")
		}
	}()
	seed := uint64(6)
	_, _ = e.EncodeFrame(nil, planarSamples(&seed, 1, 0.5))
}

// TestEncoderPanicsOnWrongSampleCount requires a channel whose sample
// count is not exactly 1152 to panic.
func TestEncoderPanicsOnWrongSampleCount(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 1, BitrateKbps: 128}
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		if recover() == nil {
			t.Fatalf("EncodeFrame with 1151 samples: want panic")
		}
	}()
	_, _ = e.EncodeFrame(nil, [][]float32{make([]float32, 1151)})
}

// TestEncoderEncodeAfterDrainPanics requires drain to be terminal: after N
// audio frames plus one nil drain, a subsequent non-nil EncodeFrame call
// panics (the caller-bug class, same precedent as the length-mismatch
// panics above), while a repeated nil call after drain remains safe (no
// panic, appends nothing). Without this, a caller could keep feeding real
// audio after draining while Drained() stayed true and that audio's tail
// (ChainDelay samples) never got flushed through the filterbank/MDCT
// history: an undefined, misleading state.
func TestEncoderEncodeAfterDrainPanics(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, BitrateKbps: 128}
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	seed := uint64(7)
	if _, err := e.EncodeFrame(nil, planarSamples(&seed, 2, 0.5)); err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	dst, err := e.EncodeFrame(nil, nil) // drain
	if err != nil {
		t.Fatalf("drain EncodeFrame: %v", err)
	}
	if !e.Drained() {
		t.Fatalf("Drained() = false after drain call")
	}

	// A repeated nil call after drain stays safe: no panic, nothing appended.
	dst2, err := e.EncodeFrame(dst, nil)
	if err != nil {
		t.Fatalf("nil call after drain: %v", err)
	}
	if len(dst2) != len(dst) {
		t.Fatalf("nil call after drain appended %d bytes, want 0", len(dst2)-len(dst))
	}

	// A non-nil call after drain panics: drain is terminal.
	defer func() {
		if recover() == nil {
			t.Fatalf("EncodeFrame with real audio after drain: want panic")
		}
	}()
	_, _ = e.EncodeFrame(dst, planarSamples(&seed, 2, 0.5))
}

// TestEncoderReservoirStreamComplete requires that, across 24 quiet/burst
// frames plus a drain, EncodeFrame's total flushed byte count exactly
// matches the paddingState-exact CBR total (mirroring TestEncoderFrameSizes'
// arithmetic, over the whole stream instead of per call), and that the
// reservoir actually buffers across calls: some call must flush nothing
// (sawZero, a frame held because the FIFO's earlier slots are not yet
// complete) and some call must flush more than one frame's worth of bytes
// (sawMulti, a call whose main-data spend completed a backlog). Without
// both, the reservoir/FIFO wiring is not actually doing anything, even if
// the byte totals happen to check out.
func TestEncoderReservoirStreamComplete(t *testing.T) {
	e := mustEncoder(t, Config{SampleRate: 44100, Channels: 1, BitrateKbps: 128})
	var dst []byte
	samples := quietThenBurstProgram(t, 24)
	perCall := make([]int, 0, 25)
	for f := range 24 {
		before := len(dst)
		var err error
		dst, err = e.EncodeFrame(dst, samples[f])
		if err != nil {
			t.Fatal(err)
		}
		perCall = append(perCall, len(dst)-before)
	}
	before := len(dst)
	dst, err := e.EncodeFrame(dst, nil) // drain
	if err != nil {
		t.Fatal(err)
	}
	perCall = append(perCall, len(dst)-before)

	// paddingState-exact total: codeFrame calls pad.next once per
	// EncodeFrame call, including the drain (TestEncoderStatsCount's
	// PaddedFrames check establishes this independently), and the FIFO
	// preserves push order, so summing an independent paddingState run
	// over the same 25 calls reproduces the drained stream's exact length.
	var pad paddingState
	want := 0
	for range 25 { // 24 audio + 1 drain frame
		wantPad := pad.next(128, 44100)
		want += frameLength(bitrateIndex128kbps, 0, wantPad)
	}
	if len(dst) != want {
		t.Fatalf("total stream length = %d, want %d (paddingState-exact CBR total)", len(dst), want)
	}

	// The load-bearing assertions:
	sawZero, sawMulti := false, false
	for _, n := range perCall {
		if n == 0 {
			sawZero = true
		}
		if n > frameLength(bitrateIndex128kbps, 0, 1) {
			sawMulti = true
		}
	}
	if !sawZero || !sawMulti {
		t.Errorf("reservoir never held/released frames across calls (zero=%v multi=%v): is the FIFO wired?", sawZero, sawMulti)
	}
}

// TestEncoderDrainFlushesAll requires that Drain empties the FIFO
// completely: after 16 quiet/burst frames (which
// TestEncoderReservoirStreamComplete already shows leaves some frames
// held), one drain call must bring the pending count to exactly 0.
// package enc's white-box tests can read e.fifo.count directly, no
// exported accessor needed.
func TestEncoderDrainFlushesAll(t *testing.T) {
	e := mustEncoder(t, Config{SampleRate: 44100, Channels: 1, BitrateKbps: 128})
	var dst []byte
	samples := quietThenBurstProgram(t, 16)
	for f := range 16 {
		var err error
		dst, err = e.EncodeFrame(dst, samples[f])
		if err != nil {
			t.Fatal(err)
		}
	}

	dst, err := e.EncodeFrame(dst, nil) // drain
	if err != nil {
		t.Fatal(err)
	}
	if e.fifo.count != 0 {
		t.Fatalf("fifo.count = %d after drain, want 0: Drain must flush every held frame", e.fifo.count)
	}
	_ = dst
}

// TestEncoderReservoirDeterminism requires the same quiet/burst program,
// run twice through fresh Encoders, to produce byte-identical streams: the
// reservoir's planFrame/commitFrame accounting and the FIFO's placement are
// integer-only and carry no hidden nondeterminism (map iteration order,
// wall-clock, goroutine scheduling) despite now spanning many frames'
// worth of buffered state instead of one frame in isolation.
func TestEncoderReservoirDeterminism(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 1, BitrateKbps: 128}
	samples := quietThenBurstProgram(t, 16)

	run := func() []byte {
		e := mustEncoder(t, cfg)
		var dst []byte
		for f := range 16 {
			var err error
			dst, err = e.EncodeFrame(dst, samples[f])
			if err != nil {
				t.Fatal(err)
			}
		}
		dst, err := e.EncodeFrame(dst, nil) // drain
		if err != nil {
			t.Fatal(err)
		}
		return dst
	}

	a, b := run(), run()
	if !bytes.Equal(a, b) {
		t.Fatalf("reservoir-enabled encode is nondeterministic: two runs of the same program diverged (len(a)=%d, len(b)=%d)", len(a), len(b))
	}
}

// TestEncodeGolden freezes sha256(full encoded stream) for a 4-frame
// LCG-noise input at three representative (sampleRate, channels, kbps)
// configurations spanning both channel counts and the bitrate extremes.
// The amd64/arm64 CI matrix makes this the cross-arch determinism gate: a
// mismatch there means some runtime float op is not actually
// FMA-fusion-blocked (or otherwise non-deterministic across arches), a
// code bug to fix, never a reason to re-freeze the hex constants.
//
// Re-frozen in Phase 4 increment 4 Task 5 (wiring the psychoacoustic
// model and outer loop into codeFrame): that change deliberately makes
// scalefactors nonzero for the first time, so every one of these three
// streams' bytes changed. Confirmed stable across two consecutive runs on
// amd64 before freezing; the arm64 CI leg is the cross-arch confirmation.
//
// Re-frozen again in Phase 4 increment 5 Task 3 (wiring the bit reservoir,
// frame-FIFO, and masking-driven budget escalation into codeFrame): none of
// these three cases drains before hashing, so the reservoir changes what
// main_data_begin, budgets, and ancillary padding every one of the four
// coded frames gets, the escalation redistributes budget to meet the masking
// contract, and the FIFO may hold a frame back that used to be flushed
// immediately, all of which change the emitted bytes even though the
// underlying audio and bitrate grid are unchanged. These hashes were
// confirmed byte-identical on native arm64 (rpi5, go1.26.1) as well as
// amd64 before freezing, directly exercising the escalation's noise>xmin and
// betterPass float branches across arches; the arm64 CI leg re-confirms it.
//
// Re-frozen again in Phase 4 increment 6 Task 2 (per-frame M/S joint
// stereo). The two amp-1.0 decorrelated stereo cases (44100_2ch_128kbps and
// 32000_2ch_32kbps) are L/R-dominant but not pure L/R: at full-scale
// broadband noise each carries exactly ONE frame whose four-way PE
// comparison legitimately selects M/S (headers: mode 01, mode_extension
// 10), so both stereo hashes changed. This is a deliberate perceptual
// re-freeze, NOT a coding-path leak: forcing e.msFrame=false in codeFrame's
// DECIDE phase reverts all three hashes to the exact pre-M/S values
// (44100_2ch_128kbps c734a1491e179a2bf6386ef3d465c2177817660b901226d8ab0523ee7930ebda,
// 48000_1ch_320kbps mono d1d7d99887552f2b2ddc4dde49e74be60fa14988a6732d0f02424a0d1f60da19,
// 32000_2ch_32kbps 11996294f75b9b529296cae97507e6e84f329ad43462fc387acfaa7687fc1a23),
// which proves the L/R coding path and mono are byte-identical to the
// pre-M/S encoder and the delta reflects only the M/S decision. The mono
// case (same granule coding path) is unchanged and stays an L/R-coding
// regression anchor. The new correlated case (identical channels, L==R)
// selects M/S on every frame (mode_extension 10 throughout), giving the
// M/S emission path its own cross-arch golden coverage. Confirmed stable
// across two consecutive amd64 runs before freezing; the arm64 CI leg is
// the cross-arch confirmation.
//
// Re-frozen again in Phase 4 increment 7 Task B2 (attack-driven window
// switching, the one-frame PCM lookahead, and the psymodel window
// re-centering, design decisions 9-11): every one of these four streams'
// bytes changed, for two compounding reasons named explicitly here rather
// than left implicit. First, the psymodel's analysis window is no longer
// causal (ending at a granule's own last sample); it is now CENTERED on
// the granule (224 samples of history, 224 of lookahead, decision 11), so
// every granule's Xmin/PE differs even for content that never switches
// block type. Second, TestEncodeGolden and TestEncodeGoldenForcedLR no
// longer drain (a 4-call loop with no trailing nil call, unchanged from
// before this task), and the held-frame lookahead (decision 11) means
// call 1 now only stashes its samples and emits nothing: the hashed
// stream is 3 coded frames' worth of bytes, not 4, and none of them is
// call 4's own content (that frame stays held, never coded within these 4
// calls). This is the expected, documented consequence of "N calls (no
// drain) emit N-1 frames" for the held-frame design, not a coding-path
// regression. All four cases also genuinely exercise block switching at
// their own cold start: full-scale broadband LCG noise beginning from
// pcmHist's all-zero initial state is exactly the "any sound is an attack
// relative to true silence" case decision 9's own rationale names, so
// granule 0 (and often more, since these are LOUD, wideband programs)
// switches to short/start/stop before the encoder settles into steady
// state (measured non-long granule counts across the 4-frame stream: 4,
// 2, 4, 4 for the four cases in order above). This re-freeze is therefore
// NOT block-switching-blind the way the doc text of an earlier draft of
// this comment assumed; TestEncodeGolden's cross-arch byte-for-byte
// determinism now covers the switch/DSP/render path along with everything
// else. A NEW transient-program case (44100_2ch_64kbps_transient) is added
// below for decision-path coverage that isolates the switch machinery on
// its own, deliberately confined to a single controlled burst rather than
// riding on this incidental cold-start effect. Confirmed stable across two
// consecutive amd64 runs before freezing; the arm64 CI leg (task B4) is
// the cross-arch confirmation.
// transientGoldenSHA freezes sha256(full encoded stream) for a controlled
// transient program (design decisions 9/10/11's decision-path coverage,
// Phase 4 increment 7 Task B2): silence, one loud stored-LCG click, then
// silence again, mono, drained. Unlike TestEncodeGolden's cases (which
// only exercise block switching incidentally at their cold start), this
// program isolates the switch machinery on a single controlled burst well
// after stream start, deliberately including the drain so the run's
// closing long-block tail is covered too. Never re-freeze on an
// arm64/amd64 mismatch (a determinism bug to fix); the arm64 CI leg (task
// B4) is the cross-arch confirmation.
const transientGoldenSHA = "8bcee4c5a93d6fd30b1f8af9534135527e5cd20513fe0daf1ad6aeae19c31c60"

func TestEncodeGoldenTransient(t *testing.T) {
	const nFrames = 10
	const burstFrame = 4

	e, err := New(Config{SampleRate: 44100, Channels: 1, BitrateKbps: 64})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	frames := clickTrainMono(nFrames, burstFrame, 0.8)
	var stream []byte
	for f := range nFrames {
		var err error
		stream, err = e.EncodeFrame(stream, frames[f:f+1])
		if err != nil {
			t.Fatalf("frame %d: EncodeFrame: %v", f, err)
		}
	}
	stream, err = e.EncodeFrame(stream, nil) // drain
	if err != nil {
		t.Fatalf("drain: EncodeFrame: %v", err)
	}

	sum := sha256.Sum256(stream)
	got := hex.EncodeToString(sum[:])
	if got != transientGoldenSHA {
		t.Fatalf("sha256 = %s, want %s", got, transientGoldenSHA)
	}
}

func TestEncodeGolden(t *testing.T) {
	cases := []struct {
		name                 string
		sampleRate, ch, kbps int
		correlated           bool // identical channels (L==R): forces M/S on every frame
		wantHex              string
	}{
		{"44100_2ch_128kbps", 44100, 2, 128, false, "e7b868ec37d11a7b653aa15d524e5e4022c9682747168a46205e0ff7f35c43a7"},
		{"48000_1ch_320kbps", 48000, 1, 320, false, "cc1886c0c5e01dd9640b7df2b382fbb5045adf08c525efb914093728302ba712"},
		{"32000_2ch_32kbps", 32000, 2, 32, false, "3cb6ea6799f179e9437d47145d9997f61cb164e49c68179bda79f6e43035df97"},
		{"44100_2ch_128kbps_ms", 44100, 2, 128, true, "690aef6d1d21d6a2b7065e32b16c4e41462212440f209320d01decbb894978c6"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := Config{SampleRate: c.sampleRate, Channels: c.ch, BitrateKbps: c.kbps}
			e, err := New(cfg)
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			seed := uint64(c.sampleRate)<<32 | uint64(c.kbps)<<8 | uint64(c.ch)
			var stream []byte
			for f := range 4 {
				samples := planarSamples(&seed, c.ch, 1.0)
				if c.correlated {
					// L == R: the S window is all zeros, so the four-way PE
					// comparison picks M/S every frame, covering the M/S
					// emission path across the amd64/arm64 CI matrix.
					copy(samples[1], samples[0])
				}
				stream, err = e.EncodeFrame(stream, samples)
				if err != nil {
					t.Fatalf("frame %d: EncodeFrame: %v", f, err)
				}
			}

			sum := sha256.Sum256(stream)
			got := hex.EncodeToString(sum[:])
			if got != c.wantHex {
				t.Fatalf("sha256 = %s, want %s", got, c.wantHex)
			}
		})
	}
}

// stereoProgram builds n frames of correlated or panned stereo from
// stored LCG values (bit-portable: no libm, no product-into-sum in the
// generation itself), per the golden-input discipline. The 0.625 scale is
// 5/8, exactly representable in binary (defense-in-depth per the agy
// review: a single multiply by any constant is correctly rounded and
// deterministic, but a dyadic scale removes even the doubt; LCGSigned's
// own construction is dyadic-exact already).
func stereoProgram(n int, kind string) [][2][]float32 {
	seed := uint64(101)
	frames := make([][2][]float32, n)
	for f := range frames {
		l := make([]float32, 1152)
		r := make([]float32, 1152)
		for i := range l {
			v := float32(testsignal.LCGSigned(&seed)) * 0.625
			switch kind {
			case "identical":
				l[i], r[i] = v, v
			case "panned":
				l[i], r[i] = v, 0
			case "inverted":
				l[i], r[i] = v, -v
			case "decorrelated":
				l[i] = v
				r[i] = float32(testsignal.LCGSigned(&seed)) * 0.625
			}
		}
		frames[f] = [2][]float32{l, r}
	}
	return frames
}

// countModes walks the encoded stream frame by frame using the internal
// header helpers: it reads each frame's length from the header (byte 2's
// bitrate_index/sampling_frequency/padding) and counts mode/mode_extension
// (byte 3). M/S frames are mode 01 with mode_extension 10; L/R frames are
// mode 00.
func countModes(t *testing.T, dst []byte) (ms, lr int) {
	t.Helper()
	for i := 0; i+4 <= len(dst); {
		if dst[i] != 0xFF || dst[i+1] != 0xFB {
			t.Fatalf("countModes: bad frame sync at offset %d: %02x %02x", i, dst[i], dst[i+1])
		}
		b2, b3 := dst[i+2], dst[i+3]
		bitrateIndex := int(b2>>4) & 0x0F
		srIndex := int(b2>>2) & 0x03
		padding := int(b2>>1) & 0x01
		mode := int(b3>>6) & 0x03
		modeExt := int(b3>>4) & 0x03
		switch {
		case mode == 1 && modeExt == 2:
			ms++
		case mode == 0:
			lr++
		}
		n := frameLength(bitrateIndex, srIndex, padding)
		if n <= 0 {
			t.Fatalf("countModes: bad frame length %d at offset %d", n, i)
		}
		i += n
	}
	return ms, lr
}

func TestEncoderMsSelection(t *testing.T) {
	// Identical channels: every audio frame must be M/S (parse the
	// headers: mode 01, mode_extension 10). Hard-panned: every frame L/R
	// (mode 00). Mono config: byte-identical to the pre-M/S encoder is
	// covered by the unchanged mono goldens.
	for _, tc := range []struct {
		kind   string
		wantMS bool
	}{{"identical", true}, {"panned", false}} {
		e := mustEncoder(t, Config{SampleRate: 44100, Channels: 2, BitrateKbps: 128})
		var dst []byte
		for _, fr := range stereoProgram(12, tc.kind) {
			var err error
			dst, err = e.EncodeFrame(dst, fr[:])
			if err != nil {
				t.Fatal(err)
			}
		}
		dst, err := e.EncodeFrame(dst, nil)
		if err != nil {
			t.Fatalf("drain EncodeFrame: %v", err)
		}
		ms, lr := countModes(t, dst)
		if tc.wantMS && ms < 10 {
			t.Errorf("%s: only %d M/S frames of %d+", tc.kind, ms, ms+lr)
		}
		if !tc.wantMS && ms != 0 {
			t.Errorf("%s: %d unexpected M/S frames", tc.kind, ms)
		}
	}
}

// TestEncoderMsInvertedSelection exercises stereoProgram's "inverted" case
// (R = -L): the mid channel is silent and the side channel carries
// everything, so M/S is the efficient representation and the PE rule
// legitimately selects it (TestMsAntiCorrelated in internal/dec proves the
// decoded audio side of the same scenario). Header-mode counting only, no
// decode. Like TestMsAntiCorrelated this deliberately asserts at least ONE
// M/S frame rather than all frames: anti-correlated material may
// legitimately mix in L/R frames, so an all-frames assertion would
// overconstrain the decision rule. Do not tighten it.
func TestEncoderMsInvertedSelection(t *testing.T) {
	e := mustEncoder(t, Config{SampleRate: 44100, Channels: 2, BitrateKbps: 128})
	var dst []byte
	for _, fr := range stereoProgram(12, "inverted") {
		var err error
		dst, err = e.EncodeFrame(dst, fr[:])
		if err != nil {
			t.Fatalf("EncodeFrame: %v", err)
		}
	}
	dst, err := e.EncodeFrame(dst, nil)
	if err != nil {
		t.Fatalf("drain EncodeFrame: %v", err)
	}
	ms, lr := countModes(t, dst)
	t.Logf("inverted: ms=%d lr=%d", ms, lr)
	if ms == 0 {
		t.Errorf("inverted input never selected M/S (ms=%d, lr=%d): PE decision regression?", ms, lr)
	}
}

func TestEncoderMsDeterminism(t *testing.T) {
	// The same decorrelated program twice through fresh Encoders yields
	// byte-identical streams (decision flips would show instantly).
	run := func() []byte {
		e := mustEncoder(t, Config{SampleRate: 44100, Channels: 2, BitrateKbps: 192})
		var dst []byte
		var err error
		for _, fr := range stereoProgram(20, "decorrelated") {
			dst, err = e.EncodeFrame(dst, fr[:])
			if err != nil {
				t.Fatalf("EncodeFrame: %v", err)
			}
		}
		dst, err = e.EncodeFrame(dst, nil)
		if err != nil {
			t.Fatalf("drain EncodeFrame: %v", err)
		}
		if len(dst) == 0 {
			t.Fatal("encoded stream is empty")
		}
		return dst
	}
	if !bytes.Equal(run(), run()) {
		t.Fatal("M/S encoder not deterministic")
	}
}
