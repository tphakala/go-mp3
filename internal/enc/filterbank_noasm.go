//go:build noasm

package enc

// partialSums implements the ISO C.1.3 "partial calculation" step: 64 sums,
// each of 8 windowed samples spaced 64 apart, into y. Plain adds of already
// rounded values, so there is no fusion hazard. This build links no simd code.
func partialSums(z *[512]float64, y *[64]float64) {
	for j := range 64 {
		sum := 0.0
		for k := range 8 {
			sum += z[j+k*64]
		}
		y[j] = sum
	}
}

// matrixStep implements the ISO C.1.3 matrixing step: each of the 32 subband
// samples is the dot product of y with one row of fbMatrix. Eight outputs
// share each loaded y[j]; every output still has its own accumulator, starts
// at +0.0 and adds its products in ascending j, so the result is the one-
// output-at-a-time loop's. The float64() conversion on each product is the
// barrier that keeps arm64 from fusing it into the add.
func matrixStep(y *[64]float64, out *[32]float64) {
	for b := 0; b < 32; b += 8 {
		m0, m1, m2, m3 := &fbMatrix[b], &fbMatrix[b+1], &fbMatrix[b+2], &fbMatrix[b+3]
		m4, m5, m6, m7 := &fbMatrix[b+4], &fbMatrix[b+5], &fbMatrix[b+6], &fbMatrix[b+7]
		var s0, s1, s2, s3, s4, s5, s6, s7 float64
		for j := range 64 {
			yj := y[j]
			s0 += float64(m0[j] * yj)
			s1 += float64(m1[j] * yj)
			s2 += float64(m2[j] * yj)
			s3 += float64(m3[j] * yj)
			s4 += float64(m4[j] * yj)
			s5 += float64(m5[j] * yj)
			s6 += float64(m6[j] * yj)
			s7 += float64(m7[j] * yj)
		}
		out[b], out[b+1], out[b+2], out[b+3] = s0, s1, s2, s3
		out[b+4], out[b+5], out[b+6], out[b+7] = s4, s5, s6, s7
	}
}
