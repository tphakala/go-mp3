package enc

// mdctScale is the output scale MDCTGranule applies to every spectral line
// so the full encode/decode chain (FlipOddSubbands -> MDCTGranule ->
// AliasReduce, dec's l3Antialias -> l3Imdct36 -> l3ChangeSign) has unity
// gain. mdctScale = 1/9 = 2/N with N = 18, the per-subband window/spectral-
// line count. A textbook forward-MDCT / IMDCT pair carries the customary
// 2/N normalization, but the decoder's l3Imdct36 (internal/dec/imdct.go)
// omits it, so the round-trip chain carries a gain of N/2 = 9 instead of
// unity; mdctScale multiplies the encoder's MDCT output by 1/9 to cancel
// that gain and restore unity. Confirmed empirically by the Task 3 TDAC
// gate (internal/dec/encx_mdct_test.go, TestEncMdctTDACRoundTrip): with no
// output scaling (mdctScale = 1), the measured round-trip chain gain on
// granule 2 is 9.00000065 across all 576 line samples (all within 0.01% of
// the mean), i.e. exactly 9 up to float32 measurement noise.
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
// Float discipline: the inner accumulate, sum += float64(mdctCos[k][n] *
// z[n]), carries an explicit float64() conversion that is the load-bearing
// barrier against arm64 fusing the multiply into an FMA with the
// accumulator (a bare local assignment does not block that; the compiler
// fuses across statements; see filterbank.go's matrixStep for the same
// discipline). The two z-population lines above it also carry a float64()
// wrap, but their products feed a store into z[i]/z[18+i], not an adjacent
// +/-, so that wrap is not a barrier there; this was verified empirically
// (GOARCH=arm64 go build -gcflags=-S produces byte-identical codegen for
// those lines with or without the wrap, the same finding as filterbank.go's
// window()). The wrap is kept anyway, as defensive uniformity across the
// package rather than a correctness requirement. Accumulation in the inner
// sum runs left to right in index order (n = 0..35), fixing the
// association order for determinism across amd64 and arm64.
func MDCTGranule(prev, cur *[18][32]float64, xr *[576]float64) {
	mdctGranuleWindowed(prev, cur, xr, &MDCTWindow)
}

// mdctGranuleWindowed is the shared 36-point forward-MDCT body behind
// MDCTGranule (blockLong, window = &MDCTWindow) and MDCTGranuleBlock's
// blockStart (&MDCTWindowStart) and blockStop (&MDCTWindowStop) cases: only
// the window differs across the three block types that share this 36-point
// kernel, so the loop itself is factored out here, byte-identical in
// statement shape, float64() wraps and left-to-right accumulation order to
// MDCTGranule's original body (TestMdctGolden unchanged is the proof for
// the blockLong case).
func mdctGranuleWindowed(prev, cur *[18][32]float64, xr *[576]float64, window *[36]float64) {
	for b := range 32 {
		var z [36]float64
		for i := range 18 {
			z[i] = float64(prev[i][b] * window[i])
		}
		for i := range 18 {
			z[18+i] = float64(cur[i][b] * window[18+i])
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

// mdctScaleShort is the output scale MDCTGranuleBlock's short-block path
// applies to every spectral line, the short-block twin of mdctScale.
// mdctScaleShort = 1/3 = 2/N with N = 6, the per-sub-window spectral line
// count (the same 2/N normalization argument as mdctScale, which uses
// N = 18, the per-half long-window spectral line count). Confirmed
// empirically by TestEncMdctShortTDACRoundTrip (internal/dec/encx_mdct_test.go)
// exactly as mdctScale was: with no output scaling (mdctScaleShort = 1),
// the measured round-trip chain gain on granule 2's short-block decode is
// 2.999999858369531 across all 576 line samples (all within 0.01% of the
// mean), i.e. exactly 3 up to float32 measurement noise.
const mdctScaleShort = 1.0 / 3.0

// MDCTGranuleBlock computes one granule's forward MDCT for the given block
// type (blockLong, blockStart, blockShort or blockStop, blocktypes.go).
// blockLong, blockStart and blockStop share mdctGranuleWindowed,
// parameterized by their respective window; blockShort runs
// mdctGranuleShort's three 12-point MDCTs per subband instead. AliasReduce
// is NOT called here for any block type; the caller applies it afterward
// for blockLong/blockStart/blockStop only, mirroring the decoder's
// antialias gating (l3Decode, internal/dec/decode.go, skips l3Antialias for
// a pure short granule via aaBands = nLongBands - 1 = -1).
func MDCTGranuleBlock(prev, cur *[18][32]float64, xr *[576]float64, blockType int) {
	switch blockType {
	case blockStart:
		mdctGranuleWindowed(prev, cur, xr, &MDCTWindowStart)
	case blockStop:
		mdctGranuleWindowed(prev, cur, xr, &MDCTWindowStop)
	case blockShort:
		mdctGranuleShort(prev, cur, xr)
	default: // blockLong
		mdctGranuleWindowed(prev, cur, xr, &MDCTWindow)
	}
}

// mdctGranuleShort computes one granule's forward MDCT under the short
// block window: for every subband b, three 12-point MDCTs run over a
// 36-sample span formed by prev's 18 samples followed by cur's 18 samples
// (span[i] = prev[i][b] for i < 18, span[i] = cur[i-18][b] for i >= 18),
// sub-window w (0, 1, 2) reading span[6+6w : 18+6w). Output is window-major
// within the subband: xr[b*18+w*6+k] is spectral line k of sub-window w of
// subband b, the interleave l3ImdctShort (internal/dec/imdct.go) expects
// once Task A2's reorder feeds it through the decoder's own grbuf layout.
//
// Float discipline: the same left-to-right, float64()-wrapped accumulation
// discipline as mdctGranuleWindowed (see MDCTGranule's doc comment); the
// z-population line's float64() wrap is likewise kept for package-wide
// uniformity rather than because it is a proven FMA barrier there.
func mdctGranuleShort(prev, cur *[18][32]float64, xr *[576]float64) {
	for b := range 32 {
		var span [36]float64
		for i := range 18 {
			span[i] = prev[i][b]
		}
		for i := range 18 {
			span[18+i] = cur[i][b]
		}
		for w := range 3 {
			var z [12]float64
			for j := range 12 {
				z[j] = float64(span[6+6*w+j] * MDCTWindowShort[j])
			}
			for k := range 6 {
				sum := 0.0
				for j := range 12 {
					sum += float64(mdctCos12[k][j] * z[j])
				}
				xr[b*18+w*6+k] = sum * mdctScaleShort
			}
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
