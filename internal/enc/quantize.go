package enc

import "math"

// maxQuant is the largest magnitude any Huffman table encodes: 15 + 2^13 - 1
// (linbits 13 escape).
const maxQuant = 8206

// pow2Quarter holds 2^0, 2^(1/4), 2^(1/2) and 2^(3/4) as exact hex float
// literals, the four quarter-integer steps invStep interpolates between
// powers of two. Hard-coded rather than computed with math.Pow at init, per
// the package's determinism rule (no math.Pow/Sin/Cos/Exp/Log in the runtime
// encode path).
var pow2Quarter = [4]float64{
	0x1p+0,
	0x1.306fe0a31b715p+0,
	0x1.6a09e667f3bcdp+0,
	0x1.ae89f995ad3adp+0,
}

// invStep returns 2^((210-gg)/4), the per-line multiplier ISO annex C.1.5.4
// applies before raising the magnitude to the 3/4 power. gg is the global
// gain (0..255).
//
// q := 210 - gg splits into an integer part q>>2 (arithmetic shift right,
// which floors for negative q too) and a remainder q&3 in [0,3] (Go's
// bitwise AND on the low two bits of a two's complement value is already
// the non-negative floor-mod, for negative q as well as positive). So
// pow2Quarter[q&3] scaled by 2^(q>>2) reproduces 2^(q/4) exactly, with no
// math.Pow call.
func invStep(gg int) float64 {
	q := 210 - gg
	return math.Ldexp(pow2Quarter[q&3], q>>2)
}

// quantizeGranule quantizes xr at global gain gg (0..255) into ix:
// ix[i] = sign(xr[i]) * nint((|xr[i]| * invStep(gg))^(3/4) - 0.0946), the ISO
// annex C.1.5.4 power-law quantizer with scalefactors all zero.
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
// low gg (invStep(0) is about 2^52.5) even a modest |xr[i]| makes v land far
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
func quantizeGranule(xr *[576]float64, gg int, ix *[576]int32) {
	is := invStep(gg)
	for i := range 576 {
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

// minGlobalGain returns the smallest gg (0..255) that keeps every |ix| <=
// maxQuant when xr is quantized at that gg. invStep(gg) is monotonically
// non-increasing in gg, so the quantized magnitude of the single largest
// |xr| value is monotonically non-increasing too (TestQuantizeMonotone
// confirms this holds for every line, not just the largest); the first gg
// that clears the bound for that largest magnitude therefore clears it for
// every line. Bounded by 256 iterations of scalar work.
//
// Like quantizeGranule, the bound check compares in float64 before any
// int32 conversion, for the same overflow-avoidance reason.
func minGlobalGain(xr *[576]float64) int {
	maxAbs := 0.0
	for _, x := range xr {
		if a := math.Abs(x); a > maxAbs {
			maxAbs = a
		}
	}

	for gg := range 256 {
		t := maxAbs * invStep(gg)
		v := math.Sqrt(t * math.Sqrt(t))
		if v+0.4054 <= float64(maxQuant) {
			return gg
		}
	}
	return 255
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

// abs32 is the int32 absolute value used by partitionSpectrum's quad scan.
func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
