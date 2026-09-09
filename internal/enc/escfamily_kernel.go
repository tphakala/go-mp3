package enc

// escFam16LinbitsI32 / escFam24LinbitsI32 are the int32 forms of the two escape
// families' per-table linbits (escFam16Linbits / escFam24Linbits are [8]int for
// the scalar accumEscFamily* path). The fused kernels read these as 8-wide int32
// constant vectors; deriving them here keeps them from drifting out of sync with
// the []int originals.
var (
	escFam16LinbitsI32 = linbitsI32(&escFam16Linbits)
	escFam24LinbitsI32 = linbitsI32(&escFam24Linbits)
)

// escFamImpossible is impossibleCost as an addressable int32 the fused kernels
// broadcast into a lane vector (VPBROADCASTD on amd64, VLD1R on arm64, both take
// memory, not an assembler immediate).
var escFamImpossible = int32(impossibleCost)

func linbitsI32(l *[8]int) [8]int32 {
	var o [8]int32
	for j, v := range l {
		o[j] = int32(v)
	}
	return o
}

// escFamilyAccumGo accumulates the escape-family big-values cost of pairs
// [0,len(ax)) into the two per-family 8-lane accumulators: acc16[j] for escape
// table 16+j, acc24[j] for table 24+j. It is the pure-Go reference the AVX2/NEON
// kernels are checked against and the fallback path when no kernel is compiled
// in (the noasm build or an architecture without one).
//
// For each pair it adds, per family table j, the branch-free per-table cost:
// impossibleCost if either magnitude exceeds that table's maxVal, else
// base + escCount*linbits[j], where escCount is how many of the pair's two
// magnitudes escape (are >= escMaxDirect), 0..2. This is the exact per-table
// result of accumEscFamilyCost and accumEscFamilyFlat, folded into one uniform
// expression (issue #66): the flat (non-escaping) case is just this with
// escCount == 0, where every table's maxVal check passes and no linbits addend
// applies. base16/base24 are the per-pair shared codeword-plus-sign costs
// (table_codes[min(ax,15)*16+min(ay,15)].len + signbits), precomputed by the
// caller so this stays free of the codebook gather.
//
// int32 accumulation is bit-exact to the prior [8]int accumulate-then-int32(...)
// store: signed 32-bit addition wraps mod 2^32, and int64-accumulate-then-
// truncate-to-int32 also reduces mod 2^32, so the two agree whether or not the
// running sum overflows (it never does here: the worst case is about
// 288*impossibleCost ~ 3e8, far inside int32).
func escFamilyAccumGo(acc16, acc24 *[8]int32, ax, ay, base16, base24 []int32) {
	// Reslice to the common length so the bounds-check prover clears the ay,
	// base16 and base24 accesses from the single len(ax) fact (they are distinct
	// backing arrays). The kernels' contract requires all four to have length n;
	// a shorter slice panics here exactly as the old per-pair index would.
	n := len(ax)
	ay, base16, base24 = ay[:n], base16[:n], base24[:n]
	for p := range ax {
		axp, ayp := ax[p], ay[p]
		b16, b24 := base16[p], base24[p]
		if axp < escMaxDirect && ayp < escMaxDirect {
			// Non-escaping pair (the common case): escCount is 0 and no table's
			// maxVal can bind (the smallest is escMaxDirect+1), so every lane
			// just adds base. This mirrors the former accumEscFamilyFlat and
			// keeps the scalar path (noasm, non-AVX2, non-amd64/arm64) cheap.
			for j := range 8 {
				acc16[j] += b16
				acc24[j] += b24
			}
			continue
		}
		var escCount int32
		if axp >= escMaxDirect {
			escCount++
		}
		if ayp >= escMaxDirect {
			escCount++
		}
		for j := range 8 {
			if axp > escFam16MaxVal[j] || ayp > escFam16MaxVal[j] {
				acc16[j] += impossibleCost
			} else {
				acc16[j] += b16 + escCount*escFam16LinbitsI32[j]
			}
			if axp > escFam24MaxVal[j] || ayp > escFam24MaxVal[j] {
				acc24[j] += impossibleCost
			} else {
				acc24[j] += b24 + escCount*escFam24LinbitsI32[j]
			}
		}
	}
}


// escFamilyPrefix fuses the whole escape-family phase of bigValuesPrefixCost
// into ONE call per granule big-values run (issue #68). It walks the coding
// bands 0..nBands-1 internally, keeping the two 8-lane accumulators resident
// across the whole run, and after each band k writes the CUMULATIVE prefix cost
// for escape tables 16..23 into prefixCost[k+1][16..23] and 24..31 into
// prefixCost[k+1][24..31]. Band k covers big-values pairs [pb[k], pb[k+1]);
// pb[0] is 0. A zero-width band still writes the (unchanged) running
// accumulator. Rows 0 and columns 0..15 are never touched, so the caller's
// non-escape table costs and the empty-prefix row 0 survive.
//
// This replaces the former ~20-39 per-band escFamilyAccum calls plus the Go
// snapshot loop with a single call: the SIMD kernels load their loop-invariant
// constant vectors once and store the snapshots in place. The base16/base24
// codebook gather stays a Go pre-pass in the caller (kept out of the vector
// path, as before).

// escFamilyPrefixGo is the pure-Go reference and the noasm/non-amd64/arm64
// fallback for escFamilyPrefix: the exact band loop bigValuesPrefixCost used
// before the fused kernels, so it is output-neutral by construction and serves
// as the differential oracle the AVX2/NEON kernels are pinned against.
func escFamilyPrefixGo(prefixCost *[40][32]int32, pb *[40]int, nBands int, ax, ay, base16, base24 *[288]int32) {
	var acc16, acc24 [8]int32
	p := 0
	for k := range nBands {
		end := pb[k+1]
		escFamilyAccumGo(&acc16, &acc24, ax[p:end], ay[p:end], base16[p:end], base24[p:end])
		p = end
		for j, t := range escFam16Tables {
			prefixCost[k+1][t] = acc16[j]
		}
		for j, t := range escFam24Tables {
			prefixCost[k+1][t] = acc24[j]
		}
	}
}
