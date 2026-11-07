package enc

// maxExtraQuarters is the largest bandExtraQuarters any scalefactor state can
// produce: 116 quarter-steps for short granules (see stepQTable's comment for
// the arithmetic), at most 72 for long ones. TestStepQTable pins it.
const maxExtraQuarters = 116

// qMin and qSpan bound the quantizer exponent q = (quantGainBase - gg) + extra
// that the exact scan can form: gg is 0..255 and extra 0..maxExtraQuarters, so
// q lies in [qMin, qMin+qSpan).
const (
	qMin  = quantGainBase - 255
	qSpan = 255 + maxExtraQuarters + 1
)

// quantCache holds, for one granule-channel's spectrum and band layout, every
// band's quantization at every quantizer exponent q the exact global_gain scan
// has asked for, plus the monotone bit floor (monoPairFloor summed over the
// band's pairs) of each. The exact scan quantizes the whole spectrum at every
// gain from minGlobalGain up to the first fit, and across the iterations of
// one outerLoop call it revisits the same (band, q) states over and over (the
// scalefactor state changes only a few bands per iteration, and a band's
// quantization depends only on its lines and q), so serving them from here
// replaces most quantizer work with copies.
//
// Keys are (band, q), never the global_gain or the scalefactor state: two gains
// and two scalefactor states that yield the same exponent for a band yield the
// same lines for it. A band's lines depend only on its own xr lines and q, not
// on neighbouring bands: quantizeBandGo is the per-band body of
// quantizeGranuleGo, and QuantizePow34Bands keeps no state across bands, so
// quantizeBand reproduces quantizeGranule's lines exactly.
//
// Lifetime: valid for one outerLoop call. xr is read-only inside it, but the
// Encoder reuses the same xr array with new contents every frame, which a
// pointer comparison cannot detect, so the cache is invalidated explicitly:
// reset runs on entry to outerLoop and at every direct codeGranule call site.
// A cache used with a different xr or lay than the one it was reset for
// panics.
//
// It is about 0.9 MB and lives by value in the Encoder (allocated once, so the
// steady-state encode stays allocation-free).
type quantCache struct {
	xr    *[576]float64
	lay   *bandLayout
	valid [39][(qSpan + 63) / 64]uint64
	bound [39][qSpan]uint16
	ix    [qSpan][576]int32
}

// reset binds the cache to xr and lay and drops every entry.
func (c *quantCache) reset(xr *[576]float64, lay *bandLayout) {
	c.xr, c.lay = xr, lay
	c.valid = [39][(qSpan + 63) / 64]uint64{}
}

// check panics if the cache is not bound to xr and lay. codeGranule calls it
// before any cached read, because entries computed from another spectrum
// would silently change the output.
func (c *quantCache) check(xr *[576]float64, lay *bandLayout) {
	if c.xr != xr || c.lay != lay {
		panic("enc: quantCache used with a spectrum or layout it was not reset for")
	}
}

// ensure fills band b (lines [off, off+w)) at exponent index k = q - qMin if
// it is not cached yet.
func (c *quantCache) ensure(b, off, w, k int) {
	word, bit := k>>6, uint64(1)<<(k&63)
	if c.valid[b][word]&bit != 0 {
		return
	}
	row := c.ix[k][off : off+w]
	quantizeBand(row, c.xr[off:off+w], stepQ(k+qMin))
	sum := 0
	for i := 0; i < w; i += 2 {
		sum += int(monoPairFloor[magClassOf(abs32(row[i]))][magClassOf(abs32(row[i+1]))])
	}
	c.bound[b][k] = uint16(sum)
	c.valid[b][word] |= bit
}

// boundAt returns the monotone lower bound on the part3 bits of the spectrum
// quantized at gg: the sum over bands of their floor sums. q0[b] is
// quantGainBase plus band b's extra quarter-steps, so band b's exponent at gg
// is q0[b]-gg. Every band floor is non-negative, so it stops filling bands as
// soon as the running sum exceeds limit and returns that partial sum; callers
// only compare the result with limit.
func (c *quantCache) boundAt(gg int, q0 *[39]int, lay *bandLayout, limit int) int {
	sum, off := 0, 0
	for b := range lay.nBands {
		w := lay.width[b]
		k := q0[b] - gg - qMin
		c.ensure(b, off, w, k)
		sum += int(c.bound[b][k])
		if sum > limit {
			return sum
		}
		off += w
	}
	return sum
}

// codeAt quantizes the spectrum at gg into gc.ix from the cache and sets
// gc.part, leaving gc.ri untouched. It is the quantize and partition half of recode, served from the cache.
func (c *quantCache) codeAt(gg int, q0 *[39]int, lay *bandLayout, gc *granuleCoding) {
	off := 0
	for b := range lay.nBands {
		w := lay.width[b]
		k := q0[b] - gg - qMin
		c.ensure(b, off, w, k)
		copy(gc.ix[off:off+w], c.ix[k][off:off+w])
		off += w
	}
	gc.part = partitionSpectrum(&gc.ix)
}

// firstAdmissibleGain returns the smallest gg in [gg0, 254] whose bound
// boundAt is at most effBudget, else 255. No gain below it can fit: a coding
// costs at least the bound, which exceeds the budget there. The bound never
// increases with gg (monoPairFloor's doc comment), so a binary search finds
// the first admitted gain; 255 stands in as an always-admitted sentinel
// because the exact scan always codes it.
func (c *quantCache) firstAdmissibleGain(gg0, effBudget int, q0 *[39]int, lay *bandLayout) int {
	if gg0 >= 255 || c.boundAt(gg0, q0, lay, effBudget) <= effBudget {
		return gg0
	}
	lo, hi := gg0+1, 255
	for lo < hi {
		mid := (lo + hi) / 2
		if c.boundAt(mid, q0, lay, effBudget) <= effBudget {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}
