// Package testsignal provides the deterministic pseudo-random and
// multi-tone generators shared across this project's test suites. It is a
// normal (non-_test) package so it can be imported from test files in any
// package, which is exactly why it exists: several test packages used to
// carry their own copy of the same LCG because Go test files are not
// importable across packages.
//
// LCG and LCGSigned are golden-pinned: the frozen sha256 goldens in the enc
// and dec test suites (TestFBGolden, TestMdctGolden, TestHuffmanGolden,
// TestEncodeGolden, and others) replay these exact sequences byte for byte.
// The expressions below may never change; doing so would silently change
// every pinned golden hash and every SNR-floor test that consumes them.
//
// MultiTone calls math.Sin. That is deliberate and safe here: the project's
// no-math.Sin/Cos-at-runtime rule (see internal/enc/mdcttables.go and
// fbtables.go, whose coefficient tables are frozen literals for bit-exact
// output) binds internal/enc's production encode path, not this test-support
// leaf package. testsignal is imported by _test.go files and by the
// tools/quality harness (a standalone CLI that measures the encoder against
// LAME), and never runs on any encode path.
package testsignal

import "math"

// LCG advances the PCG-style linear congruential generator at *seed and
// returns a pseudo-random float64 in the unit interval [0, 1).
func LCG(seed *uint64) float64 {
	*seed = *seed*6364136223846793005 + 1442695040888963407
	return float64(*seed>>11) / float64(1<<53)
}

// LCGSigned advances the same generator as LCG but returns a pseudo-random
// float64 in the signed interval [-1, 1).
func LCGSigned(seed *uint64) float64 {
	return LCG(seed)*2 - 1
}

// IdenticalNoise returns nSamples of the canonical identical-channels noise
// program: LCG noise from seed 0xC0FFEE scaled by 0.3, as
// float32(LCGSigned * 0.3) exactly (one float64 multiply, one conversion).
// Duplicated on both channels of a stereo encode, it forces the M/S decision
// every frame (the side channel is exactly zero before quantization). This
// is the single source of truth for the seed and scale of the float32 stereo
// identical-channels program; TestMsIdenticalChannels (internal/dec) and the
// M/S compat programs (root compat_test.go) consume it, and the fuzz seed
// builders in internal/dec/encx_fuzz_test.go mirror the same constants in
// int32 form (they cannot route through this float32 helper without
// double-rounding their seed bytes; see the comment there). A separate
// float64 mono program in encx_roundtrip_test.go reuses the same 0xC0FFEE/0.3
// pair for XminScale calibration; it is a different signal and deliberately
// does not route through this helper.
func IdenticalNoise(nSamples int) []float32 {
	seed := uint64(0xC0FFEE)
	x := make([]float32, nSamples)
	for i := range x {
		x[i] = float32(LCGSigned(&seed) * 0.3)
	}
	return x
}

// DecorrelatedNoise returns two fully independent noise channels with no
// shared structure: LCG noise from seeds 0x5EED1 (x) and 0x5EED2 (y), each
// scaled by 0.5, as float32(LCGSigned * 0.5) exactly. Fed as (x, y) stereo
// input it exercises whichever per-frame M/S versus L/R decisions the PE rule
// makes on decorrelated content. Single source of truth for the seeds and
// scale, on the same terms as IdenticalNoise above.
func DecorrelatedNoise(nSamples int) (x, y []float32) {
	seedX, seedY := uint64(0x5EED1), uint64(0x5EED2)
	x = make([]float32, nSamples)
	y = make([]float32, nSamples)
	for i := range x {
		x[i] = float32(LCGSigned(&seedX) * 0.5)
		y[i] = float32(LCGSigned(&seedY) * 0.5)
	}
	return x, y
}

// MultiTone returns nSamples samples of a deterministic multi-tone program:
// a 440 Hz fundamental plus overtones at -6 dB (880 Hz) and -12 dB (1320
// Hz), scaled so |x[i]| <= peak for every i regardless of phase alignment
// (dividing by the SUM of the three weights bounds the signal pointwise,
// since |sum of sines| <= sum of amplitudes always; the actual peak,
// reached only where the three tones align in phase, is <= this bound).
// chPhase offsets the signal's phase so a second, decorrelated channel can
// be built by calling this with a different chPhase, rather than
// duplicating the same samples across channels.
func MultiTone(sampleRate, nSamples int, chPhase, peak float64) []float64 {
	const f0 = 440.0
	const w1, w2, w3 = 1.0, 0.5011872336272722, 0.251188643150958 // 0, -6, -12 dB
	scale := peak / (w1 + w2 + w3)

	x := make([]float64, nSamples)
	for i := range x {
		t := float64(i) / float64(sampleRate)
		v := w1*math.Sin(2*math.Pi*f0*t+chPhase) +
			w2*math.Sin(2*math.Pi*2*f0*t+chPhase*1.3) +
			w3*math.Sin(2*math.Pi*3*f0*t+chPhase*1.7)
		x[i] = scale * v
	}
	return x
}

// FramesForOneSecond returns the smallest number of 1152-sample (the
// MPEG-1 Layer III frame size) MP3 frames covering at least one second of
// audio at sampleRate.
func FramesForOneSecond(sampleRate int) int {
	return (sampleRate + FrameSize - 1) / FrameSize
}

// FrameSize is the MPEG-1 Layer III frame size in samples per channel, and
// ClickPeriodFrames and ClickBurstFrames are the click cadence both the
// compat gate and the tools/quality corpus drive block switching with.
//
// FrameSize duplicates the root package's mp3.FrameSize because importing it
// here would cycle: internal/enc's own in-package tests import this package,
// and the root mp3 package imports internal/enc. The root's compat_test.go
// asserts the two stay equal.
const (
	FrameSize         = 1152
	ClickPeriodFrames = 4
	ClickBurstFrames  = 1
)

// ClickTrain returns nSamples of mono click-train content: silence with a
// loud LCG-noise burst (amplitude 0.8, seed 0xC1CC7A31) every
// periodFrames*FrameSize samples, each burst burstFrames*FrameSize samples
// long, repeating for the whole duration, the first burst at sample 0. It
// drives an encoder's attack detector repeatedly; the compat gate's
// block-switching programs (root compat_test.go) and the tools/quality
// corpus both consume it, so this is the single source of truth for the
// float32 click-train program's seed and amplitude. A separate int32 mirror
// in internal/dec/encx_fuzz_test.go repeats the same constants for the same
// reason IdenticalNoise's does: a fuzz seed cannot double-round through this
// float32 helper.
func ClickTrain(nSamples, periodFrames, burstFrames int) []float32 {
	x := make([]float32, nSamples)
	seed := uint64(0xC1CC7A31)
	period := periodFrames * FrameSize
	burst := burstFrames * FrameSize
	for start := 0; start+burst <= nSamples; start += period {
		for i := range burst {
			x[start+i] = float32(LCGSigned(&seed)) * 0.8
		}
	}
	return x
}

// ToneClick returns nSamples of mono tone+click content: a steady MultiTone
// (peak 0.5) interrupted every periodFrames frames by a genuine onset (576
// samples of silence, then an LCG-noise burst of burstFrames frames, seed
// 0x5A17C1CC, amplitude 0.9), clamped to [-1, 1]. The silence-then-burst
// shape is what a sub-block energy-ratio attack detector actually fires on:
// a click merely summed onto a loud tone never clears the ratio. The first
// burst starts one period in, a true mid-stream onset rather than a
// stream-start granule. Same single-source-of-truth role as ClickTrain.
func ToneClick(sampleRate, nSamples, periodFrames, burstFrames int) []float32 {
	tone := MultiTone(sampleRate, nSamples, 0, 0.5)
	seed := uint64(0x5A17C1CC)
	period := periodFrames * FrameSize
	burst := burstFrames * FrameSize
	const gap = 576
	for start := period; start+burst <= nSamples; start += period {
		for i := start - gap; i < start; i++ {
			tone[i] = 0
		}
		for i := range burst {
			tone[start+i] = LCGSigned(&seed) * 0.9
		}
	}
	x := make([]float32, nSamples)
	for i, v := range tone {
		x[i] = float32(max(-1, min(1, v)))
	}
	return x
}
