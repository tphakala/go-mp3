package enc

// loop.go: the ISO distortion-control outer loop for one granule-channel,
// wrapping codeGranule (the inner global_gain rate loop). See the phase 4
// increment 4 plan's design decisions 2-7 for the full rationale.
//
// Wired into the encoder: codeFrame (encoder.go) calls outerLoop once per
// granule-channel with the psymodel's calibrated thresholds. loop_test.go
// also exercises it directly as a pure function.

// outerLoopMaxIters bounds the distortion-control loop (design decision
// 6): single-worst-band amplification advances one band-step per
// iteration, so the cap scales with band count times per-band range; the
// progress guard and the unfixable set end almost every real run far
// earlier.
const outerLoopMaxIters = 150

// worstViolator returns the fixable scalefactor-bearing band with the
// maximum noise/xmin ratio among bands where noise > xmin, or -1 if none;
// ties break toward the LOWEST band index (deterministic). Only bands
// b < lay.nScf carry a scalefactor (long band 21, or the highest short
// triple 36..38) so the loop never considers a band beyond lay.nScf,
// independent of unfixable's contents. Pure function, unit-tested directly
// (design decision 2).
func worstViolator(noise, xmin *[39]float64, unfixable *[39]bool, sf *scfState, lay *bandLayout) int {
	best := -1
	bestRatio := 0.0
	for b := range lay.nScf {
		if unfixable[b] || noise[b] <= xmin[b] {
			continue
		}
		if ratio := noise[b] / xmin[b]; ratio > bestRatio {
			bestRatio = ratio
			best = b
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

// maskingMetrics reduces a granule-channel's per-band noise (already
// computed against xmin, e.g. via noiseGranule) to the same three
// candidate-pass quantities outerLoop's own iteration loop computes
// internally every pass and orders by betterPass: excess (total
// noise-over-xmin energy summed across violating bands), ratio (the
// worst single-band noise/xmin ratio), and over (the violating-band
// count). Factored out (additively; outerLoop's own loop is untouched,
// preserving its arm64-verified bytes) so codeFrame's masking-driven
// budget escalation (Task 3 Step 3) can compare codings produced at
// different budget grants with the exact ordering outerLoop already
// trusts within one budget.
func maskingMetrics(noise, xmin *[39]float64, lay *bandLayout) (excess, ratio float64, over int) {
	for sfb := range lay.nBands {
		if noise[sfb] <= xmin[sfb] {
			continue
		}
		over++
		excess += noise[sfb] - xmin[sfb]
		if r := noise[sfb] / xmin[sfb]; r > ratio {
			ratio = r
		}
	}
	return excess, ratio, over
}

// escalateSubblockGain performs one step of the short-granule subblock_gain
// re-expression (design decision 7): raise band w's window by one
// subblock_gain unit (8 quarter-steps, ISO's dequant formula) and give back
// the equivalent scf units from every scf-bearing band in the SAME window,
// so the window's total effective amplification is unchanged except for
// the extra headroom the new ssg step buys band w. The give-back is
// exactly 8/(2*(scalefacScale+1)) scf units per band (2 units at
// scalefacScale 1, 4 at scalefacScale 0), floored at 0 (a band already
// below the give-back keeps whatever headroom it has, it does not go
// negative). Factored out of outerLoop's escalation switch (the
// worstViolator/maskingMetrics precedent) so it can be unit-tested
// directly, deterministically, without needing a full outerLoop run to
// reach this exact state.
func escalateSubblockGain(sf *scfState, lay *bandLayout, w int) {
	win := lay.win[w]
	sf.subblockGain[win]++
	giveBack := 8 / (2 * (sf.scalefacScale + 1))
	for b := range lay.nScf {
		if lay.win[b] != win {
			continue
		}
		if sf.scf[b] -= giveBack; sf.scf[b] < 0 {
			sf.scf[b] = 0
		}
	}
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
// per-band amplification (bandExtraQuarters over the layout's coding bands
// 0..nBands-1; the last band's amplification is a constant 0) is unchanged
// from the previous iteration, the loop breaks immediately rather than
// re-measuring identical noise forever (this fires precisely when an
// amplification attempt only sets unfixable, touching no scf).
//
// Short granules (design decision 7) add one more escalation rung between
// the scalefac_scale ceil re-expression and giving up on a band: when the
// worst violator's window has spare subblock_gain headroom (< 7), ssg for
// that window is raised by one unit and every scf-bearing band sharing the
// window gives back the equivalent scf units, a pure re-expression of the
// window's baseline amplification that buys the violating band more
// individual headroom. Because the re-expression is an exact no-op on
// every band's bandExtraQuarters, it needs the ssgJustApplied exemption
// below to survive the strict progress guard for one iteration, long
// enough for the FOLLOWING iteration's worstViolator pass to spend the
// freed headroom on a genuine scf[w]++; see ssgJustApplied's doc comment.
// preflag re-expression only applies to long granules (a short granule's
// preflag is always 0).
//
// gc is caller-owned working state, overwritten every pass; best is
// caller-owned scratch (the Encoder preallocates a single reusable buffer)
// that the loop never allocates into, only copies gc's value in and back
// out of. The returned iteration count feeds tests and diagnostics.
func outerLoop(xr *[576]float64, xmin *[39]float64, budgetBits int, lay *bandLayout, gc, best *granuleCoding) (iters int) {
	var sf scfState
	var unfixable [39]bool

	// prevExtra starts at an unreachable sentinel (bandExtraQuarters never
	// returns negative) so the very first iteration's progress-guard check
	// never trips: extra != prevExtra unconditionally on iteration 1.
	var prevExtra [39]int
	for s := range lay.nBands {
		prevExtra[s] = -1
	}

	bestSet := false
	var bestExcess, bestRatio float64
	var bestOver int

	// pendingBand/pendingNoise carry the band amplified at the tail of the
	// PREVIOUS iteration (if any) and its noise just before that
	// amplification, so THIS iteration's futility check has a baseline to
	// compare the re-measured noise against. -1 means nothing is pending:
	// either this is iteration 1, the previous amplification attempt found
	// the band already at its scalefac_scale cap and only set unfixable
	// directly (touching no scf, nothing to verify), or the previous
	// iteration was a subblock_gain re-expression (see ssgJustApplied).
	pendingBand := -1
	var pendingNoise float64

	// ssgJustApplied is a one-iteration exemption from the strict progress
	// guard (design decision 7's integration fix): a subblock_gain
	// re-expression (escalateSubblockGain) is a deliberate NO-OP on every
	// band's bandExtraQuarters by construction (it raises the window's ssg
	// by one unit, worth exactly 8 quarter-steps, and gives back exactly
	// that many quarter-steps from every scf-bearing band sharing the
	// window), so the iteration right after it always recomputes an
	// identical extra[] to prevExtra. Without this exemption the progress
	// guard reads that as "nothing changed" and breaks immediately,
	// discarding the freed scf headroom before the very next iteration's
	// worstViolator pass can spend it on a genuine scf[w]++ (a real PR A
	// review finding: the re-expression rung was integrated but never
	// actually reachable). The exemption is consumed unconditionally after
	// one use, and no pendingBand is registered for the re-expression
	// iteration itself (there is nothing to verify: the re-expression does
	// not change noise on its own, by construction, so a futility check
	// against it would always spuriously fire).
	ssgJustApplied := false

	for iters = 1; iters <= outerLoopMaxIters; iters++ {
		var extra [39]int
		for s := range lay.nBands {
			extra[s] = sf.bandExtraQuarters(s, lay)
		}
		if extra == prevExtra && !ssgJustApplied {
			break // strict progress guard: last iteration changed nothing
		}
		ssgJustApplied = false
		prevExtra = extra

		gc.sf = sf
		idx, part2, ok := chooseScalefacCompress(&gc.sf, 0, lay)
		if !ok || part2 >= budgetBits {
			break // part2 alone starves the Huffman budget
		}
		// codeGranule caps its OWN ri.bits at maxPart23Length on its own
		// (frame.go's doc comment on that constant), a cap sized for
		// Phase 3's part2Bits==0 callers, where ri.bits alone equals the
		// granule's whole part_2_3_length. Once part2 is nonzero (Task 4
		// onward), that cap alone is not enough: it bounds ri.bits, not
		// part2+ri.bits, so a high-bitrate granule (budgetBits can land
		// AT maxPart23Length, e.g. 320kbps/44.1kHz/mono measures
		// 4092-4096) could still let part2+ri.bits overflow the 12-bit
		// part_2_3_length side-info field by exactly part2 bits, which
		// bits.Writer.WriteBits then silently truncates (a real bug this
		// project's own TestEncoderStructuralGrid caught at that exact
		// rate). Applying the maxPart23Length cap BEFORE subtracting
		// part2, rather than after, budgets ri.bits at
		// maxPart23Length-part2 instead, so codeGranule's redundant
		// internal min(...,maxPart23Length) can never bind on its own and
		// part2+ri.bits <= maxPart23Length always holds.
		huffBudget := min(budgetBits, maxPart23Length) - part2
		codeGranule(xr, huffBudget, lay, gc)
		gc.scfCompress, gc.part2Bits = idx, part2
		gc.part23Length = part2 + gc.ri.bits

		var noise [39]float64
		noiseGranule(xr, &gc.ix, gc.globalGain, &gc.sf, lay, &noise)

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
		for sfb := range lay.nBands {
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

		w := worstViolator(&noise, xmin, &unfixable, &sf, lay)
		if w < 0 {
			break // every remaining violator is unfixable
		}

		sfCap := sfMaxLo
		if w >= lay.slen1End {
			sfCap = sfMaxHi
		}
		switch {
		case sf.scf[w] < sfCap:
			sf.scf[w]++
			pendingBand, pendingNoise = w, noise[w]
		case sf.scalefacScale == 0:
			sf.scalefacScale = 1
			for s := range lay.nScf {
				sf.scf[s] = (sf.scf[s] + 1) >> 1
			}
			pendingBand, pendingNoise = w, noise[w]
		case lay.short && sf.subblockGain[lay.win[w]] < 7:
			// A pure re-expression, no futility check to register (see
			// ssgJustApplied's doc comment): the freed scf headroom is
			// spent by a genuine scf[w]++ on a later iteration, which DOES
			// register a pendingBand of its own.
			escalateSubblockGain(&sf, lay, w)
			ssgJustApplied = true
		default:
			unfixable[w] = true
		}

		if !lay.short && sf.preflag == 0 && preflagReady(&sf) {
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
