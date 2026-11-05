//go:build noasm || (!amd64 && !arm64)

package enc

// mdctGranuleWindowed runs mdctGranuleWindowedGo on the noasm build and on any
// architecture without a kernel.
func mdctGranuleWindowed(prev, cur *[18][32]float64, xr *[576]float64, window *[36]float64) {
	mdctGranuleWindowedGo(prev, cur, xr, window)
}
