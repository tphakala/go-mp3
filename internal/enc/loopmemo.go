package enc

// codingMemo remembers, for one outerLoop call, the per-band noise of every
// scalefactor state the call has coded. outerLoop revisits states (every
// futility rollback returns to the best pass, and the steps that follow can
// retrace earlier ones), and within one call the spectrum, the layout, the
// block type and the bit budget are fixed, so the coding, and with it the
// noise, is a function of the scalefactor state alone: a revisit can take its
// noise from here instead of running the rate loop and the noise measurement
// again.
//
// An entry holds no coding. outerLoop needs the granuleCoding of a revisited
// state only when that pass becomes its best pass or ends the loop, and then
// recode codes it again.
//
// Lifetime: one outerLoop call, like quantCache. begin runs on entry to
// outerLoop and drops every entry; codeOrRecall panics if the memo is not
// bound to the spectrum and layout it is asked about.
//
// A nil *codingMemo is valid and remembers nothing.
//
// It is about 50 KB and lives by value in the Encoder (allocated once, so the
// steady-state encode stays allocation-free).
type codingMemo struct {
	xr    *[576]float64
	lay   *bandLayout
	n     int // entries in use
	key   [outerLoopMaxIters]scfKey
	noise [outerLoopMaxIters][39]float64

	// codings counts the codeGranule calls made through the memo since
	// begin: one per state coded, plus one per recode. Nothing in the encoder
	// reads it; tests compare it with n to see what the memo saved.
	codings int
}

// scfKey is a scalefactor state packed by packScf.
type scfKey [3]uint64

// packScf packs sf into a key that is equal for two states exactly when the
// states are equal: 4 bits per scalefactor (36 of them, 16 to a word), then in
// the last word 4 bits per subblock_gain and one bit each for scalefac_scale
// and preflag. ok is false when a field does not fit its width, in which case
// the caller must not use the key; outerLoop only produces scalefactors up to
// 15 and subblock gains up to 7, so that is a backstop, not an expected path.
func packScf(sf *scfState) (k scfKey, ok bool) {
	over := (uint(sf.scalefacScale) | uint(sf.preflag)) >> 1
	for i, v := range sf.scf {
		over |= uint(v) >> 4
		k[i>>4] |= uint64(v) << (4 * (i & 15))
	}
	for i, v := range sf.subblockGain {
		over |= uint(v) >> 4
		k[2] |= uint64(v) << (16 + 4*i)
	}
	k[2] |= uint64(sf.scalefacScale)<<28 | uint64(sf.preflag)<<29
	return k, over == 0
}

// begin binds the memo to xr and lay and drops every entry.
func (m *codingMemo) begin(xr *[576]float64, lay *bandLayout) {
	if m == nil {
		return
	}
	m.xr, m.lay, m.n, m.codings = xr, lay, 0, 0
}

// check panics if the memo is not bound to xr and lay: noise recorded for
// another spectrum would silently change the output.
func (m *codingMemo) check(xr *[576]float64, lay *bandLayout) {
	if m.xr != xr || m.lay != lay {
		panic("enc: codingMemo used with a spectrum or layout it was not begun for")
	}
}

// codeOrRecall produces the per-band noise of gc.sf's coding for one
// outerLoop iteration. A state the memo holds is recalled: noise is copied out,
// gc is left as it was, and the result is true. Any other state is coded into
// gc with codeGranule, measured with noiseGranule and recorded. A scalefactor
// state packScf cannot pack is coded and not recorded, and so is every state
// when m is nil.
func (m *codingMemo) codeOrRecall(xr *[576]float64, huffBudget int, lay *bandLayout, gc *granuleCoding, qc *quantCache, noise *[39]float64) (recalled bool) {
	var key scfKey
	keyOK := false
	if m != nil {
		m.check(xr, lay)
		if key, keyOK = packScf(&gc.sf); keyOK && m.get(&key, noise) {
			return true
		}
		m.codings++
	}
	codeGranule(xr, huffBudget, lay, gc, qc)
	noiseGranule(xr, &gc.ix, gc.globalGain, &gc.sf, lay, noise)
	if keyOK {
		m.put(&key, noise)
	}
	return false
}

// get looks up k and, when the memo holds it, copies its per-band noise into
// noise.
func (m *codingMemo) get(k *scfKey, noise *[39]float64) bool {
	for i := range m.n {
		if m.key[i] == *k {
			*noise = m.noise[i]
			return true
		}
	}
	return false
}

// put records noise for k. The table has one slot per outerLoop iteration and
// outerLoop makes at most one put per iteration, so none of its puts finds the
// table full (it can be full after the last one). The guard keeps put safe for
// any other caller; a dropped entry only costs a later hit.
func (m *codingMemo) put(k *scfKey, noise *[39]float64) {
	if m.n == len(m.key) {
		return
	}
	m.key[m.n], m.noise[m.n] = *k, *noise
	m.n++
}

// recode codes gc.sf into gc for an iteration whose noise codeOrRecall
// recalled. It is the codeGranule call the iteration that first coded the
// state made, with the same spectrum, budget and scalefactor state, so it
// yields the same coding.
func (m *codingMemo) recode(xr *[576]float64, huffBudget int, lay *bandLayout, gc *granuleCoding, qc *quantCache) {
	m.codings++
	codeGranule(xr, huffBudget, lay, gc, qc)
}
