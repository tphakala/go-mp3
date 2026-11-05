//go:build arm64 && !noasm

package enc

// bigValuesPrefixCost computes the big-values prefix costs of all 32 table
// slots in one pass with the NEON kernel (NEON is baseline on arm64, so no
// runtime feature check). Integer sums have no accumulation-order hazard, so
// the result is identical to bigValuesPrefixCostGo.
func bigValuesPrefixCost(ix *[576]int32, pb *[40]int, lay *bandLayout, prefixCost *[40][32]int32) {
	prefixCostNEON(ix, pb, lay.nBands, prefixCost, &pairCostRows, &pairCostBound)
}

// prefixCostNEON keeps the 32 running sums in eight 4-lane accumulators, adds
// one pairCostRows row per pair and stores each band's sums into
// prefixCost[k+1].
//
//go:noescape
func prefixCostNEON(ix *[576]int32, pb *[40]int, nBands int, prefixCost *[40][32]int32, rows *[256][32]int32, bound *[32]int32)
