package quality

// alignSamples bounds how many reference samples AlignLag correlates over:
// plenty for codec delays, cheap enough for a few-thousand-lag search.
const alignSamples = 65536

// AlignLag returns the lag, in [minLag, maxLag], that maximizes the
// cross-correlation between ref and deg, where a positive lag means deg is
// delayed relative to ref (deg[i+lag] tracks ref[i]). The correlation runs
// over the first alignSamples reference samples (or fewer when the inputs
// are shorter); pairs that fall outside deg are skipped.
func AlignLag(ref, deg []float64, minLag, maxLag int) int {
	n := min(len(ref), alignSamples)
	bestLag, bestC := 0, -1.0
	for lag := minLag; lag <= maxLag; lag++ {
		lo := max(0, -lag)
		hi := min(n, len(deg)-lag)
		var c float64
		for i := lo; i < hi; i++ {
			c += ref[i] * deg[i+lag]
		}
		if c > bestC {
			bestC, bestLag = c, lag
		}
	}
	return bestLag
}
