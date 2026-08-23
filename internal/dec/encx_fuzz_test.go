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

		// Task 4 Step 2: M/S joint-stereo correlation seeds. Each builds
		// exactly fuzzEncMaxFrames (4) stereo frames (chSel=1 forces
		// nch=2), channel-major per fuzzBuildFrames' consumption order
		// (frame f's channel 0's 1152 samples, then its channel 1's), so
		// every generator below indexes by frameIdx = i/(2*1152), a
		// within-block offset in [0, 2*1152) that tells which channel half
		// it is in, and sampleIdx = the offset mod 1152.
		//
		// Seed/scale provenance: the 0xC0FFEE/0.3 and 0x5EED1-0x5EED2/0.5
		// pairs below mirror testsignal.IdenticalNoise and
		// testsignal.DecorrelatedNoise, the canonical definitions of these
		// programs. They stay hand-rolled here in int32/float64 form on
		// purpose: these builders compute int32(LCGSigned*scale*(1<<31)) in
		// float64 and convert once, whereas the helpers return float32;
		// routing through them would insert a float64->float32->float64
		// double rounding and change the seed corpus bytes. Keep the
		// constants in sync with testsignal if they ever change (they must
		// not: they are golden-pinned there).
		{func() []byte { // identical channels: L==R every frame (noise), the cheapest M/S case (TestMsIdenticalChannels)
			seed := uint64(0xC0FFEE)
			noise := make([]int32, 1152)
			for i := range noise {
				noise[i] = int32(testsignal.LCGSigned(&seed) * 0.3 * (1 << 31))
			}
			return fuzzSeedSamples(4*2*1152, func(i int) uint32 {
				sampleIdx := (i % (2 * 1152)) % 1152
				return fuzzInt32Bits(noise[sampleIdx])
			})
		}(), 0, 1, 8},
		{func() []byte { // inverted channels: R = -L every frame, mid silent and side fully loaded (TestMsAntiCorrelated)
			seed := uint64(0xC0FFEE)
			noise := make([]int32, 1152)
			for i := range noise {
				noise[i] = int32(testsignal.LCGSigned(&seed) * 0.3 * (1 << 31))
			}
			return fuzzSeedSamples(4*2*1152, func(i int) uint32 {
				within := i % (2 * 1152)
				v := noise[within%1152]
				if within >= 1152 { // channel 1 (R): negate
					v = -v
				}
				return fuzzInt32Bits(v)
			})
		}(), 0, 1, 8},
		{func() []byte { // hard pan alternating sides every frame: frame 0 loud on L, frame 1 loud on R, ... (TestMsHardPanSelectsLR, made to flap frame by frame)
			seed := uint64(0xBEEF)
			tone := make([]int32, 1152)
			for i := range tone {
				tone[i] = int32(testsignal.LCGSigned(&seed) * 0.7 * (1 << 31))
			}
			return fuzzSeedSamples(4*2*1152, func(i int) uint32 {
				frameIdx := i / (2 * 1152)
				within := i % (2 * 1152)
				ch := within / 1152
				if ch != frameIdx%2 {
					return 0
				}
				return fuzzInt32Bits(tone[within%1152])
			})
		}(), 0, 1, 8},
		{func() []byte { // fully decorrelated noise, independent per channel (TestMsChannelSeparation)
			seedX, seedY := uint64(0x5EED1), uint64(0x5EED2)
			x := make([]int32, 1152)
			y := make([]int32, 1152)
			for i := range x {
				x[i] = int32(testsignal.LCGSigned(&seedX) * 0.5 * (1 << 31))
				y[i] = int32(testsignal.LCGSigned(&seedY) * 0.5 * (1 << 31))
			}
			return fuzzSeedSamples(4*2*1152, func(i int) uint32 {
				within := i % (2 * 1152)
				if within < 1152 {
					return fuzzInt32Bits(x[within])
				}
				return fuzzInt32Bits(y[within-1152])
			})
		}(), 0, 1, 8},
		{func() []byte { // maximum decision flapping: identical channels and decorrelated noise alternate every frame
			seed := uint64(0xC0FFEE)
			noise := make([]int32, 1152)
			for i := range noise {
				noise[i] = int32(testsignal.LCGSigned(&seed) * 0.3 * (1 << 31))
			}
			seedX, seedY := uint64(0x5EED1), uint64(0x5EED2)
			x := make([]int32, 1152)
			y := make([]int32, 1152)
			for i := range x {
				x[i] = int32(testsignal.LCGSigned(&seedX) * 0.5 * (1 << 31))
				y[i] = int32(testsignal.LCGSigned(&seedY) * 0.5 * (1 << 31))
			}
			return fuzzSeedSamples(4*2*1152, func(i int) uint32 {
				frameIdx := i / (2 * 1152)
				within := i % (2 * 1152)
				sampleIdx := within % 1152
				if frameIdx%2 == 0 { // identical channels this frame
					return fuzzInt32Bits(noise[sampleIdx])
				}
				if within < 1152 { // decorrelated this frame
					return fuzzInt32Bits(x[sampleIdx])
				}
				return fuzzInt32Bits(y[sampleIdx])
			})
		}(), 0, 1, 8},

		// Task B4 Step 3: short-block seeds, added alongside the M/S seeds
		// above once block switching landed. Each still builds exactly
		// fuzzEncMaxFrames (4) frames. Each builder lives in its own named
		// function (rather than inline, like the seeds above) to keep this
		// aggregator's cognitive complexity within golangci-lint's gocognit
		// budget.
		{fuzzClickTrainSeed(), 0, 0, 8},             // mono, 128kbps
		{fuzzSubGranuleAlternatingSeed(), 0, 0, 13}, // mono, 320kbps
		{fuzzPerChannelClicksSeed(), 0, 1, 8},       // stereo, 128kbps
		{fuzzBurstEveryGranuleSeed(), 0, 1, 13},     // stereo, 320kbps
	}
}

// fuzzClickTrainSeed builds a mono click-train seed: brief LCG-noise clicks
// confined to the first 192 samples (one attack-detector sub-block) of
// every other frame, otherwise silent, mirroring compat_test.go's periodic
// click-train program. Distinct from the identical-channels/burst seeds
// above (whole-frame noise or square waves): the attack here sits inside an
// otherwise-silent frame, matching the real click content this seed's name
// promises.
func fuzzClickTrainSeed() []byte {
	seed := uint64(0xC1CC7A31)
	return fuzzSeedSamples(4*1152, func(i int) uint32 {
		frameIdx := i / 1152
		within := i % 1152
		if frameIdx%2 != 1 || within >= 192 {
			return 0
		}
		return fuzzInt32Bits(int32(testsignal.LCGSigned(&seed) * 0.8 * (1 << 31)))
	})
}

// fuzzSubGranuleAlternatingSeed builds a mono seed alternating silence and
// a full-scale impulse train at sub-granule period (576 samples, half a
// frame): the maximal stress case (agy review suggestion) for the attack
// detector's energy carry across granules and the outer loop's
// subblock_gain escalation, since every granule boundary flips the
// verdict.
func fuzzSubGranuleAlternatingSeed() []byte {
	return fuzzSeedSamples(4*1152, func(i int) uint32 {
		granuleIdx := i / 576
		if granuleIdx%2 == 0 {
			return 0
		}
		if i%2 == 0 {
			return fuzzInt32Bits(math.MinInt32)
		}
		return fuzzInt32Bits(math.MaxInt32)
	})
}

// fuzzPerChannelClicksSeed builds a stereo seed where channel 0 clicks only
// on frame 1 and channel 1 clicks only on frame 2, so the two channels
// never attack together. Stresses blockTypesAgree's M/S veto
// (internal/enc/stereo.go) when the channels disagree on transient timing
// (coupling-veto coverage).
func fuzzPerChannelClicksSeed() []byte {
	seed0, seed1 := uint64(0xC1CC0), uint64(0xC1CC1)
	return fuzzSeedSamples(4*2*1152, func(i int) uint32 {
		frameIdx := i / (2 * 1152)
		within := i % (2 * 1152)
		ch := within / 1152
		sampleIdx := within % 1152
		switch {
		case ch == 0 && frameIdx == 1 && sampleIdx < 192:
			return fuzzInt32Bits(int32(testsignal.LCGSigned(&seed0) * 0.8 * (1 << 31)))
		case ch == 1 && frameIdx == 2 && sampleIdx < 192:
			return fuzzInt32Bits(int32(testsignal.LCGSigned(&seed1) * 0.8 * (1 << 31)))
		default:
			return 0
		}
	})
}

// fuzzBurstEveryGranuleSeed builds a stereo seed with a few full-scale
// samples at the start of every 576-sample granule: maximal
// switch-decision flapping across the whole reachable stream (every
// granule wants short).
func fuzzBurstEveryGranuleSeed() []byte {
	// Stereo seed (chSel 1): fuzzBuildFrames consumes 2*1152 samples per
	// frame, so all four frames need 4*2*1152 samples. Supplying only 4*1152
	// would build just two frames and lose half the flapping coverage.
	return fuzzSeedSamples(4*2*1152, func(i int) uint32 {
		within := i % 576
		if within >= 4 {
			return 0
		}
		if i%2 == 0 {
			return fuzzInt32Bits(math.MinInt32)
		}
		return fuzzInt32Bits(math.MaxInt32)
	})
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
