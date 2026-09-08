//go:build amd64 && !noasm

package enc

// escFamilyAccum accumulates one run of big-values pairs into the two escape
// families' 8-lane cost accumulators. On amd64 it uses the fused AVX2 kernel
// when the CPU has AVX2 (default GOAMD64=v1 does not guarantee it, so the check
// is at runtime, sharing hasAVX2 with the region-cost kernel). The kernel
// vectorizes across the 8 tables and walks one pair per iteration, so it beats
// the scalar reference at every run length down to a single pair; only a
// genuinely empty band skips it (its cumulative prefix cost is unchanged). A
// narrow-band threshold would strand short-block bands on the slower scalar
// path, which measurably regressed the transient fast-mode case. Built into the
// default (SIMD) build; the noasm tag selects the pure-Go dispatcher instead.
func escFamilyAccum(acc16, acc24 *[8]int32, ax, ay, base16, base24 []int32) {
	switch {
	case len(ax) == 0:
		// Zero-width band: cumulative prefix cost unchanged, skip the call.
	case hasAVX2:
		escFamilyAccumAVX2(acc16, acc24, ax, ay, base16, base24)
	default:
		escFamilyAccumGo(acc16, acc24, ax, ay, base16, base24)
	}
}

//go:noescape
func escFamilyAccumAVX2(acc16, acc24 *[8]int32, ax, ay, base16, base24 []int32)
