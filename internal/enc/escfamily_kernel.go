package enc

// escFam16LinbitsI32 / escFam24LinbitsI32 are the int32 forms of the two escape
// families' per-table linbits (escFam16Linbits / escFam24Linbits are [8]int for
// the scalar accumEscFamily* path). escFamilyAccumGo reads these as its 8-lane
// int32 addends; deriving them here keeps them from drifting out of sync with
// the [8]int originals.
var (
	escFam16LinbitsI32 = linbitsI32(&escFam16Linbits)
	escFam24LinbitsI32 = linbitsI32(&escFam24Linbits)
)

func linbitsI32(l *[8]int) [8]int32 {
	var o [8]int32
	for j, v := range l {
		o[j] = int32(v)
	}
	return o
}

// escFamilyAccumGo accumulates the escape-family big-values cost of pairs
// [0,len(ax)) into the two per-family 8-lane accumulators: acc16[j] for escape
// table 16+j, acc24[j] for table 24+j. It is the escape-family half of
// bigValuesPrefixCostGo, the pure-Go reference the fused prefix-cost kernels
// are checked against.
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
	// backing arrays). All four must have length n; a shorter slice panics here
	// exactly as the old per-pair index would.
	n := len(ax)
	ay, base16, base24 = ay[:n], base16[:n], base24[:n]
	for p := range ax {
		axp, ayp := ax[p], ay[p]
		b16, b24 := base16[p], base24[p]
		if axp < escMaxDirect && ayp < escMaxDirect {
			// Non-escaping pair (the common case): escCount is 0 and no table's
			// maxVal can bind (the smallest is escMaxDirect+1), so every lane
			// just adds base. This mirrors the former accumEscFamilyFlat and
			// keeps the common case cheap.
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

// escFamilyPrefixGo is the escape-family phase of bigValuesPrefixCostGo in ONE
// call per granule big-values run (issue #68): it walks the coding bands
// 0..nBands-1, keeping the two 8-lane accumulators resident across the whole
// run, and after each band k writes the CUMULATIVE prefix cost for escape
// tables 16..23 into prefixCost[k+1][16..23] and 24..31 into
// prefixCost[k+1][24..31]. Band k covers big-values pairs [pb[k], pb[k+1]);
// pb[0] is 0. A zero-width band still writes the (unchanged) running
// accumulator. Row 0 and columns 0..15 are never touched, so the caller's
// non-escape table costs and the empty-prefix row 0 survive. The base16/base24
// codebook gather stays a pre-pass in the caller.
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
