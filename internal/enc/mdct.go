package enc

// mdctScale is the output scale MDCTGranule applies to every spectral line
// so the full encode/decode chain (FlipOddSubbands -> MDCTGranule ->
// AliasReduce, dec's l3Antialias -> l3Imdct36 -> l3ChangeSign) has unity
// gain. Measured by the Task 3 TDAC gate
// (internal/dec/encx_mdct_test.go, TestEncMdctTDACRoundTrip): with no
// output scaling (mdctScale = 1), the measured round-trip chain gain on
// granule 2 is 9.00000065 (576 line samples, all within 0.01% of the
// mean), i.e. exactly 9 up to float32 measurement noise. l3Imdct36's fast
// DCT-III decomposition (internal/dec/imdct.go) folds in its own
// normalization relative to the direct O(N^2) ISO C.1.5.1 MDCT sum this
// package computes, and that normalization lands at a factor of 9, not the
// 1/2 a plain orthogonality-style MDCT/IMDCT pair would carry; mdctScale
// folds in the matching 1/9 so the chain measures unity gain with no
// test-side normalization.
const mdctScale = 1.0 / 9.0

// FlipOddSubbands negates every odd-indexed sample of every odd subband
// (the polyphase frequency inversion; the decoder undoes it in
// l3ChangeSign, internal/dec/decode.go). Applied to each granule's subband
// samples after analysis, before they enter MDCT history: s[t][b] is
// negated for t odd (1, 3, .. 17) and b odd (1, 3, .. 31), mirroring
// l3ChangeSign's index pattern (it negates odd sample indices 1,3..17 of
// subbands 1,3..31).
//
// Exported: like Filterbank/AnalyzeGranule/PCMScale (see doc.go), the Task
// 3 brief's illustrative interface listed this unexported, but its
// decoder-inverse gates live in internal/dec (package dec) and must call it
// directly; Go visibility is package-scoped, so exporting is the only way
// to satisfy that cross-package test requirement.
func FlipOddSubbands(s *[18][32]float64) {
	for t := 1; t < 18; t += 2 {
		for b := 1; b < 32; b += 2 {
			s[t][b] = -s[t][b]
		}
	}
}

// MDCTGranule computes the 36-point forward MDCT with the long sine window
// for every subband: z = window applied over prev granule's 18 samples
// then cur's 18, per subband; output xr[b*18+k] is spectral line k of
// subband b. prev is zero for the first granule of a stream.
//
// Exported for the same cross-package test reason as FlipOddSubbands.
//
// Float discipline: every product feeding a following sum carries an
// explicit float64() conversion, the only reliable barrier against arm64
// fusing the multiply into an FMA against the accumulator (see
// filterbank.go's AnalyzeGranule for the same discipline and its
// rationale). Accumulation runs left to right in index order (n = 0..35),
// fixing the association order for determinism across amd64 and arm64.
func MDCTGranule(prev, cur *[18][32]float64, xr *[576]float64) {
	for b := range 32 {
		var z [36]float64
		for i := range 18 {
			z[i] = float64(prev[i][b] * MDCTWindow[i])
		}
		for i := range 18 {
			z[18+i] = float64(cur[i][b] * MDCTWindow[18+i])
		}
		for k := range 18 {
			sum := 0.0
			for n := range 36 {
				sum += float64(mdctCos[k][n] * z[n])
			}
			xr[b*18+k] = sum * mdctScale
		}
	}
}

// AliasReduce applies the encoder-side aliasing butterflies (the exact
// inverse rotation of the decoder's l3Antialias) across the 31 subband
// boundaries of a long-block granule, in place.
//
// Exported for the same cross-package test reason as FlipOddSubbands.
//
// CORRECTNESS PIN (re-derived and verified empirically against
// TestEncMdctTDACRoundTrip and TestEncAliasCancellation,
// internal/dec/encx_mdct_test.go; this deviates from the Task 3 brief's
// literal Step 3 pseudocode, see the task report for the full derivation):
// l3Antialias (internal/dec/stereo.go) computes, in terms of the decoder's
// own tables, g[18+i] = u*gAA0 - d*gAA1, g[17-i] = u*gAA1 + d*gAA0, with
// both gAA0 and gAA1 POSITIVE. AliasCS is bit-exact equal to gAA0, but
// AliasCA (Step 1c: c/sqrt(1+c*c) with every Table B.9 c negative) is the
// SIGNED, NEGATIVE value, so gAA1 = -AliasCA, not +AliasCA. Substituting
// that identity into l3Antialias's formula and solving the resulting
// linear system for the encoder's (u2,d2) such that applying l3Antialias
// to (u2,d2) reproduces the original (u,d) gives:
//
//	u2 = u*AliasCS[i] - d*AliasCA[i]
//	d2 = u*AliasCA[i] + d*AliasCS[i]
//
// A naive transpose of the rotation written with a POSITIVE "ca" (i.e.
// u2 = u*cs + d*ca, d2 = d*cs - u*ca) is only correct if ca is positive;
// plugging in the signed, negative AliasCA there flips a sign relative to
// the true inverse and produces gross reconstruction error (empirically
// measured max abs error 1.24 on values of order 1, versus ~1e-7 with the
// formula above). TestEncMdctTDACRoundTrip is the arbiter: it fails loudly
// on a wrong sign, and confirms the formula above, not the naive one.
func AliasReduce(xr *[576]float64) {
	for b := range 31 {
		for i := range 8 {
			u := xr[(b+1)*18+i]
			d := xr[b*18+17-i]
			u2 := float64(u*AliasCS[i]) - float64(d*AliasCA[i])
			d2 := float64(u*AliasCA[i]) + float64(d*AliasCS[i])
			xr[(b+1)*18+i], xr[b*18+17-i] = u2, d2
		}
	}
}
