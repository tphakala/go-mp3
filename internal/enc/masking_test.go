package enc

import (
	"fmt"
	"os"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// maskingGridFrames bounds each TestEncoderMaskingContract case to a short
// program instead of a full second: the measured design decision 9 defect
// cases (escalateForMasking's doc comment) showed every violation within
// the first 0-10 frames, and the reservoir reaches steady-state occupancy
// well inside this window, so 32 frames exercises the escalation path
// fully without paying for a full-duration encode across the whole grid.
// That matters because outerLoop's own worst-case iteration cap is
// routinely hit by broadband lcgnoise content (each such call costs
// hundreds of milliseconds), and escalation calls outerLoop more times
// per frame than Stage 1 alone did, so a full-second sweep over the whole
// grid risks the test binary's timeout.
const maskingGridFrames = 32

// The two TestEncoderMaskingContract programs (maskingGridProgram's name
// argument), named constants rather than repeated string literals.
const (
	maskingProgMultiTone = "multitone"
	maskingProgLCGNoise  = "lcgnoise"
)

// maskingGridProgram builds nch channels of nFrames*1152 samples for
// TestEncoderMaskingContract's grid: "multitone" (buildMultiTone's
// project-wide convention, phase-offset per channel so stereo is
// decorrelated, peak 0.9) and "lcgnoise" (broadband stored-LCG noise,
// amplitude 0.9, a different per-channel seed so stereo channels are
// independent, not a duplicate). Both are golden-input-discipline clean:
// MultiTone's math.Sin is confined to internal/testsignal (never the
// production encode path, per that package's own doc comment), and LCG is
// pure integer/stored recurrence.
func maskingGridProgram(name string, sr, nch, nFrames int) [][]float64 {
	n := nFrames * 1152
	out := make([][]float64, nch)
	switch name {
	case maskingProgMultiTone:
		for ch := range nch {
			phase := 0.0
			if ch == 1 {
				phase = 0.37
			}
			out[ch] = testsignal.MultiTone(sr, n, phase, 0.9)
		}
	case maskingProgLCGNoise:
		for ch := range nch {
			seed := uint64(sr)<<32 | uint64(ch)<<8 | 1
			buf := make([]float64, n)
			for i := range n {
				buf[i] = testsignal.LCGSigned(&seed) * 0.9
			}
			out[ch] = buf
		}
	default:
		panic("maskingGridProgram: unknown program " + name)
	}
	return out
}

// maskingGridCase is one TestEncoderMaskingContract grid point. mustRun
// marks the cases that always run, even under -short: see
// maskingGridCases' doc comment.
type maskingGridCase struct {
	sr, kbps, nch int
	prog          string
	mustRun       bool
}

// maskingGridMustRun is the fast, always-run subset of the grid (even
// under -short): the three cases design decision 9's doc comment
// measured the Stage 1-only defect on (at their original
// defect-characterizing "multitone" program, which converges quickly;
// see maskingGridFrames' doc comment for why "lcgnoise" is comparatively
// expensive), plus one mono sanity check and one clean high-bitrate
// stereo sanity check. This is the real regression surface: CI must never
// run without exercising these even when the full sweep is skipped.
var maskingGridMustRun = []maskingGridCase{
	{48000, 32, 2, maskingProgMultiTone, true},
	{48000, 128, 2, maskingProgMultiTone, true},
	{44100, 64, 2, maskingProgMultiTone, true},
	{44100, 128, 1, maskingProgMultiTone, true},
	{44100, 320, 2, maskingProgMultiTone, true},
}

// maskingGridCases builds the full grid (3 sample rates x {32,64,128,192,
// 320} kbps x mono/stereo x {multitone, lcgnoise}), flagging every case
// that also appears in maskingGridMustRun.
func maskingGridCases() []maskingGridCase {
	isMustRun := func(sr, kbps, nch int, prog string) bool {
		for _, m := range maskingGridMustRun {
			if m.sr == sr && m.kbps == kbps && m.nch == nch && m.prog == prog {
				return true
			}
		}
		return false
	}

	rates := []int{44100, 48000, 32000}
	kbpsList := []int{32, 64, 128, 192, 320}
	programs := []string{maskingProgMultiTone, maskingProgLCGNoise}

	cases := make([]maskingGridCase, 0, len(rates)*len(kbpsList)*2*len(programs))
	for _, sr := range rates {
		for _, kbps := range kbpsList {
			for _, nch := range []int{1, 2} {
				for _, prog := range programs {
					cases = append(cases, maskingGridCase{sr, kbps, nch, prog, isMustRun(sr, kbps, nch, prog)})
				}
			}
		}
	}
	return cases
}

// maskingFullGridEnv opts TestEncoderMaskingContract into its full 60-case
// grid sweep, mirroring this project's oracle-dump env-gating convention
// (e.g. MP3_REQUIRE_DUMPS): each case is a full encode plus, on every
// masking violation, up to two trial re-codes, and outerLoop's own
// worst-case iteration cap is routinely hit by broadband lcgnoise content
// at low bitrates (hundreds of milliseconds per call, escalation calling
// it more times per frame than Stage 1 alone did), so the full sweep can
// exceed go test's default 10-minute per-package timeout even at
// maskingGridFrames' shortened length. The default `go test ./...` /
// `task check` therefore runs only maskingGridMustRun (a few seconds);
// set MP3_MASKING_FULLGRID=1 with an extended -timeout (e.g. `go test
// -timeout 20m -run TestEncoderMaskingContract ./internal/enc/`) to run
// the full grid.
const maskingFullGridEnv = "MP3_MASKING_FULLGRID"

// TestEncoderMaskingContract is Phase 4 increment 5's primary acceptance
// gate (design decision 9, third revision), replacing the raw-SNR grid as
// the acceptance invariant: PE-derived budgets are estimates, so the
// contract that matters is that every granule-channel's kept coding is
// the best any legally-tried budget could have produced, bounded only by
// the frame's physical capacity, not that the encoder happens to hit a
// particular raw waveform SNR.
//
// For the grid (maskingGridCases; the full sweep runs only under
// maskingFullGridEnv, otherwise just maskingGridMustRun): encode
// maskingGridFrames frames with SetDiagHookPin capturing every
// granule-channel's FINAL (post Stage 2 escalation) DiagGranule, skip the
// drain frame (the Inc4 boundary-exclusion precedent: coded silence
// against a stale analysis window, a genuine boundary artifact, not a
// masking-budget defect). For every granule-channel whose kept coding
// violates masking (maskingMetrics' over > 0, the same measure
// escalation's own cache uses): maxGrant is the largest LEGAL budget this
// granule-channel could have been granted this frame, everything else
// held at its current kept level (min(maxPart23Length,
// BudgetBits+CapacityLeftBits)). If the kept coding already sits at
// maxGrant, there is no bigger legal budget to try: capacity-bound, PASS
// (logged). Otherwise re-code the CAPTURED spectrum (Xr) at BOTH maxGrant
// and min(flatShare, maxGrant) - the fixpoint sweep guarantees these are
// exactly escalation's own last-tried budget pair for this
// granule-channel - and compare each WHOLE re-coding against the kept
// coding via loop.go's betterPass, the SAME cross-budget ordering
// escalation's cache uses. If EITHER re-coding is betterPass-STRICTLY-
// BETTER than the kept coding, the production path left a genuinely
// better allocation on the table: FAIL (under-allocation). If neither is,
// the kept coding was already the best of every budget escalation tried:
// capacity-bound, PASS, logged (this correctly passes trade-off frames
// where different budgets satisfy different subsets of violating bands
// with no budget satisfying the union: a coding that is betterPass-worse
// despite satisfying more individual bands must have larger total excess
// elsewhere, a genuine trade-off, not a missed allocation; a coding that
// truly dominates, satisfying a superset without degrading anything else,
// always has strictly lower excess and so is always betterPass-better and
// caught here). Capacity-bound passes are expected only at the lowest
// rates (measured: the 48kHz/32kbps stereo class); per-config counts are
// logged so drift is visible.
func TestEncoderMaskingContract(t *testing.T) {
	fullGrid := os.Getenv(maskingFullGridEnv) == "1"
	for _, c := range maskingGridCases() {
		name := fmt.Sprintf("sr%d_kbps%d_nch%d_%s", c.sr, c.kbps, c.nch, c.prog)
		t.Run(name, func(t *testing.T) {
			if !c.mustRun && !fullGrid {
				t.Skip("skipping full grid; set " + maskingFullGridEnv + "=1 (with an extended -timeout) to run it")
			}
			t.Parallel()
			testMaskingContractCase(t, c.sr, c.kbps, c.nch, c.prog)
		})
	}
}

// testMaskingContractCase runs TestEncoderMaskingContract's check for one
// grid point. Split out of the parent test so a per-frame padding tracker
// (an independent paddingState, matching TestEncoderStatsCount's
// established trick: codeFrame calls pad.next once per EncodeFrame call,
// so an independent run over the same call sequence reproduces the
// encoder's own padding bit exactly) can be threaded into the closure
// without cluttering the grid-walking loop above.
func testMaskingContractCase(t *testing.T, sr, kbps, nch int, prog string) {
	t.Helper()

	cfg := Config{SampleRate: sr, Channels: nch, BitrateKbps: kbps}
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	srIndex := srIndexForRate[sr]
	bitrateIndex := bitrateIndexForKbps[kbps]
	sfb := &sfbWidthsLong[srIndex]

	const nFrames = maskingGridFrames
	drainFrame := nFrames
	input := maskingGridProgram(prog, sr, nch, nFrames)

	var pad paddingState
	currentFlatShare := 0

	frameIdx := -1
	failures := 0
	capacityBound := 0
	e.SetDiagHookPin(func(g, ch int, diag DiagGranule) {
		if g == 0 && ch == 0 {
			frameIdx++
		}
		if frameIdx == drainFrame {
			return
		}

		// The kept coding's own masking metrics, the SAME measure
		// (maskingMetrics, unfiltered by Exempt) escalation's own cache
		// compares with betterPass: apples to apples with what
		// escalation itself would have computed.
		keptExcess, keptRatio, keptOver := maskingMetrics(&diag.Noise, &diag.XminXr)
		if keptOver == 0 {
			return // fully satisfied
		}

		maxGrant := min(maxPart23Length, diag.BudgetBits+diag.CapacityLeftBits)
		if maxGrant <= diag.BudgetBits {
			capacityBound++
			return
		}

		// Escalation-gate agreement (design decision 9's fixpoint sweep):
		// maxGrant and min(flatShare, maxGrant) are exactly this
		// granule-channel's last-tried budget pair, so these two
		// re-codes replay already-attempted pure outerLoop calls.
		failed := false
		for _, budget := range [2]int{maxGrant, min(currentFlatShare, maxGrant)} {
			var trial, trialBest granuleCoding
			outerLoop(&diag.Xr, &diag.XminXr, budget, sfb, &trial, &trialBest)
			var trialNoise [22]float64
			noiseGranule(&diag.Xr, &trial.ix, trial.globalGain, &trial.sf, sfb, &trialNoise)
			reExcess, reRatio, reOver := maskingMetrics(&trialNoise, &diag.XminXr)
			if betterPass(reExcess, reRatio, reOver, keptExcess, keptRatio, keptOver) {
				failed = true
				break
			}
		}
		if failed {
			failures++
			t.Errorf("frame %d gr %d ch %d: under-allocation, a whole re-coding at a tried budget is betterPass-strictly-better than the kept coding (BudgetBits=%d CapacityLeftBits=%d maxGrant=%d keptOver=%d)",
				frameIdx, g, ch, diag.BudgetBits, diag.CapacityLeftBits, maxGrant, keptOver)
		} else {
			capacityBound++
		}
	})

	var stream []byte
	for f := range nFrames {
		wantPad := pad.next(kbps, sr)
		currentFlatShare = granuleBudgetBits(bitrateIndex, srIndex, wantPad, nch)

		samples := make([][]float32, nch)
		for ch := range nch {
			samples[ch] = make([]float32, 1152)
			for i := range 1152 {
				samples[ch][i] = float32(input[ch][f*1152+i])
			}
		}
		stream, err = e.EncodeFrame(stream, samples)
		if err != nil {
			t.Fatalf("frame %d: EncodeFrame: %v", f, err)
		}
	}
	drainPad := pad.next(kbps, sr)
	currentFlatShare = granuleBudgetBits(bitrateIndex, srIndex, drainPad, nch)
	stream, err = e.EncodeFrame(stream, nil)
	if err != nil {
		t.Fatalf("drain: EncodeFrame: %v", err)
	}
	_ = stream

	if failures == 0 {
		t.Logf("masking contract satisfied: %d capacity-bound residual(s)", capacityBound)
	}
}
