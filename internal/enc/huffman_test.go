package enc

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"slices"
	"testing"

	"github.com/tphakala/go-mp3/internal/bits"
	"github.com/tphakala/go-mp3/internal/testsignal"
)

// allHuffCodeTables lists every distinct transcribed ISO Table B.7
// codeword slice: the 15 real big-values code arrays (table16Codes and
// table24Codes represent their whole escape family; the family members
// differ only in linbits, not codewords) plus count1A. Table 0's
// synthetic {0,0} placeholder is deliberately excluded: it is not a
// transcribed table, just the "0 bits, all-zero region" convention
// bigTables[0] documents.
var allHuffCodeTables = []struct {
	name  string
	codes []codeEntry
}{
	{"table1", table1Codes},
	{"table2", table2Codes},
	{"table3", table3Codes},
	{"table5", table5Codes},
	{"table6", table6Codes},
	{"table7", table7Codes},
	{"table8", table8Codes},
	{"table9", table9Codes},
	{"table10", table10Codes},
	{"table11", table11Codes},
	{"table12", table12Codes},
	{"table13", table13Codes},
	{"table15", table15Codes},
	{"table16", table16Codes},
	{"table24", table24Codes},
	{"count1A", count1A[:]},
}

// TestHuffTablesChecksum freezes every transcribed ISO Table B.7 codeword
// (code, len) against a golden SHA-256, serialized in allHuffCodeTables'
// order as little-endian uint32 code + uint8 len per entry. A change here
// means a table transcription changed; TestEncHuffmanDecRoundTrip
// (internal/dec/encx_huffman_test.go) is the authority on whether a new
// value is correct, not this golden.
func TestHuffTablesChecksum(t *testing.T) {
	const wantHex = "9cafb5eddf802d6245937c10b0f775c3c88dfcb3c07814a6d399a6496c8b3867"

	h := sha256.New()
	var buf5 [5]byte
	for _, tbl := range allHuffCodeTables {
		for _, e := range tbl.codes {
			binary.LittleEndian.PutUint32(buf5[:4], e.code)
			buf5[4] = e.len
			h.Write(buf5[:])
		}
	}

	got := hex.EncodeToString(h.Sum(nil))
	if got != wantHex {
		t.Fatalf("huffman code table checksum = %s, want %s", got, wantHex)
	}
}

// isPrefixFree reports whether no codeword in codes is a bit-prefix of
// another: for every pair with lenA <= lenB, codeA must not equal codeB's
// top lenA bits. O(n^2) pairwise check, cheap at these table sizes (at
// most 256 entries).
func isPrefixFree(codes []codeEntry) bool {
	for i, a := range codes {
		for j, b := range codes {
			if i == j || a.len == 0 || b.len == 0 || a.len > b.len {
				continue
			}
			if a.code == b.code>>uint(b.len-a.len) {
				return false
			}
		}
	}
	return true
}

// TestHuffPrefixFree requires every transcribed table's codewords form a
// prefix-free (instantaneously decodable) set, the defining property of a
// canonical Huffman code.
func TestHuffPrefixFree(t *testing.T) {
	for _, tbl := range allHuffCodeTables {
		if !isPrefixFree(tbl.codes) {
			t.Errorf("%s: codewords are not prefix-free", tbl.name)
		}
	}
}

// TestHuffLensSane requires every transcribed codeword's length is in
// [1, 19] (ISO Table B.7's longest codeword is 19 bits, table13's
// (15,18)/(15,19) entries) and that the code value actually fits in that
// many bits.
func TestHuffLensSane(t *testing.T) {
	for _, tbl := range allHuffCodeTables {
		for i, e := range tbl.codes {
			if e.len < 1 || e.len > 19 {
				t.Errorf("%s[%d]: len=%d out of [1,19]", tbl.name, i, e.len)
			}
			if e.code>>e.len != 0 {
				t.Errorf("%s[%d]: code=0x%x does not fit in %d bits", tbl.name, i, e.code, e.len)
			}
		}
	}
}

// TestChooseRegionsExhaustiveSmall brute-forces the minimum-cost region
// split for a small, hand-picked granule independently of chooseRegions'
// prefix-sum search (a plain O(range length) cost sum per candidate
// range, not chooseRegions' own bigValuesPrefixCost/rangeCost), and
// requires chooseRegions finds the same minimum and the same tie-broken
// (region0Count, region1Count).
func TestChooseRegionsExhaustiveSmall(t *testing.T) {
	var ix [576]int32
	vals := []int32{1, -2, 0, 3, -1, 2, 4, -3, 0, 1, 5, -2, 0, 0, 2, -1, 3, 0, -4, 1}
	copy(ix[:], vals)
	part := spectrumPartition{bigValues: 10, count1: 0}
	lay := &layoutLong[0]

	got := chooseRegions(&ix, part, lay)

	pb := pairBoundaries(lay, part.bigValues)
	bruteRangeCost := func(a, b int) int {
		if pb[a] == pb[b] {
			return 0
		}
		best := impossibleCost
		for _, tnum := range validBigTables {
			bt := bigTables[tnum]
			cost := 0
			for p := pb[a]; p < pb[b]; p++ {
				cost += pairCost(bt, abs32(ix[2*p]), abs32(ix[2*p+1]))
			}
			if cost < best {
				best = cost
			}
		}
		return best
	}

	bestBits := impossibleCost
	wantR0, wantR1 := -1, -1
	for r0 := range 16 {
		for r1 := range 8 {
			if r0+r1+2 > lay.nBands {
				continue
			}
			a, c := regionBounds(r0, r1, lay.nBands)
			total := bruteRangeCost(0, a) + bruteRangeCost(a, c) + bruteRangeCost(c, lay.nBands)
			if total < bestBits {
				bestBits, wantR0, wantR1 = total, r0, r1
			}
		}
	}
	c1Bits, _ := count1Cost(&ix, part)
	bestBits += c1Bits

	if got.bits != bestBits {
		t.Fatalf("chooseRegions.bits = %d, want independently brute-forced minimum %d", got.bits, bestBits)
	}
	if got.region0Count != wantR0 || got.region1Count != wantR1 {
		t.Fatalf("chooseRegions picked region0Count=%d region1Count=%d, want %d,%d (tie-break: smallest region0Count then region1Count)",
			got.region0Count, got.region1Count, wantR0, wantR1)
	}
}

// TestWriteSpectrumBitsMatchCount runs the real partition -> chooseRegions
// -> writeSpectrum pipeline (ix is built directly from the LCG, not via
// quantizeGranule) over LCG-random granules across all three MPEG-1
// rates, and requires writeSpectrum's returned bit count
// always equals chooseRegions' predicted ri.bits: the two must never
// diverge, or a real encoder would either overrun its declared part23
// length or under-fill it.
func TestWriteSpectrumBitsMatchCount(t *testing.T) {
	var seed uint64 = 7
	next := func() float64 {
		return testsignal.LCG(&seed)
	}

	for rate := range 3 {
		lay := &layoutLong[rate]
		for trial := range 20 {
			var ix [576]int32
			for i := range ix {
				// Cubic taper concentrates magnitude in the low-frequency
				// half and empties toward the tail, like a real quantized
				// spectrum, so partitionSpectrum sometimes finds a
				// nonempty count1/rzero tail instead of always returning
				// bigValues=288.
				frac := float64(576-i) / 576
				scale := float64(maxQuant) * frac * frac * frac
				v := int32(next() * scale)
				if next() < 0.5 {
					v = -v
				}
				ix[i] = v
			}

			part := partitionSpectrum(&ix)
			ri := chooseRegions(&ix, part, lay)

			var buf []byte
			w := bits.NewWriter(buf)
			got := writeSpectrum(&w, &ix, part, ri, lay)
			if got != ri.bits {
				t.Fatalf("rate %d trial %d: writeSpectrum returned %d bits, chooseRegions costed %d", rate, trial, got, ri.bits)
			}
		}
	}
}

// TestHuffmanGolden freezes the coded bytes of a fixed, LCG-generated
// granule against a golden SHA-256: a cross-arch determinism gate for the
// partition -> chooseRegions -> writeSpectrum chain (ix is built directly
// from the LCG, not via quantizeGranule), the same role
// TestFBGolden/TestMdctGolden play for the DSP front end.
func TestHuffmanGolden(t *testing.T) {
	var seed uint64 = 42
	next := func() float64 {
		return testsignal.LCG(&seed)
	}

	var ix [576]int32
	for i := range 500 {
		v := int32(next() * 200)
		if next() < 0.5 {
			v = -v
		}
		ix[i] = v
	}
	for i := 500; i < 576; i += 4 {
		if next() < 0.3 {
			v := int32(1)
			if next() < 0.5 {
				v = -1
			}
			ix[i] = v
		}
	}

	part := partitionSpectrum(&ix)
	lay := &layoutLong[1]
	ri := chooseRegions(&ix, part, lay)

	var buf []byte
	w := bits.NewWriter(buf)
	n := writeSpectrum(&w, &ix, part, ri, lay)
	if n != ri.bits {
		t.Fatalf("writeSpectrum returned %d bits, want ri.bits=%d", n, ri.bits)
	}
	coded := w.Flush()

	const wantHex = "1f35acafba563db8dd7e554f1646fc34802e56dd2aecacd5fdfccd6fe547b72e"
	got := hex.EncodeToString(sha256Sum(coded))
	if got != wantHex {
		t.Fatalf("TestHuffmanGolden checksum = %s, want %s (coded %d bytes, %d bits)", got, wantHex, len(coded), ri.bits)
	}
}

// sha256Sum is a small local wrapper so TestHuffmanGolden reads as one
// expression; crypto/sha256's package-level Sum256 already does exactly
// this, this just spells out the []byte->[]byte call.
func sha256Sum(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}

// bigValuesPrefixCostRef is a frozen, never-optimized copy of
// bigValuesPrefixCost as it stood before issue #37's inner-cost work. It is
// the golden-neutral oracle for TestBigValuesPrefixCostEquivalence: every
// optimization of the production function must reproduce this byte for byte.
func bigValuesPrefixCostRef(ix *[576]int32, pb *[40]int, lay *bandLayout, prefixCost *[32][40]int) {
	for _, t := range validBigTables {
		bt := bigTables[t]
		cost := 0
		p := 0
		for k := range lay.nBands {
			end := pb[k+1]
			for ; p < end; p++ {
				cost += pairCost(bt, abs32(ix[2*p]), abs32(ix[2*p+1]))
			}
			prefixCost[t][k+1] = cost
		}
	}
}

// TestBigValuesPrefixCostEquivalence fuzzes ix across every long and short
// layout and a spread of magnitude scales, requiring the production
// bigValuesPrefixCost to equal the frozen reference for the full [32][40]
// table. The high scales (255, maxQuant) sweep magnitudes continuously
// through and past the escape threshold, so this catches most divergences.
// Deterministic coverage of the exact escape boundary and the impossibleCost
// branch lives in TestBigValuesPrefixCostEscapeBoundary; together they are
// the bit-exact proof for issue #37's abs-hoist and escape-family-factor
// refactors, well before the sha256 stream goldens would show a divergence.
func TestBigValuesPrefixCostEquivalence(t *testing.T) {
	layouts := []*bandLayout{
		&layoutLong[0], &layoutLong[1], &layoutLong[2],
		&layoutShort[0], &layoutShort[1], &layoutShort[2],
	}
	var seed uint64 = 20260827
	next := func() float64 { return testsignal.LCG(&seed) }

	// A spread of scales. testsignal.LCG returns [0,1) and the frac*frac
	// taper is <= 1, so int32(next()*scale*frac*frac) is strictly below
	// scale: the low scales stay under the escape threshold and only the high
	// scales (255, maxQuant) sweep magnitudes through and past it. The exact
	// boundary values are pinned deterministically in the companion test.
	scales := []int32{1, 2, 15, 16, 255, maxQuant}

	for li, lay := range layouts {
		for _, scale := range scales {
			for trial := range 40 {
				var ix [576]int32
				for i := range ix {
					// Concentrate magnitude low, empty the tail, so
					// partitionSpectrum yields varied bigValues counts.
					frac := float64(576-i) / 576
					v := int32(next() * float64(scale) * frac * frac)
					if next() < 0.5 {
						v = -v
					}
					ix[i] = v
				}
				part := partitionSpectrum(&ix)
				pb := pairBoundaries(lay, part.bigValues)

				var got, want [32][40]int
				bigValuesPrefixCost(&ix, &pb, lay, &got)
				bigValuesPrefixCostRef(&ix, &pb, lay, &want)
				if got != want {
					t.Fatalf("layout=%d (short=%v) scale=%d trial=%d: bigValuesPrefixCost diverged from frozen reference", li, lay.short, scale, trial)
				}
			}
		}
	}
}

// TestEscFamilyTablePartition asserts the hand-written big-value table
// partitions in huffman.go stay in lockstep with the canonical bigTables /
// validBigTables definitions in hufftables.go. nonEscBigTables, escFam16Tables/
// escFam16Linbits, and escFam24Tables/escFam24Linbits are hand-maintained
// copies with no compiler-enforced link to their sources, so a future reorder
// or edit could silently desync them; today that is only covered indirectly by
// TestBigValuesPrefixCostEquivalence. This pins the link directly.
func TestEscFamilyTablePartition(t *testing.T) {
	// The three partitions must sum to the ISO split: 14 non-escape + 8 + 8.
	if got := len(nonEscBigTables) + len(escFam16Tables) + len(escFam24Tables); got != len(validBigTables) {
		// Errorf, not Fatalf: the checks below iterate fixed-size arrays and
		// cannot panic, so on a size desync they still surface which member
		// dropped or duplicated instead of masking it behind the count.
		t.Errorf("partition sizes sum to %d, want %d (len(validBigTables))", got, len(validBigTables))
	}

	// They must form a set-partition of validBigTables: every member appears
	// exactly once, with no drops and no duplicates.
	valid := make(map[uint8]bool, len(validBigTables))
	for _, tbl := range validBigTables {
		valid[tbl] = true
	}
	seen := make(map[uint8]bool, len(validBigTables))
	add := func(tbl uint8) {
		switch {
		case !valid[tbl]:
			t.Errorf("partition contains table %d not in validBigTables", tbl)
		case seen[tbl]:
			t.Errorf("partition contains duplicate table %d", tbl)
		}
		seen[tbl] = true
	}
	for _, tbl := range nonEscBigTables {
		add(tbl)
	}
	for _, tbl := range escFam16Tables {
		add(tbl)
	}
	for _, tbl := range escFam24Tables {
		add(tbl)
	}
	for _, tbl := range validBigTables {
		if !seen[tbl] {
			t.Errorf("validBigTables member %d missing from the partition", tbl)
		}
	}

	// Non-escape members must be genuine non-escape codebooks: zero linbits and
	// a populated codes slice (which also rejects the invalid slots 4 and 14,
	// whose codes are nil).
	// These three are also bigValuesPrefixCost's precondition for calling
	// pairCostDirect rather than the general pairCost: that half has no escape
	// handling at all, so a member that grew linbits would be mispriced
	// silently, and dim < 1 would make its maxDirect negative.
	for _, tbl := range nonEscBigTables {
		bt := bigTables[tbl]
		if bt.linbits != 0 {
			t.Errorf("nonEscBigTables table %d: linbits=%d, want 0 (pairCostDirect cannot escape)", tbl, bt.linbits)
		}
		if len(bt.codes) == 0 {
			t.Errorf("nonEscBigTables table %d: empty codes slice", tbl)
		}
		if bt.dim < 1 {
			t.Errorf("nonEscBigTables table %d: dim=%d, want >= 1", tbl, bt.dim)
		}
	}

	// Escape families: each escFam*Linbits entry must equal the linbits of the
	// matching bigTables entry, every member must share one backing codes array
	// (table16Codes / table24Codes), and every escape table is dim escTableDim.
	checkFamily := func(name string, tables *[8]uint8, linbits *[8]int, shared []codeEntry) {
		for j, tbl := range tables {
			bt := bigTables[tbl]
			if bt.linbits != linbits[j] {
				t.Errorf("%s[%d] (table %d): linbits=%d, want %d", name, j, tbl, bt.linbits, linbits[j])
			}
			if bt.dim != escTableDim {
				t.Errorf("%s[%d] (table %d): dim=%d, want %d", name, j, tbl, bt.dim, escTableDim)
			}
			if len(bt.codes) != len(shared) || &bt.codes[0] != &shared[0] {
				t.Errorf("%s[%d] (table %d): codes is not the shared backing array", name, j, tbl)
			}
		}
	}
	checkFamily("escFam16", &escFam16Tables, &escFam16Linbits, table16Codes)
	checkFamily("escFam24", &escFam24Tables, &escFam24Linbits, table24Codes)
}

// pairCostRef is pairCost as it stood before the issue #48 split, transcribed
// verbatim from main at 8944a7d: one function branching internally on linbits.
// It exists so the split can be checked against an INDEPENDENT implementation.
//
// Checking the halves against pairCost instead would be tautological, because
// pairCost is now defined as exactly that dispatch and would agree with them
// however wrong their arithmetic became. The package's other frozen oracle,
// bigValuesPrefixCostRef, cannot stand in either: it reaches the direct half
// through pairCost, so production and reference now share that code and a
// defect inside pairCostDirect cancels out of the comparison.
//
// Frozen on purpose. Never edit this to match a future pairCost.
func pairCostRef(t huffTable, ax, ay int32) int {
	if t.codes == nil {
		return impossibleCost
	}
	maxDirect := int32(t.dim - 1)
	ix, iy := ax, ay
	if t.linbits > 0 {
		maxVal := maxDirect + (int32(1)<<uint(t.linbits) - 1)
		if ax > maxVal || ay > maxVal {
			return impossibleCost
		}
		if ix > maxDirect {
			ix = maxDirect
		}
		if iy > maxDirect {
			iy = maxDirect
		}
	} else if ax > maxDirect || ay > maxDirect {
		return impossibleCost
	}

	cost := int(t.codes[int(ix)*t.dim+int(iy)].len)
	if t.linbits > 0 {
		if ax >= maxDirect {
			cost += t.linbits
		}
		if ay >= maxDirect {
			cost += t.linbits
		}
	}
	if ax != 0 {
		cost++
	}
	if ay != 0 {
		cost++
	}
	return cost
}

// pairCostSweep returns the magnitudes TestPairCostMatchesPreSplitReference
// compares over. A hand-written list cannot do this job, because the
// interesting magnitudes are per-table and a fixed list misses most of them
// silently: bigTables carries dims 1, 2, 3, 4, 6, 8 and 16, so the
// direct/escape boundary dim-1 sits at seven different magnitudes, and the
// linbits in use put the representability ceiling maxVal anywhere from 16 to
// maxQuant. The sweep is therefore DERIVED from the table data, straddling
// every table's dim boundary and every escape table's maxVal, so a table
// added later cannot slip through with its boundaries untested.
func pairCostSweep() []int32 {
	seen := make(map[int32]bool)
	var out []int32
	add := func(v int32) {
		if v >= 0 && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	add(0)
	add(1)
	add(maxQuant)
	add(maxQuant + 1)
	for i := range bigTables {
		d := int32(bigTables[i].dim)
		add(d - 2)
		add(d - 1) // the escape marker / last direct index
		add(d)
		if l := bigTables[i].linbits; l > 0 {
			maxVal := d - 1 + (int32(1)<<uint(l) - 1)
			add(maxVal - 1)
			add(maxVal)
			add(maxVal + 1)
		}
	}
	slices.Sort(out)
	return out
}

// TestPairCostMatchesPreSplitReference is the bit-exactness guard on the
// issue #48 split. For every entry of bigTables, including the invalid
// zero-value slots 4 and 14 that carry nil codes, and every magnitude pair
// drawn from the derived sweep, the dispatcher and the half it dispatches to
// must both return exactly what the pre-split implementation returned.
//
// The halves are exercised directly, not only through pairCost, because
// production reaches pairCostDirect without going through the dispatcher at
// all. Covering all 32 slots is also what reaches each half's nil-codes
// guard, which no other test in the package can touch: every other caller
// iterates validBigTables or nonEscBigTables, and both exclude slots 4 and 14.
func TestPairCostMatchesPreSplitReference(t *testing.T) {
	mags := pairCostSweep()
	for ti := range bigTables {
		bt := bigTables[ti]
		for _, ax := range mags {
			for _, ay := range mags {
				want := pairCostRef(bt, ax, ay)
				if got := pairCost(bt, ax, ay); got != want {
					t.Fatalf("table %d, pair (%d,%d): pairCost = %d, pre-split reference = %d", ti, ax, ay, got, want)
				}
				got := pairCostDirect(bt, ax, ay)
				if bt.linbits > 0 {
					got = pairCostEsc(bt, ax, ay)
				}
				if got != want {
					t.Fatalf("table %d, pair (%d,%d): half = %d, pre-split reference = %d", ti, ax, ay, got, want)
				}
			}
		}
	}

	// Each half's nil-codes guard IS reached through bigTables, since it is
	// the first statement of both halves and slots 4 and 14 carry nil codes.
	// What no bigTables pair can do is DETECT its removal: those slots are
	// zero values, so with the guard gone dim == 0 makes maxDirect -1 and the
	// magnitude test returns impossibleCost for every magnitude anyway.
	// Deleting either guard therefore leaves the whole package green, which
	// reads as dead code and invites exactly that deletion. Pin them here
	// with synthetic tables that pair nil codes with a usable dim, where the
	// magnitude test would pass, so each guard answers for the total contract
	// pairCostRef states rather than for today's table data.
	nilDirect := huffTable{dim: 1}
	if got := pairCostDirect(nilDirect, 0, 0); got != impossibleCost {
		t.Errorf("pairCostDirect(nil codes, 0, 0) = %d, want impossibleCost %d", got, impossibleCost)
	}
	nilEsc := huffTable{dim: escTableDim, linbits: 5}
	if got := pairCostEsc(nilEsc, 0, 0); got != impossibleCost {
		t.Errorf("pairCostEsc(nil codes, 0, 0) = %d, want impossibleCost %d", got, impossibleCost)
	}
	if got := pairCost(nilEsc, 0, 0); got != impossibleCost {
		t.Errorf("pairCost(nil codes, 0, 0) = %d, want impossibleCost %d", got, impossibleCost)
	}
}

// TestBigValuesPrefixCostEscapeBoundary deterministically exercises the
// escape-table code paths that the fuzz test above only reaches incidentally.
// testsignal.LCG returns [0,1), so the fuzz draws magnitudes strictly below
// its scale and its scale=15/16 regimes top out at 14/15, never reliably
// tripping the escape flag or the impossibleCost branch. This test pins exact
// magnitudes that straddle every relevant boundary so the escape-family
// factoring is proven bit-identical to the frozen reference on the values it
// actually hinges on:
//
//	14           below the escape threshold: direct codeword, no linbits addend
//	15           the boundary: index clamped to 15 AND the linbits addend applies
//	16, 17, 18   at and just over escFam16 linbits=1 maxVal (16): representable, then impossibleCost
//	30, 31, 32   straddle the linbits=4 maxVal (30) for both families
//	255, 256     representable only by the higher-linbits members
//	maxQuant     exactly the linbits=13 maxVal (8206): the representability ceiling
func TestBigValuesPrefixCostEscapeBoundary(t *testing.T) {
	mags := []int32{14, 15, 16, 17, 18, 30, 31, 32, 63, 255, 256, maxQuant}

	var ix [576]int32
	for i, m := range mags {
		// Alternate the sign of the first slot to also exercise the sign-bit
		// additions; both slots of the pair carry a boundary magnitude.
		v := m
		if i%2 == 1 {
			v = -v
		}
		ix[2*i] = v
		ix[2*i+1] = m
	}
	// Symmetric pairs alone cannot discriminate accumEscFamilyCost's
	// "neither magnitude escapes" conjunction from a disjunction: with both
	// slots equal, `ax < 15 && ay < 15` and `ax < 15 || ay < 15` agree on
	// every pair. Append MIXED pairs, one slot below the escape marker and
	// one at or above it, in both orders, so a conjunction silently weakened
	// to a disjunction prices the escaping slot's linbits at zero and
	// diverges from the frozen reference.
	mixed := [][2]int32{{14, 15}, {15, 14}, {14, maxQuant}, {-maxQuant, 14}}
	for j, mp := range mixed {
		ix[2*(len(mags)+j)] = mp[0]
		ix[2*(len(mags)+j)+1] = mp[1]
	}
	nPairs := len(mags) + len(mixed)
	// The tail stays zero, so partitionSpectrum classifies these magnitudes
	// (all > 1) as big values rather than count1/rzero. Guard that setup
	// assumption so this test fails loudly if it ever stops covering them.
	part := partitionSpectrum(&ix)
	if part.bigValues < nPairs {
		t.Fatalf("setup: bigValues %d < %d, boundary pairs not all in the big-values region", part.bigValues, nPairs)
	}

	layouts := []*bandLayout{
		&layoutLong[0], &layoutLong[1], &layoutLong[2],
		&layoutShort[0], &layoutShort[1], &layoutShort[2],
	}
	for _, lay := range layouts {
		pb := pairBoundaries(lay, part.bigValues)
		var got, want [32][40]int
		bigValuesPrefixCost(&ix, &pb, lay, &got)
		bigValuesPrefixCostRef(&ix, &pb, lay, &want)
		if got != want {
			t.Fatalf("lay.short=%v: bigValuesPrefixCost diverged from frozen reference on escape-boundary magnitudes", lay.short)
		}
	}
}
