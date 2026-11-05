//go:build (amd64 || arm64) && !noasm

package enc

// pairCostRows holds pairCost for every pair with both magnitudes at most 15
// (indexed x*16+y) under each of the 32 big-values table slots, and
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
var pairCostRows, pairCostBound = buildPairCostRows()

// buildPairCostRows fills pairCostRows and pairCostBound from bigTables.
func buildPairCostRows() (rows [256][32]int32, bound [32]int32) {
	for t, bt := range &bigTables {
		switch {
		case bt.codes == nil:
			bound[t] = 0
		case bt.linbits > 0:
			bound[t] = int32(bt.dim-1) + int32(1)<<uint(bt.linbits)
		default:
			bound[t] = int32(bt.dim)
		}
		for x := range int32(16) {
			for y := range int32(16) {
				rows[x*16+y][t] = int32(pairCost(bt, x, y))
			}
		}
	}
	return rows, bound
}
