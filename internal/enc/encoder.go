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
	if _, ok := bitrateIndexForKbps[c.BitrateKbps]; !ok {
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

	pad paddingState
	gr  [2][2]granuleCoding

	poisoned bool
	drained  bool

	frames        int64
	bytes         int64
	paddedFrames  int64
	sumGlobalGain int64
	countGranules int64
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
// bit and main-data budget, then for each granule and channel run
// AnalyzeGranule -> FlipOddSubbands -> MDCTGranule -> save prev ->
// AliasReduce -> codeGranule, then assemble the frame with appendFrame and
// update Stats. samples == nil codes silence (e.in stays at its zero
// value, which flushes the filterbank and MDCT history through one real
// pass of the pipeline) for the drain frame.
func (e *Encoder) codeFrame(dst []byte, samples [][]float32) []byte {
	if samples == nil {
		for ch := range e.nch {
			e.in[ch] = [576]float64{}
		}
	}

	padding := e.pad.next(e.cfg.BitrateKbps, e.cfg.SampleRate)
	sfb := &sfbWidthsLong[e.srIndex]
	budget := granuleBudgetBits(e.bitrateIndex, e.srIndex, padding, e.nch)

	for g := range 2 {
		if samples != nil {
			for ch := range e.nch {
				for i := range 576 {
					v := clamp(float64(samples[ch][g*576+i]))
					e.in[ch][i] = float64(v * PCMScale)
				}
			}
		}
		for ch := range e.nch {
			e.fb[ch].AnalyzeGranule(e.in[ch][:], &e.cur)
			FlipOddSubbands(&e.cur)
			MDCTGranule(&e.prev[ch], &e.cur, &e.xr)
			e.prev[ch] = e.cur
			AliasReduce(&e.xr)
			codeGranule(&e.xr, budget, sfb, &e.gr[g][ch])

			e.sumGlobalGain += int64(e.gr[g][ch].globalGain)
			e.countGranules++
		}
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
