//go:build !noasm

package enc

import "github.com/tphakala/simd/f64"

// The two steps below rest on the documented contract of f64.VecMat
// (github.com/tphakala/simd v1.16.0, f64/vecmat.go): dst[c] is the sum over r
// of x[r]*m[r*stride+c], each column accumulated from +0.0 in ascending r,
// every product rounded to float64 before its add, never fused, on every
// path including the one SIMD_DISABLE=all selects.

// fbMatrixT is fbMatrix stored for f64.VecMat, one row per input y[j]:
// fbMatrixT[j*32+b] = fbMatrix[b][j]. It is a permutation of the committed
// literals built at package init, so no value is computed.
var fbMatrixT = func() (t [64 * 32]float64) {
	for b := range 32 {
		for j := range 64 {
			t[j*32+b] = fbMatrix[b][j]
		}
	}
	return t
}()

// fbOnes8 is the row vector that turns f64.VecMat into the eight-term column
// sum of partialSums.
var fbOnes8 = [8]float64{1, 1, 1, 1, 1, 1, 1, 1}

// partialSums implements the ISO C.1.3 "partial calculation" step: 64 sums,
// each of 8 windowed samples spaced 64 apart, into y. z read as 8 rows of 64
// puts the samples of one sum in one column, so VecMat with a row vector of
// ones adds each column in ascending row order. 1*z[i] is exact (a zero
// keeps its sign), so every sum has the bits of the scalar loop
//
//	sum := 0.0
//	for k := range 8 { sum += z[j+k*64] }
//
// which the noasm build runs. TestFilterbankParity pins that.
func partialSums(z *[512]float64, y *[64]float64) {
	f64.VecMat(y[:], fbOnes8[:], z[:], 64)
}

// matrixStep implements the ISO C.1.3 matrixing step: each of the 32 subband
// samples is the dot product of y with one row of fbMatrix. With y as the
// row vector and fbMatrixT as the matrix, VecMat computes every output as
// the scalar loop
//
//	sum := 0.0
//	for j := range 64 { sum += float64(fbMatrix[b][j] * y[j]) }
//
// does, so the result has its bits. TestFilterbankParity pins that.
func matrixStep(y *[64]float64, out *[32]float64) {
	f64.VecMat(out[:], y[:], fbMatrixT[:], 32)
}
