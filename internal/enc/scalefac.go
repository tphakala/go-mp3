package enc

import "github.com/tphakala/go-mp3/internal/bits"

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

// chooseScalefacCompress picks the cheapest scalefac_compress whose slen
// pair represents every TRANSMITTED scf (groups masked out by scfsi are
// skipped for granule 1); returns the index, the part2 bit cost, and
// ok=false if no pair covers (caller keeps scf within sfMaxLo/sfMaxHi, so
// ok=false is unreachable from the loop; it guards test misuse).
//
// A group is transmitted unless scfsi's corresponding bit is set (bit 3 =
// group 0 ... bit 0 = group 3, matching scfsiGroups' order and the
// side-info scfsi field's own bit order). Only transmitted groups
// contribute to maxLo/maxHi (the coverage requirement) and to the
// per-slen band counts (the cost), so a masked group can never drive the
// choice of scalefac_compress: it costs 0 bits regardless of its scf
// values, because those values are never written for that granule.
func chooseScalefacCompress(sf *scfState, scfsi int) (index, totalBits int, ok bool) {
	maxLo, maxHi := 0, 0
	loCount, hiCount := 0, 0
	for g, band := range scfsiGroups {
		if scfsi&(1<<(3-g)) != 0 {
			continue // reused from granule 0 via scfsi, not transmitted
		}
		width := band[1] - band[0]
		if g < 2 {
			loCount += width
			for sfb := band[0]; sfb < band[1]; sfb++ {
				if sf.scf[sfb] > maxLo {
					maxLo = sf.scf[sfb]
				}
			}
		} else {
			hiCount += width
			for sfb := band[0]; sfb < band[1]; sfb++ {
				if sf.scf[sfb] > maxHi {
					maxHi = sf.scf[sfb]
				}
			}
		}
	}

	found := false
	for i, sl := range slenTab {
		slen1, slen2 := sl[0], sl[1]
		if (1<<slen1)-1 < maxLo || (1<<slen2)-1 < maxHi {
			continue
		}
		cost := loCount*slen1 + hiCount*slen2
		if !found || cost < totalBits {
			index, totalBits, found = i, cost, true
		}
	}
	return index, totalBits, found
}

// writeScalefactors emits granule gc's transmitted scalefactors (slen1
// bits each for sfbs 0..10, slen2 for 11..20, skipping gc.scfsi groups)
// and returns the bits written, which must equal gc.part2Bits.
func writeScalefactors(w *bits.Writer, gc *granuleCoding) int {
	slen1, slen2 := slenTab[gc.scfCompress][0], slenTab[gc.scfCompress][1]
	written := 0
	for g, band := range scfsiGroups {
		if gc.scfsi&(1<<(3-g)) != 0 {
			continue
		}
		slen := slen1
		if g >= 2 {
			slen = slen2
		}
		if slen == 0 {
			continue
		}
		for sfb := band[0]; sfb < band[1]; sfb++ {
			w.WriteBits(uint32(gc.sf.scf[sfb]), slen)
			written += slen
		}
	}
	return written
}

// detectScfsi compares a channel's two coded granules and returns the
// scfsi mask (bit 3 = group 0 ... bit 0 = group 3, the side-info order)
// of groups where gr0 and gr1 scf values are equal AND scalefacScale AND
// preflag agree; 0 if the granules' states are incompatible.
//
// scalefacScale and preflag are granule-wide fields (they scale/boost
// every band's dequantized magnitude), not per-group, so a mismatch on
// either disqualifies scfsi reuse entirely: granule 1 sharing a group's
// raw scf values from granule 0 only reconstructs the same magnitude if
// both granules also agree on how those raw values are scaled.
func detectScfsi(g0, g1 *granuleCoding) int {
	if g0.sf.scalefacScale != g1.sf.scalefacScale || g0.sf.preflag != g1.sf.preflag {
		return 0
	}
	mask := 0
	for g, band := range scfsiGroups {
		same := true
		for sfb := band[0]; sfb < band[1]; sfb++ {
			if g0.sf.scf[sfb] != g1.sf.scf[sfb] {
				same = false
				break
			}
		}
		if same {
			mask |= 1 << (3 - g)
		}
	}
	return mask
}

// applyScfsi recomputes gr1's scfCompress/part2Bits/part23Length under
// the mask and returns the bits saved (never negative; 0 if mask is 0).
//
// Masking a group only relaxes chooseScalefacCompress's coverage
// requirement and can only shrink (never grow) the transmitted band
// counts feeding its cost formula, so the recomputed cost is always <=
// the unmasked cost that produced g1.part2Bits: saved is provably
// non-negative whenever mask != 0.
func applyScfsi(g1 *granuleCoding, mask int) int {
	if mask == 0 {
		return 0
	}
	idx, part2, ok := chooseScalefacCompress(&g1.sf, mask)
	if !ok {
		return 0
	}
	saved := g1.part2Bits - part2
	g1.scfsi = mask
	g1.scfCompress = idx
	g1.part2Bits = part2
	g1.part23Length -= saved
	return saved
}
