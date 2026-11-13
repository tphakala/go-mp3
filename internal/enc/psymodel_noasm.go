//go:build noasm

package enc

// spread is the long-block spreading convolution: for every partition b it
// sums the partition energies e and the weighted unpredictabilities ct over
// all partitions bb, each weighted by sprd[b][bb], into ecb[b] and cbs[b].
// Each sum starts at +0.0 and runs in ascending bb; the float64() conversion
// on each product is the barrier that keeps arm64 from fusing it into the
// add. This build links no simd code.
func (p *PsyModel) spread() {
	tab := p.tab
	n := tab.nParts
	for b := range n {
		var ecb, cbs float64
		sp := &tab.sprd[b]
		for bb := range n {
			ecb += float64(p.e[bb] * sp[bb])
			cbs += float64(p.ct[bb] * sp[bb])
		}
		p.ecb[b] = ecb
		p.cbs[b] = cbs
	}
}

// spreadShort is the short-block spreading convolution over the model's
// rate: ecbS[b] is the sum over all partitions bb of eS[bb]*sprd[b][bb],
// accumulated like spread's sums.
func (p *PsyModel) spreadShort() {
	tab := &psyShortTables[p.srIndex]
	n := tab.nParts
	for b := range n {
		var ecb float64
		sp := &tab.sprd[b]
		for bb := range n {
			ecb += float64(p.eS[bb] * sp[bb])
		}
		p.ecbS[b] = ecb
	}
}
