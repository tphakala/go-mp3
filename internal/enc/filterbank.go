package enc

// PCMScale is the input scaling from float32 [-1,1] PCM into the spectral
// domain the (minimp3-parity, hence ISO-parity) decoder expects. Measured
// by the Task 2 reconstruction gate (internal/dec/encx_filterbank_test.go,
// TestReconstructionGate): the brief predicted 32768, reasoning that
// mp3dScalePcm's final /32768 division needed to be pre-cancelled on the
// analysis side. The measurement disagreed: with PCMScale=32768 the
// round-trip chain gain came out at 65536.52 (2^16, not 1), which means the
// analysis (this package's window/matrix pair) plus synthesis
// (mp3dSynthGranule) round trip already carries an intrinsic gain of about
// 2.0 even before mp3dScalePcm's /32768 is folded in. 32768 * 2.0 / 65536 =
// 1, so the correct pre-scale is 32768/65536 = 0.5, not 32768; that
// intrinsic factor of 2 most likely traces to the analysis matrix producing
// values on a scale the decoder's dequantized-spectral-domain synthesis
// input does not otherwise carry (this port bypasses MDCT/dequantization
// entirely, so there is no independent scaling stage to absorb it). 0.5
// converges the measured gain to 1.0 within the 1% granule-consistency
// check TestReconstructionGate enforces; see that test for the frozen
// fbChainGain and fbChainDelay this value was calibrated against.
const PCMScale = 0.5

// Filterbank is the per-channel 32-band polyphase analysis filterbank
// (ISO/IEC 11172-3, Annex C, section 3-C.1.3 "Analysis Subband Filter"): a
// 512-sample sliding window, the C analysis window (fbWindow), and the
// 32x64 cosine matrix (fbMatrix).
//
// Exported: the task brief's illustrative interface listed this type and
// its methods unexported (Task 3, same package, was the only stated
// consumer), but the same brief also requires the reconstruction gate to
// live in internal/dec (package dec, a different package) and import
// internal/enc to drive it. Go visibility is package-scoped regardless of
// test-file status, so an external package's test cannot reach an
// unexported type or method; exporting Filterbank/AnalyzeGranule/PCMScale
// is the only way to satisfy that requirement. Reset is exported too, for
// the Encoder (a later task) to call, not because the cross-package test
// needs it. The package stays under internal/, so nothing leaks outside
// this module.
type Filterbank struct {
	x [512]float64 // shift register, x[0] newest
}

// Reset clears the shift register, as at the start of a fresh stream. It is
// the stream-restart hook a later task's Encoder calls between streams,
// mirroring dec.Decoder.Reset; it has no caller within this package or the
// test suite today.
func (fb *Filterbank) Reset() {
	fb.x = [512]float64{}
}

// AnalyzeGranule consumes 576 input samples (one granule) for one channel
// and produces 18 blocks of 32 subband samples: out[t][b] is subband b at
// subband-sample time t. in has length 576; samples are already scaled by
// PCMScale by the caller.
//
// Float discipline: window's product, z[i] = float64(fb.x[i] * fbWindow[i]),
// feeds a store into z[i], not an adjacent +/-, so its float64() wrap is not
// a fusion barrier; this was verified empirically (GOARCH=arm64 go build
// -gcflags=-S produces byte-identical codegen for that line with or without
// the wrap). The wrap is kept anyway, as defensive uniformity across the
// package rather than a correctness requirement. partialSums and matrixStep
// are build specific (filterbank_simd.go, filterbank_noasm.go); each keeps
// every output's sum starting at +0.0 and running in ascending index order
// with unfused products, which is what makes the result identical on amd64
// and arm64 and in both builds. TestFilterbankParity pins that against a
// step-by-step reference.
func (fb *Filterbank) AnalyzeGranule(in []float64, out *[18][32]float64) {
	var z [512]float64
	var y [64]float64
	for t := range 18 {
		fb.shiftIn(in[t*32 : t*32+32])
		fb.window(&z)
		partialSums(&z, &y)
		matrixStep(&y, &out[t])
	}
}

// shiftIn implements the ISO C.1.3 flow chart's shift step: the 480 oldest
// samples move up by 32 slots, and the 32 newest samples (in) are written
// so the most recent lands at x[0]. copy is memmove-correct for this
// overlapping up-shift, matching the decoder's own analog in
// internal/dec/synth.go.
func (fb *Filterbank) shiftIn(in []float64) {
	copy(fb.x[32:], fb.x[:480])
	for k := range 32 {
		fb.x[31-k] = in[k]
	}
}

// window multiplies the shift register by the analysis window, one product
// per sample, into z (out-pointer form, matching partialSums and matrixStep;
// issue #31).
func (fb *Filterbank) window(z *[512]float64) {
	for i := range 512 {
		z[i] = float64(fb.x[i] * fbWindow[i])
	}
}
