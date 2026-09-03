//go:build amd64 && !noasm

package enc

import "golang.org/x/sys/cpu"

var hasAVX2 = cpu.X86.HasAVX2

// subMinReduce returns min over i of a[i]-b[i]. On amd64 it uses the fused AVX2
// kernel when the CPU has AVX2 (default GOAMD64=v1 does not guarantee it, so the
// check is at runtime) and the input has at least one full 8-lane block, else
// the pure-Go reference. Built into the default (SIMD) build; the noasm tag
// selects the pure-Go dispatcher in rangecost_kernel_fallback.go instead.
func subMinReduce(a, b []int32) int32 {
	if hasAVX2 && len(a) >= 8 {
		return subMinReduceAVX2(a, b)
	}
	return subMinReduceGo(a, b)
}

//go:noescape
func subMinReduceAVX2(a, b []int32) int32
