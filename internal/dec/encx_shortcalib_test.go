package dec_test

import (
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/enc"
	"github.com/tphakala/go-mp3/internal/testsignal"
)

// shortCalibFrames/shortCalibClickFrame define the tone+click program
// TestShortBlockCalibrationRoundTrip drives: a steady tone throughout,
// with a loud click superimposed on ONE frame in the middle, far enough
// from both stream edges (ChainDelay, the drain) that "steady" measurement
// windows before and after it sit on genuinely long-block-coded content.
const (
	shortCalibFrames     = 16
	shortCalibClickFrame = 8
)

// buildShortCalibProgram returns one channel's [-1,1] samples: a steady
// multi-tone (testsignal.MultiTone, amplitude 0.5) interrupted at
// shortCalibClickFrame by a genuine onset (its first granule silenced, then
// a full-scale stored-LCG burst in its second granule) so block switching
// actually engages, then clamped to [-1,1] (EncodeFrame's own ingest clamp
// would do this anyway; clamping here keeps the "input" reference array
// identical to what the encoder actually saw).
func buildShortCalibProgram(sampleRate int) []float64 {
	n := shortCalibFrames * 1152
	x := testsignal.MultiTone(sampleRate, n, 0, 0.5)
	seed := uint64(0x5A17)
	base := shortCalibClickFrame * 1152
	// A genuine mid-stream onset, not noise summed onto the tone: silence the
	// click frame's first granule, then a full-scale burst in its second, so
	// the burst is a real >10x sub-block energy jump attackDetect fires on. A
	// click merely added to the steady 0.5 tone never clears attackRatio, so
	// it would not trigger block switching at all (it only did before via the
	// stream-start false attack the encoder no longer fabricates). The
	// amplitude-measurement frames (shortCalibClickFrame +-3) stay clean tone.
	for i := range 576 {
		x[base+i] = 0
	}
	for i := 576; i < 1152; i++ {
		x[base+i] = testsignal.LCGSigned(&seed) * 0.9
	}
	for i := range x {
		if x[i] > 1 {
			x[i] = 1
		} else if x[i] < -1 {
			x[i] = -1
		}
	}
	return x
}

// rms returns the root-mean-square of x[start:end].
func rms(x []float64, start, end int) float64 {
	var sum float64
	for i := start; i < end; i++ {
		sum += x[i] * x[i]
	}
	return math.Sqrt(sum / float64(end-start))
}

func rmsF32(y []float32, start, end int) float64 {
	var sum float64
	for i := start; i < end; i++ {
		v := float64(y[i])
		sum += v * v
	}
	return math.Sqrt(sum / float64(end-start))
}

// TestShortBlockCalibrationRoundTrip is design decision 14's calibration
// gate: a tone-with-click program, encoded and decoded through the real,
// INTEGRATED pipeline (block switching engaging naturally around the
// click, not forced through a pin), must round-trip its steady-tone
// amplitude within 0.5 dB of unity on both sides of the click. XminScaleShort
// being wrong (too tight or too loose) would show up here as either
// pre-echo-adjacent under-coding right around the transient bleeding into
// the neighboring long-block windows, or a gross amplitude mismatch from
// systematically wrong short-band budgeting; either would move these
// measured windows off 0 dB by much more than ordinary quantization noise
// does. This complements TestPsyXrCalibrationShort (which measures the
// XminScaleShort constant itself, bypassing the coding path entirely) and
// TestEncoderMaskingContract (which verifies the masking contract holds
// end to end): this one is the amplitude-domain, decoded-audio check
// decision 14 asks for specifically.
func TestShortBlockCalibrationRoundTrip(t *testing.T) {
	const sampleRate, kbps, nch = 44100, 128, 1

	input := buildShortCalibProgram(sampleRate)

	e, err := enc.New(enc.Config{SampleRate: sampleRate, Channels: nch, BitrateKbps: kbps})
	if err != nil {
		t.Fatalf("enc.New: %v", err)
	}

	var sawShort bool
	e.SetDiagHookPin(func(g, ch int, diag enc.DiagGranule) {
		if diag.BlockType != 0 { // blockLong == 0; any other value is short/start/stop
			sawShort = true
		}
	})

	var stream []byte
	for f := range shortCalibFrames {
		samples := make([]float32, 1152)
		for i := range 1152 {
			samples[i] = float32(input[f*1152+i])
		}
		stream, err = e.EncodeFrame(stream, [][]float32{samples})
		if err != nil {
			t.Fatalf("frame %d: EncodeFrame: %v", f, err)
		}
	}
	stream, err = e.EncodeFrame(stream, nil) // drain
	if err != nil {
		t.Fatalf("drain: EncodeFrame: %v", err)
	}
	if !sawShort {
		t.Fatal("no short/start/stop block ever chosen: test setup did not exercise block switching")
	}

	decoded := decodeStream(t, stream, sampleRate, kbps, nch, shortCalibFrames+1)

	// Two steady-tone windows, one full frame each, well clear of the
	// click frame on both sides and of the stream edges (ChainDelay and
	// the drain), shifted by enc.ChainDelay to align with the decoder's
	// output.
	measure := func(name string, frameIdx int) {
		start, end := frameIdx*1152, (frameIdx+1)*1152
		wantRMS := rms(input, start, end)
		gotRMS := rmsF32(decoded, start+enc.ChainDelay, end+enc.ChainDelay)
		if wantRMS <= 0 || gotRMS <= 0 {
			t.Fatalf("%s: rms want=%v got=%v, expected both > 0", name, wantRMS, gotRMS)
		}
		db := 20 * math.Log10(gotRMS/wantRMS)
		t.Logf("%s: input rms=%v decoded rms=%v ratio=%.3f dB", name, wantRMS, gotRMS, db)
		if math.Abs(db) > 0.5 {
			t.Errorf("%s: amplitude ratio = %.3f dB, want within 0.5 dB of unity", name, db)
		}
	}
	measure("before the click", shortCalibClickFrame-3)
	measure("after the click", shortCalibClickFrame+3)
}
