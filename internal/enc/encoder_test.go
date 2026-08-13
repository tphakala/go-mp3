package enc

import (
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

// TestEncoderFrameSizes checks that 40 frames at 44.1kHz/128kbps/stereo
// produce lengths in {417,418} matching the padding accumulator's exact
// pattern, and that 48kHz frames (which never pad, per paddingState's doc
// comment) have a constant length.
func TestEncoderFrameSizes(t *testing.T) {
	t.Run("44100 128 stereo", func(t *testing.T) {
		cfg := Config{SampleRate: 44100, Channels: 2, BitrateKbps: 128}
		e, err := New(cfg)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		var pad paddingState
		seed := uint64(1)
		for f := range 40 {
			samples := planarSamples(&seed, 2, 0.5)
			dst, err := e.EncodeFrame(nil, samples)
			if err != nil {
				t.Fatalf("frame %d: EncodeFrame: %v", f, err)
			}
			wantPad := pad.next(128, 44100)
			want := frameLength(kbpsToIndexEncoderTest(128), 0, wantPad)
			if len(dst) != want {
				t.Fatalf("frame %d: len = %d, want %d (padding=%d)", f, len(dst), want, wantPad)
			}
			if len(dst) != 417 && len(dst) != 418 {
				t.Fatalf("frame %d: len = %d, want 417 or 418", f, len(dst))
			}
		}
	})

	t.Run("48000 never pads", func(t *testing.T) {
		cfg := Config{SampleRate: 48000, Channels: 2, BitrateKbps: 128}
		e, err := New(cfg)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		want := frameLength(kbpsToIndexEncoderTest(128), 1, 0)
		seed := uint64(2)
		for f := range 20 {
			samples := planarSamples(&seed, 2, 0.5)
			dst, err := e.EncodeFrame(nil, samples)
			if err != nil {
				t.Fatalf("frame %d: EncodeFrame: %v", f, err)
			}
			if len(dst) != want {
				t.Fatalf("frame %d: len = %d, want constant %d", f, len(dst), want)
			}
		}
	})
}

// kbpsToIndexEncoderTest mirrors bitrateKbpsTable's inverse for this test
// file only (a small hand-specified map, independent of encoder.go's own
// table, so this test does not just check the table against itself).
func kbpsToIndexEncoderTest(kbps int) int {
	m := map[int]int{
		32: 1, 40: 2, 48: 3, 56: 4, 64: 5, 80: 6, 96: 7,
		112: 8, 128: 9, 160: 10, 192: 11, 224: 12, 256: 13, 320: 14,
	}
	return m[kbps]
}

// TestEncoderDrain requires that after N audio frames, one nil call appends
// exactly one frame and flips Drained() true, and further nil calls append
// nothing.
func TestEncoderDrain(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, BitrateKbps: 128}
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	seed := uint64(3)
	for range 3 {
		if _, err := e.EncodeFrame(nil, planarSamples(&seed, 2, 0.5)); err != nil {
			t.Fatalf("EncodeFrame: %v", err)
		}
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
// emitted stream exactly.
func TestEncoderStatsCount(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, BitrateKbps: 128}
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	seed := uint64(5)
	var stream []byte
	const nFrames = 25
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
	for range 3 {
		if _, err := e.EncodeFrame(nil, planarSamples(&seed, 2, 0.5)); err != nil {
			t.Fatalf("EncodeFrame: %v", err)
		}
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

// TestEncodeGolden freezes sha256(full encoded stream) for a 4-frame
// LCG-noise input at three representative (sampleRate, channels, kbps)
// configurations spanning both channel counts and the bitrate extremes.
// The amd64/arm64 CI matrix makes this the cross-arch determinism gate: a
// mismatch there means some runtime float op is not actually
// FMA-fusion-blocked (or otherwise non-deterministic across arches), a
// code bug to fix, never a reason to re-freeze the hex constants.
func TestEncodeGolden(t *testing.T) {
	cases := []struct {
		name                 string
		sampleRate, ch, kbps int
		wantHex              string
	}{
		{"44100_2ch_128kbps", 44100, 2, 128, "5dfeb5d9fd27efabc2f2c347a1cc7914fc80969009d81765234f1b0a77c0034d"},
		{"48000_1ch_320kbps", 48000, 1, 320, "fb0c94827c8b3e99018be73e24cb7f56e3c9466b2cf23be370dbc52f8b56717b"},
		{"32000_2ch_32kbps", 32000, 2, 32, "e399b2fc44b7f63047f7881535b1059cfc4dda9d5706c1bc9c22cea246e4d7ff"},
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
