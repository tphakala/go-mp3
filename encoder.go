package mp3

import (
	"errors"
	"fmt"

	"github.com/tphakala/go-mp3/internal/enc"
)

// FrameSize is the number of samples per channel consumed by one
// Encoder.EncodeFrame call and covered by one encoded MP3 frame (MPEG-1
// Layer III): 1152, fixed by the format.
const FrameSize = 1152

// DefaultBitrate is the CBR target, in bits per second, selected when an
// EncoderConfig leaves both Bitrate and Quality zero: 128 kb/s.
const DefaultBitrate = 128000

// standardDecoderDelay is the fixed algorithmic delay, in samples per
// channel, a standard MPEG-1 Layer III decoder contributes to a LAME-style
// tag's "encoder delay"/"decoder delay" split. This project's documentation
// convention, not an independently measured figure here; kept consistent
// with pcm/decoder.go's "~528-sample" wording.
const standardDecoderDelay = 529

// EncoderDelay is this encoder's own algorithmic delay, in samples per
// channel, in the sense a LAME-style tag's "encoder delay" field uses: a
// stream this package produces carries TotalDelay leading samples of
// algorithmic delay once decoded, of which a standard MPEG-1 Layer III
// decoder contributes its own fixed standardDecoderDelay; EncoderDelay is
// the remainder, attributable to the encoder alone. See TotalDelay for the
// number that matters when trimming a decoded stream back to the original
// input.
const EncoderDelay = enc.ChainDelay - standardDecoderDelay

// TotalDelay is the total encoder-plus-standard-decoder algorithmic delay,
// in samples per channel, carried as leading samples in the decoded output
// of any stream this package produces. This Phase 3 encoder emits tagless
// CBR streams, with no LAME gapless tag for a downstream decoder to trim
// automatically, so a caller that wants to align decoded audio back to the
// original input must subtract TotalDelay itself (drop the first
// TotalDelay samples per channel after decoding). EncoderDelay (528) is
// the encoder-only LAME-tag split: TotalDelay - standardDecoderDelay.
const TotalDelay = enc.ChainDelay

// Sentinel errors returned by Encoder methods.
var (
	// ErrInvalidAudio is returned by EncodeFrame when a non-nil samples
	// argument contains a NaN or an Inf anywhere. It appends nothing new
	// to dst and poisons the Encoder: every later call, including a nil
	// drain call, also returns ErrInvalidAudio until Reset.
	ErrInvalidAudio = errors.New("go-mp3: invalid audio sample (NaN or Inf)")

	// ErrEncoderNotInitialized is returned by EncodeFrame on an Encoder that has
	// not been successfully initialized by NewEncoder or Reset. This
	// includes a zero-value Encoder and one whose only Reset call so far
	// returned an error; it does not include an already-initialized
	// Encoder whose later Reset call errors, since that leaves the prior
	// successful state untouched.
	ErrEncoderNotInitialized = errors.New("go-mp3: encoder not initialized")

	// ErrEncoderFinalized is returned by EncodeFrame when non-nil audio is
	// submitted after the stream has already been finalized: either a
	// short (final) frame was already submitted, or the encoder has
	// already drained. Reset starts a new stream.
	ErrEncoderFinalized = errors.New("go-mp3: encoder finalized; Reset to start a new stream")
)

// Frame-shape errors: distinct values with a stable go-mp3: message, kept
// unexported since only the three sentinels above are part of the public
// error API. See EncodeFrame's doc comment for when each applies.
var (
	errWrongChannelCount     = errors.New("go-mp3: len(samples) != EncoderConfig.Channels")
	errEmptyChannel          = errors.New("go-mp3: empty channel; pass nil samples to flush instead")
	errUnequalChannelLengths = errors.New("go-mp3: channels have unequal sample counts")
	errFrameTooLong          = errors.New("go-mp3: channel sample count exceeds FrameSize")
)

// EncoderConfig configures a new or reset Encoder.
type EncoderConfig struct {
	// SampleRate is the input sample rate in Hz: 32000, 44100, or 48000.
	// Required.
	SampleRate int
	// Channels is the number of audio channels: 1 (mono) or 2 (L/R
	// stereo). Required.
	Channels int
	// Bitrate is the CBR target for the whole stream, in bits per second:
	// one of 32000, 40000, 48000, 56000, 64000, 80000, 96000, 112000,
	// 128000, 160000, 192000, 224000, 256000, or 320000. Zero selects
	// DefaultBitrate. Mutually exclusive with Quality.
	Bitrate int
	// Quality is reserved for a future VBR mode and is validated now so
	// the config surface is stable: zero means unset; any nonzero value
	// is rejected until VBR exists. Mutually exclusive with Bitrate.
	Quality int
}

// toEncConfig validates cfg and maps it to the internal enc.Config, in the
// normative order: sample rate, channel count, the Bitrate/Quality
// mutual-exclusivity rule, the Quality-reserved-for-VBR rejection, then
// the bitrate itself. The bitrate check tests %1000 before dividing:
// dividing first would silently truncate a value like 128500 to a legal
// 128 kbps instead of rejecting it.
func (cfg EncoderConfig) toEncConfig() (enc.Config, error) {
	switch cfg.SampleRate {
	case 32000, 44100, 48000:
	default:
		return enc.Config{}, fmt.Errorf("go-mp3: invalid sample rate %d, want 32000, 44100, or 48000", cfg.SampleRate)
	}
	if cfg.Channels != 1 && cfg.Channels != 2 {
		return enc.Config{}, fmt.Errorf("go-mp3: invalid channel count %d, want 1 or 2", cfg.Channels)
	}
	if cfg.Bitrate != 0 && cfg.Quality != 0 {
		return enc.Config{}, errors.New("go-mp3: Bitrate and Quality are mutually exclusive")
	}
	if cfg.Quality != 0 {
		return enc.Config{}, errors.New("go-mp3: Quality is reserved for a future VBR mode, not yet supported")
	}

	bitrate := cfg.Bitrate
	switch {
	case bitrate == 0:
		bitrate = DefaultBitrate
	case bitrate < 0:
		return enc.Config{}, fmt.Errorf("go-mp3: invalid bitrate %d, must be positive", bitrate)
	case bitrate%1000 != 0:
		return enc.Config{}, fmt.Errorf("go-mp3: invalid bitrate %d, must be a whole multiple of 1000", bitrate)
	}
	kbps := bitrate / 1000
	if !enc.ValidBitrateKbps(kbps) {
		return enc.Config{}, fmt.Errorf("go-mp3: invalid bitrate %d, want one of the 14 MPEG-1 Layer III CBR rates", bitrate)
	}

	return enc.Config{SampleRate: cfg.SampleRate, Channels: cfg.Channels, BitrateKbps: kbps}, nil
}

// Encoder is a stateful CBR MPEG-1 Layer III encoder: a thin validated
// wrapper over an internal enc.Encoder. It validates configuration and
// frame shape up front so that the internal layer's two panic contracts (a
// length mismatch, and any non-nil call after a drain) are unreachable
// through this public API. Frames from the same stream must be encoded in
// order with the same Encoder. An Encoder is not safe for concurrent use.
type Encoder struct {
	enc        *enc.Encoder
	cfg        EncoderConfig
	shortFrame bool

	// scratchBuf backs the short-final-frame zero-pad path: two full
	// FrameSize channels, embedded directly in the Encoder value so no
	// per-call allocation is ever needed. scratchView holds slice headers
	// into scratchBuf; scratch is scratchView sliced down to cfg.Channels
	// in Reset.
	scratchBuf  [2][FrameSize]float32
	scratchView [2][]float32
	scratch     [][]float32
}

// NewEncoder returns a new Encoder for cfg, or an error if cfg is invalid.
// It is equivalent to calling Reset on a zero-value Encoder.
func NewEncoder(cfg EncoderConfig) (*Encoder, error) {
	e := &Encoder{}
	if err := e.Reset(cfg); err != nil {
		return nil, err
	}
	return e, nil
}

// Reset validates cfg and clears all stream state: the internal
// filterbank/MDCT history, the padding accumulator, poison, drain, Stats,
// and this wrapper's own short-final-frame latch, as at the start of a
// fresh stream. Reset is legal on a zero-value Encoder: NewEncoder is
// exactly &Encoder{} followed by Reset. It is also the only way to clear a
// poisoned or finalized Encoder.
func (e *Encoder) Reset(cfg EncoderConfig) error {
	encCfg, err := cfg.toEncConfig()
	if err != nil {
		return err
	}

	if e.enc == nil {
		internalEnc, err := enc.New(encCfg)
		if err != nil {
			return err
		}
		e.enc = internalEnc
	} else if err := e.enc.Reset(encCfg); err != nil {
		return err
	}

	e.cfg = cfg
	e.shortFrame = false
	e.scratchView[0] = e.scratchBuf[0][:]
	e.scratchView[1] = e.scratchBuf[1][:]
	e.scratch = e.scratchView[:cfg.Channels]

	return nil
}

// EncodeFrame encodes the next frame of planar float32 PCM (one slice per
// channel, up to FrameSize samples each) and appends the encoded MP3 frame
// to dst, returning the extended slice. Finite samples are accepted at any
// magnitude; any sample outside [-1, 1] is clamped to [-1, 1] before
// encoding. A NaN or an Inf anywhere returns ErrInvalidAudio instead (see
// its doc comment above). EncodeFrame is append-style, like
// strconv.AppendInt: allocation-free when dst has spare capacity.
//
// Only the final frame of a stream may be shorter than FrameSize; a short
// frame is zero-padded internally and finalizes the stream, so submitting
// any further non-nil audio returns ErrEncoderFinalized until Reset.
//
// A non-nil samples must carry exactly one slice per configured channel,
// all of equal, nonzero length no greater than FrameSize; violating any of
// that (wrong channel count, an empty channel, unequal channel lengths, or
// a channel longer than FrameSize) returns a distinct error, none of them
// one of the three sentinels documented here.
//
// Pass a nil samples to drain: unless the encoder has been poisoned by
// prior invalid audio (which makes every later call, including a drain,
// return ErrInvalidAudio until Reset), a nil call appends the encoder's
// final flush frame and is always legal, including right after a short
// final frame; further nil calls append nothing more. Drained reports
// whether the drain has already happened. Submitting non-nil audio after a
// drain also returns ErrEncoderFinalized.
//
// On an Encoder that has not been successfully initialized by NewEncoder
// or Reset (see ErrEncoderNotInitialized above for exactly what that covers),
// EncodeFrame returns (dst, ErrEncoderNotInitialized) unconditionally.
func (e *Encoder) EncodeFrame(dst []byte, samples [][]float32) ([]byte, error) {
	if e.enc == nil {
		return dst, ErrEncoderNotInitialized
	}

	if samples == nil {
		out, err := e.enc.EncodeFrame(dst, nil)
		return out, translateInvalidAudio(err)
	}

	// Poisoned-and-drained is unreachable here: the internal encoder
	// checks poison before the nil-drain branch on every call, so it never
	// completes a drain while poisoned. Drained() true always means a
	// clean prior drain, and this check is what blocks the internal
	// drain-terminal panic.
	if e.enc.Drained() {
		return dst, ErrEncoderFinalized
	}
	if e.shortFrame {
		return dst, ErrEncoderFinalized
	}

	if len(samples) != e.cfg.Channels {
		return dst, errWrongChannelCount
	}
	n := len(samples[0])
	for _, ch := range samples {
		if len(ch) == 0 {
			return dst, errEmptyChannel
		}
	}
	for _, ch := range samples {
		if len(ch) != n {
			return dst, errUnequalChannelLengths
		}
	}
	if n > FrameSize {
		return dst, errFrameTooLong
	}

	if n == FrameSize {
		out, err := e.enc.EncodeFrame(dst, samples)
		return out, translateInvalidAudio(err)
	}

	// Short final frame: copy into the reusable scratch buffer and
	// zero-pad the tail to FrameSize. No allocation: scratch is sized
	// once, in Reset. shortFrame latches only on a successful encode: if
	// this frame is itself invalid (NaN/Inf), the internal encoder
	// poisons instead of finalizing, and every later call must see that
	// poison (ErrInvalidAudio), not a wrongly latched ErrEncoderFinalized.
	for ch := range samples {
		clear(e.scratch[ch])
		copy(e.scratch[ch], samples[ch])
	}
	out, err := e.enc.EncodeFrame(dst, e.scratch)
	err = translateInvalidAudio(err)
	if err == nil {
		e.shortFrame = true
	}
	return out, err
}

// translateInvalidAudio maps the internal enc.ErrInvalidAudio to the
// public ErrInvalidAudio. On any call this wrapper shields from the
// internal panic contracts, the internal layer can only return nil or
// enc.ErrInvalidAudio, so nothing else passes through unchanged.
func translateInvalidAudio(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, enc.ErrInvalidAudio) {
		return ErrInvalidAudio
	}
	return err
}

// Drained reports whether the encoder has already emitted its final flush
// frame in response to a nil EncodeFrame call. It returns false on a
// zero-value Encoder.
func (e *Encoder) Drained() bool {
	if e.enc == nil {
		return false
	}
	return e.enc.Drained()
}

// Delay returns EncoderDelay. It is a method, rather than callers reading
// the constant directly, so the value can become configuration-dependent
// in the future without an API break; today it does not vary, and returns
// EncoderDelay even on a zero-value Encoder.
func (e *Encoder) Delay() int { return EncoderDelay }

// Stats returns the encoder's cumulative counters since NewEncoder or the
// last Reset. It returns a zero Stats on a zero-value Encoder.
//
// Field by field, not a bare struct conversion: internal/enc.Stats gained
// a ScfsiBitsSaved field (Phase 4 increment 4) that is internal accounting
// only, not part of this public API, so a straight Stats(e.enc.Stats())
// conversion would no longer compile once the two structs' field sets
// diverged; this stays correct regardless of which fields internal/enc.Stats
// adds in the future.
func (e *Encoder) Stats() Stats {
	if e.enc == nil {
		return Stats{}
	}
	s := e.enc.Stats()
	return Stats{
		Frames:         s.Frames,
		Bytes:          s.Bytes,
		PaddedFrames:   s.PaddedFrames,
		MeanGlobalGain: s.MeanGlobalGain,
	}
}

// Stats counts what an Encoder emitted since NewEncoder or the last Reset.
type Stats struct {
	Frames         int64
	Bytes          int64
	PaddedFrames   int64
	MeanGlobalGain float64 // mean over all coded granule-channels
}
