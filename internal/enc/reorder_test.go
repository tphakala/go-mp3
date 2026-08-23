package enc

import (
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// TestBandLayouts confirms layoutLong reproduces today's long-block
// geometry exactly (nothing existing changes: widths equal sfbWidthsLong,
// nBands 22, nScf 21, slen1End 11, every band's win -1), and that
// layoutShort has the short-block shape decision 4 in the task brief
// specifies: 39 bands, 36 scalefactor-carrying, an 18-band slen1 split,
// widths summing to 576 (192 lines times 3 windows), and a win pattern of
// 0,1,2 repeating (band b = 3*sfb+w carries window w).
func TestBandLayouts(t *testing.T) {
	for r := range 3 {
		long := layoutLong[r]
		if long.nBands != 22 || long.nScf != 21 || long.slen1End != 11 || long.short {
			t.Fatalf("rate %d: layoutLong = %+v, want nBands=22 nScf=21 slen1End=11 short=false", r, long)
		}
		for sfb := range 22 {
			if long.width[sfb] != sfbWidthsLong[r][sfb] {
				t.Fatalf("rate %d: layoutLong.width[%d] = %d, want %d", r, sfb, long.width[sfb], sfbWidthsLong[r][sfb])
			}
			if long.win[sfb] != -1 {
				t.Fatalf("rate %d: layoutLong.win[%d] = %d, want -1", r, sfb, long.win[sfb])
			}
		}

		short := layoutShort[r]
		if short.nBands != 39 || short.nScf != 36 || short.slen1End != 18 || !short.short {
			t.Fatalf("rate %d: layoutShort = %+v, want nBands=39 nScf=36 slen1End=18 short=true", r, short)
		}
		sum := 0
		for b := range 39 {
			sum += short.width[b]
			wantWin := int8(b % 3)
			if short.win[b] != wantWin {
				t.Fatalf("rate %d: layoutShort.win[%d] = %d, want %d", r, b, short.win[b], wantWin)
			}
			wantWidth := sfbWidthsShort[r][b/3]
			if short.width[b] != wantWidth {
				t.Fatalf("rate %d: layoutShort.width[%d] = %d, want %d", r, b, short.width[b], wantWidth)
			}
		}
		if sum != 576 {
			t.Fatalf("rate %d: layoutShort widths sum to %d, want 576", r, sum)
		}
	}
}

// TestLayoutFor confirms layoutFor dispatches on block type: blockShort
// picks layoutShort, every other block type (including the start/stop
// transition types, which keep the full long-window scalefactor geometry)
// picks layoutLong.
func TestLayoutFor(t *testing.T) {
	for r := range 3 {
		if got := layoutFor(blockShort, r); got != &layoutShort[r] {
			t.Fatalf("rate %d: layoutFor(blockShort, ...) = %p, want %p", r, got, &layoutShort[r])
		}
		for _, bt := range []int{blockLong, blockStart, blockStop} {
			if got := layoutFor(bt, r); got != &layoutLong[r] {
				t.Fatalf("rate %d block type %d: layoutFor = %p, want %p", r, bt, got, &layoutLong[r])
			}
		}
	}
}

// TestReorderShortGolden freezes a sha256 checksum over reorderShort's
// output for LCG-generated pseudo-noise input, at each of the three
// MPEG-1 rates' sfb widths, guarding the coding-order index mapping
// against accidental edits. CI's arm64 leg failing this test while amd64
// stays green would be surprising (reorderShort is a pure index copy with
// no arithmetic feeding a +/-, hence no FMA surface) and should be
// treated as a real bug, not an FMA leak to paper over.
func TestReorderShortGolden(t *testing.T) {
	var seed uint64 = 1
	next := func() float64 {
		return testsignal.LCGSigned(&seed)
	}

	vals := make([]float64, 0, 576*3)
	for r := range 3 {
		widths := sfbWidthsShort[r]

		var src, dst [576]float64
		for i := range src {
			src[i] = next()
		}
		reorderShort(&src, &dst, &widths)
		vals = append(vals, dst[:]...)
	}

	const wantHex = "b3f4db3d1c25dc19ad193d2091f123702fb6a5804d59253017fa328b273399ca"
	got := sha256Float64s(vals...)
	if got != wantHex {
		t.Fatalf("TestReorderShortGolden checksum = %s, want %s", got, wantHex)
	}
}
