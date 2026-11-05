//go:build (amd64 || arm64) && !noasm

package enc

// fbMatrixT is fbMatrix transposed: row j holds coefficient j of all 32
// subbands, so the kernels load one row per partial sum.
var fbMatrixT = transposeFBMatrix()

// transposeFBMatrix returns fbMatrix transposed.
func transposeFBMatrix() (m [64][32]float64) {
	for b := range 32 {
		for j := range 64 {
			m[j][b] = fbMatrix[b][j]
		}
	}
	return m
}
