//go:build amd64 && !noasm

package enc

// bigValuesPrefixCost computes the big-values prefix costs of all 32 table
// slots in one pass. On amd64 it uses the fused AVX2 kernel when the CPU has
// AVX2 (default GOAMD64=v1 does not guarantee it, so the check is at runtime,
// sharing hasAVX2 with the region-cost kernel); otherwise it falls back to
// bigValuesPrefixCostGo. The result is identical to the reference because a
// pairCostRows lookup replaces the reference's per-table costing: a magnitude
// above 15 uses the clamped-to-15 row, which is exact for every table that can
// represent it (prefixcost_kernel.go), and TestPrefixCostParity,
// TestPrefixCostBounds and FuzzPrefixCostParity check it against the
// reference. Before a kernel runs it panics on a layout the kernel could not
// stay inside (checkPrefixCostLayout), as the Go reference does.
func bigValuesPrefixCost(ix *[576]int32, pb *[40]int, lay *bandLayout, prefixCost *[40][32]int32) {
	if hasAVX2 {
		checkPrefixCostLayout(ix, pb, lay.nBands)
		prefixCostAVX2(ix, pb, lay.nBands, prefixCost, &pairCostRows, &pairCostBound)
		return
	}
	bigValuesPrefixCostGo(ix, pb, lay, prefixCost)
}

// prefixCostAVX2 keeps the 32 running sums in four YMM accumulators, adds one
// pairCostRows row per pair and stores each band's sums into prefixCost[k+1].
//
//go:noescape
func prefixCostAVX2(ix *[576]int32, pb *[40]int, nBands int, prefixCost *[40][32]int32, rows *[256][32]int32, bound *[32]int32)
