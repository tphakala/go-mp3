package enc

import (
	"errors"
	"fmt"
	"math"
)

// ErrInvalidAudio is returned by EncodeFrame when samples contains a NaN or
// an Inf anywhere. Returning it appends nothing to dst and poisons the
// Encoder: every subsequent call, including a nil drain call, also returns
// ErrInvalidAudio until Reset. See EncodeFrame's doc comment for the full
// validation contract.
var ErrInvalidAudio = errors.New("go-mp3/enc: invalid audio sample")

// srIndexForRate maps a legal MPEG-1 sample rate to its header srIndex
// (0=44100, 1=48000, 2=32000), the same row order as sfbWidthsLong and
// sampleRateHzTable.
var srIndexForRate = map[int]int{44100: 0, 48000: 1, 32000: 2}

// bitrateIndexForKbps maps every legal MPEG-1 Layer III CBR bitrate to its
// header bitrate_index, ISO/IEC 11172-3 Table B.1.
var bitrateIndexForKbps = map[int]int{
	32: 1, 40: 2, 48: 3, 56: 4, 64: 5, 80: 6, 96: 7,
	112: 8, 128: 9, 160: 10, 192: 11, 224: 12, 256: 13, 320: 14,
}

// ValidBitrateKbps reports whether kbps is one of the 14 legal MPEG-1 Layer
// III CBR bitrates, ISO/IEC 11172-3 Table B.1. The single source of truth
// for bitrate legality: bitrateIndexForKbps's key set.
func ValidBitrateKbps(kbps int) bool {
	_, ok := bitrateIndexForKbps[kbps]
	return ok
}

// XminScale converts the psymodel's energy domain (PsyOut.Xmin/En, the
// Hann-windowed FFT analysis over raw [-1,1] PCM) into the coding path's
// xr domain (PCMScale-scaled MDCT lines quantizeGranule/noiseGranule work
// in): xminXr[sfb] = PsyOut.Xmin[sfb] * XminScale. MEASURED then frozen by
// internal/dec's TestPsyXrCalibration (Step 3 of Task 5): the two domains
// differ by the fixed gain between the analysis FFT and the
// polyphase+MDCT chain, the PCMScale=0.5 amplitude factor, and the window
// energy ratios; predicting it analytically repeats the quantGainBase=214
// mistake, so the measurement wins and the derivation is documented at
// TestPsyXrCalibration. Exported (unlike quantGainBase, which has no
// direct calibration test) so that test can assert against this exact
// value rather than carrying its own hand-synced copy, following
// PCMScale's and ChainDelay's precedent for measured cross-package
// calibration constants in this file.
//
// Measured as the median-of-medians of sum(xr[i]^2 over an sfb)/PsyOut.En
// [sfb] across a 3-sample-rate x 2-program grid (broadband LCG noise and
// testsignal.MultiTone), restricted to bands with non-negligible per-line
// energy density (a tonal program leaves most bands pure window-sidelobe
// leakage, whose ratio is meaningless noise): see
// psyXrCalibrationDensityFloor's doc comment in encx_roundtrip_test.go.
// The winning case was 32kHz LCG noise, 9.514823425525474e-07; the six
// per-case medians clustered tightly (9.4466e-07 to 1.0171e-06, under 8%
// spread end to end), and TestPsyXrCalibration re-asserts every surviving
// individual per-band ratio stays within 4x of this frozen value.
const XminScale float64 = 0x1.fed2bcc6bd0f8p-21

// Config is the validated internal encoder configuration.
type Config struct {
	SampleRate  int // 32000, 44100, 48000
	Channels    int // 1 or 2
	BitrateKbps int // 32..320, the 14 MPEG-1 Layer III values
}

// validate reports whether cfg names a legal MPEG-1 Layer III CBR
// configuration: one of the three MPEG-1 sample rates, 1 or 2 channels, and
// one of the 14 CBR bitrates. Scope IN per doc.go: no Layer I/II, no MPEG-2/
// 2.5, no free format.
func (c Config) validate() error {
	if _, ok := srIndexForRate[c.SampleRate]; !ok {
		return fmt.Errorf("go-mp3/enc: invalid sample rate %d, want 32000, 44100, or 48000", c.SampleRate)
	}
	if c.Channels != 1 && c.Channels != 2 {
		return fmt.Errorf("go-mp3/enc: invalid channel count %d, want 1 or 2", c.Channels)
	}
	if !ValidBitrateKbps(c.BitrateKbps) {
		return fmt.Errorf("go-mp3/enc: invalid bitrate %d kbps, want one of the 14 MPEG-1 Layer III CBR rates", c.BitrateKbps)
	}
	return nil
}

// Encoder is a stateful MPEG-1 Layer III encoder: it carries the analysis
// filterbank shift registers, the MDCT overlap history, and the CBR padding
// accumulator between EncodeFrame calls, so frames from the same stream
// must be encoded in order with the same Encoder. An Encoder is not safe
// for concurrent use.
type Encoder struct {
	cfg          Config
	bitrateIndex int
	srIndex      int
	mode         int // 0 = stereo, 3 = single_channel
	nch          int

	fb   [2]Filterbank      // per-channel analysis filterbank
	prev [2][18][32]float64 // per-channel MDCT overlap history
	cur  [18][32]float64    // scratch: one channel's just-analyzed granule

	// xr and xminXr hold every granule-channel's spectrum and calibrated
	// threshold for the WHOLE frame (granule-major, channel-minor, the same
	// shape as gr below), not just one scratch slot: codeFrame's analysis
	// pass (Task 3) must finish every granule-channel before planFrame can
	// see the frame's total demand, so the coding pass that follows needs
	// every earlier granule-channel's xr/xminXr still around.
	xr     [2][2][576]float64
	xminXr [2][2][22]float64

	in [2][576]float64 // per-granule staging: clamped, PCMScale-scaled samples

	// Four-way psychoacoustic model bank: repL/repR analyze the two
	// physical channels' windows, repM/repS the butterflied M/S windows.
	// Mono uses repL only. psyOuts holds every representation's per-granule
	// output (representation-major, granule-minor) so the M/S decision can
	// compare all four before the coding path commits to one.
	psy      [4]PsyModel      // psychoacoustic model 2 state, one per representation
	psyWin   [2][1024]float64 // per-channel causal analysis window: clamped [-1,1] samples, BEFORE PCMScale
	psyWinMS [2][1024]float64 // M (index 0) and S (index 1) windows: butterflied from psyWin
	psyOuts  [4][2]PsyOut     // [representation][granule] psymodel output
	xrM, xrS [576]float64     // coding-path M/S butterfly scratch (written back into e.xr for an M/S frame)
	msFrame  bool             // this frame codes M/S joint stereo (mode 01, mode_extension 10)

	pad         paddingState
	gr          [2][2]granuleCoding
	bestScratch granuleCoding // outerLoop's caller-owned best-pass scratch
	esc         escState      // codeFrame's masking-driven escalation scratch (Stage 2)

	resv reservoir // bit reservoir accountant: tracks main-data occupancy across frames
	fifo frameFIFO // pending-frame ring: the physical realization of the reservoir

	// mainScratch is the reusable render buffer for both header+side-info
	// and main-data bytes (renderFrameInto copies its output into the FIFO
	// slot via push before mainScratch is reused for renderMainData, so the
	// two renders never alias). Sized to the LARGER of the Huffman field
	// ceiling and the frame's main-data area (renderMainData pads up to the
	// area when the reservoir forces a high spendMin; see Reset), plus
	// mainScratchSlack headroom, preallocated once in Reset so codeFrame
	// never grows it.
	mainScratch []byte

	poisoned bool
	drained  bool

	frames        int64
	bytes         int64
	paddedFrames  int64
	sumGlobalGain int64
	countGranules int64
	scfsiSaved    int64

	diagHook func(g, ch int, diag DiagGranule) // test-only, nil in every production path; see SetDiagHookPin
}

// maskEscalationMaxCalls bounds codeFrame's Stage 2 masking-driven budget
// escalation (design decision 9, third revision): a pure COST ceiling on
// outerLoop invocations, not a correctness requirement. Each escalation
// attempt (escAttempt) issues up to two outerLoop calls (the full-capacity
// budget and the flat-budget probe when they differ) and the cap is checked
// per attempt, so the effective bound is up to 2*maskEscalationMaxCalls
// invocations; the loose factor is immaterial for a cost ceiling. Termination is
// proven without it (see escalateForMasking's doc comment): the main
// loop's Phi measure (count of unsatisfied-unparked granule-channels,
// then their summed over-count, lexicographic) strictly decreases every
// non-breaking iteration and is bounded by nGC + 22*nGC (about 92 for
// stereo); the fixpoint sweep terminates because an attempt requires a
// granule-channel's offer pair to have changed since its last attempt,
// which only happens after a betterPass acceptance, and acceptances are
// strictly improving over a finite set of reachable outcomes per
// granule-channel. The cap only truncates pathological slow-converging
// cases (each outerLoop call itself capped at outerLoopMaxIters); on
// truncation the frame is not at fixpoint and TestEncoderMaskingContract
// may legitimately fire, which is the cost tripwire working as intended,
// not a false positive.
const maskEscalationMaxCalls = 128

// escState is codeFrame's per-frame Stage 2 scratch, preallocated in the
// Encoder value and re-seeded at the start of every masking-driven
// escalation (design decision 9, third revision): the cross-budget
// best-pass cache (best[i] plus its cached bestExcess/bestRatio/bestOver
// metrics, ordered by loop.go's betterPass) that guarantees escalation is
// monotone non-worsening per granule-channel and doubles as the kept
// coding's current over-count (bestOver[i] == 0 means satisfied, since
// gr[i] always equals best[i]); parked marks a granule-channel the main
// loop gave up on (its full-capacity shot did not reduce its over-count);
// triedHi/triedLo record the exact budget pair of the granule-channel's
// most recent attempt, which the fixpoint sweep compares against a fresh
// offer to decide whether a re-attempt is needed, and which
// TestEncoderMaskingContract's end-of-frame re-code replays exactly once
// the sweep reaches its fixpoint (escalation-gate agreement by
// construction); calls counts outerLoop invocations against
// maskEscalationMaxCalls. Sized to the maximum nGC (4, stereo); mono
// codeFrame calls use only the first two entries, indexed g*nch+ch same
// as gr.
type escState struct {
	best                  [4]granuleCoding
	bestExcess, bestRatio [4]float64
	bestOver              [4]int
	parked                [4]bool
	triedHi, triedLo      [4]int
	calls                 int
}

// New returns a new Encoder for cfg, or an error if cfg is not a legal
// MPEG-1 Layer III CBR configuration.
func New(cfg Config) (*Encoder, error) {
	e := &Encoder{}
	if err := e.Reset(cfg); err != nil {
		return nil, err
	}
	return e, nil
}

// mainScratchSlack is headroom added on top of mainScratch's worst-case
// content size (see Reset, which sizes the buffer for the LARGER of the
// coded Huffman ceiling and the frame's main-data area) to preallocate:
// bits.Writer.Flush's final partial-byte pad can cost up to a byte per
// granule-channel, well inside this margin.
const mainScratchSlack = 8

// Reset clears all stream state (filterbank history, MDCT overlap, padding
// accumulator, bit reservoir, pending FIFO, poison, drain, and Stats) and
// revalidates cfg, as at the start of a fresh stream. It is the only way to
// clear a poisoned Encoder.
func (e *Encoder) Reset(cfg Config) error {
	if err := cfg.validate(); err != nil {
		return err
	}

	*e = Encoder{cfg: cfg}
	e.srIndex = srIndexForRate[cfg.SampleRate]
	e.bitrateIndex = bitrateIndexForKbps[cfg.BitrateKbps]
	e.nch = cfg.Channels
	if cfg.Channels == 2 {
		e.mode = 0
	} else {
		e.mode = 3
	}
	for ch := range e.fb {
		e.fb[ch].Reset()
	}
	// Reset every representation's psymodel (repL/repR for the physical
	// channels, repM/repS for the M/S windows); mono only ever drives repL.
	for rep := range e.psy {
		e.psy[rep].Reset(e.srIndex)
	}
	// renderMainData reuses this buffer for two renders: the coded Huffman
	// bytes (bounded by the part_2_3_length ceiling) and the ancillary pad
	// up to spendMin==lo, which reaches the frame's main-data area when
	// occupancy sits at the reservoir cap. Size for the LARGER so codeFrame
	// never grows it (a grown slice is never stored back, so growth would
	// allocate on every frame); mono high-rate (e.g. 320kbps/32kHz, area
	// 1419) exceeds the Huffman ceiling (1024), stereo the reverse.
	huffCapBytes := (2*e.nch*maxPart23Length + 7) / 8
	maxAreaBytes := mainAreaBytes(e.bitrateIndex, e.srIndex, 1, e.nch)
	e.mainScratch = make([]byte, 0, max(huffCapBytes, maxAreaBytes)+mainScratchSlack)
	return nil
}

// Drained reports whether the encoder has emitted its final (silence,
// history-flushing) frame in response to a nil EncodeFrame call.
func (e *Encoder) Drained() bool { return e.drained }

// EncodeFrame consumes exactly 1152 samples per channel of planar float32
// PCM in [-1, 1] and appends zero or more complete MP3 frames to dst: the
// bit reservoir (Task 3) holds a coded frame in the internal FIFO until its
// main-data area is fully threaded through by later frames' spend, so one
// call may append nothing, one frame, or several frames that only just
// became complete.
//
// Validation order: while the encoder is poisoned (a prior call saw a NaN
// or Inf), every call, including a nil drain call, returns dst unchanged
// with ErrInvalidAudio; only Reset clears that state. Otherwise, samples ==
// nil drains: it encodes one final frame of silence that flushes the
// filterbank and MDCT history (ChainDelay < 1152 guarantees that one frame
// suffices), then force-flushes every frame still held in the FIFO
// (zero-filling any unfilled area bytes), marks the encoder drained, and is
// counted in Stats; further nil calls after that append nothing and return
// a nil error.
//
// Drain is terminal: once drained, any subsequent non-nil call panics
// (a caller-bug class, matching the length-mismatch panics below), rather
// than silently encoding more audio whose tail (ChainDelay samples) would
// never get flushed and whose Drained()==true state would then be
// misleading. Reset is the only way to encode more audio after draining.
//
// For a non-nil call, samples must have exactly Channels entries, each of
// exactly 1152 samples; a violation panics (a caller-bug class, distinct
// from ErrInvalidAudio, matching bits.Writer's n-range panic precedent).
// The encoder then scans every sample for NaN or Inf before touching any
// state: a hit returns dst unchanged with ErrInvalidAudio and poisons the
// stream. Once the scan passes, every finite sample is clamped to [-1, 1]
// at ingest, which also guarantees global_gain 255 keeps every quantized
// line within maxQuant, so loud input cannot drive the coder out of range.
func (e *Encoder) EncodeFrame(dst []byte, samples [][]float32) ([]byte, error) {
	if e.poisoned {
		return dst, ErrInvalidAudio
	}

	if samples == nil {
		if e.drained {
			return dst, nil
		}
		e.drained = true
		dst = e.codeFrame(dst, nil)
		before := len(dst)
		dst = e.fifo.flushAll(dst)
		e.bytes += int64(len(dst) - before)
		return dst, nil
	}

	if e.drained {
		panic("go-mp3/enc: EncodeFrame called after drain; Reset to reuse the encoder")
	}

	if len(samples) != e.nch {
		panic("go-mp3/enc: EncodeFrame: len(samples) != Config.Channels")
	}
	for ch := range samples {
		if len(samples[ch]) != 1152 {
			panic("go-mp3/enc: EncodeFrame: channel sample count != 1152")
		}
	}

	for ch := range samples {
		for _, s := range samples[ch] {
			f := float64(s)
			if math.IsNaN(f) || math.IsInf(f, 0) {
				e.poisoned = true
				return dst, ErrInvalidAudio
			}
		}
	}

	return e.codeFrame(dst, samples), nil
}

// clamp restricts x to [-1, 1], the documented input domain: it bounds
// |xr| so minGlobalGain plus quantizeGranule's maxQuant clamp guarantee
// every |ix| <= 8206, which linbits 13 and 15 (both + 8191 = 8206) can
// always represent, regardless of how loud the finite input was.
func clamp(x float64) float64 {
	if x > 1 {
		return 1
	}
	if x < -1 {
		return -1
	}
	return x
}

// codeFrame runs the full per-frame pipeline in two passes over the frame's
// granule-channels, plus the reservoir/FIFO handoff:
//
// Pass 1 (analysis): for each granule and channel, slide the psymodel's
// causal window, run AnalyzeGranule to get this granule's masking
// thresholds and perceptual entropy (PE), scale the thresholds into the xr
// noise domain, then run AnalyzeGranule -> FlipOddSubbands -> MDCTGranule
// -> save prev -> AliasReduce to get the spectrum. Every granule-channel's
// spectrum and threshold are kept (e.xr/e.xminXr, granule-major
// channel-minor) rather than a single scratch slot, and each one's PE
// becomes its part23 demand estimate (gc.peBits), because the reservoir
// needs the WHOLE frame's demand before it can split the frame's main-data
// budget: coding cannot start until every granule-channel has been
// analyzed.
//
// Between the passes: e.resv.planFrame turns the four (or two, mono)
// demands into per-granule-channel Huffman budgets that together never
// exceed the demand-driven huffTarget. When the reservoir's occupancy
// forces this frame to physically spend more than that (occupancy sitting
// at the cap with sustained low-demand content has nowhere else to put the
// difference), codeFrame tops every budget back up to use the WHOLE forced
// spend instead of letting the gap fall through to ancillary padding: see
// the topping-up step's own comment below for the measured regression that
// makes this necessary, not optional.
//
// Pass 2 (coding): for each granule-channel, outerLoop picks scalefactors
// and quantizes against its planned budget. After both granules, scfsi is
// detected and applied per channel (its savings shrink the part23 sum: an
// automatic reservoir deposit, no different from a granule-channel that
// simply needed fewer bits than planned).
//
// Reservoir handoff: this frame's main_data_begin is the reservoir's
// occupancy BEFORE this frame's own spend (mdb), since that many
// previously-buffered bytes are what this frame's decoder must be able to
// reach back into. The header+side info render into mainScratch and get
// pushed into the FIFO as a new pending slot; the main data renders into
// the same (now-reused) mainScratch, floored at the reservoir's own
// physical spend lower bound (spendMin, never the coded huffTarget, so a
// granule-channel that coded more cheaply than its PE demand predicted
// banks the difference as a real reservoir deposit instead of burning it as
// ancillary padding), and threads through the FIFO's pending slots via
// place. commitFrame then advances occupancy by this frame's actual spend,
// and flushInto appends every slot that is now complete.
//
// samples == nil codes silence (the per-granule staging loop below writes
// zero into e.in/e.psyWin instead of a real sample, which flushes the
// filterbank, MDCT, and psymodel history through one real pass of the
// pipeline) for the drain frame; EncodeFrame's drain branch force-flushes
// whatever the FIFO still holds after this call returns.
func (e *Encoder) codeFrame(dst []byte, samples [][]float32) []byte {
	padding := e.pad.next(e.cfg.BitrateKbps, e.cfg.SampleRate)
	sfb := &sfbWidthsLong[e.srIndex]
	nGC := 2 * e.nch
	area := mainAreaBytes(e.bitrateIndex, e.srIndex, padding, e.nch)
	capBytes := resCapBytes(e.bitrateIndex, e.srIndex, e.nch)
	meanGB := area * 8 / nGC

	// ANALYZE: run the per-physical-channel DSP chain and the four-way
	// psychoacoustic analysis for the WHOLE frame, holding every result
	// until the M/S decision below. e.xr[g][ch] receives physical channel
	// ch's MDCT spectrum (the coding pass may overwrite it in place with
	// the M/S spectrum). e.xminXr and e.gr.peBits are NOT written here:
	// they are wired from the chosen representation in the CODE phase,
	// strictly after DECIDE, so an L/R PE can never budget an M/S frame.
	for g := range 2 {
		for ch := range e.nch {
			// Slide the psymodel's causal 1024-sample window forward by
			// 576 (the newest 448 old samples stay, this granule's 576
			// clamped [-1,1] samples land at the tail), then stage the
			// SAME clamped value into e.in (PCMScale-scaled) so the two
			// analysis paths see identical input.
			copy(e.psyWin[ch][:1024-576], e.psyWin[ch][576:])
			for i := range 576 {
				v := 0.0
				if samples != nil {
					v = clamp(float64(samples[ch][g*576+i]))
				}
				e.psyWin[ch][1024-576+i] = v
				e.in[ch][i] = float64(v * PCMScale)
			}

			e.fb[ch].AnalyzeGranule(e.in[ch][:], &e.cur)
			FlipOddSubbands(&e.cur)
			MDCTGranule(&e.prev[ch], &e.cur, &e.xr[g][ch])
			e.prev[ch] = e.cur
			AliasReduce(&e.xr[g][ch])
		}

		// Four-way psychoacoustic analysis. Mono drives repL alone; stereo
		// also butterflies the two windows into M/S and analyzes all four
		// representations. Each psymodel's history advances once per granule
		// exactly as before, so repL/repR see the identical window sequence
		// the pre-M/S encoder fed them.
		e.psy[repL].AnalyzeGranule(e.psyWin[0][:], &e.psyOuts[repL][g])
		if e.nch == 2 {
			e.psy[repR].AnalyzeGranule(e.psyWin[1][:], &e.psyOuts[repR][g])
			butterflyWindows(&e.psyWin[0], &e.psyWin[1], &e.psyWinMS[0], &e.psyWinMS[1])
			e.psy[repM].AnalyzeGranule(e.psyWinMS[0][:], &e.psyOuts[repM][g])
			e.psy[repS].AnalyzeGranule(e.psyWinMS[1][:], &e.psyOuts[repS][g])
		}
	}

	// DECIDE: choose L/R or M/S from the four PEs, each summed over both
	// granules. Mono is always L/R. Inc7 seam (design decision 8): the
	// block-switch increment adds a veto here for frames whose channels
	// disagree on block type.
	e.msFrame = false
	if e.nch == 2 {
		peL := e.psyOuts[repL][0].PE + e.psyOuts[repL][1].PE
		peR := e.psyOuts[repR][0].PE + e.psyOuts[repR][1].PE
		peM := e.psyOuts[repM][0].PE + e.psyOuts[repM][1].PE
		peS := e.psyOuts[repS][0].PE + e.psyOuts[repS][1].PE
		e.msFrame = msDecide(peL, peR, peM, peS)
	}

	// CODE: strictly after DECIDE. For an M/S frame, butterfly each
	// granule's L/R spectra into M/S in place, so e.xr now holds exactly
	// what the outer loop, escalation, and render read. Each coded channel
	// takes its threshold (Xmin) and demand (PE) ONLY from its chosen
	// representation (chosenRep: repL/repR for L/R, repM/repS for M/S); the
	// phase indexes psyOuts exclusively through chosenRep so a
	// cross-representation wiring mistake is compile-visible, not a silent
	// budget leak.
	for g := range 2 {
		if e.msFrame {
			butterflyXr(&e.xr[g][0], &e.xr[g][1], &e.xrM, &e.xrS)
			e.xr[g][0] = e.xrM
			e.xr[g][1] = e.xrS
		}
		for ch := range e.nch {
			rep := e.chosenRep(ch)
			for s := range 22 {
				e.xminXr[g][ch][s] = float64(e.psyOuts[rep][g].Xmin[s] * XminScale)
			}
			e.gr[g][ch].peBits = granuleDemandBits(e.psyOuts[rep][g].PE, meanGB)
		}
	}

	var demands [4]int
	for g := range 2 {
		for ch := range e.nch {
			demands[g*e.nch+ch] = e.gr[g][ch].peBits
		}
	}
	spend, huffTarget, budgets := e.resv.planFrame(&demands, nGC, area, capBytes)

	// planFrame's budgets sum to only huffTarget*8 bits, the psychoacoustic
	// DEMAND-driven coded target. But the physical spend the reservoir will
	// force this frame to (spend, clamped no higher when occupancy is not
	// saturated) can run ahead of that target: once occupancy sits at cap
	// with sustained low-PE content (nothing to draw the reservoir back
	// down for), spend pins at area every frame while huffTarget stays at
	// the demand floor, and the gap between them was, before this step,
	// wasted as ancillary zero bytes. Regressed TestEncoderRoundTripSNR
	// stereo cases down by tens of dB confirmed this in practice: at
	// 44.1kHz/128kbps/stereo, budgets floored to ~382 bits/granule-channel
	// (half the pre-reservoir flat rate, 762) while the forced spend had
	// room for the full 764 (spend/nGC), and outerLoop's resulting coarser
	// quantization tanked raw waveform SNR even though it stayed inside
	// every band's masking threshold. Topping budgets up to use the WHOLE
	// forced spend (not just the demand estimate) turns that would-be
	// ancillary waste back into real precision: it can only add bits
	// relative to planFrame's own budgets, so every existing invariant
	// (sum(budgets) <= spend*8 <= hi*8, each budget <= maxPart23Length)
	// still holds, and a granule-channel that does not need the extra
	// simply leaves it as ancillary padding exactly as before, matching
	// this increment's headline expectation that the reservoir can only
	// help, never hurt, over the old flat-budget scheme.
	if extraBits := (spend - huffTarget) * 8; extraBits > 0 {
		share := extraBits / nGC
		rem := extraBits - share*nGC
		for i := range nGC {
			add := share
			if i == nGC-1 {
				add += rem
			}
			budgets[i] = min(budgets[i]+add, maxPart23Length)
		}
	}

	for g := range 2 {
		for ch := range e.nch {
			budget := budgets[g*e.nch+ch]
			_ = outerLoop(&e.xr[g][ch], &e.xminXr[g][ch], budget, sfb, &e.gr[g][ch], &e.bestScratch)
		}
	}

	e.escalateForMasking(nGC, sfb, padding, area, capBytes)

	for ch := range e.nch {
		mask := detectScfsi(&e.gr[0][ch], &e.gr[1][ch])
		e.gr[1][ch].scfsi = mask
		e.scfsiSaved += int64(applyScfsi(&e.gr[1][ch], mask))
	}

	// Emit mode/mode_extension: L/R keeps e.mode (0 stereo, 3 mono) with
	// mode_extension 0; an M/S frame emits joint-stereo mode 01 with
	// mode_extension 10 (M/S on, intensity stereo off).
	mode, modeExt := e.mode, 0
	if e.msFrame {
		mode, modeExt = 1, 2
	}
	mdb := e.resv.occ
	hdr, _ := renderFrameInto(e.mainScratch[:0], e.bitrateIndex, e.srIndex, padding, mode, modeExt, &e.gr, e.nch, mdb)
	n := frameLength(e.bitrateIndex, e.srIndex, padding)
	e.fifo.push(hdr, n)

	lo, _ := e.resv.spendBounds(area, capBytes)
	spendMin := max(lo, 0)
	mainData := renderMainData(e.mainScratch[:0], &e.gr, e.nch, spendMin)
	e.fifo.place(mainData)
	e.resv.commitFrame(area, len(mainData))

	before := len(dst)
	dst = e.fifo.flushInto(dst)

	e.frames++
	e.bytes += int64(len(dst) - before)
	if padding != 0 {
		e.paddedFrames++
	}
	return dst
}

// chosenRep maps a coded channel index (0 or 1) to the psymodel
// representation codeFrame's CODE phase reads for it: repL/repR for an L/R
// frame, repM/repS for an M/S frame. The CODE phase indexes psyOuts
// exclusively through this helper so a cross-representation wiring mistake
// (budgeting an M/S frame from an L/R PE, say) is a compile-visible oddity
// rather than a silent bug.
func (e *Encoder) chosenRep(ch int) int {
	if e.msFrame {
		if ch == 0 {
			return repM
		}
		return repS
	}
	if ch == 0 {
		return repL
	}
	return repR
}

// escFreeCap returns the frame's current free capacity in bits: the
// identity hi*8 minus the sum of every granule-channel's kept coding
// (part23Length). Reclaim is implicit in this identity (design decision
// 9, third revision): a granule-channel that improved to fewer bits, or
// was never granted more, automatically contributes less to the sum and
// more to the free capacity, with no separate budget-bookkeeping array to
// keep in sync.
func (e *Encoder) escFreeCap(nGC, hi int) int {
	total := 0
	for i := range nGC {
		g, ch := i/e.nch, i%e.nch
		total += e.gr[g][ch].part23Length
	}
	return hi*8 - total
}

// escOffer returns granule-channel i's full-capacity budget right now:
// its kept coding's part23Length plus every bit of the frame's current
// free capacity, capped at the field limit.
func (e *Encoder) escOffer(i, nGC, hi int) int {
	g, ch := i/e.nch, i%e.nch
	return min(maxPart23Length, e.gr[g][ch].part23Length+e.escFreeCap(nGC, hi))
}

// escNeediest returns the unsatisfied (bestOver != 0), unparked
// granule-channel with the largest kept over-count, tie-broken by the
// largest kept noise/xmin ratio (bit-portable float, the same class
// betterPass and loop.go's worstViolator already compare), tie-broken by
// lowest index via ascending scan with strict-greater-only replacement.
// Returns -1 if none is eligible.
func (e *Encoder) escNeediest(nGC int) int {
	best := -1
	for i := range nGC {
		if e.esc.parked[i] || e.esc.bestOver[i] == 0 {
			continue
		}
		if best < 0 || e.esc.bestOver[i] > e.esc.bestOver[best] ||
			(e.esc.bestOver[i] == e.esc.bestOver[best] && e.esc.bestRatio[i] > e.esc.bestRatio[best]) {
			best = i
		}
	}
	return best
}

// escTryBudget re-codes granule-channel i at budget, then keeps the
// result in the cross-budget best-pass cache (e.esc.best[i]) only if
// betterPass finds it a strict improvement over the cached metrics;
// otherwise gr[i] rolls back to the cached best. Counts one call against
// maskEscalationMaxCalls. Never itself decides satisfied/parked; callers
// (the main loop, the fixpoint sweep) do that from the resulting
// over-count (e.esc.bestOver[i] after the call).
func (e *Encoder) escTryBudget(i int, sfb *[22]int, budget int) {
	e.esc.calls++
	g, ch := i/e.nch, i%e.nch
	gc := &e.gr[g][ch]
	_ = outerLoop(&e.xr[g][ch], &e.xminXr[g][ch], budget, sfb, gc, &e.bestScratch)

	var noise [22]float64
	noiseGranule(&e.xr[g][ch], &gc.ix, gc.globalGain, &gc.sf, sfb, &noise)
	excess, ratio, over := maskingMetrics(&noise, &e.xminXr[g][ch])
	if betterPass(excess, ratio, over, e.esc.bestExcess[i], e.esc.bestRatio[i], e.esc.bestOver[i]) {
		e.esc.best[i] = *gc
		e.esc.bestExcess[i], e.esc.bestRatio[i], e.esc.bestOver[i] = excess, ratio, over
	} else {
		*gc = e.esc.best[i] // roll back: this budget did not improve masking
	}
}

// escAttempt codes granule-channel i at the budget pair (hiB, loB) via
// escTryBudget, recording the pair as its most recently tried so the
// fixpoint sweep can detect when a fresh offer differs from it; loB (the
// flat-budget probe, the no-regression anchor against the pre-reservoir
// Inc4 coding) is attempted only when it differs from hiB. hiB is always
// attempted first: a fixed, deterministic order matters because
// betterPass ties break earliest-wins.
func (e *Encoder) escAttempt(i int, sfb *[22]int, hiB, loB int) {
	e.esc.triedHi[i] = hiB
	e.esc.triedLo[i] = loB
	e.escTryBudget(i, sfb, hiB)
	if loB != hiB {
		e.escTryBudget(i, sfb, loB)
	}
}

// escalateForMasking runs codeFrame's Stage 2 masking-driven budget
// escalation (design decision 9, third revision) after Stage 1's initial
// coding pass: PE-derived budgets are estimates and can under-allocate
// below the masking threshold on frames the old flat pre-reservoir budget
// satisfied. nGC, sfb, padding, area, capBytes mirror codeFrame's own
// locals. Also captures the (now final) per-granule-channel diagnostic via
// SetDiagHookPin and updates the running global-gain stats, both moved
// here (from the single Stage 1 coding pass) so they observe the frame's
// FINAL coding rather than the pre-escalation one.
//
// GREEDY NEEDIEST-FIRST MAIN LOOP: free capacity is the identity hi*8
// minus the sum of every granule-channel's kept part23Length (escFreeCap;
// reclaim is implicit, so no granule-channel's unused headroom can stay
// trapped). Each iteration picks the neediest unsatisfied, unparked
// granule-channel (escNeediest: max kept over-count, tie max kept
// noise/xmin ratio, tie lowest index) and offers it its ENTIRE free
// capacity (escOffer), with the flat-budget probe min(flatShare, offer)
// riding along on every attempt (escAttempt). Progress is judged by
// INTEGER masking progress, the granule-channel's over-count, NEVER bit
// counts: a granule-channel whose over-count did not strictly reduce at
// its FULL offered capacity is parked (out of the main loop for good; the
// fixpoint sweep below still revisits it), one that reaches over==0
// leaves the working set satisfied, and one whose over-count did
// strictly reduce stays eligible for another, later, possibly larger or
// smaller offer.
//
// DROP-RULE DEFECT this replaces (measured 48000/32kbps stereo frame 0,
// granule 0 channel 1, sfb 12): the previous revision dropped a
// granule-channel when its kept part23Length failed to GROW after a
// grant. That bit-count proxy is broken by outerLoop's own
// non-monotonicity: a bigger budget can find a strictly-better-masking
// coding that uses FEWER bits (189 -> 195 bits offered, outerLoop
// returned a strictly better pass at only 183, betterPass accepted it,
// and the bit-count rule then falsely dropped a granule-channel that had
// just genuinely improved, stranding real capacity). Judging progress by
// over-count instead of bit count makes that impossible: an accepted
// improvement always reduces (or satisfies) over-count regardless of how
// many bits it used.
//
// FIXPOINT SWEEP: later acceptances move free capacity in both
// directions (an accepted coding that shrank frees bits for others; one
// that grew consumes them), so a parked or still-unsatisfied
// granule-channel can face a different full-capacity offer than the one
// it was last tried at. The sweep re-attempts every unsatisfied
// granule-channel (parked or not) whose current offer pair
// (escOffer(i), min(flatShare, escOffer(i))) differs from its last tried
// pair (triedHi[i], triedLo[i]), ascending index order, until a full pass
// makes zero attempts. At that fixpoint, every unsatisfied
// granule-channel has been attempted at EXACTLY its end-of-frame
// full-capacity offer and probe pair: TestEncoderMaskingContract's
// end-of-frame re-code (maxGrant = BudgetBits + CapacityLeftBits, and
// min(flatShare, maxGrant)) replays those exact already-attempted pure
// outerLoop calls and returns the identical verdict, so a correctly
// escalated frame can never trip the gate: escalation-gate agreement by
// construction, not by hope.
//
// Termination (proven without maskEscalationMaxCalls, itself only a cost
// ceiling on outerLoop invocations): the main loop's measure Phi = (count
// of unsatisfied-unparked granule-channels, their summed over-count),
// compared lexicographically, strictly decreases every non-breaking
// iteration (satisfying or parking a granule-channel strictly drops the
// first component; reducing its over-count while it stays eligible
// strictly drops the second, and the switch below admits no other
// outcome), and both components are non-negative integers bounded above
// by nGC and 22*nGC, so the main loop terminates within nGC + 22*nGC
// iterations regardless of the cost cap. The sweep terminates because an
// attempt requires a granule-channel's offer pair to have changed since
// its last attempt, which only happens after a betterPass acceptance
// somewhere in the frame, and acceptances are strictly improving over a
// finite set of reachable (excess, ratio, over) outcomes per
// granule-channel (outerLoop is a pure deterministic function of its
// inputs), so the frame admits finitely many acceptances total and a
// pass that makes no attempt therefore ends the sweep for good.
//
// No-regression, three tiers (weakest premise last): HARD, the
// cross-budget best-pass cache makes every granule-channel's kept coding
// the betterPass-best over every budget attempted this frame, so
// escalation can never leave a granule-channel's masking worse than
// Stage 1's initial pass. ANCHORED, the flat-budget probe rides along on
// every attempt whose hiB differs from it, so every unsatisfied
// granule-channel has been coded at min(flatShare, its final
// full-capacity offer); outerLoop is a pure function of its inputs, so
// whenever flatShare fits the offer that attempt IS the pre-reservoir
// Inc4 coding, bit for bit, and the cache keeps it whenever it is the
// best seen. ENFORCED, TestEncoderMaskingContract is an independent
// tripwire (it recomputes everything from the captured spectrum and the
// free-capacity identity, sharing no state with this method): the
// escalation-gate agreement argument above means it can only fire when a
// frame was NOT escalated to fixpoint, e.g. a maskEscalationMaxCalls
// truncation, which is the cost tripwire doing its job, not a false
// positive.
func (e *Encoder) escalateForMasking(nGC int, sfb *[22]int, padding, area, capBytes int) {
	_, hi := e.resv.spendBounds(area, capBytes)
	flatShare := granuleBudgetBits(e.bitrateIndex, e.srIndex, padding, e.nch)

	// Seed the cross-budget best-pass cache from Stage 1's initial pass.
	// triedHi/triedLo start at -1 (an offer can never legally be
	// negative), an unreachable sentinel that forces the fixpoint sweep
	// to attempt any granule-channel the main loop never got to.
	e.esc.calls = 0
	for i := range nGC {
		g, ch := i/e.nch, i%e.nch
		e.esc.best[i] = e.gr[g][ch]
		var noise [22]float64
		noiseGranule(&e.xr[g][ch], &e.gr[g][ch].ix, e.gr[g][ch].globalGain, &e.gr[g][ch].sf, sfb, &noise)
		e.esc.bestExcess[i], e.esc.bestRatio[i], e.esc.bestOver[i] = maskingMetrics(&noise, &e.xminXr[g][ch])
		e.esc.parked[i] = false
		e.esc.triedHi[i] = -1
		e.esc.triedLo[i] = -1
	}

	// MAIN LOOP: greedy neediest-first, judged by integer over-count
	// progress alone (never bit counts; see the doc comment above).
	for e.esc.calls < maskEscalationMaxCalls {
		g := e.escNeediest(nGC)
		if g < 0 || e.escFreeCap(nGC, hi) <= 0 {
			break
		}
		gg, gch := g/e.nch, g%e.nch
		prevOver := e.esc.bestOver[g]
		hiB := e.escOffer(g, nGC, hi)
		if hiB <= e.gr[gg][gch].part23Length {
			e.esc.parked[g] = true // field cap reached, nothing to offer
			continue
		}
		e.escAttempt(g, sfb, hiB, min(flatShare, hiB))
		switch {
		case e.esc.bestOver[g] == 0:
			// Satisfied: escNeediest excludes it for good from here on.
		case e.esc.bestOver[g] >= prevOver:
			e.esc.parked[g] = true // no over-count progress at full capacity
		default:
			// over strictly reduced: stays eligible for another offer.
		}
	}

	// FIXPOINT SWEEP: re-attempt every unsatisfied granule-channel
	// (parked or not) whose current offer pair differs from its last
	// tried pair, ascending, until a full pass makes zero attempts.
	for changed := true; changed && e.esc.calls < maskEscalationMaxCalls; {
		changed = false
		for i := range nGC {
			if e.esc.bestOver[i] == 0 {
				continue
			}
			hiB := e.escOffer(i, nGC, hi)
			loB := min(flatShare, hiB)
			if hiB != e.esc.triedHi[i] || loB != e.esc.triedLo[i] {
				e.escAttempt(i, sfb, hiB, loB)
				changed = true
				if e.esc.calls >= maskEscalationMaxCalls {
					break
				}
			}
		}
	}

	// At the fixpoint gr[i] == e.esc.best[i] for every i (every attempt
	// either accepts into the cache or rolls back to it), and
	// capacityLeftBits is the same free-capacity identity the gate uses
	// to reproduce maxGrant, captured once here so every granule-channel
	// diagnosed this frame sees the identical end-of-frame value.
	capacityLeftBits := e.escFreeCap(nGC, hi)

	for i := range nGC {
		g, ch := i/e.nch, i%e.nch
		gc := &e.gr[g][ch]

		if e.diagHook != nil {
			var noise [22]float64
			noiseGranule(&e.xr[g][ch], &gc.ix, gc.globalGain, &gc.sf, sfb, &noise)
			atCap := diagAtCap(&gc.sf)
			var exempt [22]bool
			for s := range 22 {
				exempt[s] = diagFloorBound(&e.xr[g][ch], sfb, gc.part23Length, gc, s, atCap[s])
			}
			e.diagHook(g, ch, DiagGranule{
				Noise:            noise,
				XminXr:           e.xminXr[g][ch],
				Exempt:           exempt,
				BudgetBits:       gc.part23Length,
				CapacityLeftBits: capacityLeftBits,
				Xr:               e.xr[g][ch],
			})
		}

		e.sumGlobalGain += int64(gc.globalGain)
		e.countGranules++
	}
}

// Stats counts what the encoder emitted since New/Reset.
type Stats struct {
	Frames         int64
	Bytes          int64
	PaddedFrames   int64
	MeanGlobalGain float64 // mean over all coded granule-channels
	ScfsiBitsSaved int64   // total part2 bits saved by scfsi across every coded frame
}

// Stats returns the encoder's cumulative counters. MeanGlobalGain divides
// the integer sum accumulated in codeFrame only here, at read time, so the
// float division never touches the encode path (and so never perturbs the
// determinism goldens).
func (e *Encoder) Stats() Stats {
	mean := 0.0
	if e.countGranules > 0 {
		mean = float64(e.sumGlobalGain) / float64(e.countGranules)
	}
	return Stats{
		Frames:         e.frames,
		Bytes:          e.bytes,
		PaddedFrames:   e.paddedFrames,
		MeanGlobalGain: mean,
		ScfsiBitsSaved: e.scfsiSaved,
	}
}

// ChainDelay is the measured encoder+decoder algorithmic delay in samples
// per channel: decoding this encoder's output reproduces the input shifted
// by exactly this many samples. Measured once at 44.1kHz/320kbps/mono by
// cross-correlating a deterministic multi-tone input against the decoded
// output (internal/dec/encx_roundtrip_test.go, TestEncoderChainDelay: the
// lag that maximizes the raw, unnormalized input-vs-output dot product,
// over a lag window of [0, 2304)): 1057 samples, exactly the predicted
// 1057 = 576 (one granule of MDCT prev-history buffering: MDCTGranule
// needs the PRECEDING granule's subband samples before it emits the first
// granule's true output) + 481
// (the analysis+synthesis polyphase filterbank chain delay, frozen as
// fbChainDelay in internal/dec/encx_filterbank_test.go:90). ChainDelay <
// 1152 is asserted alongside the measurement: it is what makes the
// one-silence-frame drain design correct, since a single flush frame
// (1152 samples) covers more than the chain's total lag, so draining once
// is enough to push every real sample through the pipeline and out the
// decoder.
const ChainDelay = 1057

// --- Test-only cross-package surface ---
//
// DiagGranule/SetDiagHookPin mirror frame.go's AppendFramePin precedent:
// the minimal exported surface internal/dec's pipeline loop-contract gate
// needs to inspect the real Encoder's per-granule noise/xmin relationship,
// without duplicating codeFrame's logic or exposing scfState across the
// package boundary.

// DiagGranule is one granule-channel's post-escalation diagnostic
// snapshot (captured AFTER Stage 2's masking-driven budget escalation, so
// every field reflects the frame's FINAL coding, design decision 9): the
// per-sfb quantization noise the final coding produced (recomputed via
// noiseGranule against that granule-channel's own gc.ix/globalGain/sf),
// the calibrated xminXr threshold the outer loop targeted, and which bands
// are beyond the loop's reach (diagFloorBound: structurally capped, or
// empirically proven futile to amplify further), where a residual xmin
// violation is expected, not a defect. BudgetBits is the ceiling this
// granule-channel was last coded against (post-escalation reclaim, so it
// equals its kept part23Length); CapacityLeftBits is the frame-level
// headroom beyond every granule-channel's current BudgetBits (hi*8 minus
// their sum), the same value for every granule-channel diagnosed in one
// frame. Xr is a copy of this granule-channel's MDCT spectrum, so a gate
// can re-run outerLoop against the captured spectrum at a larger budget to
// check whether a residual violation is genuinely capacity-bound or
// under-allocation with room to spare (TestEncoderMaskingContract's
// distinguisher). Test-only, filled by SetDiagHookPin.
type DiagGranule struct {
	Noise            [22]float64
	XminXr           [22]float64
	Exempt           [22]bool
	BudgetBits       int
	CapacityLeftBits int
	Xr               [576]float64
}

// SetDiagHookPin installs a test-only hook that codeFrame invokes once per
// granule-channel, immediately after the outer loop lands its result in
// gc, with that granule-channel's diagnostic snapshot (g: 0 or 1, ch: the
// channel index). Nil (the default, and the only value any production
// path sets) disables it: the single not-nil check codeFrame makes per
// granule-channel costs nothing measurable, and the Exempt computation
// (diagFloorBound, a per-band recode-and-compare probe) runs only when a
// hook is installed, so this method and its hook body never allocate or
// spend cycles on the encode path.
func (e *Encoder) SetDiagHookPin(hook func(g, ch int, diag DiagGranule)) {
	e.diagHook = hook
}

// diagAtCap reports, per band, whether sf's scalefactor sits at its
// representable cap (scalefacScale==1 and scf[sfb] at sfMaxLo/sfMaxHi), or
// sfb 21 which carries no scalefactor at all: bands the outer loop cannot
// amplify any further under this state, mirroring loop_test.go's
// package-internal bandAtCap helper.
func diagAtCap(sf *scfState) [22]bool {
	var atCap [22]bool
	for sfb := range 22 {
		if sfb >= 21 {
			atCap[sfb] = true
			continue
		}
		maxv := sfMaxLo
		if sfb >= slen1Bands {
			maxv = sfMaxHi
		}
		atCap[sfb] = sf.scalefacScale == 1 && sf.scf[sfb] >= maxv
	}
	return atCap
}

// diagFloorBound reports whether band s in gc's final coding is beyond the
// outer loop's reach: either structurally capped (atCap, diagAtCap's
// per-band result) or empirically floor-bound, meaning one more
// scalefactor unit on s, recoded against the same budget, would not
// reduce noise[s]. This mirrors loop_test.go's package-internal bandLocked
// helper (the same probe outerLoop's own futility check runs internally)
// as the cross-package diagnostic SetDiagHookPin exposes: diagAtCap alone
// is NOT sufficient here, since a band can be genuinely floor-bound (its
// minGlobalGain-pinning line prevents any noise reduction) without its
// scalefactor ever reaching the structural cap, exactly the scenario
// outerLoop's own futility check exists to catch.
func diagFloorBound(xr *[576]float64, sfbWidths *[22]int, budget int, gc *granuleCoding, s int, atCap bool) bool {
	if atCap {
		return true
	}
	sfCap := sfMaxLo
	if s >= slen1Bands {
		sfCap = sfMaxHi
	}
	if s >= 21 || gc.sf.scf[s] >= sfCap {
		return true // no room to even try one more unit
	}

	var noiseNow [22]float64
	noiseGranule(xr, &gc.ix, gc.globalGain, &gc.sf, sfbWidths, &noiseNow)

	trial := *gc
	trial.sf.scf[s]++
	idx, part2, ok := chooseScalefacCompress(&trial.sf, 0)
	if !ok || part2 >= budget {
		return true // no budget to even try the bump
	}
	huffBudget := min(budget, maxPart23Length) - part2
	codeGranule(xr, huffBudget, sfbWidths, &trial)
	trial.scfCompress, trial.part2Bits = idx, part2

	var noiseTrial [22]float64
	noiseGranule(xr, &trial.ix, trial.globalGain, &trial.sf, sfbWidths, &noiseTrial)
	return !(noiseTrial[s] < noiseNow[s])
}
