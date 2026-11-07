//go:build arm64 && !noasm

package enc

// bigValuesPrefixCost computes the big-values prefix costs of all 32 table
// slots in one pass with the NEON kernel (NEON is baseline on arm64, so no
// runtime feature check). The result is identical to bigValuesPrefixCostGo
// because a pairCostRows lookup replaces the reference's per-table costing: a
// magnitude above 15 uses the clamped-to-15 row, which is exact for every table
// that can represent it (prefixcost_kernel.go), and TestPrefixCostParity,
// TestPrefixCostBounds and FuzzPrefixCostParity check it against the
// reference. Before a kernel runs it panics on a layout the kernel could not
// stay inside (checkPrefixCostLayout), as the Go reference does.
func bigValuesPrefixCost(ix *[576]int32, pb *[40]int, lay *bandLayout, prefixCost *[40][32]int32) {
	checkPrefixCostLayout(ix, pb, lay.nBands)
	prefixCostNEON(ix, pb, lay.nBands, prefixCost, &pairCostRows, &pairCostBound)
}

// prefixCostNEON keeps the 32 running sums in eight 4-lane accumulators, adds
// one pairCostRows row per pair and stores each band's sums into
// prefixCost[k+1].
//
//go:noescape
func prefixCostNEON(ix *[576]int32, pb *[40]int, nBands int, prefixCost *[40][32]int32, rows *[256][32]int32, bound *[32]int32)
