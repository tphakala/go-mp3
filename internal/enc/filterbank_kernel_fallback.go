//go:build noasm || (!amd64 && !arm64)

package enc

// subbandKernel returns false on the noasm build and on any architecture
// without a kernel, so the caller runs the Go steps.
func subbandKernel(x *[512]float64, out *[32]float64) bool {
	return false
}
