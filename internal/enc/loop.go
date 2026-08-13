package enc

// loop.go: the ISO distortion-control outer loop for one granule-channel,
// wrapping codeGranule (the inner global_gain rate loop). See the phase 4
// increment 4 plan's design decisions 2-7 for the full rationale.
//
// Not yet wired into the encoder: outerLoop is a pure function here, only
// exercised by loop_test.go. Task 5 wires it into codeFrame and re-freezes
// the golden.

// outerLoopMaxIters bounds the distortion-control loop (design decision
// 6): single-worst-band amplification advances one band-step per
// iteration, so the cap scales with band count times per-band range; the
// progress guard and the unfixable set end almost every real run far
// earlier.
const outerLoopMaxIters = 150

// worstViolator returns the fixable band with the maximum noise/xmin
// ratio among bands where noise > xmin, or -1 if none; ties break toward
// the LOWEST sfb index (deterministic). sfb 21 carries no scalefactor
// (sf.scf only spans indices 0..20, len(sf.scf) == 21) so it is never a
// candidate, independent of unfixable's contents. Pure function,
// unit-tested directly (design decision 2).
func worstViolator(noise, xmin *[22]float64, unfixable *[22]bool, sf *scfState) int {
	best := -1
	bestRatio := 0.0
	for sfb := range len(sf.scf) {
		if unfixable[sfb] || noise[sfb] <= xmin[sfb] {
			continue
		}
		if ratio := noise[sfb] / xmin[sfb]; ratio > bestRatio {
			bestRatio = ratio
			best = sfb
		}
	}
	return best
}

// preflagReady reports whether every band 11..20 already carries a scf at
// least pretabLong[sfb]: the precondition for the pure preflag
// re-expression (design decision 4), where subtracting pretabLong[sfb]
// from scf[sfb] must never go negative.
func preflagReady(sf *scfState) bool {
	for sfb := 11; sfb < 21; sfb++ {
		if sf.scf[sfb] < pretabLong[sfb] {
			return false
		}
	}
	return true
}

// betterPass reports whether a candidate pass (excess, ratio, over) is a
// STRICT improvement over the current best pass, ordered by smallest
// total excess noise energy, then smallest max-band violation ratio, then
// fewest violating bands (design decision 5). Strict improvement only, so
// among exact ties on all three the earliest pass keeps the crown.
func betterPass(excess, ratio float64, over int, bestExcess, bestRatio float64, bestOver int) bool {
	if excess != bestExcess {
		return excess < bestExcess
	}
	if ratio != bestRatio {
		return ratio < bestRatio
	}
	return over < bestOver
}

// outerLoop runs the ISO distortion-control loop for one granule-channel:
// repeatedly it (a) picks scalefac_compress for the current scalefactor
// state and runs the inner rate loop (codeGranule) within budget-part2,
// (b) measures per-band noise against xmin, (c) empirically checks
// whether the amplification applied at the tail of the PREVIOUS iteration
// actually reduced its own band's noise (the futility check below), (d)
// keeps the best pass seen so far (betterPass), (e) amplifies ONLY the
// single worst violating fixable band by one scalefactor unit, escalating
// scalefac_scale (ceil re-expression) when that band is at its slen cap,
// and re-expressing with preflag once all upper bands cover pretab.
//
// Futility check: minGlobalGain (Task 2, unmodified) always returns the
// exact boundary-tight global gain for whichever band's worst line is
// closest to the maxQuant anti-overflow ceiling. Amplifying THAT band
// raises minGlobalGain by the identical amount (the two terms of
// (quantGainBase-gg)+bandExtraQuarters(sfb) cancel exactly), so the
// band's own noise does not improve, while every other band's noise can
// only worsen (a higher global_gain never helps an unamplified band).
// This is real, expected MPEG behavior, not a bug in minGlobalGain: a
// band that anchors the anti-overflow floor cannot be improved by
// scalefactor amplification alone, no matter how many units are spent on
// it. So every iteration, after the previous iteration amplified some
// band and this iteration re-measured noise against it, outerLoop
// compares that band's new noise to its noise just before the
// amplification: if it did not strictly decrease, the band is floor-bound
// and gets marked unfixable (worstViolator then skips it, exactly like a
// band at its scalefac_scale cap), and the working scalefactor state is
// rolled back to the best pass seen so far, so the futile global_gain
// increase does not degrade every other band for nothing. The loop then
// tries a different worst violator on the FOLLOWING iteration, gracefully
// reallocating precision to bands that can actually be improved.
//
// Exits on zero violations, no fixable band, the iteration cap, part2
// starving the budget, or the strict progress guard: if the effective
// per-band amplification (bandExtraQuarters over sfbs 0..20) is unchanged
// from the previous iteration, the loop breaks immediately rather than
// re-measuring identical noise forever (this fires precisely when an
// amplification attempt only sets unfixable, touching no scf).
//
// gc is caller-owned working state, overwritten every pass; best is
// caller-owned scratch (the Encoder preallocates one per granule-channel)
// that the loop never allocates into, only copies gc's value in and back
// out of. The returned iteration count feeds tests and diagnostics.
func outerLoop(xr *[576]float64, xmin *[22]float64, budgetBits int, sfbWidths *[22]int, gc, best *granuleCoding) (iters int) {
	var sf scfState
	var unfixable [22]bool

	// prevExtra starts at an unreachable sentinel (bandExtraQuarters never
	// returns negative) so the very first iteration's progress-guard check
	// never trips: extra != prevExtra unconditionally on iteration 1.
	var prevExtra [21]int
	for s := range prevExtra {
		prevExtra[s] = -1
	}

	bestSet := false
	var bestExcess, bestRatio float64
	var bestOver int

	// pendingBand/pendingNoise carry the band amplified at the tail of the
	// PREVIOUS iteration (if any) and its noise just before that
	// amplification, so THIS iteration's futility check has a baseline to
	// compare the re-measured noise against. -1 means nothing is pending:
	// either this is iteration 1, or the previous amplification attempt
	// found the band already at its scalefac_scale cap and only set
	// unfixable directly, touching no scf (nothing to verify).
	pendingBand := -1
	var pendingNoise float64

	for iters = 1; iters <= outerLoopMaxIters; iters++ {
		var extra [21]int
		for s := range extra {
			extra[s] = sf.bandExtraQuarters(s)
		}
		if extra == prevExtra {
			break // strict progress guard: last iteration changed nothing
		}
		prevExtra = extra

		gc.sf = sf
		idx, part2, ok := chooseScalefacCompress(&gc.sf, 0)
		if !ok || part2 >= budgetBits {
			break // part2 alone starves the Huffman budget
		}
		codeGranule(xr, budgetBits-part2, sfbWidths, gc)
		gc.scfCompress, gc.part2Bits = idx, part2
		gc.part23Length = part2 + gc.ri.bits

		var noise [22]float64
		noiseGranule(xr, &gc.ix, gc.globalGain, &gc.sf, sfbWidths, &noise)

		if pendingBand >= 0 {
			target := pendingBand
			pendingBand = -1
			if !(noise[target] < pendingNoise) {
				// Empirically futile: this band anchors minGlobalGain, so
				// amplifying it further cannot help. Mark it permanently
				// unfixable and undo the futile global_gain cost by
				// rolling back to the best pass seen so far, rather than
				// letting it degrade every other band for nothing; a new
				// worst violator is picked on the next iteration.
				unfixable[target] = true
				sf = best.sf
				continue
			}
		}

		var excess, ratio float64
		over := 0
		for sfb := range 22 {
			if noise[sfb] <= xmin[sfb] {
				continue
			}
			over++
			excess += noise[sfb] - xmin[sfb]
			if r := noise[sfb] / xmin[sfb]; r > ratio {
				ratio = r
			}
		}

		if !bestSet || betterPass(excess, ratio, over, bestExcess, bestRatio, bestOver) {
			*best = *gc
			bestSet = true
			bestExcess, bestRatio, bestOver = excess, ratio, over
		}

		if over == 0 {
			return iters // gc already holds the winning (best) pass
		}

		w := worstViolator(&noise, xmin, &unfixable, &sf)
		if w < 0 {
			break // every remaining violator is unfixable
		}

		sfCap := sfMaxLo
		if w >= slen1Bands {
			sfCap = sfMaxHi
		}
		switch {
		case sf.scf[w] < sfCap:
			sf.scf[w]++
			pendingBand, pendingNoise = w, noise[w]
		case sf.scalefacScale == 0:
			sf.scalefacScale = 1
			for s := range sf.scf {
				sf.scf[s] = (sf.scf[s] + 1) >> 1
			}
			pendingBand, pendingNoise = w, noise[w]
		default:
			unfixable[w] = true
		}

		if sf.preflag == 0 && preflagReady(&sf) {
			sf.preflag = 1
			for sfb := 11; sfb < 21; sfb++ {
				sf.scf[sfb] -= pretabLong[sfb]
			}
		}
	}

	if bestSet {
		*gc = *best
	}
	return iters
}
