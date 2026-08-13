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
	var sf scfState
	if idx, n, ok := chooseScalefacCompress(&sf, 0); !ok || idx != 0 || n != 0 {
		t.Fatalf("all-zero scf: got (%d,%d,%v), want (0,0,true)", idx, n, ok)
	}
	sf.scf[0] = 1 // needs slen1 >= 1: cheapest covering is index 5 (1,1): 11+10=21? no:
	// slen2 may be 0 only via indexes 0 and 4; max hi is 0 here, so index 4
	// (3,0) costs 33, index 1 (0,1)... does not cover lo max 1. Walk the
	// table: covering means 2^slen1-1 >= maxLo AND 2^slen2-1 >= maxHi.
	// maxLo=1, maxHi=0: candidates: 4 (3,0) cost 33, 5 (1,1) cost 21,
	// 1 (0,1) does NOT cover lo. Cheapest with slen1>=1, slen2>=0:
	// index 5 (1,1) costs 11*1+10*1=21; index 4 costs 33. Want (5, 21).
	if idx, n, ok := chooseScalefacCompress(&sf, 0); !ok || idx != 5 || n != 21 {
		t.Fatalf("maxLo=1: got (%d,%d,%v), want (5,21,true)", idx, n, ok)
	}
	sf.scf[0] = 15
	sf.scf[11] = 7 // needs slen1=4, slen2=3: only index 15 (4,3): 44+30=74
	if idx, n, ok := chooseScalefacCompress(&sf, 0); !ok || idx != 15 || n != 74 {
		t.Fatalf("maxima: got (%d,%d,%v), want (15,74,true)", idx, n, ok)
	}
	sf.scf[0] = 16 // beyond sfMaxLo: no pair covers
	if _, _, ok := chooseScalefacCompress(&sf, 0); ok {
		t.Fatal("scf 16 must not be coverable")
	}
	sf.scf[0] = 15
	// scfsi mask 0b1000 skips group 0 (sfbs 0..5): maxLo now over 6..10.
	if idx, n, ok := chooseScalefacCompress(&sf, 0b1000); !ok || idx == 15 {
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
	_, before, _ := chooseScalefacCompress(&g1.sf, 0)
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
	for s := range gc.sf.scf {
		gc.sf.scf[s] = min(s, 7)
	}
	idx, nbits, ok := chooseScalefacCompress(&gc.sf, 0)
	if !ok {
		t.Fatal("cover failed")
	}
	gc.scfCompress, gc.part2Bits = idx, nbits
	w := bits.NewWriter(nil)
	if got := writeScalefactors(&w, &gc); got != nbits {
		t.Fatalf("wrote %d bits, counted %d", got, nbits)
	}
}
