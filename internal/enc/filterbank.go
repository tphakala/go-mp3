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
// unexported type or method; exporting Filterbank/Reset/AnalyzeGranule/
// PCMScale is the only way to satisfy that requirement. The package stays
// under internal/, so nothing leaks outside this module.
type Filterbank struct {
	x [512]float64 // shift register, x[0] newest
}

// Reset clears the shift register, as at the start of a fresh stream.
func (fb *Filterbank) Reset() {
	fb.x = [512]float64{}
}

// AnalyzeGranule consumes 576 input samples (one granule) for one channel
// and produces 18 blocks of 32 subband samples: out[t][b] is subband b at
// subband-sample time t. in has length 576; samples are already scaled by
// PCMScale by the caller.
//
// Float discipline: window multiplies each shift-register sample by
// its window coefficient, a product that feeds a following sum; matrixStep
// multiplies each partial sum by its matrix coefficient, a product that
// feeds a following accumulate. Both products carry an explicit float64()
// conversion, the only reliable barrier against arm64 fusing the multiply
// into an FMA against the accumulator (a bare local assignment does not
// block that; the compiler fuses across statements). The partial sums in
// step 3 are plain adds of already-rounded float64 values with no multiply
// involved, so they need no such wrapping. Accumulation in both steps runs
// left to right in index order, fixing the association order for
// determinism across amd64 and arm64.
func (fb *Filterbank) AnalyzeGranule(in []float64, out *[18][32]float64) {
	for t := range 18 {
		fb.shiftIn(in[t*32 : t*32+32])
		z := fb.window()
		y := partialSums(&z)
		matrixStep(&y, &out[t])
	}
}

// shiftIn implements the ISO C.1.3 flow chart's shift step: the 480 oldest
// samples move up by 32 slots, and the 32 newest samples (in) are written
// so the most recent lands at x[0].
func (fb *Filterbank) shiftIn(in []float64) {
	for i := 511; i >= 32; i-- {
		fb.x[i] = fb.x[i-32]
	}
	for k := range 32 {
		fb.x[31-k] = in[k]
	}
}

// window multiplies the shift register by the analysis window, one product
// per sample.
func (fb *Filterbank) window() [512]float64 {
	var z [512]float64
	for i := range 512 {
		z[i] = float64(fb.x[i] * fbWindow[i])
	}
	return z
}

// partialSums implements the ISO C.1.3 "partial calculation" step: 64 sums,
// each of 8 windowed samples spaced 64 apart.
func partialSums(z *[512]float64) [64]float64 {
	var y [64]float64
	for j := range 64 {
		sum := 0.0
		for k := range 8 {
			sum += z[j+k*64]
		}
		y[j] = sum
	}
	return y
}

// matrixStep implements the ISO C.1.3 matrixing step: each of the 32
// subband samples is the dot product of y with one row of fbMatrix.
func matrixStep(y *[64]float64, out *[32]float64) {
	for b := range 32 {
		sum := 0.0
		for j := range 64 {
			sum += float64(fbMatrix[b][j] * y[j])
		}
		out[b] = sum
	}
}
