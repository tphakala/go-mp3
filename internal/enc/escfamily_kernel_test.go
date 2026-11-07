package enc

import (
	"math/rand/v2"
	"testing"
)

// escFamilyAccumScalarOld reproduces the pre-issue-66 escape-family accumulation
// exactly (accumEscFamilyFlat for a non-escaping pair, accumEscFamilyCost
// otherwise), as the differential oracle for escFamilyAccumGo. It mirrors
// bigValuesPrefixCostGo's former inner pair loop.
func escFamilyAccumScalarOld(ax, ay []int32) (acc16, acc24 [8]int) {
	for p := range ax {
		x, y := ax[p], ay[p]
		if x < escMaxDirect && y < escMaxDirect {
			accumEscFamilyFlat(&acc16, table16Codes, x, y)
			accumEscFamilyFlat(&acc24, table24Codes, x, y)
			continue
		}
		accumEscFamilyCost(&acc16, &escFam16Linbits, &escFam16MaxVal, table16Codes, x, y)
		accumEscFamilyCost(&acc24, &escFam24Linbits, &escFam24MaxVal, table24Codes, x, y)
	}
	return
}

// escFamilyBaseFor precomputes the per-pair shared codeword+sign costs that
// escFamilyAccumGo consumes (the codebook gather kept out of the accumulation
// loop). Mirrors bigValuesPrefixCostGo's base16/base24 pre-pass.
func escFamilyBaseFor(ax, ay []int32) (base16, base24 []int32) {
	base16 = make([]int32, len(ax))
	base24 = make([]int32, len(ax))
	for p := range ax {
		x, y := ax[p], ay[p]
		cx, cy := x, y
		if cx > escMaxDirect {
			cx = escMaxDirect
		}
		if cy > escMaxDirect {
			cy = escMaxDirect
		}
		var sign int32
		if x != 0 {
			sign++
		}
		if y != 0 {
			sign++
		}
		base16[p] = int32(table16Codes[int(cx)*escTableDim+int(cy)].len) + sign
		base24[p] = int32(table24Codes[int(cx)*escTableDim+int(cy)].len) + sign
	}
	return
}

// randMag draws a big-values magnitude weighted to exercise every branch of the
// escape-family cost: the dense direct range 0..14, the escape boundary at
// escMaxDirect (15), moderate escapes, the largest maxVal boundary (8206, from
// linbits 13), and magnitudes beyond it where every table is impossibleCost.
func randMag(r *rand.Rand) int32 {
	switch r.IntN(5) {
	case 0:
		return int32(r.IntN(15)) // direct, non-escaping
	case 1:
		return int32(14 + r.IntN(4)) // straddle escMaxDirect (14..17)
	case 2:
		return int32(15 + r.IntN(256)) // moderate escapes
	case 3:
		return int32(8200 + r.IntN(12)) // straddle the maxVal 8206 boundary
	default:
		return int32(r.IntN(9000)) // full range incl. all-impossible
	}
}

func escFamilyAccumGoWrap(ax, ay []int32) (acc16, acc24 [8]int32) {
	base16, base24 := escFamilyBaseFor(ax, ay)
	escFamilyAccumGo(&acc16, &acc24, ax, ay, base16, base24)
	return
}

// TestEscFamilyAccumGoMatchesScalar pins escFamilyAccumGo (the per-run
// primitive escFamilyPrefixGo builds on) bit-for-bit to the former
// accumEscFamilyFlat/accumEscFamilyCost path over randomized pair runs spanning
// every branch, so the folded uniform formula (issue #66) is proven
// output-neutral independently of the full-encode golden gate.
func TestEscFamilyAccumGoMatchesScalar(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for iter := range 4000 {
		n := 1 + r.IntN(288)
		ax := make([]int32, n)
		ay := make([]int32, n)
		for p := range n {
			ax[p] = randMag(r)
			ay[p] = randMag(r)
		}
		want16, want24 := escFamilyAccumScalarOld(ax, ay)
		got16, got24 := escFamilyAccumGoWrap(ax, ay)
		for j := range 8 {
			if int(got16[j]) != want16[j] {
				t.Fatalf("iter %d: acc16[%d] = %d, want %d", iter, j, got16[j], want16[j])
			}
			if int(got24[j]) != want24[j] {
				t.Fatalf("iter %d: acc24[%d] = %d, want %d", iter, j, got24[j], want24[j])
			}
		}
	}
}
