//go:build arm64 && !noasm

package enc

// escFamilyAccum on arm64 currently forwards to the pure-Go reference: the NEON
// kernel is a follow-up (issue #66). Keeping the dispatcher here, build-tagged
// like the amd64 one, means dropping in escFamilyAccumNEON later is a one-line
// change and the arm64 default build stays bit-exact in the meantime.
func escFamilyAccum(acc16, acc24 *[8]int32, ax, ay, base16, base24 []int32) {
	escFamilyAccumGo(acc16, acc24, ax, ay, base16, base24)
}
