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
// It also holds each band's largest |xr| and a memo of each band's minimum
// gain per amplification (minGain), and the answer of an earlier bound search
// (hint). The first two depend only on xr and lay, so reset refills and clears
// them with the rows; the hint is not one of the entries and survives reset.
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

	// bandMax is each band's largest |xr|, and minGainOf[b][e] band b's
	// bandMinGain at e extra quarter-steps where minGainValid has bit e set:
	// minGain's memo. Both depend only on xr and lay, so reset refills the one
	// and clears the other.
	bandMax      [39]float64
	minGainOf    [39][maxExtraQuarters + 1]uint8
	minGainValid [39][(maxExtraQuarters + 64) / 64]uint64

	// hint is the answer of the last firstAdmissibleGain search that ran past
	// its lower end (a search answered at the lower end leaves it as it was).
	// It only chooses where the next search probes first, never the result, so
	// it survives reset.
	hint int
}

// reset binds the cache to xr and lay and drops every entry. It keeps hint.
func (c *quantCache) reset(xr *[576]float64, lay *bandLayout) {
	c.xr, c.lay = xr, lay
	c.valid = [39][(qSpan + 63) / 64]uint64{}
	bandMaxima(xr, lay, &c.bandMax)
	c.minGainValid = [39][(maxExtraQuarters + 64) / 64]uint64{}
}

// minGain is minGlobalGain(c.xr, sf, c.lay), with each band's own minimum
// gain remembered per amplification: across the iterations of one outerLoop
// call the scalefactor state changes a few bands at a time, so most bands ask
// for an amplification they have asked for before. lay must be the layout the
// cache was reset for (codeGranule checks it first).
func (c *quantCache) minGain(sf *scfState, lay *bandLayout) int {
	gg := 0
	for b := range lay.nBands {
		e := sf.bandExtraQuarters(b, lay)
		if uint(e) > maxExtraQuarters {
			// Outside what outerLoop produces: no memo slot.
			gg = max(gg, bandMinGain(c.bandMax[b], e))
			continue
		}
		word, bit := e>>6, uint64(1)<<(e&63)
		if c.minGainValid[b][word]&bit == 0 {
			c.minGainOf[b][e] = uint8(bandMinGain(c.bandMax[b], e))
			c.minGainValid[b][word] |= bit
		}
		gg = max(gg, int(c.minGainOf[b][e]))
	}
	return gg
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
// gc.part, leaving gc.ri untouched: the quantize and partition half of a
// coding, served from the cache.
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
//
// hint (the answer of an earlier search) only picks where the probes start, so
// the answer is the same with any hint, including none.
func (c *quantCache) firstAdmissibleGain(gg0, effBudget int, q0 *[39]int, lay *bandLayout) int {
	if gg0 >= 255 || c.boundAt(gg0, q0, lay, effBudget) <= effBudget {
		return gg0
	}
	// The answer lies in [lo, hi]: lo-1 is rejected, hi is admitted (or is the
	// 255 sentinel). Consecutive searches usually land near each other, so
	// probe the hint first and gallop away from it before bisecting what is
	// left. The bound is monotone, so every probe narrows
	// the same interval a plain bisection would and the result is the same.
	lo, hi := gg0+1, 255
	if h := c.hint; h >= lo && h < hi {
		if c.boundAt(h, q0, lay, effBudget) <= effBudget {
			hi = h
			for step := 1; hi-step >= lo; step *= 2 {
				if c.boundAt(hi-step, q0, lay, effBudget) <= effBudget {
					hi -= step
				} else {
					lo = hi - step + 1
					break
				}
			}
		} else {
			lo = h + 1
			for step := 1; lo+step-1 < hi; step *= 2 {
				if p := lo + step - 1; c.boundAt(p, q0, lay, effBudget) <= effBudget {
					hi = p
					break
				} else {
					lo = p + 1
				}
			}
		}
	}
	for lo < hi {
		mid := (lo + hi) / 2
		if c.boundAt(mid, q0, lay, effBudget) <= effBudget {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	c.hint = lo
	return lo
}
