package enc

import (
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// TestPackScf requires packScf to give different keys to scalefactor states
// that differ in any one bit of any one field, from the all-zero state and
// from the state with every field at its largest packable value (where a field
// shifted onto its neighbour would collide), and to refuse a state with a
// field outside its width.
func TestPackScf(t *testing.T) {
	// Every field at its largest packable value.
	var full scfState
	for i := range full.scf {
		full.scf[i] = 15
	}
	full.subblockGain = [3]int{15, 15, 15}
	full.scalefacScale, full.preflag = 1, 1

	for _, base := range []scfState{{}, full} {
		baseKey, ok := packScf(&base)
		if !ok {
			t.Fatalf("packScf refused an in-range state %+v", base)
		}
		check := func(name string, sf scfState) {
			t.Helper()
			k, ok := packScf(&sf)
			if !ok {
				t.Fatalf("%s: packScf refused an in-range state", name)
			}
			if k == baseKey {
				t.Fatalf("%s: a state differing in that field has the same key", name)
			}
		}
		// Each bit of each 4-bit field on its own.
		for bit := range 4 {
			for i := range base.scf {
				sf := base
				sf.scf[i] ^= 1 << bit
				check("scf", sf)
			}
			for i := range base.subblockGain {
				sf := base
				sf.subblockGain[i] ^= 1 << bit
				check("subblockGain", sf)
			}
		}
		sf := base
		sf.scalefacScale ^= 1
		check("scalefacScale", sf)
		sf = base
		sf.preflag ^= 1
		check("preflag", sf)
	}

	for name, mutate := range map[string]func(*scfState){
		"scf 16":           func(sf *scfState) { sf.scf[35] = 16 },
		"scf -1":           func(sf *scfState) { sf.scf[0] = -1 },
		"subblockGain 16":  func(sf *scfState) { sf.subblockGain[2] = 16 },
		"subblockGain -1":  func(sf *scfState) { sf.subblockGain[0] = -1 },
		"scalefacScale 2":  func(sf *scfState) { sf.scalefacScale = 2 },
		"preflag 3":        func(sf *scfState) { sf.preflag = 3 },
		"scalefacScale -1": func(sf *scfState) { sf.scalefacScale = -1 },
	} {
		var sf scfState
		mutate(&sf)
		if _, ok := packScf(&sf); ok {
			t.Errorf("%s: packScf accepted an out-of-range field", name)
		}
	}
}

// TestCodingMemoRoundTrip requires the memo to hold one entry per outerLoop
// iteration, to return each entry's own noise (the oldest and the newest
// included), to miss an absent key, to drop a put on a full table without
// disturbing it, and to be empty again after begin.
func TestCodingMemoRoundTrip(t *testing.T) {
	var xr [576]float64
	lay := &layoutLong[0]
	memo := new(codingMemo)
	memo.begin(&xr, lay)
	keyOf := func(i int) scfKey {
		var sf scfState
		sf.scf[0], sf.scf[20], sf.scf[35] = i&15, i>>4, 15-i&15
		k, _ := packScf(&sf)
		return k
	}
	var noise, got [39]float64
	for i := range outerLoopMaxIters {
		k := keyOf(i)
		if memo.get(&k, &got) {
			t.Fatalf("entry %d found before it was put", i)
		}
		noise[0], noise[38] = float64(i), float64(-i)
		memo.put(&k, &noise)
	}
	extra := keyOf(outerLoopMaxIters)
	memo.put(&extra, &noise)
	if memo.get(&extra, &got) {
		t.Fatal("a put on a full table was stored")
	}
	for i := range outerLoopMaxIters {
		k := keyOf(i)
		if !memo.get(&k, &got) || got[0] != float64(i) || got[38] != float64(-i) {
			t.Fatalf("entry %d of %d: not found, or noise %v, %v is not its own", i, outerLoopMaxIters, got[0], got[38])
		}
	}
	memo.begin(&xr, lay)
	first := keyOf(0)
	if memo.get(&first, &got) {
		t.Fatal("an entry survived begin")
	}
}

// memoCase is one deterministic outerLoop input.
type memoCase struct {
	xr        [576]float64
	xmin      [39]float64
	budget    int
	lay       *bandLayout
	blockType int
}

// memoDenseCase builds a broadband granule with a spectral tilt and per-band
// thresholds 5 to 40 dB under the band energy, on a long layout, a short
// layout, or a long-typed granule over a short layout (39 coding bands), at a
// budget anywhere from starved to generous. Most of these runs amplify many
// bands, roll back, and revisit scalefactor states.
func memoDenseCase(seed uint64) (c memoCase) {
	s := seed*0x9E3779B97F4A7C15 + 1
	r := func() float64 { return testsignal.LCG(&s) }
	layoutBT := blockLong
	switch k := r(); {
	case k < 0.25:
		c.blockType, layoutBT = blockShort, blockShort
	case k < 0.35:
		layoutBT = blockShort // blockLong over the short layout: nBands 39
	}
	c.lay = layoutFor(layoutBT, int(r()*3))
	amp := 100 * (1 + 400*r()*r())
	tilt := r() * 3
	for i := range c.xr {
		c.xr[i] = testsignal.LCGSigned(&s) * amp / (1 + tilt*float64(i)/576)
	}
	smr := 5 + 35*r()
	off := 0
	for b := range c.lay.nBands {
		e := 0.0
		for i := off; i < off+c.lay.width[b]; i++ {
			e += c.xr[i] * c.xr[i]
		}
		off += c.lay.width[b]
		c.xmin[b] = e
		for range int((smr + 15*(r()-0.5)) / 3) {
			c.xmin[b] /= 2
		}
	}
	c.budget = 300 + int(r()*3800)
	return c
}

// memoCapCase builds a long granule whose only demanding bands are a few
// upper ones (scalefactor cap 7) with thresholds no amplification reaches.
// They climb to the cap together, so the scalefac_scale switch is tried for
// one band, found futile and rolled back, and tried again for another band:
// the same scalefactor state, the second time served from the memo and, when
// it helps that band, accepted as the best pass. That is the path on which
// outerLoop codes a state again because its coding is needed.
func memoCapCase(seed uint64) (c memoCase) {
	s := seed*0x9E3779B97F4A7C15 + 7
	r := func() float64 { return testsignal.LCG(&s) }
	c.lay = &layoutLong[int(r()*3)]
	amp := 20 * (1 + 20*r())
	for i := range c.xr {
		c.xr[i] = testsignal.LCGSigned(&s) * amp
	}
	for b := range c.xmin {
		c.xmin[b] = 1e30
	}
	for range 3 + int(r()*5) {
		c.xmin[11+int(r()*10)] = 1e-3 * (1 + 1000*r())
	}
	c.budget = 1500 + int(r()*2600)
	return c
}

// memoMatchesRecompute runs outerLoop on c with memo and without one and
// fails unless both return the same coding and iteration count. memo keeps
// the first run's counters.
func memoMatchesRecompute(t *testing.T, name string, seed uint64, c *memoCase, qc *quantCache, memo *codingMemo) {
	t.Helper()
	var got, gotBest, want, wantBest granuleCoding
	got.blockType, want.blockType = c.blockType, c.blockType
	gotIters := outerLoop(&c.xr, &c.xmin, c.budget, c.lay, &got, &gotBest, qc, memo)
	wantIters := outerLoop(&c.xr, &c.xmin, c.budget, c.lay, &want, &wantBest, qc, nil)
	if got != want || gotIters != wantIters {
		t.Fatalf("%s seed %d: with the memo iters=%d gain=%d part23=%d, without iters=%d gain=%d part23=%d (ix equal: %v, sf equal: %v)",
			name, seed, gotIters, got.globalGain, got.part23Length, wantIters, want.globalGain, want.part23Length, got.ix == want.ix, got.sf == want.sf)
	}
}

// TestOuterLoopMemoMatchesRecompute requires outerLoop with a coding memo to
// return the same coding and iteration count as outerLoop without one, on
// granules that revisit scalefactor states. The memo and the quantization
// cache are shared by every case, as the Encoder shares them between
// granule-channels and frames.
func TestOuterLoopMemoMatchesRecompute(t *testing.T) {
	memo, qc := new(codingMemo), new(quantCache)
	for seed := uint64(1); seed <= 600; seed++ {
		c := memoDenseCase(seed)
		memoMatchesRecompute(t, "dense", seed, &c, qc, memo)
	}
}

// memoRecodeSeeds are memoCapCase seeds whose run accepts a state served from
// the memo as its final best pass while gc still holds a different state's
// lines, so the result is right only if outerLoop codes the accepted state
// again. They were found by removing that recode and keeping the seeds whose
// result then changed; a change to the amplification rule needs them found
// again the same way.
var memoRecodeSeeds = []uint64{46920, 58386, 63553, 63824, 68783, 76137, 116283}

// TestOuterLoopMemoRecodesAcceptedState requires the coding outerLoop returns
// to be the coding of the winning scalefactor state when that state was
// served from the memo on the iteration that accepted it.
func TestOuterLoopMemoRecodesAcceptedState(t *testing.T) {
	memo, qc := new(codingMemo), new(quantCache)
	for _, seed := range memoRecodeSeeds {
		c := memoCapCase(seed)
		memoMatchesRecompute(t, "cap", seed, &c, qc, memo)
		if memo.codings == memo.n {
			t.Fatalf("cap seed %d: no state was coded again; the case no longer reaches the path under test", seed)
		}
	}
}

// TestOuterLoopMemoCodesEachStateOnce requires an outerLoop call with a memo
// to run the rate loop once per distinct scalefactor state on granules that
// revisit states: no two entries hold the same state (a revisit that missed
// would add a second one), and the call makes exactly one codeGranule call per
// entry (none of these cases accepts a recalled pass, so none codes a state
// again). The cases must revisit states, or the property would be empty.
func TestOuterLoopMemoCodesEachStateOnce(t *testing.T) {
	memo, qc := new(codingMemo), new(quantCache)
	iters, states := 0, 0
	for seed := uint64(1); seed <= 200; seed++ {
		c := memoDenseCase(seed)
		var gc, best granuleCoding
		gc.blockType = c.blockType
		iters += outerLoop(&c.xr, &c.xmin, c.budget, c.lay, &gc, &best, qc, memo)
		states += memo.n
		if memo.codings != memo.n {
			t.Fatalf("seed %d: %d codeGranule calls for %d distinct states", seed, memo.codings, memo.n)
		}
		for i := range memo.n {
			for j := range i {
				if memo.key[i] == memo.key[j] {
					t.Fatalf("seed %d: entries %d and %d hold the same scalefactor state", seed, j, i)
				}
			}
		}
	}
	if states == 0 || states > iters*9/10 {
		t.Fatalf("%d distinct states in %d iterations: the cases no longer revisit enough states", states, iters)
	}
}
