package enc

// subMinReduceGo is the pure-Go reference for the fused region-cost reduction:
// the minimum over i of a[i]-b[i], a single pass with no scratch buffer. It is
// both the fallback when no SIMD kernel is compiled in (the noasm build or an
// architecture without one) and the differential oracle the AVX2/NEON kernels
// are checked against. Caller guarantees len(a) == len(b) > 0.
//
// rangeCost feeds it two band-major prefix-cost rows (prefixCost[b], prefixCost[a],
// each a full 32-wide row including the invalid table columns 4 and 14, which
// carry a per-band ramp so their difference can never be the minimum). The min
// over the whole 32-wide row therefore equals the min over the valid tables.
func subMinReduceGo(a, b []int32) int32 {
	best := a[0] - b[0]
	for i := 1; i < len(a); i++ {
		c := a[i] - b[i]
		if c < best {
			best = c
		}
	}
	return best
}
