package enc

// slenTab[scalefac_compress] = {slen1, slen2}: ISO/IEC 11172-3, 2.4.2.7.
var slenTab = [16][2]int{
	{0, 0}, {0, 1}, {0, 2}, {0, 3}, {3, 0}, {1, 1}, {1, 2}, {1, 3},
	{2, 1}, {2, 2}, {2, 3}, {3, 1}, {3, 2}, {3, 3}, {4, 2}, {4, 3},
}

// pretabLong[sfb], sfb 0..20: the preemphasis table (2.4.3.4.5); sfb 21
// carries no scalefactor.
var pretabLong = [21]int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 3, 3, 3, 2}

// scfsiGroups: the four long-block scfsi band groups, [lo, hi) over sfbs.
var scfsiGroups = [4][2]int{{0, 6}, {6, 11}, {11, 16}, {16, 21}}

const (
	slen1Bands = 11 // sfbs 0..10 coded with slen1
	slen2Bands = 10 // sfbs 11..20 coded with slen2
	sfMaxLo    = 15 // max scf representable in sfbs 0..10 (slen1 <= 4)
	sfMaxHi    = 7  // max scf representable in sfbs 11..20 (slen2 <= 3)
)

// PretabLongPin exposes pretabLong to the internal/dec encx_ cross-check
// (test-only entry; the AppendFramePin precedent).
func PretabLongPin() [21]int { return pretabLong }
