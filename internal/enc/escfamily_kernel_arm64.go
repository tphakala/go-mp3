//go:build arm64 && !noasm

package enc

// escFamilyAccum on arm64 uses the fused NEON kernel (NEON is baseline on arm64,
// so no runtime feature check) for every non-empty band: the kernel vectorizes
// across the 8 tables and walks one pair per iteration, so it beats the scalar
// reference at every run length down to a single pair. Only a genuinely empty
// band skips it (its cumulative prefix cost is unchanged). Built into the
// default (SIMD) build; the noasm tag selects the pure-Go dispatcher instead.
func escFamilyAccum(acc16, acc24 *[8]int32, ax, ay, base16, base24 []int32) {
	if len(ax) == 0 {
		return // zero-width band: cumulative prefix cost unchanged
	}
	escFamilyAccumNEON(acc16, acc24, ax, ay, base16, base24)
}

//go:noescape
func escFamilyAccumNEON(acc16, acc24 *[8]int32, ax, ay, base16, base24 []int32)
