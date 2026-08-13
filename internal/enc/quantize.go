package enc

import "math"

// maxQuant is the largest magnitude any Huffman table encodes: 15 + 2^13 - 1
// (linbits 13 escape).
const maxQuant = 8206

// pow2Quarter holds 2^0, 2^(1/4), 2^(1/2) and 2^(3/4) as exact hex float
// literals, the four quarter-integer steps stepQ interpolates between
// powers of two. Hard-coded rather than computed with math.Pow at init, per
// the package's determinism rule (no math.Pow/Sin/Cos/Exp/Log in the runtime
// encode path).
var pow2Quarter = [4]float64{
	0x1p+0,
	0x1.306fe0a31b715p+0,
	0x1.6a09e667f3bcdp+0,
	0x1.ae89f995ad3adp+0,
}

// stepQ returns 2^(q/4) exactly, q may be negative: q splits into an integer
// part q>>2 (arithmetic shift right, which floors for negative q too) and a
// remainder q&3 in [0,3] (Go's bitwise AND on the low two bits of a two's
// complement value is already the non-negative floor-mod, for negative q as
// well as positive), so pow2Quarter[q&3] scaled by 2^(q>>2) via math.Ldexp
// reproduces 2^(q/4) exactly, with no math.Pow call.
//
// invStep(gg) is the gg-only special case stepQ(quantGainBase - gg). Task 2
// (the per-band quantizer, quantizeGranule/minGlobalGain/noiseGranule)
// generalizes on this: those need a per-band exponent, gg plus that band's
// bandExtraQuarters amplification, not just gg alone.
func stepQ(q int) float64 {
	return math.Ldexp(pow2Quarter[q&3], q>>2)
}

// quantGainBase is 214, not the textbook ISO constant 210: the Task 7
// full-chain round-trip gate (internal/dec/encx_roundtrip_test.go,
// TestEncoderRoundTripSNR) discovered that this project's decoder (a
// faithful port of minimp3, not an abstract ISO reference decoder)
// dequantizes with gainExp = globalGain - 4 - 210 (internal/dec/
// scalefactors.go's l3ReadScalefactors: gainExp := int(gr.globalGain) +
// bitsDequantizerOut*4 - 210 - msAdj, with bitsDequantizerOut = -1), an
// extra -4 baked into minimp3's own fixed-point convention that the pure
// textbook formula does not carry. Left uncorrected, the encoder and
// decoder's exponents diverge by a constant 4 (in quarter-steps), i.e. a
// factor of 2^(-4/4) = 0.5, on every reconstructed line, independent of
// gg: TestEncoderRoundTripSNR measured a flat ~6dB SNR ceiling across
// every bitrate and sample rate before this fix (exactly what a systematic
// half-amplitude reconstruction predicts: 20*log10(2) = 6.02dB), jumping to
// 30-78dB after it (see TestEncoderRoundTripSNR's doc comment,
// internal/dec/encx_roundtrip_test.go, for the full measured ranges). This
// mirrors PCMScale and mdctScale (filterbank.go, mdct.go): a value the ISO
// formula alone predicts incorrectly, because
// minimp3's internal fixed-point conventions don't match the textbook
// convention exponent-for-exponent, and only an end-to-end round trip
// against the real decoder can catch it. See PCMScale's doc comment for
// the same pattern in the filterbank stage.
const quantGainBase = 214

// invStep returns 2^((quantGainBase-gg)/4), the per-line multiplier ISO
// annex C.1.5.4 applies before raising the magnitude to the 3/4 power, with
// every band's scalefactor amplification at 0 (bandExtraQuarters' zero
// case). gg is the global gain (0..255). quantizeGranule and noiseGranule
// no longer call this directly (they need the per-band exponent stepQ
// exposes), but it stays as the documented gg-only special case.
func invStep(gg int) float64 {
	return stepQ(quantGainBase - gg)
}

// scfState is one granule-channel's scalefactor state. The zero value is
// exactly Phase 3 behavior (no amplification, no preemphasis, half-step
// scale), which is what keeps PR A behavior-preserving.
type scfState struct {
	scf           [21]int // integer scalefactors, sfbs 0..20
	scalefacScale int     // 0 or 1
	preflag       int     // 0 or 1
}

// bandExtraQuarters returns the amplification exponent for band sfb in
// quarter-power-of-two steps: 2*(scalefacScale+1)*(scf+preflag*pretab). sfb
// 21 (no scalefactor) returns 0.
func (sf *scfState) bandExtraQuarters(sfb int) int {
	if sfb >= 21 {
		return 0
	}
	pretab := 0
	if sf.preflag != 0 {
		pretab = pretabLong[sfb]
	}
	return 2 * (sf.scalefacScale + 1) * (sf.scf[sfb] + pretab)
}

// quantizeGranule quantizes xr at global gain gg (0..255) under the per-band
// scalefactor state sf into ix: for each line i in band sfb, ix[i] =
// sign(xr[i]) * nint((|xr[i]| * stepQ((quantGainBase-gg)+sf.bandExtraQuarters(sfb)))^0.75
// - 0.0946), the ISO annex C.1.5.4 power-law quantizer generalized with
// per-band scalefactor amplification (2.4.3.4.5): scalefac_scale doubles the
// scalefactor's quarter-step weight, preflag adds a fixed per-band boost
// from pretabLong, and bandExtraQuarters folds both into the single
// quarter-power-of-two exponent stepQ consumes. sf's zero value makes
// bandExtraQuarters 0 for every band, so this reduces exactly to Phase 3's
// invStep(gg) formula (TestQuantizeScaledKnownAnswers' zero-state case
// checks this holds bit-for-bit against a frozen copy of the Phase 3 body).
//
// The band step is hoisted out of the per-line loop: one stepQ call per
// band (bandExtraQuarters and gg are constant across a band), not one per
// line.
//
// x^0.75 is computed as sqrt(x*sqrt(x)) rather than math.Pow, per the
// package's determinism rule; TestQuantizePowRef documents the substitution
// agrees with math.Pow closely enough that the two never disagree once
// truncated to the nint step below, sampled over 1e-3..1e6. nint is v +
// 0.4054 truncated: that
// equals (v - 0.0946) + 0.5, and since v >= 0 the sum is always >= 0, so
// Go's truncating float64-to-int conversion implements floor(v - 0.0946 +
// 0.5), i.e. round-half-up on v - 0.0946.
//
// maxQuant is a hard clamp applied to every line: a safety net independent
// of how gg was chosen, so no line is ever uncodeable by any Huffman table
// regardless of the caller. The clamp is applied as a float64 comparison
// BEFORE the int32 conversion, not as an int32 range check after it: for a
// low gg (invStep(0) is about 2^53.5) even a modest |xr[i]| makes v land far
// outside int32's range, and converting an out-of-range float64 to int32 is
// implementation-defined per the Go spec, not merely a theoretical risk
// here: Task 4's review traced the divergence to the instruction each
// arch's compiler emits for the conversion (amd64's CVTTSD2SI returns an
// INT_MIN sentinel on overflow; arm64's FCVTZS saturates to INT_MAX
// instead), which the project's cross-arch determinism rule forbids.
// Comparing in float64 first, and only converting to int32
// once the value is known to be within [0, maxQuant], sidesteps that
// entirely; the two forms agree on every line that would not have overflowed
// anyway.
func quantizeGranule(xr *[576]float64, gg int, sf *scfState, sfbWidths *[22]int, ix *[576]int32) {
	i := 0
	for sfb := range 22 {
		is := stepQ((quantGainBase - gg) + sf.bandExtraQuarters(sfb))
		end := i + sfbWidths[sfb]
		for ; i < end; i++ {
			t := math.Abs(xr[i]) * is
			v := math.Sqrt(t * math.Sqrt(t))

			var m int32
			if nint := v + 0.4054; nint <= float64(maxQuant) {
				m = int32(nint)
			} else {
				m = maxQuant
			}

			if xr[i] < 0 {
				m = -m
			}
			ix[i] = m
		}
	}
}

// minGlobalGain returns the smallest gg (0..255) that keeps every amplified
// line's quantized magnitude within maxQuant under sf, scanning per band
// with that band's own worst-case |xr|. bandExtraQuarters can vary the
// effective step from band to band, so the single loudest line in the whole
// spectrum no longer determines the bound by itself (as it did in Phase 3,
// where every band shared the same step): minGlobalGain tracks each band's
// own worst-case line and requires the returned gg to clear every band's
// bound, not just the globally loudest one.
//
// stepQ(...) is monotonically non-increasing in gg for any fixed extra, so
// each band's worst-case quantized magnitude is itself monotonically
// non-increasing in gg (the same reasoning TestQuantizeMonotone established
// for Phase 3's uniform step); the first gg that clears every band's bound
// is therefore also the smallest. Bounded by 256 iterations of scalar work,
// same as Phase 3.
//
// Like quantizeGranule, the bound check compares in float64 before any
// int32 conversion, for the same overflow-avoidance reason.
func minGlobalGain(xr *[576]float64, sf *scfState, sfbWidths *[22]int) int {
	var bandMax [22]float64
	i := 0
	for sfb := range 22 {
		m := 0.0
		end := i + sfbWidths[sfb]
		for ; i < end; i++ {
			if a := math.Abs(xr[i]); a > m {
				m = a
			}
		}
		bandMax[sfb] = m
	}

	for gg := range 256 {
		fits := true
		for sfb := range 22 {
			is := stepQ((quantGainBase - gg) + sf.bandExtraQuarters(sfb))
			t := bandMax[sfb] * is
			v := math.Sqrt(t * math.Sqrt(t))
			if v+0.4054 > float64(maxQuant) {
				fits = false
				break
			}
		}
		if fits {
			return gg
		}
	}
	return 255
}

// noiseGranule measures quantization noise energy per band: for each band
// sfb, the sum over the band's lines of (|xr[i]| - pow43[|ix[i]|] *
// stepQ((gg-quantGainBase)-sf.bandExtraQuarters(sfb)))^2. The stepQ argument
// is the exact negation of quantizeGranule's own per-band exponent, so
// stepQ((gg-quantGainBase)-extra) is the exact inverse of the step that
// produced ix: pow43[|ix[i]|]*that inverse is the dequantized magnitude the
// decoder would reconstruct at this gg and sf, not an approximation.
//
// The dequant-error arithmetic (pow43 lookup, difference, square, running
// sum) is FMA-blocked: float64(diff*diff) forces the squared difference to
// round to float64 before the running sum's addition, so the result is
// bit-identical on amd64 and arm64 regardless of whether the compiler would
// otherwise fuse the multiply-add (see quantGainBase's doc comment, and the
// package's determinism rule, for why this matters).
func noiseGranule(xr *[576]float64, ix *[576]int32, gg int, sf *scfState, sfbWidths *[22]int, noise *[22]float64) {
	i := 0
	for sfb := range 22 {
		inv := stepQ((gg - quantGainBase) - sf.bandExtraQuarters(sfb))
		end := i + sfbWidths[sfb]
		sum := 0.0
		for ; i < end; i++ {
			dequant := float64(pow43[abs32(ix[i])] * inv)
			diff := math.Abs(xr[i]) - dequant
			sum += float64(diff * diff)
		}
		noise[sfb] = sum
	}
}

// spectrumPartition is the rzero/count1/big-values split of a quantized
// granule, scanned per ISO 2.4.2.7 semantics.
type spectrumPartition struct {
	bigValues int // pairs in the big-values region (region boundaries within it chosen later)
	count1    int // quads (all |v| <= 1) following the big-values region
	// the rest of the 576 lines is the implicit zero region
}

// partitionSpectrum scans ix from line 576 downward: first in pairs while
// both lines are exactly zero (the rzero region, which spectrumPartition
// leaves implicit), then in quads while all four |v| <= 1 (the count1
// region). What remains, lines [0, i), is the big-values region: bigValues
// = i/2 pairs. bigValues <= 288 always holds structurally, since i starts
// at 576 and every step only decreases it.
func partitionSpectrum(ix *[576]int32) spectrumPartition {
	i := 576
	for i >= 2 && ix[i-1] == 0 && ix[i-2] == 0 {
		i -= 2
	}

	count1 := 0
	for i >= 4 && abs32(ix[i-1]) <= 1 && abs32(ix[i-2]) <= 1 && abs32(ix[i-3]) <= 1 && abs32(ix[i-4]) <= 1 {
		i -= 4
		count1++
	}

	return spectrumPartition{bigValues: i / 2, count1: count1}
}

// abs32 is the int32 absolute value used by partitionSpectrum's quad scan
// and noiseGranule's pow43 lookup.
func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
