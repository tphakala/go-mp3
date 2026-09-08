//go:build arm64 && !noasm

package enc

// escFamilyAccum on arm64 uses the fused NEON kernel (NEON is baseline on arm64,
// so no runtime feature check) when the run has at least a few pairs, else the
// pure-Go reference. Built into the default (SIMD) build; the noasm tag selects
// the pure-Go dispatcher instead.
func escFamilyAccum(acc16, acc24 *[8]int32, ax, ay, base16, base24 []int32) {
	if len(ax) >= 4 {
		escFamilyAccumNEON(acc16, acc24, ax, ay, base16, base24)
		return
	}
	escFamilyAccumGo(acc16, acc24, ax, ay, base16, base24)
}

//go:noescape
func escFamilyAccumNEON(acc16, acc24 *[8]int32, ax, ay, base16, base24 []int32)
