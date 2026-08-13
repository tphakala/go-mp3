package enc

import (
	"math"
	"testing"
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
