//go:build noasm || (!amd64 && !arm64)

package enc

// bigValuesPrefixCost falls back to bigValuesPrefixCostGo on the noasm build
// and on any architecture without a kernel.
func bigValuesPrefixCost(ix *[576]int32, pb *[40]int, lay *bandLayout, prefixCost *[40][32]int32) {
	bigValuesPrefixCostGo(ix, pb, lay, prefixCost)
}
