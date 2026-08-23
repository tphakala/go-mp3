// FuzzEncoderAPI is the CF-B enforcement gate for the public mp3.Encoder:
// arbitrary, non-pre-legalized config ints plus an arbitrary opcode
// sequence must never panic, whatever combination of frame lengths
// (0, 1, 1151, 1152, oversized), nil drains, audio-after-drain,
// audio-after-short, wrong channel counts, or unequal channel lengths the
// opcodes produce. This is exactly the set of internal enc.Encoder panic
// contracts (see encoder.go's Encoder type doc comment) the public Encoder
// promises to shield behind ordinary errors.
//
// The structural fuzz target FuzzEncodeValidate (internal/dec/encx_fuzz_test.go)
// drives the internal encoder directly and honors its panic contracts by
// construction, since package dec cannot import this root package (an
// import cycle). This target is the complementary proof: it deliberately
// violates those contracts through the public API and requires errors, not
// panics. It also carries the public-decoder leg displaced from that
// target: after the fuzzed opcode chaos, a fresh, contract-clean sequence
// (full frames then a drain) must produce bytes that decode with zero
// errors through the public mp3.Decoder's documented frame loop.

package mp3_test

import (
	"math"
	"testing"

	mp3 "github.com/tphakala/go-mp3"
)

// The fuzzOp* constants enumerate the call shapes FuzzEncoderAPI's opcode
// byte stream can select, one call per opcode; fuzzOpCount is the modulus
// fuzzDriveEncoderOpcodes reduces each opcode byte by.
const (
	fuzzOpNilDrain       = iota // nil samples: drain
	fuzzOpFullFrame             // len == FrameSize, right channel count
	fuzzOpEmptyFrame            // len == 0
	fuzzOpOneSample             // len == 1
	fuzzOpShortFrame            // len == FrameSize-1 (1151)
	fuzzOpWrongChannels         // right length, wrong channel count
	fuzzOpUnequalLengths        // channels disagree on length
	fuzzOpOversizedFrame        // len == FrameSize+1
	fuzzOpReset                 // Reset back to the same cfg mid-sequence
	fuzzOpCount
)

// FuzzEncoderAPI fuzzes raw EncoderConfig ints (not pre-legalized) plus an
// opcode byte sequence. See this file's header comment for the full
// contract.
func FuzzEncoderAPI(f *testing.F) {
	for _, seed := range fuzzEncoderAPISeeds() {
		f.Add(seed.sampleRate, seed.channels, seed.bitrate, seed.quality, seed.ops)
	}

	f.Fuzz(func(t *testing.T, sampleRate, channels, bitrate, quality int, ops []byte) {
		cfg := mp3.EncoderConfig{SampleRate: sampleRate, Channels: channels, Bitrate: bitrate, Quality: quality}

		e, err := mp3.NewEncoder(cfg)
		if err != nil {
			if e != nil {
				t.Fatalf("NewEncoder(%+v): non-nil Encoder returned alongside error %v", cfg, err)
			}
			return // invalid config errored cleanly from NewEncoder; nothing further to fuzz
		}

		fuzzDriveEncoderOpcodes(t, e, cfg, ops)
		fuzzAssertCleanSequenceDecodes(t, e, cfg)
	})
}

// fuzzEncoderAPISeed is one FuzzEncoderAPI seed corpus entry.
type fuzzEncoderAPISeed struct {
	sampleRate, channels, bitrate, quality int
	ops                                    []byte
}

// fuzzEncoderAPISeeds returns one seed per EncoderConfig validation-error
// class (bad rate, bad channels, bad bitrate including the 128500 %1000
// trap, nonzero Quality, Bitrate+Quality both set), plus opcode sequences
// covering a clean encode-and-drain, audio-after-drain, audio-after-short,
// and an empty non-nil frame.
func fuzzEncoderAPISeeds() []fuzzEncoderAPISeed {
	valid := fuzzEncoderAPISeed{sampleRate: 44100, channels: 2, bitrate: 128000, quality: 0}
	return []fuzzEncoderAPISeed{
		{sampleRate: 22050, channels: 2, bitrate: 128000},             // bad sample rate
		{sampleRate: 44100, channels: 3, bitrate: 128000},             // bad channel count
		{sampleRate: 44100, channels: 2, bitrate: 128500},             // 128500: the %1000 trap
		{sampleRate: 44100, channels: 2, bitrate: 129000},             // multiple of 1000, still illegal
		{sampleRate: 44100, channels: 2, bitrate: 0, quality: 5},      // Quality unsupported (CBR-only)
		{sampleRate: 44100, channels: 2, bitrate: 128000, quality: 1}, // Bitrate and Quality both set
		{valid.sampleRate, valid.channels, valid.bitrate, valid.quality, // clean encode-and-drain
			[]byte{fuzzOpFullFrame, fuzzOpFullFrame, fuzzOpNilDrain}},
		{valid.sampleRate, valid.channels, valid.bitrate, valid.quality, // audio after drain
			[]byte{fuzzOpFullFrame, fuzzOpNilDrain, fuzzOpFullFrame}},
		{valid.sampleRate, valid.channels, valid.bitrate, valid.quality, // audio after a short final frame
			[]byte{fuzzOpShortFrame, fuzzOpFullFrame}},
		{valid.sampleRate, valid.channels, valid.bitrate, valid.quality, // empty non-nil frame
			[]byte{fuzzOpEmptyFrame}},
	}
}

// fuzzDriveEncoderOpcodes replays ops against e, one EncodeFrame or Reset
// call per opcode byte. Every error is intentionally discarded (most
// opcodes are chosen to trigger one): the only requirement exercised here
// is that nothing panics, whatever order or repetition the fuzzer finds.
func fuzzDriveEncoderOpcodes(t *testing.T, e *mp3.Encoder, cfg mp3.EncoderConfig, ops []byte) {
	t.Helper()

	var stream []byte
	for i, op := range ops {
		switch int(op) % fuzzOpCount {
		case fuzzOpNilDrain:
			stream, _ = e.EncodeFrame(stream, nil)
		case fuzzOpFullFrame:
			stream, _ = e.EncodeFrame(stream, fuzzMakeChannels(cfg.Channels, mp3.FrameSize, op))
		case fuzzOpEmptyFrame:
			stream, _ = e.EncodeFrame(stream, fuzzMakeChannels(cfg.Channels, 0, op))
		case fuzzOpOneSample:
			stream, _ = e.EncodeFrame(stream, fuzzMakeChannels(cfg.Channels, 1, op))
		case fuzzOpShortFrame:
			stream, _ = e.EncodeFrame(stream, fuzzMakeChannels(cfg.Channels, mp3.FrameSize-1, op))
		case fuzzOpWrongChannels:
			stream, _ = e.EncodeFrame(stream, fuzzMakeChannels(fuzzWrongChannelCount(cfg.Channels, i), mp3.FrameSize, op))
		case fuzzOpUnequalLengths:
			stream, _ = e.EncodeFrame(stream, fuzzMakeUnequalChannels(cfg.Channels, op))
		case fuzzOpOversizedFrame:
			stream, _ = e.EncodeFrame(stream, fuzzMakeChannels(cfg.Channels, mp3.FrameSize+1, op))
		case fuzzOpReset:
			if err := e.Reset(cfg); err != nil {
				t.Fatalf("Reset(%+v) with the config NewEncoder already accepted: %v", cfg, err)
			}
			stream = nil
		}
	}
}

// fuzzAssertCleanSequenceDecodes resets e to a known-clean state and drives
// a contract-clean sequence (full frames, then a drain): the emitted stream
// must decode with zero errors through the public mp3.Decoder's documented
// frame loop (decodeAll, decoder_test.go), regardless of whatever opcode
// chaos e was just put through.
func fuzzAssertCleanSequenceDecodes(t *testing.T, e *mp3.Encoder, cfg mp3.EncoderConfig) {
	t.Helper()

	if err := e.Reset(cfg); err != nil {
		t.Fatalf("Reset(%+v) with the config NewEncoder already accepted: %v", cfg, err)
	}

	var stream []byte
	for f := range 2 {
		samples := fuzzMakeChannels(cfg.Channels, mp3.FrameSize, byte(f))
		var err error
		stream, err = e.EncodeFrame(stream, samples)
		if err != nil {
			t.Fatalf("clean-sequence EncodeFrame: %v", err)
		}
	}
	stream, err := e.EncodeFrame(stream, nil)
	if err != nil {
		t.Fatalf("clean-sequence drain EncodeFrame: %v", err)
	}

	d := mp3.NewDecoder()
	decodeAll(t, d, stream) // fails the test via t.Fatalf on any decode error
}

// fuzzWrongChannelCount returns a channel count guaranteed to differ from
// nch (never negative): nch+1, or nch-1 on alternating calls when nch > 1.
func fuzzWrongChannelCount(nch, i int) int {
	if nch > 1 && i%2 == 0 {
		return nch - 1
	}
	return nch + 1
}

// fuzzMakeChannels returns nch channels of n deterministic samples each,
// seeded by salt so different opcodes vary the content. nch <= 0 yields no
// channels at all (still a valid, non-nil [][]float32).
func fuzzMakeChannels(nch, n int, salt byte) [][]float32 {
	if nch < 0 {
		nch = 0
	}
	out := make([][]float32, nch)
	for c := range out {
		out[c] = make([]float32, n)
		for i := range out[c] {
			v := math.Sin(float64(i)*0.01 + float64(c) + float64(salt))
			out[c][i] = float32(v * 0.5)
		}
	}
	return out
}

// fuzzMakeUnequalChannels returns nch channels whose lengths deliberately
// disagree (even channels one sample shorter), when nch >= 2; for nch < 2
// "unequal" is meaningless, so it falls back to a normal full frame.
func fuzzMakeUnequalChannels(nch int, salt byte) [][]float32 {
	if nch < 2 {
		return fuzzMakeChannels(nch, mp3.FrameSize, salt)
	}
	out := make([][]float32, nch)
	for c := range out {
		n := mp3.FrameSize
		if c%2 == 0 {
			n = mp3.FrameSize - 1 - int(salt)%10
		}
		out[c] = make([]float32, n)
	}
	return out
}
