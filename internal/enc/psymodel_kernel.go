//go:build (amd64 || arm64) && !noasm

package enc

// psySprdT and psySprdTS are the long and short spreading matrices of every
// rate transposed, sprd[b][bb] stored at [bb][b]: the kernels read one
// contiguous row per source partition bb while each lane accumulates one
// destination partition b in the reference's bb order.
var psySprdT, psySprdTS = transposeSpreading()

// transposeSpreading returns the transposed spreading matrices of
// psyRateTables and psyShortTables.
func transposeSpreading() (long [3][psyMaxParts][psyMaxParts]float64, short [3][psyMaxPartsS][psyMaxPartsS]float64) {
	for r := range psyRateTables {
		for b := range psyMaxParts {
			for bb := range psyMaxParts {
				long[r][bb][b] = psyRateTables[r].sprd[b][bb]
			}
		}
	}
	for r := range psyShortTables {
		for b := range psyMaxPartsS {
			for bb := range psyMaxPartsS {
				short[r][bb][b] = psyShortTables[r].sprd[b][bb]
			}
		}
	}
	return long, short
}
