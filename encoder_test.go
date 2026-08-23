package mp3_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"testing"

	mp3 "github.com/tphakala/go-mp3"
	"github.com/tphakala/go-mp3/internal/testsignal"
	mp3pcm "github.com/tphakala/go-mp3/pcm"
)

// planarNoise builds an nch x n planar float32 buffer of LCG noise scaled
// to amp, staying within [-1, 1].
func planarNoise(seed *uint64, nch, n int, amp float32) [][]float32 {
	out := make([][]float32, nch)
	for ch := range nch {
		out[ch] = make([]float32, n)
		for i := range out[ch] {
			v := float32(testsignal.LCG(seed))*2 - 1
			out[ch][i] = v * amp
		}
	}
	return out
}

// planarSine builds an nch x n planar float32 buffer of a sine wave at
// freqHz sampled at sampleRate, phase-continuous across calls via phase so
// a caller can build a longer signal out of successive fixed-size chunks.
func planarSine(nch, n, sampleRate, freqHz int, phase *float64, amp float32) [][]float32 {
	out := make([][]float32, nch)
	for ch := range nch {
		out[ch] = make([]float32, n)
	}
	step := 2 * math.Pi * float64(freqHz) / float64(sampleRate)
	for i := range n {
		v := float32(math.Sin(*phase)) * amp
		for ch := range nch {
			out[ch][i] = v
		}
		*phase += step
	}
	return out
}

// newTestEncoder returns a valid 44.1kHz/128kbps/stereo Encoder or fails
// the test.
func newTestEncoder(t *testing.T) *mp3.Encoder {
	t.Helper()
	e, err := mp3.NewEncoder(mp3.EncoderConfig{SampleRate: 44100, Channels: 2, Bitrate: 128000})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	return e
}

// TestEncoderConfigValidation is table-driven over every EncoderConfig
// validation rule: bad sample rate, bad channel count, the Bitrate/Quality
// mutual-exclusivity rule, the Quality-unsupported rejection, a
// negative bitrate, a bitrate that is not a multiple of 1000 (128500, the
// %1000-before-divide trap: dividing first would wrongly truncate this to
// a legal 128 kbps), and a bitrate that is a multiple of 1000 but not one
// of the 14 legal MPEG-1 Layer III rates.
func TestEncoderConfigValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  mp3.EncoderConfig
		ok   bool
	}{
		{"valid, default bitrate", mp3.EncoderConfig{SampleRate: 44100, Channels: 2}, true},
		{"valid, explicit bitrate", mp3.EncoderConfig{SampleRate: 44100, Channels: 2, Bitrate: 192000}, true},
		{"valid mono", mp3.EncoderConfig{SampleRate: 32000, Channels: 1, Bitrate: 32000}, true},
		{"valid highest rate", mp3.EncoderConfig{SampleRate: 48000, Channels: 2, Bitrate: 320000}, true},
		{"bad sample rate", mp3.EncoderConfig{SampleRate: 22050, Channels: 2}, false},
		{"zero sample rate", mp3.EncoderConfig{SampleRate: 0, Channels: 2}, false},
		{"zero channels", mp3.EncoderConfig{SampleRate: 44100, Channels: 0}, false},
		{"three channels", mp3.EncoderConfig{SampleRate: 44100, Channels: 3}, false},
		{"bitrate and quality both set", mp3.EncoderConfig{SampleRate: 44100, Channels: 2, Bitrate: 128000, Quality: 1}, false},
		{"quality unsupported", mp3.EncoderConfig{SampleRate: 44100, Channels: 2, Quality: 5}, false},
		{"negative bitrate", mp3.EncoderConfig{SampleRate: 44100, Channels: 2, Bitrate: -128000}, false},
		{"bitrate not a multiple of 1000 (128500 trap)", mp3.EncoderConfig{SampleRate: 44100, Channels: 2, Bitrate: 128500}, false},
		{"bitrate multiple of 1000 but illegal (129000)", mp3.EncoderConfig{SampleRate: 44100, Channels: 2, Bitrate: 129000}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := mp3.NewEncoder(c.cfg)
			if c.ok && err != nil {
				t.Fatalf("NewEncoder(%+v): unexpected error: %v", c.cfg, err)
			}
			if !c.ok && err == nil {
				t.Fatalf("NewEncoder(%+v): want error, got nil", c.cfg)
			}
		})
	}

	t.Run("accepts full legal grid", func(t *testing.T) {
		rates := []int{32000, 44100, 48000}
		kbpsList := []int{32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
		for _, sr := range rates {
			for _, nch := range []int{1, 2} {
				for _, kb := range kbpsList {
					cfg := mp3.EncoderConfig{SampleRate: sr, Channels: nch, Bitrate: kb * 1000}
					if _, err := mp3.NewEncoder(cfg); err != nil {
						t.Fatalf("NewEncoder(%+v): unexpected error: %v", cfg, err)
					}
				}
			}
		}
	})
}

// TestEncoderZeroValue requires every method on a never-initialized
// Encoder to behave per the documented zero-value contract, without
// panicking: EncodeFrame returns ErrEncoderNotInitialized and leaves dst
// unchanged, Drained is false, Delay is EncoderDelay, Stats is zero.
func TestEncoderZeroValue(t *testing.T) {
	var e mp3.Encoder

	dst, err := e.EncodeFrame(nil, [][]float32{make([]float32, mp3.FrameSize), make([]float32, mp3.FrameSize)})
	if !errors.Is(err, mp3.ErrEncoderNotInitialized) {
		t.Fatalf("EncodeFrame on zero value: err = %v, want ErrEncoderNotInitialized", err)
	}
	if dst != nil {
		t.Fatalf("EncodeFrame on zero value: dst = %v, want nil (unchanged)", dst)
	}
	if e.Drained() {
		t.Fatalf("Drained() on zero value = true, want false")
	}
	if got := e.Delay(); got != mp3.EncoderDelay {
		t.Fatalf("Delay() on zero value = %d, want %d", got, mp3.EncoderDelay)
	}
	if st := e.Stats(); st != (mp3.Stats{}) {
		t.Fatalf("Stats() on zero value = %+v, want zero Stats", st)
	}
}

// TestEncoderShortFinalFrame requires a 799-sample final frame (short on
// all channels) to zero-pad and finalize the stream: after it, further
// non-nil audio errors with ErrEncoderFinalized, a nil drain still
// succeeds, and Reset clears both the short-final latch and the drain.
func TestEncoderShortFinalFrame(t *testing.T) {
	cfg := mp3.EncoderConfig{SampleRate: 44100, Channels: 2, Bitrate: 128000}
	e, err := mp3.NewEncoder(cfg)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}

	seed := uint64(42)
	full := planarNoise(&seed, 2, mp3.FrameSize, 0.5)
	var stream []byte
	for range 3 {
		stream, err = e.EncodeFrame(stream, full)
		if err != nil {
			t.Fatalf("EncodeFrame: %v", err)
		}
	}

	// The short final frame zero-pads and finalizes, but with the bit
	// reservoir a frame may be held in the FIFO rather than flushed on its
	// own EncodeFrame call, so this call can legitimately append nothing; the
	// nil drain below is what force-flushes the held backlog and the final
	// history-flushing frame.
	short := planarNoise(&seed, 2, 799, 0.5)
	stream, err = e.EncodeFrame(stream, short)
	if err != nil {
		t.Fatalf("EncodeFrame with short final frame: %v", err)
	}

	if _, err := e.EncodeFrame(stream, full); !errors.Is(err, mp3.ErrEncoderFinalized) {
		t.Fatalf("EncodeFrame after short final frame: err = %v, want ErrEncoderFinalized", err)
	}

	streamAfterDrain, err := e.EncodeFrame(stream, nil)
	if err != nil {
		t.Fatalf("drain EncodeFrame after short final frame: %v", err)
	}
	if len(streamAfterDrain) == len(stream) {
		t.Fatalf("drain after short final frame appended nothing")
	}
	if !e.Drained() {
		t.Fatalf("Drained() = false after drain")
	}

	if err := e.Reset(cfg); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if e.Drained() {
		t.Fatalf("Drained() = true right after Reset")
	}
	if _, err := e.EncodeFrame(nil, full); err != nil {
		t.Fatalf("EncodeFrame after Reset: %v", err)
	}
}

// TestEncoderDefaultBitrate requires a zero Bitrate to encode at
// DefaultBitrate (128 kb/s), verified by decoding the emitted frame and
// checking FrameInfo.Bitrate, which is in kilobits per second.
func TestEncoderDefaultBitrate(t *testing.T) {
	e, err := mp3.NewEncoder(mp3.EncoderConfig{SampleRate: 44100, Channels: 2})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}

	seed := uint64(7)
	samples := planarNoise(&seed, 2, mp3.FrameSize, 0.5)
	stream, err := e.EncodeFrame(nil, samples)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	// The bit reservoir may hold the first frame in the FIFO rather than
	// flush it on its own call, so drain to force the stream out before
	// decoding.
	stream, err = e.EncodeFrame(stream, nil)
	if err != nil {
		t.Fatalf("drain EncodeFrame: %v", err)
	}

	d := mp3.NewDecoder()
	pcmBuf := make([]float32, mp3.FrameSize*2)
	n, info, err := d.DecodeFrame(stream, pcmBuf)
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}
	if n == 0 {
		t.Fatalf("DecodeFrame produced no samples")
	}
	if info.Bitrate != 128 {
		t.Fatalf("info.Bitrate = %d, want 128 (kbps)", info.Bitrate)
	}
}

// TestEncoderResetDeterminism requires the same input encoded twice around
// a Reset to produce byte-identical streams, since Reset performs a full
// state wipe of the internal encoder and this wrapper introduces no new
// arithmetic (only copies and zero-fills).
func TestEncoderResetDeterminism(t *testing.T) {
	cfg := mp3.EncoderConfig{SampleRate: 44100, Channels: 2, Bitrate: 128000}
	e, err := mp3.NewEncoder(cfg)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}

	encodeRun := func(seedVal uint64) []byte {
		seed := seedVal
		var stream []byte
		for range 5 {
			stream, err = e.EncodeFrame(stream, planarNoise(&seed, 2, mp3.FrameSize, 0.5))
			if err != nil {
				t.Fatalf("EncodeFrame: %v", err)
			}
		}
		stream, err = e.EncodeFrame(stream, nil)
		if err != nil {
			t.Fatalf("drain EncodeFrame: %v", err)
		}
		return stream
	}

	first := encodeRun(99)

	if err := e.Reset(cfg); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	second := encodeRun(99)

	if !bytes.Equal(first, second) {
		t.Fatalf("Reset did not reproduce byte-identical output (len %d vs %d)", len(first), len(second))
	}
}

// TestEncoderSteadyStateAllocs mirrors TestDecodeFrameSteadyStateAllocs
// (decoder_test.go): EncodeFrame must not allocate per call in steady
// state on the full-frame happy path, with a pre-grown dst and reused
// sample slices.
func TestEncoderSteadyStateAllocs(t *testing.T) {
	e := newTestEncoder(t)

	seed := uint64(11)
	samples := planarNoise(&seed, 2, mp3.FrameSize, 0.5)
	dst := make([]byte, 0, 4096)

	var err error
	for range 2 { // warmup
		dst, err = e.EncodeFrame(dst[:0], samples)
		if err != nil {
			t.Fatalf("warmup EncodeFrame: %v", err)
		}
	}

	avg := testing.AllocsPerRun(50, func() {
		var encErr error
		dst, encErr = e.EncodeFrame(dst[:0], samples)
		if encErr != nil {
			t.Fatalf("EncodeFrame: %v", encErr)
		}
	})
	if avg != 0 {
		t.Fatalf("steady-state allocs = %v, want 0", avg)
	}
}

// TestEncoderPanicShields drives every documented error path with plain
// calls (no recover): an escaped panic fails the test naturally. Each
// case must return an error, never panic, for input the internal
// enc.Encoder would otherwise reject with a panic (a caller-bug class at
// that layer that this wrapper must make unreachable).
func TestEncoderPanicShields(t *testing.T) {
	t.Run("audio after drain", func(t *testing.T) {
		e := newTestEncoder(t)
		seed := uint64(21)
		full := planarNoise(&seed, 2, mp3.FrameSize, 0.5)
		if _, err := e.EncodeFrame(nil, full); err != nil {
			t.Fatalf("EncodeFrame: %v", err)
		}
		if _, err := e.EncodeFrame(nil, nil); err != nil {
			t.Fatalf("drain EncodeFrame: %v", err)
		}
		if _, err := e.EncodeFrame(nil, full); !errors.Is(err, mp3.ErrEncoderFinalized) {
			t.Fatalf("EncodeFrame after drain: err = %v, want ErrEncoderFinalized", err)
		}
	})

	t.Run("wrong channel count", func(t *testing.T) {
		e := newTestEncoder(t)
		seed := uint64(22)
		one := planarNoise(&seed, 1, mp3.FrameSize, 0.5)
		if _, err := e.EncodeFrame(nil, one); err == nil {
			t.Fatalf("EncodeFrame with 1 channel against a 2-channel config: want error, got nil")
		}
	})

	t.Run("empty non-nil frame", func(t *testing.T) {
		e := newTestEncoder(t)
		empty := [][]float32{{}, {}}
		if _, err := e.EncodeFrame(nil, empty); err == nil {
			t.Fatalf("EncodeFrame with empty channels: want error, got nil")
		}
	})

	t.Run("unequal channel lengths", func(t *testing.T) {
		e := newTestEncoder(t)
		uneven := [][]float32{make([]float32, 500), make([]float32, 600)}
		if _, err := e.EncodeFrame(nil, uneven); err == nil {
			t.Fatalf("EncodeFrame with unequal channel lengths: want error, got nil")
		}
	})

	t.Run("oversized frame", func(t *testing.T) {
		e := newTestEncoder(t)
		big := [][]float32{make([]float32, mp3.FrameSize+1), make([]float32, mp3.FrameSize+1)}
		if _, err := e.EncodeFrame(nil, big); err == nil {
			t.Fatalf("EncodeFrame with %d samples/channel: want error, got nil", mp3.FrameSize+1)
		}
	})
}

// TestEncoderInvalidAudio requires a NaN or Inf anywhere in a non-nil
// frame to return the public ErrInvalidAudio, translated from the
// internal enc.ErrInvalidAudio, append nothing new to dst, and poison the
// encoder until Reset: every later call, including a nil drain call, also
// returns ErrInvalidAudio.
func TestEncoderInvalidAudio(t *testing.T) {
	cases := []struct {
		name string
		bad  float32
	}{
		{"NaN", float32(math.NaN())},
		{"PositiveInf", float32(math.Inf(1))},
		{"NegativeInf", float32(math.Inf(-1))},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := mp3.EncoderConfig{SampleRate: 44100, Channels: 2, Bitrate: 128000}
			e, err := mp3.NewEncoder(cfg)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}

			seed := uint64(55)
			samples := planarNoise(&seed, 2, mp3.FrameSize, 0.5)
			samples[0][300] = c.bad

			dst, err := e.EncodeFrame(nil, samples)
			if !errors.Is(err, mp3.ErrInvalidAudio) {
				t.Fatalf("EncodeFrame with %s: err = %v, want ErrInvalidAudio", c.name, err)
			}
			if len(dst) != 0 {
				t.Fatalf("EncodeFrame with %s: appended %d bytes, want 0", c.name, len(dst))
			}

			clean := planarNoise(&seed, 2, mp3.FrameSize, 0.5)
			if _, err := e.EncodeFrame(nil, clean); !errors.Is(err, mp3.ErrInvalidAudio) {
				t.Fatalf("EncodeFrame after %s poison: err = %v, want ErrInvalidAudio", c.name, err)
			}
			if _, err := e.EncodeFrame(nil, nil); !errors.Is(err, mp3.ErrInvalidAudio) {
				t.Fatalf("nil drain after %s poison: err = %v, want ErrInvalidAudio", c.name, err)
			}

			if err := e.Reset(cfg); err != nil {
				t.Fatalf("Reset: %v", err)
			}
			if _, err := e.EncodeFrame(nil, clean); err != nil {
				t.Fatalf("EncodeFrame after Reset (%s): %v", c.name, err)
			}
		})
	}
}

// TestEncoderShortFrameInvalidAudio requires a NaN/Inf inside a short
// final frame to poison the encoder rather than finalize it: EncodeFrame
// must not latch shortFrame on a failed encode, or a subsequent call
// would wrongly observe ErrEncoderFinalized instead of the poison's
// ErrInvalidAudio. Regression test for the precedence bug where
// shortFrame was set before the delegating call's result was known.
func TestEncoderShortFrameInvalidAudio(t *testing.T) {
	cfg := mp3.EncoderConfig{SampleRate: 44100, Channels: 2, Bitrate: 128000}
	e, err := mp3.NewEncoder(cfg)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}

	seed := uint64(66)
	short := planarNoise(&seed, 2, 799, 0.5)
	short[0][400] = float32(math.NaN()) // interior sample, not the first or last

	dst, err := e.EncodeFrame(nil, short)
	if !errors.Is(err, mp3.ErrInvalidAudio) {
		t.Fatalf("EncodeFrame with NaN in short final frame: err = %v, want ErrInvalidAudio", err)
	}
	if len(dst) != 0 {
		t.Fatalf("EncodeFrame with NaN in short final frame: appended %d bytes, want 0", len(dst))
	}

	// (b) The failed short frame must not have latched shortFrame: a
	// subsequent valid full frame must still observe the poison
	// (ErrInvalidAudio), not ErrEncoderFinalized. This is the assertion
	// that fails without the shortFrame-latches-only-on-success fix.
	full := planarNoise(&seed, 2, mp3.FrameSize, 0.5)
	if _, err := e.EncodeFrame(nil, full); !errors.Is(err, mp3.ErrInvalidAudio) {
		t.Fatalf("EncodeFrame with a valid full frame after a poisoned short frame: err = %v, want ErrInvalidAudio", err)
	}

	// (c) A nil drain also observes the poison.
	if _, err := e.EncodeFrame(nil, nil); !errors.Is(err, mp3.ErrInvalidAudio) {
		t.Fatalf("nil drain after a poisoned short frame: err = %v, want ErrInvalidAudio", err)
	}

	// (d) Reset clears the poison and a fresh encode succeeds.
	if err := e.Reset(cfg); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if _, err := e.EncodeFrame(nil, full); err != nil {
		t.Fatalf("EncodeFrame after Reset: %v", err)
	}
}

// TestEncoderDelaySplit pins TotalDelay and EncoderDelay against a
// ChainDelay change: TotalDelay must equal 1057, and EncoderDelay must
// equal TotalDelay - 529 (the standard decoder's own contribution). It also
// requires the Delay() and TotalDelay() methods (issue #31b's method
// mirrors of the constants, so a caller holding an *Encoder need not import
// the constants directly) to agree with those constants exactly, on both a
// zero-value Encoder and a freshly constructed one, so the mirrors can
// never silently drift from what they mirror.
func TestEncoderDelaySplit(t *testing.T) {
	if mp3.TotalDelay != 1057 {
		t.Fatalf("TotalDelay = %d, want 1057", mp3.TotalDelay)
	}
	if mp3.EncoderDelay != mp3.TotalDelay-529 {
		t.Fatalf("EncoderDelay = %d, want TotalDelay-529 = %d", mp3.EncoderDelay, mp3.TotalDelay-529)
	}

	var zero mp3.Encoder
	if got := zero.Delay(); got != mp3.EncoderDelay {
		t.Fatalf("zero-value Encoder.Delay() = %d, want EncoderDelay = %d", got, mp3.EncoderDelay)
	}
	if got := zero.TotalDelay(); got != mp3.TotalDelay {
		t.Fatalf("zero-value Encoder.TotalDelay() = %d, want TotalDelay = %d", got, mp3.TotalDelay)
	}

	e, err := mp3.NewEncoder(mp3.EncoderConfig{SampleRate: 44100, Channels: 2, Bitrate: 128000})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if got := e.Delay(); got != mp3.EncoderDelay {
		t.Fatalf("Encoder.Delay() = %d, want EncoderDelay = %d", got, mp3.EncoderDelay)
	}
	if got := e.TotalDelay(); got != mp3.TotalDelay {
		t.Fatalf("Encoder.TotalDelay() = %d, want TotalDelay = %d", got, mp3.TotalDelay)
	}
	if e.Delay()+standardDecoderDelayForTest != e.TotalDelay() {
		t.Fatalf("Delay() + standard decoder delay (%d) = %d, want TotalDelay() = %d", standardDecoderDelayForTest, e.Delay()+standardDecoderDelayForTest, e.TotalDelay())
	}
}

// standardDecoderDelayForTest mirrors the unexported standardDecoderDelay
// constant so this external test package can check Delay()/TotalDelay()
// consistency without exporting an internal implementation detail.
const standardDecoderDelayForTest = 529

// TestEncoderPcmDecoderRoundTrip encodes a 2-second 44.1kHz/128kbps/stereo
// program with the public API, feeds the resulting stream to
// pcm.NewDecoder, and checks that it decodes cleanly: the decoded sample
// count matches Stats().Frames*FrameSize exactly (both channels
// interleaved S16), and the decoded audio's RMS is within 10% of the
// input RMS. The pcm layer applies no gapless trim to this tagless
// stream, and the ChainDelay leading samples are part of the decoded
// output, so this compares energy, not sample-for-sample alignment.
func TestEncoderPcmDecoderRoundTrip(t *testing.T) {
	const (
		sampleRate = 44100
		channels   = 2
		bitrate    = 128000
		freqHz     = 440
		amp        = float32(0.5)
		durationS  = 2
	)

	e, err := mp3.NewEncoder(mp3.EncoderConfig{SampleRate: sampleRate, Channels: channels, Bitrate: bitrate})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}

	totalSamples := sampleRate * durationS
	var stream []byte
	var sumSquares float64
	var inputCount int

	phase := 0.0
	for pos := 0; pos < totalSamples; {
		n := mp3.FrameSize
		if pos+n > totalSamples {
			n = totalSamples - pos
		}
		frame := planarSine(channels, n, sampleRate, freqHz, &phase, amp)
		for ch := range channels {
			for _, s := range frame[ch] {
				sumSquares += float64(s) * float64(s)
				inputCount++
			}
		}
		stream, err = e.EncodeFrame(stream, frame)
		if err != nil {
			t.Fatalf("EncodeFrame at pos %d: %v", pos, err)
		}
		pos += n
	}
	stream, err = e.EncodeFrame(stream, nil) // drain
	if err != nil {
		t.Fatalf("drain EncodeFrame: %v", err)
	}

	stats := e.Stats()
	wantSamplesPerChannel := stats.Frames * mp3.FrameSize
	wantBytes := stats.Frames * mp3.FrameSize * channels * 2 // S16: 2 bytes/sample

	d, err := mp3pcm.NewDecoder(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("pcm.NewDecoder: %v", err)
	}
	pcmData, err := io.ReadAll(d)
	if err != nil {
		t.Fatalf("io.ReadAll: %v", err)
	}
	if int64(len(pcmData)) != wantBytes {
		t.Fatalf("decoded byte length = %d, want %d", len(pcmData), wantBytes)
	}
	gotSamplesPerChannel := int64(len(pcmData)) / 2 / channels
	if gotSamplesPerChannel != wantSamplesPerChannel {
		t.Fatalf("decoded samples/channel = %d, want %d", gotSamplesPerChannel, wantSamplesPerChannel)
	}

	inputRMS := math.Sqrt(sumSquares / float64(inputCount))

	var outSumSquares float64
	nOutSamples := len(pcmData) / 2
	for i := range nOutSamples {
		s := int16(binary.LittleEndian.Uint16(pcmData[i*2 : i*2+2]))
		v := float64(s) / 32768.0
		outSumSquares += v * v
	}
	outputRMS := math.Sqrt(outSumSquares / float64(nOutSamples))

	if outputRMS < inputRMS*0.9 || outputRMS > inputRMS*1.1 {
		t.Fatalf("output RMS = %v, input RMS = %v, want within 10%%", outputRMS, inputRMS)
	}
}
