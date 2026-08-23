package enc

import (
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/bits"
)

func TestPow43ExactSpots(t *testing.T) {
	// Perfect cubes make k^(4/3) an exact integer; the generator computes
	// k*cbrt(k), so these are bit-exact, not approximate.
	for _, c := range []struct {
		k    int
		want float64
	}{
		{0, 0}, {1, 1}, {8, 16}, {27, 81}, {64, 256},
		{1000, 10000}, {4096, 65536}, {8000, 160000},
	} {
		if pow43[c.k] != c.want {
			t.Errorf("pow43[%d] = %v, want exactly %v", c.k, pow43[c.k], c.want)
		}
	}
	if len(pow43) != maxQuant+1 {
		t.Fatalf("pow43 has %d entries, want %d", len(pow43), maxQuant+1)
	}
}

func TestPow43Recompute(t *testing.T) {
	for k := range pow43 {
		want := float64(k) * math.Cbrt(float64(k))
		if d := ulpDistance(pow43[k], want); d > 1 {
			t.Errorf("pow43[%d]: %d ulp from k*cbrt(k)", k, d)
		}
	}
	for k := 1; k < len(pow43); k++ {
		if pow43[k] <= pow43[k-1] {
			t.Fatalf("pow43 not strictly increasing at %d", k)
		}
	}
}

func TestSlenTabKnownAnswer(t *testing.T) {
	// ISO 2.4.2.7 slen pairs, transcribed; spot-checked here in full.
	want := [16][2]int{
		{0, 0}, {0, 1}, {0, 2}, {0, 3}, {3, 0}, {1, 1}, {1, 2}, {1, 3},
		{2, 1}, {2, 2}, {2, 3}, {3, 1}, {3, 2}, {3, 3}, {4, 2}, {4, 3},
	}
	if slenTab != want {
		t.Fatalf("slenTab = %v, want the ISO 2.4.2.7 table", slenTab)
	}
	if sfMaxLo != 15 || sfMaxHi != 7 {
		t.Fatalf("sfMax constants %d/%d, want 15/7 (2^4-1, 2^3-1)", sfMaxLo, sfMaxHi)
	}
}

func TestScfsiGroupsCoverage(t *testing.T) {
	// The four groups tile sfbs 0..20 contiguously.
	last := 0
	for g, r := range scfsiGroups {
		if r[0] != last {
			t.Fatalf("group %d starts at %d, want %d", g, r[0], last)
		}
		last = r[1]
	}
	if last != 21 {
		t.Fatalf("groups end at %d, want 21", last)
	}
}

// pow43SHA freezes the table (freeze-on-first-run, house pattern).
const pow43SHA = "4ba4d00f8029d4b5fd332368b8cf26b5d300c4c001eaa511cdaf8bc4d028b9d7" // FROZEN in Task 1 Step 4

func TestPow43Checksum(t *testing.T) {
	got := sha256Float64s(pow43[:]...)
	if pow43SHA == "" {
		t.Fatalf("FREEZE ME: const pow43SHA = %q", got)
	}
	if got != pow43SHA {
		t.Fatalf("pow43 changed: %s, frozen %s", got, pow43SHA)
	}
}

func TestChooseScalefacCompress(t *testing.T) {
	lay := &layoutLong[0]
	var sf scfState
	if idx, n, ok := chooseScalefacCompress(&sf, 0, lay); !ok || idx != 0 || n != 0 {
		t.Fatalf("all-zero scf: got (%d,%d,%v), want (0,0,true)", idx, n, ok)
	}
	sf.scf[0] = 1 // needs slen1 >= 1: cheapest covering is index 5 (1,1): 11+10=21? no:
	// slen2 may be 0 only via indexes 0 and 4; max hi is 0 here, so index 4
	// (3,0) costs 33, index 1 (0,1)... does not cover lo max 1. Walk the
	// table: covering means 2^slen1-1 >= maxLo AND 2^slen2-1 >= maxHi.
	// maxLo=1, maxHi=0: candidates: 4 (3,0) cost 33, 5 (1,1) cost 21,
	// 1 (0,1) does NOT cover lo. Cheapest with slen1>=1, slen2>=0:
	// index 5 (1,1) costs 11*1+10*1=21; index 4 costs 33. Want (5, 21).
	if idx, n, ok := chooseScalefacCompress(&sf, 0, lay); !ok || idx != 5 || n != 21 {
		t.Fatalf("maxLo=1: got (%d,%d,%v), want (5,21,true)", idx, n, ok)
	}
	sf.scf[0] = 15
	sf.scf[11] = 7 // needs slen1=4, slen2=3: only index 15 (4,3): 44+30=74
	if idx, n, ok := chooseScalefacCompress(&sf, 0, lay); !ok || idx != 15 || n != 74 {
		t.Fatalf("maxima: got (%d,%d,%v), want (15,74,true)", idx, n, ok)
	}
	sf.scf[0] = 16 // beyond sfMaxLo: no pair covers
	if _, _, ok := chooseScalefacCompress(&sf, 0, lay); ok {
		t.Fatal("scf 16 must not be coverable")
	}
	sf.scf[0] = 15
	// scfsi mask 0b1000 skips group 0 (sfbs 0..5): maxLo now over 6..10.
	if idx, n, ok := chooseScalefacCompress(&sf, 0b1000, lay); !ok || idx == 15 {
		t.Fatalf("masked group still drives selection: (%d,%d,%v)", idx, n, ok)
	}
}

func TestScfsiDetectApply(t *testing.T) {
	var g0, g1 granuleCoding
	for s := range g0.sf.scf {
		g0.sf.scf[s] = s % 4
		g1.sf.scf[s] = s % 4
	}
	g1.sf.scf[8] = 3 // break group 1 (sfbs 6..10)
	mask := detectScfsi(&g0, &g1)
	if mask != 0b1011 {
		t.Fatalf("mask = %04b, want 1011 (group 1 differs)", mask)
	}
	g1.sf.scalefacScale = 1
	if detectScfsi(&g0, &g1) != 0 {
		t.Fatal("scalefacScale mismatch must disable scfsi")
	}
	g1.sf.scalefacScale = 0
	g1.lay = &layoutLong[0]
	_, before, _ := chooseScalefacCompress(&g1.sf, 0, g1.lay)
	g1.part2Bits = before
	g1.part23Length = before + 100
	saved := applyScfsi(&g1, mask)
	if saved <= 0 || g1.part2Bits >= before || g1.part23Length != before+100-saved {
		t.Fatalf("applyScfsi: saved %d, part2 %d->%d, part23 %d",
			saved, before, g1.part2Bits, g1.part23Length)
	}
}

func TestWriteScalefactorsBitCount(t *testing.T) {
	var gc granuleCoding
	gc.lay = &layoutLong[0]
	for s := range gc.sf.scf {
		gc.sf.scf[s] = min(s, 7)
	}
	idx, nbits, ok := chooseScalefacCompress(&gc.sf, 0, gc.lay)
	if !ok {
		t.Fatal("cover failed")
	}
	gc.scfCompress, gc.part2Bits = idx, nbits
	w := bits.NewWriter(nil)
	if got := writeScalefactors(&w, &gc); got != nbits {
		t.Fatalf("wrote %d bits, counted %d", got, nbits)
	}
}

// TestChooseScalefacCompressShort checks chooseScalefacCompress against a
// short layout: no scfsi grouping (every one of the 36 scf-bearing bands
// contributes), the low/high split at lay.slen1End (18/18 at 44.1kHz), and
// caps sfMaxLo/sfMaxHi (15/7) same as long.
func TestChooseScalefacCompressShort(t *testing.T) {
	lay := &layoutShort[0]
	if lay.slen1End != 18 || lay.nScf != 36 {
		t.Fatalf("test setup: layoutShort[0] slen1End=%d nScf=%d, want 18/36", lay.slen1End, lay.nScf)
	}

	var sf scfState
	if idx, n, ok := chooseScalefacCompress(&sf, 0, lay); !ok || idx != 0 || n != 0 {
		t.Fatalf("all-zero short scf: got (%d,%d,%v), want (0,0,true)", idx, n, ok)
	}

	sf.scf[0] = 15 // low half max (band 0 < slen1End 18)
	sf.scf[18] = 7 // high half max (band 18 is the first band >= slen1End)
	// maxLo=15 needs slen1>=4 (cap 15); maxHi=7 needs slen2>=3 (cap 7):
	// only index 15 (4,3) covers both, ISO 2.4.2.7's slenTab.
	idx, n, ok := chooseScalefacCompress(&sf, 0, lay)
	if !ok || idx != 15 {
		t.Fatalf("short maxima: got (%d,%d,%v), want idx 15", idx, n, ok)
	}
	if wantBits := 18*4 + 18*3; n != wantBits {
		t.Fatalf("short maxima bits: got %d, want %d (18 lo bands * slen1=4 + 18 hi bands * slen2=3)", n, wantBits)
	}

	sf.scf[0] = 16 // beyond sfMaxLo: no pair covers
	if _, _, ok := chooseScalefacCompress(&sf, 0, lay); ok {
		t.Fatal("short scf 16 must not be coverable")
	}
}

// TestWriteScalefactorsShortOrder checks writeScalefactors, bit-level,
// against a short layout: all 36 scf-bearing bands in coding order (no
// scfsi masking), slen1 below lay.slen1End and slen2 at or above it.
func TestWriteScalefactorsShortOrder(t *testing.T) {
	var gc granuleCoding
	gc.lay = &layoutShort[0]
	for b := range gc.lay.nScf {
		gc.sf.scf[b] = b % 6 // stays well within both slen caps
	}

	idx, nbits, ok := chooseScalefacCompress(&gc.sf, 0, gc.lay)
	if !ok {
		t.Fatal("cover failed")
	}
	gc.scfCompress, gc.part2Bits = idx, nbits

	w := bits.NewWriter(nil)
	got := writeScalefactors(&w, &gc)
	if got != nbits {
		t.Fatalf("wrote %d bits, counted %d", got, nbits)
	}

	coded := w.Flush()
	r := bits.NewReader(coded)
	slen1, slen2 := slenTab[idx][0], slenTab[idx][1]
	for b := range gc.lay.nScf {
		slen := slen1
		if b >= gc.lay.slen1End {
			slen = slen2
		}
		if slen == 0 {
			continue
		}
		if v := r.Bits(slen); int(v) != gc.sf.scf[b] {
			t.Errorf("band %d: read %d, want %d (coding order, slen split at %d)", b, v, gc.sf.scf[b], gc.lay.slen1End)
		}
	}
}

// TestDetectScfsiShortGuard checks design decision 6: detectScfsi returns
// 0 (scfsi never applies) unless BOTH granules are blockLong, regardless
// of how well their scf values otherwise agree.
func TestDetectScfsiShortGuard(t *testing.T) {
	var g0, g1 granuleCoding
	g0.blockType, g1.blockType = blockLong, blockLong
	for s := range g0.sf.scf {
		g0.sf.scf[s] = s % 4
		g1.sf.scf[s] = s % 4
	}
	if mask := detectScfsi(&g0, &g1); mask != 0b1111 {
		t.Fatalf("both blockLong, identical scf: mask = %04b, want 1111", mask)
	}

	g1.blockType = blockShort
	if mask := detectScfsi(&g0, &g1); mask != 0 {
		t.Fatalf("g1 blockShort: mask = %04b, want 0 (decision 6 guard)", mask)
	}

	g0.blockType, g1.blockType = blockShort, blockShort
	if mask := detectScfsi(&g0, &g1); mask != 0 {
		t.Fatalf("both blockShort: mask = %04b, want 0 (scfsi never applies to short granules)", mask)
	}
}
