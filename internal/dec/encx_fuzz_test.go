// FuzzEncodeValidate is the structural fuzz target for Task 9 (package dec,
// not dec_test): it drives the internal enc.Encoder directly and reuses
// validateFrames (encx_frame_test.go), the same structural-plus-decode
// validator the Task 6/7 grids use. It must live in package dec because
// validateFrames is unexported; per PROVENANCE.md and internal/enc/doc.go
// this is the same sanctioned "internal/dec test file imports internal/enc"
// exception those grids already rely on.
//
// A package dec test file cannot import the root mp3 package (mp3 already
// imports internal/dec, so the reverse import in a same-package test file
// would form a cycle Go rejects; the same conflict the T6/T7 grids hit).
// validateFrames' decode leg therefore runs the internal dec.Decoder, the
// same engine the public mp3.Decoder delegates to, not the public API
// itself. The PUBLIC-decoder proof for fuzzed input lives in the sibling
// target FuzzEncoderAPI (root encoder_fuzz_test.go), which fuzzes the
// public mp3.Encoder/mp3.Decoder pair directly.
//
// Two variants run per fuzz input, each against a fresh Encoder:
//
//   - Variant 1 (fuzzEncodeValidateVariant1) builds samples IN-RANGE BY
//     CONSTRUCTION: each 4-byte chunk maps to [-1, 1] via
//     float64(int32(...))/(1<<31), never by clamping a raw float. This can
//     never carry a NaN/Inf or an out-of-range value, so EncodeFrame must
//     never error, and the resulting stream (plus the mandatory drain
//     frame) must be fully validateFrames-clean.
//   - Variant 2 (fuzzEncodeValidateVariant2) reinterprets the SAME bytes as
//     raw float32 bit patterns via math.Float32frombits, so it can and does
//     produce NaN/Inf and out-of-range-but-finite values: a NaN/Inf frame
//     must return enc.ErrInvalidAudio, leave dst unchanged by that call,
//     and poison the encoder (checked to persist across a further call and
//     a nil drain); an all-finite frame (however far outside [-1, 1]) must
//     encode without panicking and validate cleanly, per the Task 7 ingest
//     clamp and the Task 4 maxQuant clamp.

package dec

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/enc"
	"github.com/tphakala/go-mp3/internal/testsignal"
)

// fuzzEncMaxFrames caps how many real (non-drain) frames FuzzEncodeValidate
// builds from one fuzz input, per channel: enough to exercise the encoder's
// steady state without letting a single fuzz iteration blow up in cost.
const fuzzEncMaxFrames = 4

// fuzzEncSampleRates and fuzzEncBitratesKbps are the literal legal-value
// sets FuzzEncodeValidate indexes into with the fuzzed selector bytes, so
// every generated Config is guaranteed legal (enc.New never errors here).
var fuzzEncSampleRates = [3]int{44100, 48000, 32000}

var fuzzEncBitratesKbps = [14]int{
	32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320,
}

// FuzzEncodeValidate drives the internal enc.Encoder with a mix of
// in-range-by-construction and raw-reinterpreted float32 samples, over
// every legal (sample rate, channel count, bitrate) combination, and
// requires no panic plus the validation contracts documented in this file's
// header comment.
func FuzzEncodeValidate(f *testing.F) {
	for _, seed := range fuzzEncodeValidateSeeds() {
		f.Add(seed.data, seed.srSel, seed.chSel, seed.brSel)
	}

	f.Fuzz(func(t *testing.T, data []byte, srSel, chSel, brSel uint8) {
		// R-D-6: truncate to a clean multiple of 4 bytes BEFORE either
		// variant's Uint32 loop, so binary.LittleEndian.Uint32 never runs
		// on a trailing 1-3 byte remainder (which would panic inside the
		// harness itself and pollute fuzz findings).
		data = data[:len(data)-len(data)%4]

		sr := fuzzEncSampleRates[srSel%3]
		nch := int(chSel)%2 + 1
		kbps := fuzzEncBitratesKbps[brSel%14]
		cfg := enc.Config{SampleRate: sr, Channels: nch, BitrateKbps: kbps}

		fuzzEncodeValidateVariant1(t, cfg, data, sr, kbps, nch)
		fuzzEncodeValidateVariant2(t, cfg, data, sr, kbps, nch)
	})
}

// fuzzEncSeed is one FuzzEncodeValidate seed corpus entry.
type fuzzEncSeed struct {
	data                []byte
	srSel, chSel, brSel uint8
}

// fuzzEncodeValidateSeeds returns the hand-built seed corpus: silence,
// full-scale square wave (worst-case spectrum), a single impulse, a
// multi-tone buffer, and a NaN-bearing buffer. These are generated with
// small local helpers, not the dec_test package's buildMultiTone and
// friends: those helpers live in package dec_test (encx_roundtrip_test.go),
// a different, non-importable test package, invisible to package dec.
func fuzzEncodeValidateSeeds() []fuzzEncSeed {
	return []fuzzEncSeed{
		{fuzzSeedSamples(2*2*1152, func(int) uint32 { return 0 }), 0, 0, 0}, // silence
		// math.MaxInt32's bit pattern (0x7fffffff) is a NaN under Variant2's
		// math.Float32frombits reinterpretation, so this seed also exercises
		// the NaN/poison path on half its samples: bonus coverage beyond the
		// square wave's own purpose (worst-case spectrum for Variant1).
		{fuzzSeedSamples(2*2*1152, func(i int) uint32 { // full-scale square wave
			if i%2 == 0 {
				return fuzzInt32Bits(math.MinInt32)
			}
			return fuzzInt32Bits(math.MaxInt32)
		}), 0, 1, 8},
		{fuzzSeedSamples(1152, func(i int) uint32 { // single impulse; also NaN under Variant2 (see the square-wave seed's comment above)
			if i == 0 {
				return fuzzInt32Bits(math.MaxInt32)
			}
			return 0
		}), 1, 0, 13},
		{fuzzSeedSamples(2*1152, func(i int) uint32 { // multi-tone
			t := float64(i)
			v := 0.6*math.Sin(t*0.05) + 0.3*math.Sin(t*0.013) + 0.1*math.Sin(t*0.211)
			return uint32(int32(v * (1 << 29)))
		}), 2, 1, 0},
		{fuzzSeedSamples(64, func(int) uint32 { // NaN-bearing
			return math.Float32bits(float32(math.NaN()))
		}), 0, 0, 8},

		// Task 4 Step 2: adversarial seeds driving maximum bit-reservoir
		// swing at 32kbps stereo (fuzzEncBitratesKbps[0], the tightest bit
		// budget in the grid: resCapBytes's 7*area occupancy cap is
		// smallest here, so reservoir swing is most stressed). All three
		// build exactly fuzzEncMaxFrames (4) frames of content, since
		// fuzzBuildFrames never builds more frames than that regardless of
		// how much data a seed provides; the brief's literal frame counts
		// (8 silent frames, quietThenBurstProgram's 8-quiet/8-burst cycle)
		// are adapted down to fit that 4-frame budget while keeping each
		// pattern's stress character.
		{fuzzSeedSamples(4*2*1152, func(i int) uint32 { // full-scale square wave alternating with silence every frame
			frameIdx := i / (2 * 1152)
			if frameIdx%2 != 0 {
				return 0
			}
			if i%2 == 0 {
				return fuzzInt32Bits(math.MinInt32)
			}
			return fuzzInt32Bits(math.MaxInt32)
		}), 0, 1, 0},
		{fuzzSeedSamples(4*2*1152, func(i int) uint32 { // deep withdrawal: 3 quiet frames bank reservoir credit, then one full-scale frame spends it all at once
			frameIdx := i / (2 * 1152)
			if frameIdx < 3 {
				return 0
			}
			if i%2 == 0 {
				return fuzzInt32Bits(math.MinInt32)
			}
			return fuzzInt32Bits(math.MaxInt32)
		}), 0, 1, 0},
		{func() []byte { // quietThenBurst-style: internal/enc's quietThenBurstProgram (encoder_test.go) alternates 8 silent frames with 8 LCG-noise frames at amplitude 0.7, mono, 128kbps; that test helper is unexported and lives in a different, non-importable package, so this independently reproduces the same stored-LCG-noise construction (golden-input discipline: integer/stored construction, no libm), alternating every frame across the 4 reachable frames instead of every 8th.
			seed := uint64(0xA5A5)
			return fuzzSeedSamples(4*1152, func(i int) uint32 {
				frameIdx := i / 1152
				if frameIdx%2 == 0 {
					return 0
				}
				v := testsignal.LCGSigned(&seed)
				return fuzzInt32Bits(int32(v * 0.7 * (1 << 31)))
			})
		}(), 0, 0, 8},
	}
}

// fuzzInt32Bits reinterprets v's two's-complement bit pattern as a uint32.
// It exists so callers can pass math.MinInt32/math.MaxInt32 through a
// non-constant conversion: uint32(int32(math.MinInt32)) as a bare constant
// expression is rejected at compile time ("constant -2147483648 overflows
// uint32"), since constant conversions must be representable, unlike the
// runtime conversion this function performs.
func fuzzInt32Bits(v int32) uint32 { return uint32(v) }

// fuzzSeedSamples builds n little-endian 4-byte chunks from gen(i), one per
// sample index i in [0, n).
func fuzzSeedSamples(n int, gen func(i int) uint32) []byte {
	buf := make([]byte, n*4)
	for i := range n {
		binary.LittleEndian.PutUint32(buf[i*4:], gen(i))
	}
	return buf
}

// fuzzBuildFrames splits data into up to fuzzEncMaxFrames frames of exactly
// nch channels x 1152 samples each, converting every 4-byte chunk with
// convert. Chunks are consumed frame by frame; within each frame, channel-
// major (channel 0's full 1152 samples first, then channel 1's), so each
// frame's per-channel data comes from its own contiguous run of the input
// before consumption moves on to the next frame. The last frame that had
// any real data at all is zero-filled to exactly 1152 in every channel (the
// internal length contract), matching the brief's "zero-fill the final
// partial frame"; a frame with no real data left at all is not built.
func fuzzBuildFrames(data []byte, nch int, convert func(chunk []byte) float32) [][][]float32 {
	nChunks := len(data) / 4
	frames := make([][][]float32, 0, fuzzEncMaxFrames)
	pos := 0

	for range fuzzEncMaxFrames {
		samples := make([][]float32, nch)
		gotData := false
		for c := range nch {
			samples[c] = make([]float32, 1152)
			for i := range 1152 {
				if pos >= nChunks {
					continue
				}
				samples[c][i] = convert(data[pos*4 : pos*4+4])
				pos++
				gotData = true
			}
		}
		if !gotData {
			break
		}
		frames = append(frames, samples)
	}
	return frames
}

// fuzzConvertInRange maps a 4-byte little-endian chunk to [-1, 1] BY
// CONSTRUCTION: never NaN, never Inf, never outside [-1, 1], regardless of
// the chunk's bit pattern. This is the load-bearing requirement Variant 1
// relies on to assert EncodeFrame never errors.
func fuzzConvertInRange(chunk []byte) float32 {
	u := binary.LittleEndian.Uint32(chunk)
	return float32(float64(int32(u)) / (1 << 31))
}

// fuzzConvertRaw reinterprets a 4-byte little-endian chunk as a raw float32
// bit pattern: can be NaN, Inf, or any finite value including far outside
// [-1, 1]. Variant 2 uses this to pin the ErrInvalidAudio/poison and the
// finite-but-out-of-range clamp paths.
func fuzzConvertRaw(chunk []byte) float32 {
	u := binary.LittleEndian.Uint32(chunk)
	return math.Float32frombits(u)
}

// fuzzEncodeValidateVariant1 runs the in-range-by-construction leg: no
// EncodeFrame call may ever error, and the whole stream (real frames plus
// the mandatory drain flush frame) must validate cleanly.
func fuzzEncodeValidateVariant1(t *testing.T, cfg enc.Config, data []byte, sr, kbps, nch int) {
	t.Helper()

	e, err := enc.New(cfg)
	if err != nil {
		t.Fatalf("enc.New(%+v): %v", cfg, err)
	}

	frames := fuzzBuildFrames(data, nch, fuzzConvertInRange)

	var stream []byte
	for _, samples := range frames {
		stream, err = e.EncodeFrame(stream, samples)
		if err != nil {
			t.Fatalf("Variant1 EncodeFrame: unexpected error on in-range-by-construction samples: %v", err)
		}
	}
	stream, err = e.EncodeFrame(stream, nil) // drain: +1 flush frame
	if err != nil {
		t.Fatalf("Variant1 drain EncodeFrame: %v", err)
	}

	validateFrames(t, stream, sr, kbps, nch, len(frames)+1, true)
}

// fuzzEncodeValidateVariant2 runs the raw-float leg: a frame containing any
// NaN/Inf must poison the encoder (and that poisoning must persist), a
// fully finite stream must validate cleanly.
func fuzzEncodeValidateVariant2(t *testing.T, cfg enc.Config, data []byte, sr, kbps, nch int) {
	t.Helper()

	e, err := enc.New(cfg)
	if err != nil {
		t.Fatalf("enc.New(%+v): %v", cfg, err)
	}

	frames := fuzzBuildFrames(data, nch, fuzzConvertRaw)

	var stream []byte
	submitted := 0
	for _, samples := range frames {
		bad := fuzzFrameHasNaNOrInf(samples)
		before := len(stream)

		var encErr error
		stream, encErr = e.EncodeFrame(stream, samples)

		if bad {
			if !errors.Is(encErr, enc.ErrInvalidAudio) {
				t.Fatalf("Variant2 EncodeFrame with NaN/Inf: err = %v, want ErrInvalidAudio", encErr)
			}
			if len(stream) != before {
				t.Fatalf("Variant2 EncodeFrame with NaN/Inf: dst changed by %d bytes, want unchanged", len(stream)-before)
			}
			fuzzCheckPoisonPersists(t, e, stream, nch)
			return // frames encoded before this one are legitimate output; nothing further to validate
		}
		if encErr != nil {
			t.Fatalf("Variant2 EncodeFrame: unexpected error on an all-finite frame: %v", encErr)
		}
		submitted++
	}

	stream, err = e.EncodeFrame(stream, nil) // drain: +1 flush frame
	if err != nil {
		t.Fatalf("Variant2 drain EncodeFrame: %v", err)
	}

	validateFrames(t, stream, sr, kbps, nch, submitted+1, true)
}

// fuzzFrameHasNaNOrInf reports whether any sample in samples is NaN or Inf.
func fuzzFrameHasNaNOrInf(samples [][]float32) bool {
	for _, ch := range samples {
		for _, s := range ch {
			f := float64(s)
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return true
			}
		}
	}
	return false
}

// fuzzCheckPoisonPersists requires a poisoned Encoder to keep returning
// ErrInvalidAudio, with dst unchanged, on both a further non-nil call and a
// nil drain call.
func fuzzCheckPoisonPersists(t *testing.T, e *enc.Encoder, stream []byte, nch int) {
	t.Helper()

	dummy := make([][]float32, nch)
	for c := range dummy {
		dummy[c] = make([]float32, 1152)
	}

	before := len(stream)
	out, err := e.EncodeFrame(stream, dummy)
	if !errors.Is(err, enc.ErrInvalidAudio) {
		t.Fatalf("poison did not persist on the next non-nil call: err = %v", err)
	}
	if len(out) != before {
		t.Fatalf("poisoned non-nil call appended %d bytes, want 0", len(out)-before)
	}

	out2, err := e.EncodeFrame(stream, nil)
	if !errors.Is(err, enc.ErrInvalidAudio) {
		t.Fatalf("poison did not persist on a nil drain call: err = %v", err)
	}
	if len(out2) != before {
		t.Fatalf("poisoned nil drain appended %d bytes, want 0", len(out2)-before)
	}
}
