//go:build arm64 && !noasm

package enc

// subMinReduce returns min over i of a[i]-b[i]. On arm64 it uses the fused NEON
// kernel (NEON is baseline on arm64, so no runtime feature check) when the input
// has at least one full 4-lane block, else the pure-Go reference. Built into the
// default (SIMD) build; the noasm tag selects the pure-Go dispatcher in
// rangecost_kernel_fallback.go instead.
func subMinReduce(a, b []int32) int32 {
	if len(a) >= 4 {
		return subMinReduceNEON(a, b)
	}
	return subMinReduceGo(a, b)
}

//go:noescape
func subMinReduceNEON(a, b []int32) int32
