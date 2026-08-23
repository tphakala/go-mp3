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

// XminScaleShort is XminScale's short-block counterpart (design decision
// 14): xminXr[b] = PsyOut.XminS[b] * XminScaleShort for a short granule's
// coding band b. Decision 14 predicted XminScale itself would apply
// unchanged to XminS, since the psymodel's band-energy mapping and the
// coding-path spectrum transform look structurally identical for short
// bands; that prediction did NOT hold. TestPsyXrCalibrationShort
// (internal/enc/psymodel_test.go) measures the SAME ratio methodology
// TestPsyXrCalibration uses for XminScale (sum(xr[i]^2 over a coding
// band)/PsyOut.EnS[band], density-floored, median-of-medians across a
// 3-sample-rate x 2-program grid of STATIONARY content, entirely bypassing
// Encoder/quantizeGranule/outerLoop so the freeze is not tautological) and
// found a systematic, tightly-clustered factor of about 15.76x XminScale
// (the six per-case medians spanned only 1.31e-05 to 1.53e-05, a much
// TIGHTER spread than XminScale's own long-block calibration grid), not
// the "applies unchanged" the decision predicted. This surfaced as a real
// masking-contract regression before the freeze: TestEncoderMaskingContract
// failed on a hard silence-to-full-amplitude onset (the granule immediately
// following stream start, where attackDetect's zero initial carry
// legitimately calls it an attack) once short blocks were live, because
// XminScale alone made XminS-derived thresholds far tighter than the
// short-band coding path could ever satisfy, independent of budget.
// Freezing this separate, measured constant is exactly decision 14's
// documented contingency for that outcome, not a silent absorption.
const XminScaleShort float64 = 0x1.f72d3b5af9b7dp-17

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

// pcmHist layout constants (design decision 11): prevTail (one granule of
// history before the held frame) + held (the frame codeFrame is about to
// code) + next (the one-frame lookahead). See the Encoder.pcmHist field
// doc comment for the full rationale.
const (
	pcmPrevTailLen = 576
	pcmHeldLen     = 1152
	pcmNextLen     = 1152
	pcmHistLen     = pcmPrevTailLen + pcmHeldLen + pcmNextLen // 2880

	// pcmWindowCenterOffset is how far the re-centered psymodel window
	// (decision 11) starts before its granule's own first sample: half of
	// 1024-576, so the granule sits centered in the window with an equal
	// 224-sample margin of history and lookahead on each side.
	pcmWindowCenterOffset = (1024 - 576) / 2
)

// pcmWindowStart returns pcmHist's offset for granule g's (0 or 1)
// re-centered 1024-sample psymodel analysis window (design decision 11):
// the granule's own 576 samples occupy the window's local [224,800), with
// pcmWindowCenterOffset samples of history before them (reaching back into
// prevTail for g==0) and the same amount of lookahead after (reaching into
// next for g==1).
func pcmWindowStart(g int) int {
	return pcmPrevTailLen + g*576 - pcmWindowCenterOffset
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
	xminXr [2][2][39]float64 // long fills indices 0..21, the rest stay zero

	in [2][576]float64 // per-granule staging: clamped, PCMScale-scaled samples

	// Four-way psychoacoustic model bank: repL/repR analyze the two
	// physical channels' windows, repM/repS the butterflied M/S windows.
	// Mono uses repL only. psyOuts holds every representation's per-granule
	// output (representation-major, granule-minor) so the M/S decision can
	// compare all four before the coding path commits to one.
	psy      [4]PsyModel      // psychoacoustic model 2 state, one per representation
	psyWinMS [2][1024]float64 // M (index 0) and S (index 1) windows: butterflied per-granule from pcmHist's L/R slices
	psyOuts  [4][2]PsyOut     // [representation][granule] psymodel output
	xrM, xrS [576]float64     // coding-path M/S butterfly scratch (written back into e.xr for an M/S frame)
	msFrame  bool             // this frame codes M/S joint stereo (mode 01, mode_extension 10)

	// reorderScratch is short-granule DSP's reorderShort destination
	// buffer (encoder-value scratch, the e.cur/e.xrM/e.xrS precedent):
	// reorderShort cannot write in place (coding order interleaves the
	// three windows' slices), so the reordered spectrum lands here first
	// and is copied back into e.xr[g][ch].
	reorderScratch [576]float64

	// pcmHist is the one-frame PCM lookahead's sliding analysis history
	// (design decision 11), per channel: clamped [-1,1] samples (BEFORE
	// PCMScale), laid out prevTail(576) + held(1152) + next(1152) =
	// pcmHistLen. prevTail is the granule immediately preceding the held
	// frame; held is the frame codeFrame is about to code (two granules);
	// next is the frame just staged by the current EncodeFrame call,
	// consulted only as lookahead (attack detection for wantShort, and the
	// re-centered psymodel window's trailing 224 samples for granule 1).
	// slidePcmHist advances it by one frame (1152 samples) after every
	// codeFrame call.
	pcmHist [2][pcmHistLen]float64

	// held reports whether pcmHist's held region holds a real frame ready
	// to code: false only before the very first EncodeFrame call, which
	// stashes its samples directly into held and returns without coding
	// (decision 11: there is no lookahead yet).
	held bool

	// attackCarry is attackDetect's cross-call carry (design decision 9):
	// the energy of the sub-block immediately preceding the held frame's
	// granule 0, i.e. the carry attackDetect would have returned right
	// after processing the PREVIOUS call's held granule 1 (which sits, in
	// stream order, exactly one granule before this call's held granule
	// 0). Updated at the end of every analyzeAttacks call.
	attackCarry [2]float64

	// wantShort caches this codeFrame call's four attack verdicts per
	// channel, in stream order: index 0 = held granule 0, 1 = held granule
	// 1, 2 = next granule 0 (also held granule 1's wantNext), 3 = next
	// granule 1 (cached; becomes held granule 1's own verdict, i.e. index
	// 1, on the FOLLOWING call once pcmHist slides).
	wantShort [2][4]bool

	// blockPrev is channel ch's most recently CODED granule's decided
	// block type: seeds blockTypeFor's prev argument for the next call's
	// held granule 0. Updated to bt[1][ch] at the end of every codeFrame
	// call.
	blockPrev [2]int

	// bt is this frame's decided block types, granule-major: bt[g][ch].
	// Decided once per codeFrame call (decision 10's state machine) before
	// the DSP chain runs, since MDCTGranuleBlock and layoutFor both need it.
	bt [2][2]int

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
// escTryBudget calls (each a distinct outerLoop invocation, or a memo hit
// once escTryBudget caches per (granule-channel, budget)), not a correctness
// requirement. Each escalation attempt (escAttempt) issues up to two
// escTryBudget calls (the full-capacity budget and the flat-budget probe when
// they differ) and the cap is checked per attempt, so the effective bound is
// up to 2*maskEscalationMaxCalls calls; the loose factor is immaterial for a
// cost ceiling. Termination is
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
// construction); calls counts escTryBudget calls (outerLoop invocations plus
// memo hits) against maskEscalationMaxCalls. Sized to the maximum nGC (4,
// stereo); mono codeFrame calls use only the first two entries, indexed
// g*nch+ch same as gr.
type escState struct {
	best                  [4]granuleCoding
	bestExcess, bestRatio [4]float64
	bestOver              [4]int
	parked                [4]bool
	triedHi, triedLo      [4]int
	calls                 int

	// memo caches each distinct outerLoop result within one frame's
	// escalation, keyed by (granule-channel index i, budget). outerLoop is a
	// pure function of (xr[i], xmin[i], budget, lay); those inputs are frozen
	// for the whole escalation (codeFrame writes them in its CODE phase,
	// strictly before escalateForMasking, which never rewrites them), so
	// (i, budget) is an exact key. memoN is the live length, reset to 0 each
	// frame; the backing array is preallocated once in Reset. See
	// escMemoEntry and escTryBudget.
	memo  []escMemoEntry
	memoN int
}

// escMemoEntry is one cached outerLoop-plus-measure result for a distinct
// (i, budget) pair within a single frame's escalation. gc is the raw
// outerLoop result (the coded granule-channel BEFORE any accept/rollback);
// excess/ratio/over are its masking metrics (noiseGranule+maskingMetrics),
// stored so a cache hit reproduces betterPass's operands bit-for-bit without
// recomputing them. granuleCoding is a fixed-size value type with no slices
// (its only pointer, lay, addresses a frame-constant shared table), so a
// by-value copy into the preallocated store never heap-allocates.
type escMemoEntry struct {
	i, budget int
	over      int
	excess    float64
	ratio     float64
	gc        granuleCoding
}

// escMemoCap bounds the per-frame memo store. Only cache MISSES insert an
// entry, and the number of misses is at most the number of escTryBudget
// calls, which escalateForMasking caps at maskEscalationMaxCalls+1 (the last
// escAttempt can fire the second of its two probes just after the count
// reaches the cap). maskEscalationMaxCalls+2 is therefore a provable hard
// bound: the store never overflows, so insertion is unconditionally
// zero-allocation. Defining it in terms of maskEscalationMaxCalls keeps it
// correct if that cap ever changes.
const escMemoCap = maskEscalationMaxCalls + 2

// memoGet returns the cached (i, budget) result seen earlier this frame, or
// (nil, false). Linear scan over memo[:memoN]: memoN is at most ~129, so the
// worst-case scan cost is trivial next to even one 150-iteration outerLoop.
func (s *escState) memoGet(i, budget int) (*escMemoEntry, bool) {
	for k := range s.memoN {
		if s.memo[k].i == i && s.memo[k].budget == budget {
			return &s.memo[k], true
		}
	}
	return nil, false
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
	// Preallocate the escalation memo store once (like mainScratch above): its
	// length is reset to 0 per frame in escalateForMasking, never regrown, so
	// the memoized outer loop stays zero-allocation in steady state.
	e.esc.memo = make([]escMemoEntry, escMemoCap)
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
		// Drain: zero next (silence lookahead), code the currently held
		// real frame if there is one, then code the silence flush frame
		// (design decision 11's held-frame drain contract: N input calls
		// plus drain still yield N+1 stream frames). If N==0 (held is
		// still false: EncodeFrame was never called with real samples),
		// pcmHist is already all zero and only the flush frame is needed.
		for ch := range e.nch {
			for i := pcmPrevTailLen + pcmHeldLen; i < pcmHistLen; i++ {
				e.pcmHist[ch][i] = 0
			}
		}
		if e.held {
			dst = e.codeFrame(dst)
			e.slidePcmHist()
		}
		dst = e.codeFrame(dst) // the silence flush frame
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

	if !e.held {
		// Call 1: stash directly into the held slot and return without
		// coding (design decision 11: there is no lookahead yet, since
		// held granule 1's wantNext needs a NEXT frame's granule 0).
		for ch := range samples {
			for i := range 1152 {
				e.pcmHist[ch][pcmPrevTailLen+i] = clamp(float64(samples[ch][i]))
			}
		}
		e.held = true
		return dst, nil
	}

	// Call n >= 2: stash into next, then code the frame that has been
	// sitting in held since the previous call, now that next supplies its
	// lookahead.
	for ch := range samples {
		for i := range 1152 {
			e.pcmHist[ch][pcmPrevTailLen+pcmHeldLen+i] = clamp(float64(samples[ch][i]))
		}
	}
	dst = e.codeFrame(dst)
	e.slidePcmHist()
	return dst, nil
}

// slidePcmHist advances pcmHist by one frame (1152 samples) after a
// codeFrame call: the held frame's second granule becomes the new
// prevTail, next becomes the new held, and next itself is cleared to zero
// (silence) pending the following call's stash or the drain's own
// explicit zero.
func (e *Encoder) slidePcmHist() {
	for ch := range e.nch {
		h := &e.pcmHist[ch]
		copy(h[0:pcmPrevTailLen], h[pcmPrevTailLen+576:pcmPrevTailLen+pcmHeldLen])
		copy(h[pcmPrevTailLen:pcmPrevTailLen+pcmHeldLen], h[pcmPrevTailLen+pcmHeldLen:pcmHistLen])
		for i := pcmPrevTailLen + pcmHeldLen; i < pcmHistLen; i++ {
			h[i] = 0
		}
	}
}

// analyzeAttacks runs attackDetect over the four granules pcmHist currently
// spans (held granule 0, held granule 1, next granule 0, next granule 1),
// in stream order, chaining each call's carry into the next and filling
// e.wantShort[ch]; e.attackCarry[ch] is left holding the carry in effect
// right before next granule 0's own call, which is exactly the carry the
// FOLLOWING codeFrame call needs to seed its own held granule 0 (that
// granule IS this call's next granule 0, once pcmHist slides).
func (e *Encoder) analyzeAttacks() {
	for ch := range e.nch {
		h := &e.pcmHist[ch]
		heldG0 := h[pcmPrevTailLen : pcmPrevTailLen+576]
		heldG1 := h[pcmPrevTailLen+576 : pcmPrevTailLen+pcmHeldLen]
		nextG0 := h[pcmPrevTailLen+pcmHeldLen : pcmPrevTailLen+pcmHeldLen+576]
		nextG1 := h[pcmPrevTailLen+pcmHeldLen+576 : pcmHistLen]

		var last0, last1, last2 float64
		e.wantShort[ch][0], last0 = attackDetect(heldG0, e.attackCarry[ch])
		e.wantShort[ch][1], last1 = attackDetect(heldG1, last0)
		e.wantShort[ch][2], last2 = attackDetect(nextG0, last1)
		e.wantShort[ch][3], _ = attackDetect(nextG1, last2)

		e.attackCarry[ch] = last1
	}
}

// decideBlockTypes advances the per-channel window state machine (design
// decision 10) for the held frame's two granules, from the cached
// wantShort verdicts and each channel's blockPrev, and leaves blockPrev
// holding this frame's granule 1 for the next call.
func (e *Encoder) decideBlockTypes() {
	for ch := range e.nch {
		bt0 := blockTypeFor(e.blockPrev[ch], e.wantShort[ch][0], e.wantShort[ch][1])
		bt1 := blockTypeFor(bt0, e.wantShort[ch][1], e.wantShort[ch][2])
		e.bt[0][ch] = bt0
		e.bt[1][ch] = bt1
		e.blockPrev[ch] = bt1
	}
}

// layFor returns the coding-order band geometry for granule-channel index
// i (g*e.nch+ch, matching e.gr's indexing), from that granule's own decided
// block type: escalateForMasking and its helpers use this instead of a
// single frame-shared layout, since a frame's granule-channels can now
// carry different block types.
func (e *Encoder) layFor(i int) *bandLayout {
	g, ch := i/e.nch, i%e.nch
	return layoutFor(e.bt[g][ch], e.srIndex)
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

// forceLRForTest, when true, pins codeFrame's DECIDE outcome to L/R
// (msFrame=false) for every frame regardless of the four-way PE comparison.
// Test-only hook: production code never sets it, and its zero value makes
// the DECIDE phase byte-identical to the unhooked encoder.
// TestEncodeGoldenForcedLR uses it to prove the L/R coding path still
// reproduces the pre-M/S golden hashes. Unsynchronized: a test that sets it
// must not run in parallel and must restore it via t.Cleanup.
var forceLRForTest bool

// codeFrame runs the full per-frame pipeline over the HELD frame in
// pcmHist (design decision 11: the caller has already stashed this call's
// real input, if any, into next and will slide pcmHist afterward), in four
// phases plus the reservoir/FIFO handoff:
//
// INGEST is EncodeFrame's job, not this method's: by the time codeFrame
// runs, pcmHist already holds prevTail/held/next for the frame about to be
// coded.
//
// ANALYZE: attackDetect runs over the four granules pcmHist spans (held
// granule 0 and 1, next granule 0 and 1) to get this frame's wantShort
// verdicts, decideBlockTypes turns those into bt[g][ch] (design decision
// 10), then for each granule and channel the DSP chain
// (AnalyzeGranule -> FlipOddSubbands -> MDCTGranuleBlock -> save prev ->
// AliasReduce for long/start/stop, or reorderShort into coding order for
// short) produces the spectrum, and the four-way psychoacoustic analysis
// runs over the re-centered pcmHist window (design decision 11) to get
// this granule's masking thresholds and perceptual entropy. e.xminXr and
// e.gr.peBits are NOT written here: they are wired from the chosen
// representation in the CODE phase, strictly after DECIDE, so an L/R PE
// can never budget an M/S frame.
//
// Between ANALYZE and CODE: e.resv.planFrame turns the four (or two, mono)
// demands into per-granule-channel Huffman budgets that together never
// exceed the demand-driven huffTarget. When the reservoir's occupancy
// forces this frame to physically spend more than that (occupancy sitting
// at the cap with sustained low-demand content has nowhere else to put the
// difference), codeFrame tops every budget back up to use the WHOLE forced
// spend instead of letting the gap fall through to ancillary padding: see
// the topping-up step's own comment below for the measured regression that
// makes this necessary, not optional.
//
// CODE: for each granule-channel, outerLoop picks scalefactors and
// quantizes against its planned budget, against that granule's OWN
// bandLayout (layoutFor(bt[g][ch], srIndex): a frame's granule-channels
// can now carry different block types). After both granules, scfsi is
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
func (e *Encoder) codeFrame(dst []byte) []byte {
	padding := e.pad.next(e.cfg.BitrateKbps, e.cfg.SampleRate)
	nGC := 2 * e.nch
	area := mainAreaBytes(e.bitrateIndex, e.srIndex, padding, e.nch)
	capBytes := resCapBytes(e.bitrateIndex, e.srIndex, e.nch)
	meanGB := area * 8 / nGC

	// ANALYZE: attack detection and the window-switching decision come
	// first (design decisions 9/10), since the DSP chain below needs
	// bt[g][ch] to pick MDCTGranuleBlock's window and reorderShort.
	e.analyzeAttacks()
	e.decideBlockTypes()

	for g := range 2 {
		for ch := range e.nch {
			bt := e.bt[g][ch]
			for i := range 576 {
				e.in[ch][i] = e.pcmHist[ch][pcmPrevTailLen+g*576+i] * PCMScale
			}

			e.fb[ch].AnalyzeGranule(e.in[ch][:], &e.cur)
			FlipOddSubbands(&e.cur)
			MDCTGranuleBlock(&e.prev[ch], &e.cur, &e.xr[g][ch], bt)
			e.prev[ch] = e.cur
			if bt == blockShort {
				reorderShort(&e.xr[g][ch], &e.reorderScratch, &sfbWidthsShort[e.srIndex])
				e.xr[g][ch] = e.reorderScratch
			} else {
				AliasReduce(&e.xr[g][ch])
			}
		}

		// Four-way psychoacoustic analysis over the re-centered window
		// (design decision 11). Mono drives repL alone; stereo also
		// butterflies the two channels' windows into M/S and analyzes all
		// four representations.
		winStart := pcmWindowStart(g)
		lWin := (*[1024]float64)(e.pcmHist[0][winStart : winStart+1024])
		e.psy[repL].AnalyzeGranule(lWin[:], &e.psyOuts[repL][g])
		if e.nch == 2 {
			rWin := (*[1024]float64)(e.pcmHist[1][winStart : winStart+1024])
			e.psy[repR].AnalyzeGranule(rWin[:], &e.psyOuts[repR][g])
			butterflyWindows(lWin, rWin, &e.psyWinMS[0], &e.psyWinMS[1])
			e.psy[repM].AnalyzeGranule(e.psyWinMS[0][:], &e.psyOuts[repM][g])
			e.psy[repS].AnalyzeGranule(e.psyWinMS[1][:], &e.psyOuts[repS][g])
		}
	}

	// DECIDE: choose L/R or M/S from the four PEs, each summed over both
	// granules, using PES instead of PE for a granule the decided tiling
	// codes as short (design decision 13). Mono is always L/R. The
	// block-switch veto (design decision 13): mismatched channel block
	// types make M/S structurally undefined, so msDecide's PE-driven
	// choice is overridden to L/R whenever the channels disagree.
	e.msFrame = false
	if e.nch == 2 && !forceLRForTest {
		peL := e.repPE(repL, 0) + e.repPE(repL, 1)
		peR := e.repPE(repR, 0) + e.repPE(repR, 1)
		peM := e.repPE(repM, 0) + e.repPE(repM, 1)
		peS := e.repPE(repS, 0) + e.repPE(repS, 1)
		e.msFrame = msDecide(peL, peR, peM, peS) && blockTypesAgree(&e.bt)
	}

	// CODE: strictly after DECIDE. For an M/S frame, butterfly each
	// granule's L/R spectra into M/S in place, so e.xr now holds exactly
	// what the outer loop, escalation, and render read. Each coded channel
	// takes its threshold (Xmin/XminS) and demand (PE/PES) ONLY from its
	// chosen representation (chosenRep: repL/repR for L/R, repM/repS for
	// M/S), selected by that granule's OWN decided block type; the phase
	// indexes psyOuts exclusively through chosenRep so a cross-
	// representation wiring mistake is compile-visible, not a silent
	// budget leak.
	for g := range 2 {
		if e.msFrame {
			butterflyXr(&e.xr[g][0], &e.xr[g][1], &e.xrM, &e.xrS)
			e.xr[g][0] = e.xrM
			e.xr[g][1] = e.xrS
		}
		for ch := range e.nch {
			bt := e.bt[g][ch]
			e.gr[g][ch].blockType = bt
			lay := layoutFor(bt, e.srIndex)
			rep := e.chosenRep(ch)
			if bt == blockShort {
				for s := range lay.nBands {
					e.xminXr[g][ch][s] = float64(e.psyOuts[rep][g].XminS[s] * XminScaleShort)
				}
				e.gr[g][ch].peBits = granuleDemandBits(e.psyOuts[rep][g].PES, meanGB)
			} else {
				for s := range lay.nBands {
					e.xminXr[g][ch][s] = float64(e.psyOuts[rep][g].Xmin[s] * XminScale)
				}
				e.gr[g][ch].peBits = granuleDemandBits(e.psyOuts[rep][g].PE, meanGB)
			}
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
			lay := layoutFor(e.bt[g][ch], e.srIndex)
			_ = outerLoop(&e.xr[g][ch], &e.xminXr[g][ch], budget, lay, &e.gr[g][ch], &e.bestScratch)
		}
	}

	e.escalateForMasking(nGC, padding, area, capBytes)

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

// repPE returns representation rep's PE-domain cost estimate for granule g
// in the DECIDE phase's four-way comparison (design decision 13): PES when
// the granule's decided tiling codes that representation's reference
// channel as short, PE otherwise. repL/repM/repS all reference physical
// channel 0's decided block type (repM/repS are only ever coded when both
// channels already agree, per blockTypesAgree's veto, so channel 0's type
// is as good a reference as channel 1's); repR references channel 1.
func (e *Encoder) repPE(rep, g int) float64 {
	ch := 0
	if rep == repR {
		ch = 1
	}
	if e.bt[g][ch] == blockShort {
		return e.psyOuts[rep][g].PES
	}
	return e.psyOuts[rep][g].PE
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
func (e *Encoder) escTryBudget(i, budget int) {
	// Count every attempt (hit or miss). The memo changes cost, not control
	// flow: the budgets tried, their order, and where maskEscalationMaxCalls
	// binds all stay byte-identical to the pre-memo escalation.
	e.esc.calls++
	g, ch := i/e.nch, i%e.nch
	gc := &e.gr[g][ch]
	lay := e.layFor(i)

	var excess, ratio float64
	var over int
	if rec, ok := e.esc.memoGet(i, budget); ok {
		// outerLoop(i, budget) is pure within a frame, so its cached result and
		// the metrics derived from it are exactly what a recompute would yield.
		*gc = rec.gc
		excess, ratio, over = rec.excess, rec.ratio, rec.over
	} else {
		_ = outerLoop(&e.xr[g][ch], &e.xminXr[g][ch], budget, lay, gc, &e.bestScratch)
		var noise [39]float64
		noiseGranule(&e.xr[g][ch], &gc.ix, gc.globalGain, &gc.sf, lay, &noise)
		excess, ratio, over = maskingMetrics(&noise, &e.xminXr[g][ch], lay)
		// Store the RAW outerLoop result, captured before the accept/rollback
		// below mutates *gc or best[i]. The memoN < len guard is provably
		// always true (see escMemoCap); it only backstops a future change to
		// the call accounting that could break that bound.
		if e.esc.memoN < len(e.esc.memo) {
			e.esc.memo[e.esc.memoN] = escMemoEntry{i: i, budget: budget, over: over, excess: excess, ratio: ratio, gc: *gc}
			e.esc.memoN++
		}
	}

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
func (e *Encoder) escAttempt(i, hiB, loB int) {
	e.esc.triedHi[i] = hiB
	e.esc.triedLo[i] = loB
	e.escTryBudget(i, hiB)
	if loB != hiB {
		e.escTryBudget(i, loB)
	}
}

// escalateForMasking runs codeFrame's Stage 2 masking-driven budget
// escalation (design decision 9, third revision) after Stage 1's initial
// coding pass: PE-derived budgets are estimates and can under-allocate
// below the masking threshold on frames the old flat pre-reservoir budget
// satisfied. nGC, lay, padding, area, capBytes mirror codeFrame's own
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
// ceiling on escTryBudget calls): the main loop's measure Phi = (count
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
func (e *Encoder) escalateForMasking(nGC, padding, area, capBytes int) {
	_, hi := e.resv.spendBounds(area, capBytes)
	flatShare := granuleBudgetBits(e.bitrateIndex, e.srIndex, padding, e.nch)

	// Seed the cross-budget best-pass cache from Stage 1's initial pass.
	// triedHi/triedLo start at -1 (an offer can never legally be
	// negative), an unreachable sentinel that forces the fixpoint sweep
	// to attempt any granule-channel the main loop never got to.
	e.esc.calls = 0
	// Drop the previous frame's memo: (i, budget) results are only valid for
	// this frame's frozen xr/xmin. O(1); lookups scan only memo[:memoN], so
	// stale entries below the old length are overwritten before they are read.
	e.esc.memoN = 0
	for i := range nGC {
		g, ch := i/e.nch, i%e.nch
		lay := e.layFor(i)
		e.esc.best[i] = e.gr[g][ch]
		var noise [39]float64
		noiseGranule(&e.xr[g][ch], &e.gr[g][ch].ix, e.gr[g][ch].globalGain, &e.gr[g][ch].sf, lay, &noise)
		e.esc.bestExcess[i], e.esc.bestRatio[i], e.esc.bestOver[i] = maskingMetrics(&noise, &e.xminXr[g][ch], lay)
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
		e.escAttempt(g, hiB, min(flatShare, hiB))
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
				e.escAttempt(i, hiB, loB)
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
		lay := e.layFor(i)

		if e.diagHook != nil {
			var noise [39]float64
			noiseGranule(&e.xr[g][ch], &gc.ix, gc.globalGain, &gc.sf, lay, &noise)
			atCap := diagAtCap(&gc.sf, lay)
			var exempt [39]bool
			for s := range lay.nBands {
				exempt[s] = diagFloorBound(&e.xr[g][ch], lay, gc.part23Length, gc, &noise, s, atCap[s])
			}
			e.diagHook(g, ch, DiagGranule{
				Noise:            noise,
				XminXr:           e.xminXr[g][ch],
				Exempt:           exempt,
				BudgetBits:       gc.part23Length,
				CapacityLeftBits: capacityLeftBits,
				Xr:               e.xr[g][ch],
				BlockType:        gc.blockType,
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
// fbChainDelay in internal/dec/encx_filterbank_test.go:90).
//
// Re-measured (and reconfirmed at this same 1057) after the held-frame
// one-frame PCM lookahead for attack detection and block switching landed
// (design decision 11): EncodeFrame now needs one extra input call before a
// frame's coding can even start (call 1 stashes into held and appends
// nothing; call n codes the frame stashed since call n-1, once call n's
// samples supply that frame's lookahead), so N input calls plus a drain
// still total N+1 emitted frames, and the drain call itself now codes TWO
// frames back to back (the held real frame, once next is zeroed to
// silence, then the silence flush frame) instead of one. Both of those are
// CALL-LATENCY and drain-shape facts about the EncodeFrame API contract,
// not sample-domain delay: frame k of the emitted STREAM still codes input
// samples [k*1152, (k+1)*1152) exactly as before the held-frame design,
// so the decoded-output-vs-input lag this constant measures is unchanged.
// A roadmap draft once proposed 1057 + 1152 = 2209 for this constant,
// reasoning from the one-frame call latency; that conflates call latency
// with stream alignment and was rejected (decision 12): migrating the
// constant would falsely claim the bitstream carries an extra leading
// silence frame, which would corrupt any downstream gapless-playback math
// built on it. ChainDelay < 1152 is asserted alongside the measurement: it
// is what makes the drain design correct regardless of how many frames the
// drain call itself codes, since even the wider two-frame drain flush
// covers more than the chain's total lag, so draining once is enough to
// push every real sample through the pipeline and out the decoder.
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
	Noise            [39]float64
	XminXr           [39]float64
	Exempt           [39]bool
	BudgetBits       int
	CapacityLeftBits int
	Xr               [576]float64

	// BlockType is this granule-channel's decided window shape
	// (blocktypes.go's blockLong/Start/Short/Stop), added for Inc7's block
	// switching: a caller needs it to pick the matching bandLayout
	// (layoutFor) before re-running outerLoop/noiseGranule/maskingMetrics
	// against the captured Xr/XminXr, since a frame's granule-channels can
	// now carry different block types.
	BlockType int
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
// representable cap (scalefacScale==1 and scf[b] at sfMaxLo/sfMaxHi), or a
// band that carries no scalefactor at all (b >= lay.nScf): bands the outer
// loop cannot amplify any further under this state, mirroring
// loop_test.go's package-internal bandAtCap helper.
func diagAtCap(sf *scfState, lay *bandLayout) [39]bool {
	var atCap [39]bool
	for b := range lay.nBands {
		if b >= lay.nScf {
			atCap[b] = true
			continue
		}
		maxv := sfMaxLo
		if b >= lay.slen1End {
			maxv = sfMaxHi
		}
		atCap[b] = sf.scalefacScale == 1 && sf.scf[b] >= maxv
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
func diagFloorBound(xr *[576]float64, lay *bandLayout, budget int, gc *granuleCoding, noiseNow *[39]float64, s int, atCap bool) bool {
	if atCap {
		return true
	}
	sfCap := sfMaxLo
	if s >= lay.slen1End {
		sfCap = sfMaxHi
	}
	if s >= lay.nScf || gc.sf.scf[s] >= sfCap {
		return true // no room to even try one more unit
	}

	trial := *gc
	trial.sf.scf[s]++
	idx, part2, ok := chooseScalefacCompress(&trial.sf, 0, lay)
	if !ok || part2 >= budget {
		return true // no budget to even try the bump
	}
	huffBudget := min(budget, maxPart23Length) - part2
	codeGranule(xr, huffBudget, lay, &trial)
	trial.scfCompress, trial.part2Bits = idx, part2

	var noiseTrial [39]float64
	noiseGranule(xr, &trial.ix, trial.globalGain, &trial.sf, lay, &noiseTrial)
	return !(noiseTrial[s] < noiseNow[s])
}
