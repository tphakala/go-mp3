package enc

import "github.com/tphakala/go-mp3/internal/bits"

// impossibleCost marks a (table, magnitude) combination the table cannot
// represent at all: a non-escape table asked for a value beyond its
// dim-1, or an escape table asked for a magnitude beyond 15+2^linbits-1.
// Large enough to always lose chooseRegions' minimization against any real
// codeword cost, yet small enough that summing it across a full 288-pair
// granule (worst case about 3*10^8) stays far inside int's range on both
// amd64 and arm64.
const impossibleCost = 1 << 20

// validBigTables lists every real (non-alias, non-invalid) big-values
// codebook number: table 0 (the all-zero table) plus every populated
// bigTables entry except the invalid slots 4 and 14. Tables 16-23 and
// 24-31 are listed individually even though each family shares one
// codes slice, because their linbits (hence their escape cost) differ.
var validBigTables = [30]uint8{
	0, 1, 2, 3, 5, 6, 7, 8, 9, 10, 11, 12, 13, 15,
	16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31,
}

// regionInfo is a coded big-values region layout: boundaries expressed as
// side-info counts, chosen tables, and the exact part3 bit cost.
type regionInfo struct {
	region0Count int      // side-info field, 0..15
	region1Count int      // side-info field, 0..7
	tableSelect  [3]uint8 // codebook per region (0 = region empty/all-zero)
	count1Table  uint8    // 0 = table A, 1 = table B
	bits         int      // exact part3 bits (big values + count1, incl. signs and linbits)
}

// pairCost returns the number of bits table t spends encoding the
// magnitude pair (ax, ay): the table's own codeword length at the
// (possibly escape-clamped) index, plus t.linbits extra bits for each of
// ax, ay that triggered the escape (index saturated at dim-1), plus one
// sign bit per nonzero value. Returns impossibleCost if t cannot
// represent this pair (ax or ay exceeds what t.linbits can escape to, or
// exceeds dim-1 on a non-escape table).
//
// Escape tables can never represent a magnitude of exactly dim-1 (15)
// directly: index 15 is reserved as the escape marker (ISO 2.4.2.7), so
// even a magnitude of 15 costs an extra t.linbits bits (with an escape
// value of 0), mirroring l3Huffman's `if lsb == 15` branch
// (internal/dec/huffman.go:171) rather than checking ax > 15.
func pairCost(t huffTable, ax, ay int32) int {
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

// pairBoundaries returns, for coding-band layout lay and a big-values
// region of bigValues pairs, the cumulative pair count through each of
// lay's coding bands (pb[0] = 0, pb[lay.nBands] = bigValues), clamped so a
// region that runs out of big-values pairs mid-band never claims pairs
// beyond what actually exists. This mirrors l3Huffman's walk exactly:
// bigValCnt there stops the decode the moment it hits 0, regardless of how
// many nominal sfb's a region's side-info count implies
// (internal/dec/huffman.go:151, :199-204). chooseRegions and writeSpectrum
// both call this so their region-boundary arithmetic can never diverge.
func pairBoundaries(lay *bandLayout, bigValues int) [40]int {
	var pb [40]int
	cum := 0
	for k := range lay.nBands {
		cum += lay.width[k] / 2
		if cum > bigValues {
			cum = bigValues
		}
		pb[k+1] = cum
	}
	return pb
}

// bigValuesPrefixCost fills prefixCost[t][k]: the bits needed to encode
// pairs [0, pb[k]) of ix using table t, for every table t in
// validBigTables and every coding-band prefix k in [0,lay.nBands]. Fills a
// caller-owned array in place rather than returning by value, since it runs
// up to 150x per granule inside the outer loop's masking escalation and the
// [32][40]int result is large enough that a by-value return costs a
// measurable copy on every call.
func bigValuesPrefixCost(ix *[576]int32, pb *[40]int, lay *bandLayout, prefixCost *[32][40]int) {
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

// rangeCost returns the cheapest single table (and its cost) for encoding
// pairs [pb[a], pb[b]) of a big-values region, using prefixCost's
// per-table per-band-prefix sums. An empty range (pb[a]==pb[b]) costs 0
// bits with table 0, the "region empty/all-zero" convention regionInfo
// documents.
func rangeCost(prefixCost *[32][40]int, pb *[40]int, a, b int) (cost int, table uint8) {
	if pb[a] == pb[b] {
		return 0, 0
	}
	best := impossibleCost
	var bestT uint8
	for _, t := range validBigTables {
		c := prefixCost[t][b] - prefixCost[t][a]
		if c < best {
			best, bestT = c, t
		}
	}
	return best, bestT
}

// count1Cost returns the bit cost of the count1 (quad) region under the
// cheaper of table A and table B, and which table that is (0 = A,
// 1 = B). partitionSpectrum guarantees every quad element's magnitude is
// 0 or 1 here, so the presence pattern (which elements are nonzero) IS
// the magnitude pattern.
func count1Cost(ix *[576]int32, part spectrumPartition) (bitsCost int, table uint8) {
	start := part.bigValues * 2
	costA, costB := 0, 0
	for i := range part.count1 {
		base := start + 4*i
		v, w, x, y := ix[base], ix[base+1], ix[base+2], ix[base+3]
		idx := quadIndex(v, w, x, y)
		signBits := quadSignBits(v, w, x, y)
		costA += int(count1A[idx].len) + signBits
		costB += 4 + signBits
	}
	if costA <= costB {
		return costA, 0
	}
	return costB, 1
}

// quadIndex packs a count1 quad's presence pattern (1 for nonzero) into
// the v<<3|w<<2|x<<1|y index count1A is keyed by, ISO Table B.7's count1
// codebook layout.
func quadIndex(v, w, x, y int32) int {
	idx := 0
	if v != 0 {
		idx |= 8
	}
	if w != 0 {
		idx |= 4
	}
	if x != 0 {
		idx |= 2
	}
	if y != 0 {
		idx |= 1
	}
	return idx
}

// quadSignBits counts how many of a count1 quad's four elements are
// nonzero, i.e. how many sign bits l3Huffman's count1 walk reads for it
// (internal/dec/huffman.go:270-312).
func quadSignBits(v, w, x, y int32) int {
	n := 0
	for _, e := range [4]int32{v, w, x, y} {
		if e != 0 {
			n++
		}
	}
	return n
}

// regionBounds converts region0Count/region1Count (ISO side-info
// scalefactor-band counts) into pair-boundary band indices a and c: a is the
// end of region 0 (clamped to nBands), c is the end of region 1 (clamped to
// nBands, computed from the already-clamped a).
func regionBounds(region0Count, region1Count, nBands int) (a, c int) {
	a = min(region0Count+1, nBands)
	c = min(a+region1Count+1, nBands)
	return
}

// chooseRegions picks region boundaries (on scalefactor-band edges, per
// ISO 2.4.2.7) and per-region codebooks minimizing total bits, by exact
// search over region0Count x region1Count with per-sfb per-table prefix
// cost sums. Ties break toward smaller region0Count then region1Count
// (guaranteed by iterating both ascending and only replacing the running
// best on a strictly smaller total).
//
// The region0Count+region1Count+2<=lay.nBands bound (ISO 2.4.2.7's
// region0_count and region1_count each contribute count+1 scalefactor
// bands) keeps every explored split within lay's real coding bands; a
// wider search would only reach nominal boundaries that already saturate to
// lay.nBands via pairBoundaries' clamp, so nothing optimal is excluded.
func chooseRegions(ix *[576]int32, part spectrumPartition, lay *bandLayout) regionInfo {
	pb := pairBoundaries(lay, part.bigValues)
	var prefixCost [32][40]int
	bigValuesPrefixCost(ix, &pb, lay, &prefixCost)

	bestBits := impossibleCost
	var best regionInfo
	for r0 := range 16 {
		for r1 := range 8 {
			if r0+r1+2 > lay.nBands {
				continue
			}
			a, c := regionBounds(r0, r1, lay.nBands)
			cost0, t0 := rangeCost(&prefixCost, &pb, 0, a)
			cost1, t1 := rangeCost(&prefixCost, &pb, a, c)
			cost2, t2 := rangeCost(&prefixCost, &pb, c, lay.nBands)
			total := cost0 + cost1 + cost2
			if total < bestBits {
				bestBits = total
				best = regionInfo{
					region0Count: r0,
					region1Count: r1,
					tableSelect:  [3]uint8{t0, t1, t2},
					bits:         total,
				}
			}
		}
	}

	c1Bits, c1Table := count1Cost(ix, part)
	best.count1Table = c1Table
	best.bits += c1Bits
	return best
}

// chooseRegionsWS picks per-table costs for a window-switching granule's
// two FIXED big-values regions (ISO 2.4.2.7, design decision 5): unlike
// chooseRegions' exhaustive search, the region boundary is not a free
// parameter here, because the decoder never reads region0_count/
// region1_count for a window-switching granule at all
// (internal/dec/sideinfo.go:119-124 hard-codes regionCount[0] to 7 for a
// start/stop granule and 8 for a pure-short one, with regionCount[1]=255
// swallowing everything else). Region 0 therefore covers coding bands
// [0, a): a=8 for start/stop (the first 8 long sfb's, the long geometry
// blockType!=blockShort still carries per bandLayout's doc comment) or
// a=9 for short (the first 9 coding bands, i.e. the first 3 short sfb's
// across all 3 windows). Region 1 covers the rest of big_values; there is
// no region 2 (table_select[2] is never transmitted for a window-
// switching granule, so it is filled in equal to table_select[1] here,
// though writeSpectrum never actually reaches it once region1 is widened
// to lay.nBands).
//
// The returned regionInfo's region0Count/region1Count are chosen so
// regionBounds reproduces this exact split (region0Count=a-1, region1Count
// wide enough to saturate the end at lay.nBands): they are cache-only,
// consumed by writeSpectrum, and writeSideInfo's window-switching branch
// never emits them to the bitstream.
func chooseRegionsWS(ix *[576]int32, part spectrumPartition, lay *bandLayout, blockType int) regionInfo {
	a := 8
	if blockType == blockShort {
		a = 9
	}
	a = min(a, lay.nBands)

	pb := pairBoundaries(lay, part.bigValues)
	var prefixCost [32][40]int
	bigValuesPrefixCost(ix, &pb, lay, &prefixCost)

	cost0, t0 := rangeCost(&prefixCost, &pb, 0, a)
	cost1, t1 := rangeCost(&prefixCost, &pb, a, lay.nBands)

	ri := regionInfo{
		region0Count: a - 1,
		region1Count: lay.nBands, // saturates regionBounds' c to lay.nBands: no region 2
		tableSelect:  [3]uint8{t0, t1, t1},
		bits:         cost0 + cost1,
	}

	c1Bits, c1Table := count1Cost(ix, part)
	ri.count1Table = c1Table
	ri.bits += c1Bits
	return ri
}

// writeSpectrum emits the Huffman-coded spectrum (big values then count1)
// exactly as counted by chooseRegions; returns bits written, which must
// equal ri.bits.
//
// lay must be the same layout chooseRegions was called with.
// region0Count/region1Count are ISO side-info scalefactor-band counts,
// not pair positions (regionInfo's doc comment), so recovering which
// pairs belong to which region needs the same band-width table
// chooseRegions used; this mirrors how l3Huffman derives the walk from
// gr.sfbTab at decode time rather than from a precomputed boundary
// (internal/dec/huffman.go:144-243). Deviates from the task-5 brief's
// sketched 4-parameter signature for exactly this reason.
func writeSpectrum(w *bits.Writer, ix *[576]int32, part spectrumPartition, ri regionInfo, lay *bandLayout) int {
	before := w.BitsWritten()
	pb := pairBoundaries(lay, part.bigValues)
	a, c := regionBounds(ri.region0Count, ri.region1Count, lay.nBands)
	region0End, region1End := pb[a], pb[c]

	for p := range part.bigValues {
		var table uint8
		switch {
		case p < region0End:
			table = ri.tableSelect[0]
		case p < region1End:
			table = ri.tableSelect[1]
		default:
			table = ri.tableSelect[2]
		}
		writePair(w, bigTables[table], ix[2*p], ix[2*p+1])
	}

	start := part.bigValues * 2
	for i := range part.count1 {
		base := start + 4*i
		v, wv, x, y := ix[base], ix[base+1], ix[base+2], ix[base+3]
		idx := quadIndex(v, wv, x, y)
		if ri.count1Table == 0 {
			e := count1A[idx]
			w.WriteBits(e.code, int(e.len))
		} else {
			// Table B needs no static array: every codeword is 4 bits,
			// the bitwise complement of the presence index (verified
			// against tab33Table when the tables were derived; see
			// hufftables.go's count1A doc comment).
			w.WriteBits(uint32(^idx&0xF), 4)
		}
		writeSign(w, v)
		writeSign(w, wv)
		writeSign(w, x)
		writeSign(w, y)
	}

	return w.BitsWritten() - before
}

// writePair emits one big-values pair's codeword followed by its two
// values' escape/sign fields, in l3Huffman's exact read order: codeword,
// then x's [linbits][sign], then y's [linbits][sign]
// (internal/dec/huffman.go:166-197 and :213-234, the `for range 2`
// nibble loop).
func writePair(w *bits.Writer, bt huffTable, x, y int32) {
	ax, ay := abs32(x), abs32(y)
	// ix/iy stay unclamped when bt.linbits == 0 (no escape): that is safe
	// only because the caller never hands this function an out-of-range
	// index for such a table. quantizeGranule clamps every magnitude to
	// maxQuant=8206, which is exactly the largest value any table can
	// represent (15 + 2^13-1, the linbits-13 escape family's capacity),
	// so chooseRegions' cost search (pairCost) always finds at least one
	// feasible table for every pair and never selects a non-escape table
	// whose dim-1 the pair's magnitude exceeds; writePair is only ever
	// invoked with the table chooseRegions actually picked.
	ix, iy := ax, ay
	if bt.linbits > 0 {
		maxDirect := int32(bt.dim - 1)
		if ix > maxDirect {
			ix = maxDirect
		}
		if iy > maxDirect {
			iy = maxDirect
		}
	}
	e := bt.codes[int(ix)*bt.dim+int(iy)]
	w.WriteBits(e.code, int(e.len))
	writeValue(w, bt, x)
	writeValue(w, bt, y)
}

// writeValue emits one big-values nibble's escape field (if the index
// saturated at dim-1) and sign bit (if nonzero), mirroring l3Huffman's
// per-nibble decode (see writePair's doc comment for the exact lines).
func writeValue(w *bits.Writer, bt huffTable, v int32) {
	mag := abs32(v)
	if bt.linbits > 0 {
		maxDirect := int32(bt.dim - 1)
		if mag >= maxDirect {
			w.WriteBits(uint32(mag-maxDirect), bt.linbits)
		}
	}
	writeSign(w, v)
}

// writeSign emits a single sign bit (1 = negative, 0 = positive/zero) iff
// v is nonzero, matching l3Huffman's `if lsb != 0 { flushBits(bs, 1) }`
// pattern used by both the big-values and count1 decode loops.
func writeSign(w *bits.Writer, v int32) {
	if v == 0 {
		return
	}
	sign := uint32(0)
	if v < 0 {
		sign = 1
	}
	w.WriteBits(sign, 1)
}

// --- Test-only cross-package surface ---
//
// regionInfo, chooseRegions and writeSpectrum stay unexported per the
// task-5 brief's sketched API: they are internal encoder plumbing, not
// this package's real public surface (Task 8 defines that). But
// internal/dec's round-trip decode gate (encx_huffman_test.go) is a
// white-box test living in package dec, needed there because it drives
// the decoder's unexported l3Huffman/grInfo/scfLongTable directly; Go
// visibility rules mean it can only reach this package's EXPORTED names,
// even though "internal/dec importing internal/enc in a _test.go file"
// is itself the sanctioned exception to "enc must never import dec" (see
// PROVENANCE.md). The four symbols below are the minimal exported
// surface that gate needs, mirroring Task 2's identical precedent
// (internal/enc/filterbank.go's Filterbank/AnalyzeGranule/PCMScale
// exports for the same cross-package white-box reason): internal/-scoped,
// so no public API risk.

// EncodeHuffmanPin Huffman-codes ix's first bigValues pairs (all under
// codebook table) followed by count1 count1-quads (under count1Table,
// 0 = table A, 1 = table B), exactly as writeSpectrum would for a
// chooseRegions result that picked table for every region. Returns the
// bits written.
func EncodeHuffmanPin(w *bits.Writer, ix *[576]int32, bigValues, count1 int, table, count1Table uint8, sfbWidths *[22]int) int {
	part := spectrumPartition{bigValues: bigValues, count1: count1}
	ri := regionInfo{
		// region0Count=21 covers all 22 real long-block sfb's in one
		// region; harmless since table is pinned across all three
		// regions anyway (region1Count/tableSelect[1:] are never
		// consulted once bigValCnt hits 0 inside region 0's walk).
		region0Count: 21,
		region1Count: 0,
		tableSelect:  [3]uint8{table, table, table},
		count1Table:  count1Table,
	}
	// writeSpectrum now takes a *bandLayout rather than a raw sfb-width
	// table; this exported pin's own signature stays fixed (internal/dec's
	// encx_huffman_test.go calls it with a plain [22]int), so build a
	// minimal long-block layout from sfbWidths locally. Only width/nBands
	// matter to writeSpectrum's own walk (region/count1 emission never
	// reads win/nScf/slen1End), but the whole struct is filled in for
	// clarity.
	var lay bandLayout
	lay.nBands = 22
	lay.nScf = 21
	lay.slen1End = 11
	for i := range 22 {
		lay.width[i] = sfbWidths[i]
		lay.win[i] = -1
	}
	return writeSpectrum(w, ix, part, ri, &lay)
}

// BigTableDim and BigTableLinbits expose bigTables[t]'s shape: the
// round-trip gate needs a table's dimension (to sweep every (x,y) pair in
// its domain) and linbits (to build escape-magnitude test values and to
// cross-check against the decoder's linbitsTable) without duplicating ISO
// Table B.7's per-table layout in the test. See this section's doc
// comment for why they are exported.
func BigTableDim(t int) int { return bigTables[t].dim }

// BigTableLinbits is documented on BigTableDim.
func BigTableLinbits(t int) int { return bigTables[t].linbits }

// SfbWidthsLongRow returns a copy of sfbWidthsLong[rate] (rate: 0=44100,
// 1=48000, 2=32000 Hz), for the round-trip gate's grInfo.sfbTab pin and
// for cross-checking against the decoder's scfLongTable rows. Returned by
// value, not as a pointer into the package-level sfbWidthsLong array: a
// live pointer would let any future cross-package caller mutate (or race
// on) this package's internal table. See this section's doc comment for
// why it is exported.
func SfbWidthsLongRow(rate int) [22]int { return sfbWidthsLong[rate] }
