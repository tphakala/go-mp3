package enc

import "math/bits"

// This file holds a cheap lower bound on a granule's part3 bits, used by the
// exact global_gain scan to skip the region search at steps that provably do
// not fit. It lives apart from huffman.go because that file imports
// internal/bits under the name bits, which collides with math/bits.

// pairMinDirect[x][y] is the fewest bits any valid big-values table spends on
// the magnitude pair (x, y) with both below escTableDim, including sign bits
// and, for the escape tables, the linbits escape a value of 15 pays. Built
// from the real tables at package init so it cannot drift from them.
var pairMinDirect = buildPairMinDirect()

// minLinbitsFam16 / minLinbitsFam24 map n, the bit length of the largest
// escape value (magnitude minus escMaxDirect) of a pair, to the smallest
// linbits in that family that can carry it. Both families end at linbits 13,
// so every n in 0..13 has an entry; larger n is beyond maxQuant.
var (
	minLinbitsFam16 = buildMinLinbits(&escFam16Linbits)
	minLinbitsFam24 = buildMinLinbits(&escFam24Linbits)
)

func buildPairMinDirect() [escTableDim][escTableDim]uint8 {
	var m [escTableDim][escTableDim]uint8
	for x := range escTableDim {
		for y := range escTableDim {
			best := impossibleCost
			for _, t := range validBigTables {
				best = min(best, pairCost(bigTables[t], int32(x), int32(y)))
			}
			m[x][y] = uint8(best)
		}
	}
	return m
}

func buildMinLinbits(linb *[8]int) [14]int {
	var m [14]int
	for n := range m {
		for _, l := range linb {
			if l >= n {
				m[n] = l
				break
			}
		}
	}
	return m
}

// pairMinCostEsc is the escape half of the per-pair minimum, for a pair with
// max(ax, ay) >= escTableDim: only the two escape families can code it (every
// other table's largest direct magnitude is 15 or less). Within a family the
// codeword and sign bits are shared and each escaped coordinate adds linbits,
// so the cheapest member is the one with the smallest linbits that still
// carries the larger escape value. Magnitudes beyond maxQuant return
// impossibleCost.
func pairMinCostEsc(ax, ay int32) int {
	if ax > maxQuant || ay > maxQuant {
		return impossibleCost
	}
	nEsc, need := 0, int32(0)
	if ax >= escMaxDirect {
		nEsc++
		need = ax - escMaxDirect
	}
	if ay >= escMaxDirect {
		nEsc++
		need = max(need, ay-escMaxDirect)
	}
	n := bits.Len32(uint32(need))

	idx := int(min(ax, escMaxDirect))*escTableDim + int(min(ay, escMaxDirect))
	sign := 0
	if ax != 0 {
		sign++
	}
	if ay != 0 {
		sign++
	}
	c16 := int(table16Codes[idx].len) + sign + minLinbitsFam16[n]*nEsc
	c24 := int(table24Codes[idx].len) + sign + minLinbitsFam24[n]*nEsc
	return min(c16, c24)
}

// part3LowerBound returns a lower bound on regionInfo.bits (what regionsFor
// would compute) for the quantized granule ix with partition part. It sums,
// over exactly the big-values pairs the region search costs, the per-pair
// minimum over all valid tables, then adds the count1 cost, which the region
// search computes identically. Any region layout picks one table per region,
// so its cost is at least the sum of per-pair minima; every summand is
// non-negative, so the loop stops as soon as the running sum exceeds limit and
// returns that partial sum (greater than limit, not the full bound). The
// decision "bound > limit" is the same as with the full sum.
//
// The bound holds for magnitudes up to maxQuant, which quantizeGranule
// guarantees. Beyond it rangeCost caps a region at one impossibleCost while
// this sum adds one per pair, but such a coding costs at least impossibleCost
// either way, far over any budget (maxPart23Length), so skip decisions agree.
//
// Integer-only: no float math, no libm, no assembly. The direct-table path is
// written inline because Go charges a non-inlinable call heavily (issue #48).
func part3LowerBound(ix *[576]int32, part spectrumPartition, limit int) int {
	lb := 0
	for p := range part.bigValues {
		ax, ay := abs32(ix[2*p]), abs32(ix[2*p+1])
		if ax < escTableDim && ay < escTableDim {
			lb += int(pairMinDirect[ax][ay])
		} else {
			lb += pairMinCostEsc(ax, ay)
		}
		if lb > limit {
			return lb
		}
	}
	c1, _ := count1Cost(ix, part)
	return lb + c1
}
