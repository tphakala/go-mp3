// This file is package dec (not dec_test): it needs the decoder's own
// unexported side-info walk (grInfo, l3ReadSideInfo, hdrIsMsStereo) to
// prove the M/S side channel actually goes near-empty, so it cannot use
// the public mp3.Decoder (package dec cannot import the root mp3 package;
// mp3 itself imports internal/dec, so that import would be a cycle - see
// validateFrameDecode's doc comment in encx_frame_test.go for the same
// constraint applied to the structural grid). Every gate here therefore
// drives the internal Decoder directly (decodeStereo) rather than the
// public API's frame loop.
package dec

import (
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/bits"
	"github.com/tphakala/go-mp3/internal/enc"
	"github.com/tphakala/go-mp3/internal/testsignal"
)

// mustEncoder builds a fresh nch-channel encoder for the given sample
// rate/bitrate, failing the test on error. Package-dec-local: internal/enc's
// own mustEncoder test helper (encoder_test.go) is unexported to package enc
// and not visible here.
func mustEncoder(t *testing.T, sampleRate, nch, kbps int) *enc.Encoder {
	t.Helper()
	e, err := enc.New(enc.Config{SampleRate: sampleRate, Channels: nch, BitrateKbps: kbps})
	if err != nil {
		t.Fatalf("enc.New: %v", err)
	}
	return e
}

// encodeStereoFrames builds nFrames of stereo input (gen(ch, sampleIdx)
// supplies every sample) and encodes them through a fresh stereo encoder at
// (sampleRate, kbps), draining with the one extra EncodeFrame(nil) call the
// encoder's flush contract requires (the same pattern
// runEncoderStructuralGrid and encx_roundtrip_test.go's encodeMultiTone
// use). It returns the encoded stream plus the exact per-channel float32
// input fed to the encoder, so callers can compare decoded audio against
// exactly what the encoder saw rather than recomputing gen.
func encodeStereoFrames(t *testing.T, sampleRate, kbps, nFrames int, gen func(ch, sampleIdx int) float32) (stream []byte, left, right []float32) {
	t.Helper()
	e := mustEncoder(t, sampleRate, 2, kbps)
	left = make([]float32, nFrames*1152)
	right = make([]float32, nFrames*1152)
	for f := range nFrames {
		l := make([]float32, 1152)
		r := make([]float32, 1152)
		for i := range 1152 {
			idx := f*1152 + i
			l[i] = gen(0, idx)
			r[i] = gen(1, idx)
			left[idx] = l[i]
			right[idx] = r[i]
		}
		var err error
		stream, err = e.EncodeFrame(stream, [][]float32{l, r})
		if err != nil {
			t.Fatalf("frame %d: EncodeFrame: %v", f, err)
		}
	}
	var err error
	stream, err = e.EncodeFrame(stream, nil) // drain: flush filterbank + MDCT history
	if err != nil {
		t.Fatalf("drain: EncodeFrame: %v", err)
	}
	return stream, left, right
}

// countModes walks stream frame by frame using the decoder's own header
// accessors (hdrValid/hdrFrameBytes/hdrPadding) and classifies each frame by
// header byte 3's mode/mode_extension: M/S (mode 01, mode_extension 10)
// versus L/R (mode 00, mode_extension 00). It mirrors internal/enc's own
// countModes test helper (encoder_test.go), which this package cannot reuse
// (that helper is unexported to package enc's test binary); duplicated here
// rather than shared, the same way msPart23Sums and decodeStereo below
// duplicate rather than import across the enc/dec test boundary.
func countModes(t *testing.T, stream []byte) (ms, lr int) {
	t.Helper()
	pos := 0
	for pos < len(stream) {
		h := stream[pos:]
		if !hdrValid(h) {
			t.Fatalf("countModes: hdrValid = false at byte %d", pos)
		}
		mode := (h[3] >> 6) & 3
		modeExt := (h[3] >> 4) & 3
		switch {
		case mode == 1 && modeExt == 2:
			ms++
		case mode == 0 && modeExt == 0:
			lr++
		}
		pos += hdrFrameBytes(h, 0) + hdrPadding(h)
	}
	return ms, lr
}

// assertModes requires every frame countModes walks to agree on a single
// representation: wantAllMs true requires every frame be M/S and none L/R;
// false requires the reverse. It fits the gates whose input deterministically
// forces one representation across the whole stream
// (TestMsSqrt2Calibration, TestMsIdenticalChannels, TestMsHardPanSelectsLR).
// TestMsAntiCorrelated's input only forces M/S for the frames it applies to
// (it need not be every frame to prove the decision is legitimate), so it
// calls countModes directly instead of this all-or-nothing helper.
func assertModes(t *testing.T, stream []byte, wantAllMs bool) {
	t.Helper()
	ms, lr := countModes(t, stream)
	if ms+lr == 0 {
		t.Fatalf("assertModes: countModes classified zero frames")
	}
	if wantAllMs && lr != 0 {
		t.Fatalf("assertModes: want every frame M/S, got ms=%d lr=%d", ms, lr)
	}
	if !wantAllMs && ms != 0 {
		t.Fatalf("assertModes: want every frame L/R, got ms=%d lr=%d", ms, lr)
	}
}

// msPart23Sums walks stream frame by frame and, for every M/S frame
// (hdrIsMsStereo), sums channel 0's and channel 1's part23Length across both
// granules via l3ReadSideInfo: the same side-info walk validateGranules
// (encx_frame_test.go) already uses to parse granule-channel invariants.
// nch must be 2 (M/S only exists for stereo streams).
func msPart23Sums(t *testing.T, stream []byte, nch int) (ch0, ch1 int) {
	t.Helper()
	pos := 0
	for pos < len(stream) {
		h := stream[pos:]
		if !hdrValid(h) {
			t.Fatalf("msPart23Sums: hdrValid = false at byte %d", pos)
		}
		frameBytes := hdrFrameBytes(h, 0) + hdrPadding(h)
		frame := h[:frameBytes]
		if hdrIsMsStereo(frame[:4]) {
			rd := bits.NewReader(frame[4:])
			gr := make([]grInfo, 2*nch)
			l3ReadSideInfo(&rd, gr, frame[:4], len(frame)-4)
			for gi := range 2 {
				ch0 += int(gr[gi*nch+0].part23Length)
				ch1 += int(gr[gi*nch+1].part23Length)
			}
		}
		pos += frameBytes
	}
	return ch0, ch1
}

// decodeStereo drives the internal Decoder's documented frame loop (the same
// engine validateFrameDecode and, one layer up, the public mp3.Decoder both
// delegate to) over a 2-channel stream and deinterleaves the resulting
// float32 PCM into independent per-channel slices. This is the
// package-dec-local equivalent of the public decoder's stereo loop; see this
// file's top doc comment for why the public API is unavailable here.
func decodeStereo(t *testing.T, stream []byte) (left, right []float32) {
	t.Helper()
	d := NewDecoder()
	pcm := make([]float32, maxSamplesPerFrame)
	var fi FrameInfo
	pos := 0
	for pos < len(stream) {
		n := d.DecodeFrame(stream[pos:], pcm, &fi)
		if n != 1152 {
			t.Fatalf("decodeStereo: DecodeFrame at byte %d: n = %d, want 1152", pos, n)
		}
		if fi.Channels != 2 {
			t.Fatalf("decodeStereo: FrameInfo.Channels = %d, want 2", fi.Channels)
		}
		for i := range n {
			left = append(left, pcm[2*i])
			right = append(right, pcm[2*i+1])
		}
		pos += fi.FrameBytes
	}
	return left, right
}

// f64 converts a []float32 to []float64, computeSNR's (encx_filterbank_test.go)
// reference-signal parameter type.
func f64(x []float32) []float64 {
	out := make([]float64, len(x))
	for i, v := range x {
		out[i] = float64(v)
	}
	return out
}

// msNoiseFloorDB is the do-not-regress reconstruction SNR floor
// TestMsIdenticalChannels/TestMsAntiCorrelated/TestMsHardPanSelectsLR share,
// at 44.1kHz/320kbps: noise content (TestMsIdenticalChannels) is much harder
// to compress than the tonal content encx_roundtrip_test.go's
// roundTripSNRFloorsDB grid uses (no low-entropy structure for the
// masking-driven bit escalation to exploit), so this floor is set
// independently rather than reusing that grid's tonal numbers, and 3dB below
// the measured minimum across the three gates (measured: 16.45dB noise,
// 78.84dB tone; see each test's t.Logf output and task-3-report.md).
const msNoiseFloorDB = 13.0

// TestMsSqrt2Calibration is the silent-factor tripwire (the
// quantGainBase-214 class, see quantize.go's doc comment for that earlier
// bug): a center-panned pure tone (identical L/R channels) forces the PE
// decision to M/S every frame, and the decoded per-channel amplitude must be
// UNITY relative to the input. A systematic sqrt2 factor anywhere in the
// butterfly/quantizer/dequant interplay shows as +-3dB (or +-6dB for
// sqrt2^2) here, unmissable against the 0.5dB tolerance. Tone generation
// uses math.Cos: legal here, this is an amplitude/SNR gate over decoded
// audio, not a cross-arch golden (see PROVENANCE.md's math.Sin/Cos
// restriction, which binds golden-input generation, not general test
// signals).
func TestMsSqrt2Calibration(t *testing.T) {
	const sampleRate, kbps, n = 44100, 320, 30
	const freq = 1000.0

	stream, _, _ := encodeStereoFrames(t, sampleRate, kbps, n, func(_, i int) float32 {
		ti := float64(i) / sampleRate
		return float32(0.5 * math.Cos(2*math.Pi*freq*ti))
	})
	assertModes(t, stream, true) // every audio frame M/S

	left, right := decodeStereo(t, stream)

	// Steady-state RMS over the middle, skipping enc.ChainDelay's startup
	// transient and the drain frame's tail.
	lo, hi := 8*1152, (n-4)*1152
	inRMS := 0.5 / math.Sqrt2 // RMS of a 0.5-amplitude cosine
	for _, tc := range []struct {
		name string
		ch   []float32
	}{{"L", left}, {"R", right}} {
		var sum float64
		for i := lo; i < hi; i++ {
			sum += float64(tc.ch[i]) * float64(tc.ch[i])
		}
		rms := math.Sqrt(sum / float64(hi-lo))
		ratioDB := 20 * math.Log10(rms/inRMS)
		t.Logf("%s: decoded RMS = %.6f, ratio = %.3f dB off unity", tc.name, rms, ratioDB)
		if math.Abs(ratioDB) > 0.5 {
			t.Fatalf("%s: decoded level %.2f dB off unity (sqrt2-class factor?)", tc.name, ratioDB)
		}
	}
}

// TestMsIdenticalChannels feeds identical L==R noise (the PE decision's
// cheapest possible M/S case: the side channel is exactly zero before
// quantization) and requires: M/S selected every frame; the side channel's
// coded bits collapse to near nothing relative to the mid channel's (summed
// part23Length, via msPart23Sums); and the decoded L/R reconstruct the
// (identical) input at or above the noise SNR floor, with L and R matching
// each other far more tightly than either matches the original (since both
// come from the same decoded mid value once the side channel is
// near-silent).
func TestMsIdenticalChannels(t *testing.T) {
	const sampleRate, kbps, n = 44100, 320, 20

	seed := uint64(0xC0FFEE)
	noise := make([]float32, n*1152)
	for i := range noise {
		noise[i] = float32(testsignal.LCGSigned(&seed) * 0.3)
	}

	stream, left, _ := encodeStereoFrames(t, sampleRate, kbps, n, func(_, i int) float32 {
		return noise[i]
	})
	assertModes(t, stream, true)

	ch0Sum, ch1Sum := msPart23Sums(t, stream, 2)
	t.Logf("side-info part23 sums: ch0(mid)=%d ch1(side)=%d (%.2f%%)", ch0Sum, ch1Sum, 100*float64(ch1Sum)/float64(ch0Sum))
	if float64(ch1Sum) >= 0.10*float64(ch0Sum) {
		t.Fatalf("side channel not near-empty: ch1 part23 sum = %d, want < 10%% of ch0 = %d", ch1Sum, ch0Sum)
	}

	decL, decR := decodeStereo(t, stream)

	margin := enc.ChainDelay + 1152
	start, end := margin, len(left)-margin

	lSNR := computeSNR(f64(left), decL, enc.ChainDelay, 1, start, end)
	rSNR := computeSNR(f64(left), decR, enc.ChainDelay, 1, start, end) // left==right input
	t.Logf("reconstruction SNR: L=%.2f dB R=%.2f dB (floor %.2f)", lSNR, rSNR, msNoiseFloorDB)
	if lSNR < msNoiseFloorDB || rSNR < msNoiseFloorDB {
		t.Fatalf("reconstruction SNR below floor: L=%.2f R=%.2f, want >= %.2f dB", lSNR, rSNR, msNoiseFloorDB)
	}

	// L and R must match each other far more tightly than either matches
	// the original: both come from the same decoded mid value.
	lrSNR := computeSNR(f64(decL), decR, 0, 1, 0, len(decL))
	t.Logf("L-vs-R SNR: %.2f dB", lrSNR)
	if lrSNR < lSNR {
		t.Fatalf("L-vs-R SNR (%.2f dB) below reconstruction SNR (%.2f dB): decoded channels should agree at least as well as they match the input", lrSNR, lSNR)
	}
}

// TestMsAntiCorrelated feeds R = -L. Design decision 7 (see the phase plan)
// settles that the PE rule LEGITIMATELY selects M/S here: the mid channel is
// silent and the side channel carries everything, the efficient
// representation, not a decision bug. This gate therefore asserts what
// actually matters: M/S frames are present, decoded R reconstructs -L (the
// decoder's own sum-difference butterfly applied to a near-silent mid and a
// fully-loaded side channel), and both channels meet the noise SNR floor.
func TestMsAntiCorrelated(t *testing.T) {
	const sampleRate, kbps, n = 44100, 320, 20

	tone := testsignal.MultiTone(sampleRate, n*1152, 0, 0.7)
	stream, left, right := encodeStereoFrames(t, sampleRate, kbps, n, func(ch, i int) float32 {
		if ch == 0 {
			return float32(tone[i])
		}
		return float32(-tone[i])
	})

	ms, lr := countModes(t, stream)
	t.Logf("mode counts: ms=%d lr=%d", ms, lr)
	if ms == 0 {
		t.Fatalf("anti-correlated input never selected M/S (ms=%d, lr=%d): PE decision regression?", ms, lr)
	}

	decL, decR := decodeStereo(t, stream)
	margin := enc.ChainDelay + 1152
	start, end := margin, len(left)-margin

	lSNR := computeSNR(f64(left), decL, enc.ChainDelay, 1, start, end)
	rSNR := computeSNR(f64(right), decR, enc.ChainDelay, 1, start, end)
	t.Logf("reconstruction SNR: L=%.2f dB R=%.2f dB (floor %.2f)", lSNR, rSNR, msNoiseFloorDB)
	if lSNR < msNoiseFloorDB || rSNR < msNoiseFloorDB {
		t.Fatalf("reconstruction SNR below floor: L=%.2f R=%.2f, want >= %.2f dB", lSNR, rSNR, msNoiseFloorDB)
	}

	// decoded R must reconstruct -decoded L (both decoder outputs, already
	// delay-aligned to each other: delay 0).
	negL := make([]float32, len(decL))
	for i, v := range decL {
		negL[i] = -v
	}
	invSNR := computeSNR(f64(negL), decR, 0, 1, 0, len(negL))
	t.Logf("R-vs-(-L) SNR: %.2f dB", invSNR)
	if invSNR < rSNR {
		t.Fatalf("R-vs-(-L) SNR (%.2f dB) below R's reconstruction SNR (%.2f dB): decoded R should track -L at least as well as it tracks the original R", invSNR, rSNR)
	}
}

// TestMsHardPanSelectsLR feeds (x, 0): a hard pan. Unlike identical or
// anti-correlated channels, a hard pan makes M and S carry EQUAL energy
// (M = S = x before normalization), so M/S costs strictly more than L/R
// here (L/R's silent channel is nearly free; M/S has no silent half). The
// PE rule is therefore expected to select L/R for every frame. The silent
// channel must stay silent and the loud channel must meet the SNR floor.
func TestMsHardPanSelectsLR(t *testing.T) {
	const sampleRate, kbps, n = 44100, 320, 20

	tone := testsignal.MultiTone(sampleRate, n*1152, 0, 0.7)
	stream, left, _ := encodeStereoFrames(t, sampleRate, kbps, n, func(ch, i int) float32 {
		if ch == 0 {
			return float32(tone[i])
		}
		return 0
	})
	assertModes(t, stream, false) // L/R only, no M/S

	decL, decR := decodeStereo(t, stream)

	var maxAbsR float32
	for _, v := range decR {
		if v < 0 {
			v = -v
		}
		if v > maxAbsR {
			maxAbsR = v
		}
	}
	t.Logf("silent channel max |R| = %g", maxAbsR)
	if maxAbsR >= 1e-3 {
		t.Fatalf("silent channel R: max abs = %g, want < 1e-3", maxAbsR)
	}

	margin := enc.ChainDelay + 1152
	start, end := margin, len(left)-margin
	snr := computeSNR(f64(left), decL, enc.ChainDelay, 1, start, end)
	t.Logf("loud channel L SNR = %.2f dB (floor %.2f)", snr, msNoiseFloorDB)
	if snr < msNoiseFloorDB {
		t.Fatalf("loud channel L: SNR = %.2f dB, want >= %.2f dB", snr, msNoiseFloorDB)
	}
}

// msSeparationFloorDB is the separation gate's do-not-regress floor: the
// brief's initial 40dB value, deliberately NOT tightened to the measured
// value (see TestMsChannelSeparation's doc comment: the measurement landed
// far above 40dB). Tightening to within a few dB of a number that far above
// the floor would make the gate brittle against ordinary content-dependent
// correlation-estimate noise for no real regression-catching benefit; 40dB
// stays the safe floor.
const msSeparationFloorDB = 40.0

// TestMsChannelSeparation feeds fully decorrelated noise (x on L, y on R)
// and measures how much of x leaks into the decoded right channel,
// independent of whichever per-frame M/S/L-R decisions the PE rule makes.
//
// A silence-reference measurement (encode (x, silence) separately and read
// off its right channel's energy as "the leak") was tried first and
// rejected: decorrelated (x, y) content routinely selects L/R for a hard
// pan like (x, silence) (TestMsHardPanSelectsLR's own scenario), and L/R
// codes each channel independently, so that reference exercises no M/S
// butterfly at all and measures exactly zero leak by construction,
// regardless of what the REAL (x, y) stream's M/S frames do.
//
// Instead this decomposes the actual decoded right channel from the real
// (x, y) stream via linear regression against x and y separately: bx (the
// best-fit x coefficient) and by (the best-fit y coefficient) are each
// cov(decR, ref)/var(ref), valid to first order because x and y are
// independently generated LCG noise whose cross term averages toward zero
// over this many samples. by*by*energy(y) is decR's y-explained power; the
// analogous bx*bx*energy(x) is decR's x-explained power - whatever the
// mechanism (a direct L/R leak, or M/S quantization noise on the shared mid
// channel correlating with x), that IS the leak by definition: however much
// of decR's variance a best-fit multiple of x alone explains.
func TestMsChannelSeparation(t *testing.T) {
	const sampleRate, kbps, n = 44100, 320, 20

	seedX, seedY := uint64(0x5EED1), uint64(0x5EED2)
	x := make([]float32, n*1152)
	y := make([]float32, n*1152)
	for i := range x {
		x[i] = float32(testsignal.LCGSigned(&seedX) * 0.5)
		y[i] = float32(testsignal.LCGSigned(&seedY) * 0.5)
	}

	stream, _, _ := encodeStereoFrames(t, sampleRate, kbps, n, func(ch, i int) float32 {
		if ch == 0 {
			return x[i]
		}
		return y[i]
	})
	ms, lr := countModes(t, stream)
	t.Logf("mode counts: ms=%d lr=%d", ms, lr)

	_, decR := decodeStereo(t, stream)

	margin := enc.ChainDelay + 1152
	lo, hi := margin, len(x)-margin

	var xr, xx, yr, yy float64
	for i := lo; i < hi; i++ {
		j := i + enc.ChainDelay
		if j < 0 || j >= len(decR) {
			continue
		}
		r := float64(decR[j])
		xr += r * float64(x[i])
		xx += float64(x[i]) * float64(x[i])
		yr += r * float64(y[i])
		yy += float64(y[i]) * float64(y[i])
	}
	bx := xr / xx
	by := yr / yy
	leakEnergy := bx * bx * xx
	signalEnergy := by * by * yy

	if leakEnergy == 0 {
		t.Fatalf("measured zero x-correlated energy; measurement window likely misaligned")
	}
	separationDB := 10 * math.Log10(signalEnergy/leakEnergy)
	t.Logf("separation = %.2f dB (bx=%.6g by=%.6g signalEnergy=%.6g leakEnergy=%.6g, floor %.2f)", separationDB, bx, by, signalEnergy, leakEnergy, msSeparationFloorDB)
	if separationDB < msSeparationFloorDB {
		t.Fatalf("channel separation = %.2f dB, want >= %.2f dB", separationDB, msSeparationFloorDB)
	}
}
