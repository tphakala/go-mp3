//go:build (amd64 || arm64) && !noasm

package enc

// pairCostRows holds pairCost for every pair with both magnitudes at most 15
// (indexed x*escTableDim+y) under each of the 32 big-values table slots, and
// pairCostBound the smallest magnitude each slot cannot represent: dim for a
// non-escape table, 15 + 1<<linbits for an escape table, 0 for the nil-codes
// slots 4 and 14. The fused prefix-cost kernels add one row per pair. A pair
// with a magnitude above 15 takes the row of its magnitudes clamped to 15,
// which is its exact cost under every escape table that can represent it (the
// codeword index saturates at 15 and the row already pays linbits for each
// magnitude at 15), and impossibleCost in each slot whose bound the larger
// magnitude reaches. The nil-codes slots cost impossibleCost for every pair, so
// their prefix sums are the pb[k]*impossibleCost ramp bigValuesPrefixCostGo
// writes there.
//
// The kernels hardcode this geometry: a 16-wide row index (x<<4 | y), a
// 128-byte row stride (32 slots x 4 bytes) and a clamp of 15, so escTableDim
// and escMaxDirect cannot change without editing the assembly.
var pairCostRows, pairCostBound = buildPairCostRows()

// buildPairCostRows fills pairCostRows and pairCostBound from bigTables.
func buildPairCostRows() (rows [escTableDim * escTableDim][32]int32, bound [32]int32) {
	for t, bt := range &bigTables {
		switch {
		case bt.codes == nil:
			// bound[t] stays 0: the slot cannot represent any pair.
		case bt.linbits > 0:
			bound[t] = int32(bt.dim-1) + int32(1)<<uint(bt.linbits)
		default:
			bound[t] = int32(bt.dim)
		}
		for x := range int32(escTableDim) {
			for y := range int32(escTableDim) {
				rows[x*escTableDim+y][t] = int32(pairCost(bt, x, y))
			}
		}
	}
	return rows, bound
}

// checkPrefixCostLayout panics, as the Go reference's bounds checks do, when
// the layout would make a kernel leave its arrays: nBands above 39 reads
// pb[40] and writes prefixCost[40], and pb[nBands] above the 288 pairs of ix
// reads past ix. One compare per call; it does not allocate.
//
// Interior boundaries are not checked: the kernels need pb non-decreasing from
// pb[0] = 0 and within 288, which pairBoundaries, the only producer, yields by
// construction for bigValues in 0..288, the range partitionSpectrum caps it to
// (TestPairBoundariesProducerContract pins it). A walk over pb here measured
// about 1% on a short-block frame benchmark on arm64.
func checkPrefixCostLayout(ix *[576]int32, pb *[40]int, nBands int) {
	if uint(nBands) >= uint(len(pb)) || pb[nBands] > len(ix)/2 {
		panic("enc: prefix cost layout out of range")
	}
}
