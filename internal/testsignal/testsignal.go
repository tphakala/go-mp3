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
// leaf package. testsignal is imported only by _test.go files and never runs
// on any encode path.
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
	return (sampleRate + 1152 - 1) / 1152
}
