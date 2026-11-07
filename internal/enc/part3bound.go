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

// buildPairMinDirect fills pairMinDirect by costing every pair below
// escTableDim in every valid table and keeping the cheapest.
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

// buildMinLinbits fills minLinbitsFam16 or minLinbitsFam24 from a family's
// ascending linbits list: entry n is the smallest linbits that is at least n.
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

// nMagClasses is the number of magnitude classes magClassOf distinguishes.
const nMagClasses = 29

// magClassOf maps a quantized magnitude 0..maxQuant to its class: the value
// itself up to escMaxDirect, then one class per bit length of the escape part
// (m - escMaxDirect). pairMinCostEsc reads a pair only through min(m, 15),
// m != 0 and the bit length of the larger escape part, so every magnitude in
// a class costs the same minimum bits (TestMonoPairFloorTable pins this over
// all pairs). maxQuant lands in class nMagClasses-1.
func magClassOf(m int32) uint8 {
	if m <= escMaxDirect {
		return uint8(m)
	}
	return uint8(escMaxDirect + bits.Len32(uint32(m-escMaxDirect)))
}

// magClassRep is the smallest magnitude of class c.
func magClassRep(c int) int32 {
	if c <= escMaxDirect {
		return int32(c)
	}
	return int32(escMaxDirect + 1<<(c-escMaxDirect-1))
}

// monoPairFloor[cx][cy] is a lower bound on the part3 bits one line pair
// spends when its magnitudes lie in classes of at least cx and cy, and it is
// non-decreasing in both indices. It exists for the exact global_gain scan's
// start: summed over the 288 pairs of a quantized granule it gives a
// partition-free lower bound on ri.bits that, unlike part3LowerBound, never
// increases with global_gain, so a binary search can find the first gain the
// bound admits.
//
// Why it is sound: the partition splits the pairs into big-values pairs,
// count1 pairs and rzero pairs. A big-values pair costs at least its minimum
// over all valid tables (minPair, part3LowerBound's own per-pair term). A
// count1 quad starts at an even line, so it is exactly two pairs, and it costs
// at least 4 + its nonzero count under both count1 tables (table A lengths for
// presence patterns 1..15 are at least 4, table B is exactly 4 + signs); a
// pair of magnitudes at most 1 with s nonzero lines is therefore charged
// s + 2, at most half of the quad's 4 + s1 + s2. An rzero pair costs 0 and has
// floor 0. The entry is the smaller of minPair and, for pairs of at most 1,
// the count1 charge, which is below the true cost whichever region the
// partition picks. With the current tables minPair already equals that charge
// there (3 for one nonzero line, 4 for two, 0 for none), so the explicit
// minimum changes no entry; it keeps the bound sound if a table ever changes,
// and TestMonoPairFloorTable pins the premises.
//
// Why it is monotone: each line's magnitude is non-increasing in global_gain
// (TestQuantizeMonotone), the class map is non-decreasing, and the table is
// closed under suffix minimum (entry = min over all larger-or-equal classes),
// so the sum cannot increase as the gain rises. The bound must therefore not
// include any partition term: the partition moves with the gain and
// part3LowerBound, which depends on it, is not monotone.
//
// Integer-only (841 minPair calls at init), so it is identical on every
// architecture.
var monoPairFloor = buildMonoPairFloor()

func buildMonoPairFloor() (fm [nMagClasses][nMagClasses]uint8) {
	var f [nMagClasses][nMagClasses]int
	for cx := range nMagClasses {
		for cy := range nMagClasses {
			v := minPair(magClassRep(cx), magClassRep(cy))
			if cx <= 1 && cy <= 1 {
				v = min(v, cx+cy+2)
				if cx+cy == 0 {
					v = 0
				}
			}
			f[cx][cy] = v
		}
	}
	for cx := nMagClasses - 1; cx >= 0; cx-- {
		for cy := nMagClasses - 1; cy >= 0; cy-- {
			v := f[cx][cy]
			if cx+1 < nMagClasses {
				v = min(v, int(fm[cx+1][cy]))
			}
			if cy+1 < nMagClasses {
				v = min(v, int(fm[cx][cy+1]))
			}
			fm[cx][cy] = uint8(v)
		}
	}
	return fm
}

// minPair is the cheapest cost of the magnitude pair (ax, ay) in any valid
// big-values table, as part3LowerBound sums it.
func minPair(ax, ay int32) int {
	if ax < escTableDim && ay < escTableDim {
		return int(pairMinDirect[ax][ay])
	}
	return pairMinCostEsc(ax, ay)
}
