//go:build !noasm

package enc

import "github.com/tphakala/simd/f64"

// psySprdT is each rate's long-block spreading matrix stored for f64.VecMat,
// one row per source partition bb: psySprdT[r][bb*psyMaxParts+b] =
// psyRateTables[r].sprd[b][bb]. psySprdTS is its short-block sibling over
// psyShortTables, with the short tables' own row width psyMaxPartsS. Both
// are permutations of the committed literals built at package init, so no
// value is computed.
var (
	psySprdT = func() (t [3][psyMaxParts * psyMaxParts]float64) {
		for r := range psyRateTables {
			sprd := &psyRateTables[r].sprd
			for b := range psyMaxParts {
				for bb := range psyMaxParts {
					t[r][bb*psyMaxParts+b] = sprd[b][bb]
				}
			}
		}
		return t
	}()
	psySprdTS = func() (t [3][psyMaxPartsS * psyMaxPartsS]float64) {
		for r := range psyShortTables {
			sprd := &psyShortTables[r].sprd
			for b := range psyMaxPartsS {
				for bb := range psyMaxPartsS {
					t[r][bb*psyMaxPartsS+b] = sprd[b][bb]
				}
			}
		}
		return t
	}()
)

// spread is the long-block spreading convolution: for every partition b it
// sums the partition energies e and the weighted unpredictabilities ct over
// all partitions bb, each weighted by sprd[b][bb], into ecb[b] and cbs[b].
// With e (or ct) as the row vector and psySprdT as the matrix, f64.VecMat
// computes every output as the scalar loop
//
//	sum := 0.0
//	for bb := range n { sum += float64(e[bb] * sprd[b][bb]) }
//
// does: from +0.0 in ascending bb, every product rounded before its add,
// never fused, on every path including the one SIMD_DISABLE=all selects (its
// documented contract, github.com/tphakala/simd v1.16.0, f64/vecmat.go). That
// is the loop the noasm build runs, so the result has its bits;
// TestPsySpreadParity pins that. The stride is the stored row width
// psyMaxParts; the rate's partition count n only bounds the rows and columns
// used.
func (p *PsyModel) spread() {
	n := p.tab.nParts
	m := psySprdT[p.srIndex][:]
	f64.VecMat(p.ecb[:n], p.e[:n], m, psyMaxParts)
	f64.VecMat(p.cbs[:n], p.ct[:n], m, psyMaxParts)
}

// spreadShort is the short-block spreading convolution over the model's
// rate: ecbS[b] is the sum over all partitions bb of eS[bb]*sprd[b][bb],
// computed by f64.VecMat as in spread, with stride psyMaxPartsS.
func (p *PsyModel) spreadShort() {
	n := psyShortTables[p.srIndex].nParts
	f64.VecMat(p.ecbS[:n], p.eS[:n], psySprdTS[p.srIndex][:], psyMaxPartsS)
}
