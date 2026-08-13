// Package dec_test holds the full-chain round-trip gate for Task 7's
// internal/enc.Encoder: encode with the PURE-GO encoder, decode with the
// PUBLIC mp3.Decoder, and require the round trip reproduces the input at a
// bitrate-appropriate SNR floor. It lives in internal/dec/ (not a new
// top-level test package) so it can reuse this directory's grid
// conventions and fixtures, but it must be package dec_test (the external
// test package): a package dec test file cannot import the root mp3
// package, since mp3 itself imports internal/dec and an in-package test
// import would form a cycle Go rejects. dec_test, being external, may
// import both mp3 and internal/enc freely. This file is therefore the ONE
// place the public mp3.Decoder loop is exercised end to end against this
// project's own encoder.
package dec_test

import (
	"fmt"
	"math"
	"testing"

	mp3 "github.com/tphakala/go-mp3"
	"github.com/tphakala/go-mp3/internal/enc"
	"github.com/tphakala/go-mp3/internal/testsignal"
)

// roundTripSideInfoBytes returns the byte length of one frame's side-info
// block for nch channels (17 mono, 32 stereo), independently derived here
// (not calling into internal/enc) from the same field widths
// internal/enc.writeSideInfo packs: main_data_begin(9) + private_bits(5
// mono/3 stereo) + scfsi(4/channel) + 2 granules * nch channels * 59
// bits/granule-channel = 136 bits (mono) / 256 bits (stereo), both exact
// byte multiples.
func roundTripSideInfoBytes(nch int) int {
	if nch == 2 {
		return 32
	}
	return 17
}

// buildMultiTone returns nSamples samples of a deterministic multi-tone
// program: a 440 Hz fundamental plus overtones at -6 dB (880 Hz) and -12 dB
// (1320 Hz), scaled so |x[i]| <= peak for every i regardless of phase
// alignment. chPhase offsets the signal's phase so a second, decorrelated
// channel can be built by calling this with a different chPhase, rather
// than duplicating the same samples across channels. Delegates to
// testsignal.MultiTone (peak 0.9), the shared generator this project's test
// suites now reuse rather than each carrying its own copy.
func buildMultiTone(sampleRate, nSamples int, chPhase float64) []float64 {
	return testsignal.MultiTone(sampleRate, nSamples, chPhase, 0.9)
}

// framesForOneSecond returns the smallest number of 1152-sample MP3 frames
// covering at least one second of audio at sampleRate.
func framesForOneSecond(sampleRate int) int {
	return testsignal.FramesForOneSecond(sampleRate)
}

// encodeMultiTone builds nch channels of buildMultiTone (phase-offset per
// channel, so stereo gets a decorrelated right channel, not the left
// channel duplicated), encodes framesForOneSecond(sampleRate) frames
// through a fresh enc.Encoder plus one drain frame, and returns the
// encoded stream alongside the exact per-channel float64 input fed to the
// encoder (pre-quantization to float32, so the caller compares against
// what the encoder actually saw).
func encodeMultiTone(t *testing.T, sampleRate, nch, kbps int) (stream []byte, input [][]float64, nFrames int) {
	t.Helper()

	e, err := enc.New(enc.Config{SampleRate: sampleRate, Channels: nch, BitrateKbps: kbps})
	if err != nil {
		t.Fatalf("enc.New: %v", err)
	}

	nFrames = framesForOneSecond(sampleRate)
	totalSamples := nFrames * 1152

	input = make([][]float64, nch)
	for ch := range nch {
		phase := 0.0
		if ch == 1 {
			phase = 0.37 // arbitrary offset: decorrelates the right channel
		}
		input[ch] = buildMultiTone(sampleRate, totalSamples, phase)
	}

	for f := range nFrames {
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
	stream, err = e.EncodeFrame(stream, nil) // drain: flush filterbank + MDCT history
	if err != nil {
		t.Fatalf("drain: EncodeFrame: %v", err)
	}
	return stream, input, nFrames
}

// decodeStream drives the PUBLIC mp3.Decoder's documented frame loop
// (decoder.go's DecodeFrame doc comment) over stream and returns the
// interleaved float32 PCM it produced. It requires every invariant the
// round-trip gate depends on: zero decode errors, an exact-fit final frame
// consumed with no sentinel header, every frame's header fields matching
// the encoder's own configuration, and a decoded sample count of exactly
// wantFrames*1152 per channel.
func decodeStream(t *testing.T, stream []byte, wantSampleRate, wantKbps, wantNch, wantFrames int) []float32 {
	t.Helper()

	d := mp3.NewDecoder()
	pcm := make([]float32, 1152*2)
	pos := 0
	frames := 0
	decoded := make([]float32, 0, wantFrames*1152*wantNch)
	for pos < len(stream) {
		n, info, err := d.DecodeFrame(stream[pos:], pcm)
		if err != nil {
			t.Fatalf("frame %d at byte %d: DecodeFrame error: %v", frames, pos, err)
		}
		if info.FrameBytes == 0 {
			break // empty data: stream end
		}
		if n != 1152 {
			t.Fatalf("frame %d: n = %d, want 1152", frames, n)
		}
		if info.FrameOffset != 0 {
			t.Fatalf("frame %d: FrameOffset = %d, want 0", frames, info.FrameOffset)
		}
		if info.Layer != 3 {
			t.Fatalf("frame %d: Layer = %d, want 3", frames, info.Layer)
		}
		if info.SampleRate != wantSampleRate || info.Channels != wantNch || info.Bitrate != wantKbps {
			t.Fatalf("frame %d: SampleRate/Channels/Bitrate = %d/%d/%d, want %d/%d/%d",
				frames, info.SampleRate, info.Channels, info.Bitrate, wantSampleRate, wantNch, wantKbps)
		}
		decoded = append(decoded, pcm[:n*info.Channels]...)
		pos += info.FrameBytes
		frames++
	}
	if pos != len(stream) {
		t.Fatalf("stream not fully consumed: pos = %d, len(stream) = %d", pos, len(stream))
	}
	if frames != wantFrames {
		t.Fatalf("decoded frame count = %d, want %d", frames, wantFrames)
	}
	if len(decoded) != wantFrames*1152*wantNch {
		t.Fatalf("decoded sample count = %d, want %d", len(decoded), wantFrames*1152*wantNch)
	}
	return decoded
}

// deinterleaveChannel extracts channel ch (0-based) from PCM interleaved
// with nch channels.
func deinterleaveChannel(pcm []float32, nch, ch int) []float32 {
	out := make([]float32, len(pcm)/nch)
	for i := range out {
		out[i] = pcm[i*nch+ch]
	}
	return out
}

// measureChainDelay cross-correlates y (the decoded, delayed
// reconstruction) against x (the original encoder input) over lag in
// [0, 2304) and returns the lag that maximizes their dot product,
// restricted to a steady-state sample range so startup and tail edge
// effects do not bias the search. If y[n] approx x[n-delay] (unity gain,
// no normalization: that is exactly the contract this gate proves), the
// dot product sum_i x[i]*y[i+lag] is maximized at lag == delay. 2304 = two
// frames, comfortably above the predicted ~1057 sample chain delay.
func measureChainDelay(x []float64, y []float32) int {
	const maxLag = 2304
	const margin = 4000

	bestLag := 0
	bestCorr := math.Inf(-1)
	for lag := range maxLag {
		sum := 0.0
		for i := margin; i < len(x)-margin; i++ {
			j := i + lag
			if j < 0 || j >= len(y) {
				continue
			}
			sum += x[i] * float64(y[j])
		}
		if sum > bestCorr {
			bestCorr = sum
			bestLag = lag
		}
	}
	return bestLag
}

// computeSNR returns 10*log10(signal power / noise power) in dB, comparing
// x[i] against y[i+delay] over [start, end): NO gain normalization. Unity
// gain is part of the round-trip contract (the PCMScale/mdctScale
// calibration from Tasks 2-3 already lands the chain at gain 1.0), so a
// compensating factor here would mask a calibration regression rather than
// catch one.
func computeSNR(x []float64, y []float32, delay, start, end int) float64 {
	var sigPower, noisePower float64
	n := 0
	for i := start; i < end; i++ {
		j := i + delay
		if j < 0 || j >= len(y) {
			continue
		}
		e := x[i] - float64(y[j])
		sigPower += x[i] * x[i]
		noisePower += e * e
		n++
	}
	if n == 0 || noisePower == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(sigPower/noisePower)
}

// TestEncoderChainDelay measures the encoder+decoder algorithmic delay
// once, at 44.1 kHz / 320 kbps / mono (the addendum's designated
// measurement point), and requires it to equal the frozen enc.ChainDelay
// constant, plus the < 1152 bound the one-silence-frame drain design
// depends on. A mismatch here means encoder.go's constant needs
// re-measuring and re-freezing (see enc.ChainDelay's doc comment for the
// derivation), never a change to this test's expectation.
func TestEncoderChainDelay(t *testing.T) {
	stream, input, nFrames := encodeMultiTone(t, 44100, 1, 320)
	decoded := decodeStream(t, stream, 44100, 320, 1, nFrames+1)

	delay := measureChainDelay(input[0], decoded)
	if delay != enc.ChainDelay {
		t.Fatalf("measured chain delay = %d, want frozen enc.ChainDelay = %d (re-measure and update the constant if this is a legitimate change)", delay, enc.ChainDelay)
	}
	if enc.ChainDelay >= 1152 {
		t.Fatalf("enc.ChainDelay = %d, want < 1152 (the one-silence-frame drain design requires this)", enc.ChainDelay)
	}
	t.Logf("measured chain delay = %d samples", delay)
}

// roundTripSNRFloorsDB are the do-not-regress SNR floors per bitrate,
// tightened to 3dB below the measured minimum across the whole grid (every
// sample rate, both channel modes) for that bitrate, after the brief's
// initial pre-measurement floors (12/25/35/45/55 dB at 32/64/128/192/320
// kbps) went green. See TestEncoderRoundTripSNR's doc comment for the full
// measured ranges these were tightened against. These remain do-not-regress
// backstops against constant-SMR flat quantization (Phase 3 has no
// psychoacoustic model), not quality claims.
var roundTripSNRFloorsDB = map[int]float64{
	32:  26.8, // measured min 29.89dB (48kHz stereo), max 56.06dB (32kHz mono)
	64:  51.2, // measured min 54.22dB (44.1kHz stereo), max 75.32dB (44.1kHz mono); 44.1kHz-only spot check
	128: 71.8, // measured min 74.89dB (48kHz stereo), max 78.30dB (48kHz mono)
	192: 75.2, // measured min 78.29dB, max 78.37dB (44.1kHz-only spot check)
	320: 74.2, // measured min 77.29dB (32kHz), max 78.37dB (44.1kHz stereo)
}

// TestEncoderRoundTripSNR is the full-chain round-trip gate: for every
// sample rate x channel mode x bitrate in {32,128,320}, plus {64,192}
// 44.1kHz spot checks, it encodes one second of buildMultiTone through
// enc.Encoder, drains, decodes the whole stream with the PUBLIC
// mp3.Decoder's documented loop, and requires the decoded audio matches
// the input (shifted by the single frozen enc.ChainDelay, unity gain, no
// normalization) at or above roundTripSNRFloorsDB[kbps]. Reusing the SAME
// frozen constant for every grid case, rather than re-measuring per case,
// doubles as a cross-rate/cross-mode delay-consistency check: a delay that
// were only correct at the one measurement point would misalign every
// other case's comparison and collapse its SNR well below the floor.
//
// Measured values (this comment; floors above are 3dB below the minimum
// observed per bitrate): 32kbps 29.89-56.06dB, 64kbps 54.22-75.32dB (44.1kHz
// spot check only), 128kbps 74.89-78.30dB, 192kbps 78.29-78.37dB (44.1kHz
// spot check only), 320kbps 77.29-78.37dB. Mono consistently lands at the
// high end (no inter-channel content to lose) and 32kbps stereo at the low
// end (the tightest bit budget). These SNR levels reflect a real bug fix
// this gate discovered: internal/enc/quantize.go's invStep originally used
// the textbook ISO annex C.1.5.4 constant 210, but this project's decoder
// (a faithful minimp3 port, internal/dec/scalefactors.go) actually
// dequantizes against 214 (globalGain - 4 - 210, the -4 from minimp3's own
// bitsDequantizerOut convention), a mismatch that silently halved every
// reconstructed sample's amplitude and capped SNR at a flat ~6dB regardless
// of bitrate; see quantGainBase's doc comment in quantize.go for the full
// derivation. Even after the fix, constant-SMR flat quantization (Phase 3
// has no psychoacoustic model yet) keeps these numbers well below what a
// perceptually-shaped encoder would reach; they are regression backstops
// for this phase, not quality claims.
func TestEncoderRoundTripSNR(t *testing.T) {
	type gridCase struct {
		sampleRate, kbps, nch int
	}

	rates := []int{44100, 48000, 32000}
	mainBitrates := []int{32, 128, 320}
	cases := make([]gridCase, 0, len(rates)*len(mainBitrates)*2+4)
	for _, sr := range rates {
		for _, kbps := range mainBitrates {
			for _, nch := range []int{1, 2} {
				cases = append(cases, gridCase{sr, kbps, nch})
			}
		}
	}
	for _, kbps := range []int{64, 192} { // 44.1kHz spot checks
		for _, nch := range []int{1, 2} {
			cases = append(cases, gridCase{44100, kbps, nch})
		}
	}

	for _, c := range cases {
		t.Run(fmt.Sprintf("sr%d_kbps%d_nch%d", c.sampleRate, c.kbps, c.nch), func(t *testing.T) {
			t.Parallel()
			stream, input, nFrames := encodeMultiTone(t, c.sampleRate, c.nch, c.kbps)
			decoded := decodeStream(t, stream, c.sampleRate, c.kbps, c.nch, nFrames+1)

			totalSamples := nFrames * 1152
			margin := enc.ChainDelay + 1152
			start, end := margin, totalSamples-margin

			floor, ok := roundTripSNRFloorsDB[c.kbps]
			if !ok {
				t.Fatalf("no SNR floor declared for %d kbps; add one to roundTripSNRFloorsDB", c.kbps)
			}
			for ch := range c.nch {
				y := deinterleaveChannel(decoded, c.nch, ch)
				snr := computeSNR(input[ch], y, enc.ChainDelay, start, end)
				if snr < floor {
					t.Fatalf("ch %d: SNR = %.2f dB, want >= %.2f dB", ch, snr, floor)
				}
				t.Logf("sr=%d kbps=%d nch=%d ch=%d: SNR = %.2f dB (floor %.2f)", c.sampleRate, c.kbps, c.nch, ch, snr, floor)
			}
		})
	}
}

// TestEncoderSilence requires one second of exact-zero PCM to round-trip
// to near-zero decoded output, and requires every encoded frame's
// main-data area to be all zero bytes: silence quantizes to
// minGlobalGain(all-zero xr) = 0 and partitionSpectrum's rzero scan
// consumes the whole 576-line spectrum, so part23Length is exactly 0 for
// every granule and the frame's entire post-side-info region is zero
// stuffing. Checking the raw bytes (rather than needing an exported
// part23Length accessor) is a direct, self-contained proxy for "part23
// lengths near zero".
func TestEncoderSilence(t *testing.T) {
	const sampleRate, nch, kbps = 44100, 2, 128
	nFrames := framesForOneSecond(sampleRate)

	e, err := enc.New(enc.Config{SampleRate: sampleRate, Channels: nch, BitrateKbps: kbps})
	if err != nil {
		t.Fatalf("enc.New: %v", err)
	}

	zeroFrame := make([][]float32, nch)
	for ch := range nch {
		zeroFrame[ch] = make([]float32, 1152)
	}

	sideBytes := roundTripSideInfoBytes(nch)
	var stream []byte
	for f := range nFrames {
		before := len(stream)
		stream, err = e.EncodeFrame(stream, zeroFrame)
		if err != nil {
			t.Fatalf("frame %d: EncodeFrame: %v", f, err)
		}
		mainData := stream[before+4+sideBytes:]
		for i, b := range mainData {
			if b != 0 {
				t.Fatalf("frame %d: main-data byte %d = 0x%02x, want 0x00 (part23Length should be exactly 0 for silence)", f, i, b)
			}
		}
	}
	stream, err = e.EncodeFrame(stream, nil)
	if err != nil {
		t.Fatalf("drain: EncodeFrame: %v", err)
	}

	decoded := decodeStream(t, stream, sampleRate, kbps, nch, nFrames+1)
	maxAbs := float32(0)
	for _, s := range decoded {
		a := s
		if a < 0 {
			a = -a
		}
		if a > maxAbs {
			maxAbs = a
		}
	}
	if maxAbs >= 1e-4 {
		t.Fatalf("max abs decoded sample = %v, want < 1e-4", maxAbs)
	}
	t.Logf("silence round-trip: max abs decoded sample = %v", maxAbs)
}
