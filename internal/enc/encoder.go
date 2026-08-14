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
	xr   [576]float64       // scratch: one granule-channel's MDCT spectrum

	in [2][576]float64 // per-granule staging: clamped, PCMScale-scaled samples

	psy    [2]PsyModel      // per-channel psychoacoustic model 2 state
	psyWin [2][1024]float64 // per-channel causal analysis window: clamped [-1,1] samples, BEFORE PCMScale
	psyOut PsyOut           // scratch: one channel's just-analyzed psymodel output
	xminXr [22]float64      // scratch: PsyOut.Xmin scaled into the xr noise domain (XminScale)

	pad         paddingState
	gr          [2][2]granuleCoding
	bestScratch granuleCoding // outerLoop's caller-owned best-pass scratch

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

// New returns a new Encoder for cfg, or an error if cfg is not a legal
// MPEG-1 Layer III CBR configuration.
func New(cfg Config) (*Encoder, error) {
	e := &Encoder{}
	if err := e.Reset(cfg); err != nil {
		return nil, err
	}
	return e, nil
}

// Reset clears all stream state (filterbank history, MDCT overlap, padding
// accumulator, poison, drain, and Stats) and revalidates cfg, as at the
// start of a fresh stream. It is the only way to clear a poisoned Encoder.
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
		e.psy[ch].Reset(e.srIndex)
	}
	return nil
}

// Drained reports whether the encoder has emitted its final (silence,
// history-flushing) frame in response to a nil EncodeFrame call.
func (e *Encoder) Drained() bool { return e.drained }

// EncodeFrame consumes exactly 1152 samples per channel of planar float32
// PCM in [-1, 1] and appends exactly one MP3 frame to dst.
//
// Validation order: while the encoder is poisoned (a prior call saw a NaN
// or Inf), every call, including a nil drain call, returns dst unchanged
// with ErrInvalidAudio; only Reset clears that state. Otherwise, samples ==
// nil drains: it encodes one final frame of silence that flushes the
// filterbank and MDCT history (ChainDelay < 1152 guarantees that one frame
// suffices), marks the encoder drained, and is counted in Stats; further
// nil calls after that append nothing and return a nil error.
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
		return e.codeFrame(dst, nil), nil
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

// codeFrame runs the full per-frame pipeline: pick this frame's padding
// bit and main-data budget, then for each granule and channel slide the
// psymodel's causal window, run AnalyzeGranule to get this granule's
// masking thresholds, scale them into the xr noise domain, run
// AnalyzeGranule -> FlipOddSubbands -> MDCTGranule -> save prev ->
// AliasReduce to get the spectrum, then outerLoop to pick scalefactors and
// quantize against the psymodel's targets. After both granules, scfsi is
// detected and applied per channel, then the frame is assembled with
// appendFrame and Stats updated. samples == nil codes silence (the
// per-granule staging loop below writes zero into e.in/e.psyWin instead of
// a real sample, which flushes the filterbank, MDCT, and psymodel history
// through one real pass of the pipeline) for the drain frame.
func (e *Encoder) codeFrame(dst []byte, samples [][]float32) []byte {
	padding := e.pad.next(e.cfg.BitrateKbps, e.cfg.SampleRate)
	sfb := &sfbWidthsLong[e.srIndex]
	budget := granuleBudgetBits(e.bitrateIndex, e.srIndex, padding, e.nch)

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

			e.psy[ch].AnalyzeGranule(e.psyWin[ch][:], &e.psyOut)
			for s := range 22 {
				e.xminXr[s] = float64(e.psyOut.Xmin[s] * XminScale)
			}

			e.fb[ch].AnalyzeGranule(e.in[ch][:], &e.cur)
			FlipOddSubbands(&e.cur)
			MDCTGranule(&e.prev[ch], &e.cur, &e.xr)
			e.prev[ch] = e.cur
			AliasReduce(&e.xr)

			_ = outerLoop(&e.xr, &e.xminXr, budget, sfb, &e.gr[g][ch], &e.bestScratch)

			if e.diagHook != nil {
				gc := &e.gr[g][ch]
				var noise [22]float64
				noiseGranule(&e.xr, &gc.ix, gc.globalGain, &gc.sf, sfb, &noise)
				atCap := diagAtCap(&gc.sf)
				var exempt [22]bool
				for s := range 22 {
					exempt[s] = diagFloorBound(&e.xr, sfb, budget, gc, s, atCap[s])
				}
				e.diagHook(g, ch, DiagGranule{Noise: noise, XminXr: e.xminXr, Exempt: exempt})
			}

			e.sumGlobalGain += int64(e.gr[g][ch].globalGain)
			e.countGranules++
		}
	}

	for ch := range e.nch {
		mask := detectScfsi(&e.gr[0][ch], &e.gr[1][ch])
		e.gr[1][ch].scfsi = mask
		e.scfsiSaved += int64(applyScfsi(&e.gr[1][ch], mask))
	}

	before := len(dst)
	dst = appendFrame(dst, e.bitrateIndex, e.srIndex, padding, e.mode, &e.gr, e.nch)

	e.frames++
	e.bytes += int64(len(dst) - before)
	if padding != 0 {
		e.paddedFrames++
	}
	return dst
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

// DiagGranule is one granule-channel's outer-loop diagnostic snapshot:
// the per-sfb quantization noise the final coding produced (recomputed via
// noiseGranule against that granule-channel's own gc.ix/globalGain/sf),
// the calibrated xminXr threshold the outer loop targeted, and which bands
// are beyond the loop's reach (diagFloorBound: structurally capped, or
// empirically proven futile to amplify further), where a residual xmin
// violation is expected, not a defect. Test-only, filled by
// SetDiagHookPin.
type DiagGranule struct {
	Noise  [22]float64
	XminXr [22]float64
	Exempt [22]bool
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
