package enc

// Stereo representation indexes for the four-way psymodel bank.
const (
	repL = 0
	repR = 1
	repM = 2
	repS = 3
)

// msPeMarginBits is the decision's static bias toward L/R (design
// decision 4; stateless, not hysteresis): M/S is
// chosen only when its summed PE undercuts L/R's by at least this many
// bits per frame.
const msPeMarginBits = 16.0

// butterflyWindows fills the M and S analysis windows from the L and R
// windows: m[i] = (l[i]+r[i])*sqrtHalf, s[i] = (l[i]-r[i])*sqrtHalf. The
// sum/difference happens first and the sqrtHalf product is stored, never
// fed into an addition, so no float64() wrap is needed (and adding one
// would not change the bits).
func butterflyWindows(l, r, m, s *[1024]float64) {
	for i := range l {
		m[i] = (l[i] + r[i]) * sqrtHalf
		s[i] = (l[i] - r[i]) * sqrtHalf
	}
}

// butterflyXr fills xrM/xrS from the two channels' MDCT spectra, the
// coding-path butterfly (same arithmetic shape as butterflyWindows).
func butterflyXr(xrL, xrR, xrM, xrS *[576]float64) {
	for i := range xrL {
		xrM[i] = (xrL[i] + xrR[i]) * sqrtHalf
		xrS[i] = (xrL[i] - xrR[i]) * sqrtHalf
	}
}

// msDecide reports whether the frame codes M/S: true when
// peM+peS + msPeMarginBits <= peL+peR, each summed over both granules.
// Inc7 seam (design decision 8): the block-switch increment adds a veto
// here for frames whose channels disagree on block type.
func msDecide(peL, peR, peM, peS float64) bool {
	return peM+peS+msPeMarginBits <= peL+peR
}
