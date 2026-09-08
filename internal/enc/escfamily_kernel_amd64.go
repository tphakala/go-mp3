//go:build amd64 && !noasm

package enc

// escFamilyAccum accumulates one run of big-values pairs into the two escape
// families' 8-lane cost accumulators. On amd64 it uses the fused AVX2 kernel
// when the CPU has AVX2 (default GOAMD64=v1 does not guarantee it, so the check
// is at runtime, sharing hasAVX2 with the region-cost kernel) and the run has at
// least a few pairs, else the pure-Go reference. Built into the default (SIMD)
// build; the noasm tag selects the pure-Go dispatcher instead.
func escFamilyAccum(acc16, acc24 *[8]int32, ax, ay, base16, base24 []int32) {
	if hasAVX2 && len(ax) >= 4 {
		escFamilyAccumAVX2(acc16, acc24, ax, ay, base16, base24)
		return
	}
	escFamilyAccumGo(acc16, acc24, ax, ay, base16, base24)
}

//go:noescape
func escFamilyAccumAVX2(acc16, acc24 *[8]int32, ax, ay, base16, base24 []int32)
