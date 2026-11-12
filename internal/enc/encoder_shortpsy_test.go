package enc

import (
	"bytes"
	"math"
	"testing"
)

// poisonShortPsy overwrites every representation's short-path outputs with
// NaN, so that after the next coded frame a still-NaN output is one the frame
// did not compute and any read of it reaches the stream.
func poisonShortPsy(e *Encoder) {
	nan := math.NaN()
	for rep := range e.psyOuts {
		for g := range e.psyOuts[rep] {
			out := &e.psyOuts[rep][g]
			for b := range out.XminS {
				out.XminS[b], out.EnS[b] = nan, nan
			}
			out.PES = nan
		}
	}
}

// shortPsyOutputs is the number of values in a PsyOut's short-path outputs:
// XminS, EnS and PES.
const shortPsyOutputs = 2*len(PsyOut{}.XminS) + 1

// shortPsyNaNs counts the NaN values among out's short-path outputs: 0 when
// the short analysis filled them all, shortPsyOutputs when it left them all
// untouched after poisonShortPsy.
func shortPsyNaNs(out *PsyOut) int {
	n := 0
	for b := range out.XminS {
		if math.IsNaN(out.XminS[b]) {
			n++
		}
		if math.IsNaN(out.EnS[b]) {
			n++
		}
	}
	if math.IsNaN(out.PES) {
		n++
	}
	return n
}

// TestShortPsyAnalysisFollowsBlockType pins both directions of codeFrame's
// short-analysis gate on a stereo program with mismatched channel block types
// and short granules in M/S frames: a representation's short-path outputs are
// computed exactly when its reference channel (channel 1 for repR, channel 0
// for repL, repM and repS) is coded short in that granule, and the stream never
// depends on an output the frame skipped (poisoning them all with NaN before
// every frame leaves the bytes unchanged).
func TestShortPsyAnalysisFollowsBlockType(t *testing.T) {
	cfg := Config{SampleRate: 48000, Channels: 2, BitrateKbps: 32}
	prog := shortShapesProgram()

	encode := func(poison bool) []byte {
		e := mustEncoder(t, cfg)
		var stream []byte
		var filled, skipped int
		var shape shortPathShape
		for f, fr := range prog {
			if poison {
				poisonShortPsy(e)
			}
			coded := e.frames
			var err error
			stream, err = e.EncodeFrame(stream, fr)
			if err != nil {
				t.Fatalf("frame %d: EncodeFrame: %v", f, err)
			}
			if !poison || e.frames != coded+1 {
				continue
			}
			shape.observe(e)
			for g := range 2 {
				for rep := range e.psyOuts {
					// The reference-channel mapping is restated here, not
					// taken from repShort, so a wrong repShort cannot hide.
					refCh := 0
					if rep == repR {
						refCh = 1
					}
					short := e.bt[g][refCh] == blockShort
					nans := shortPsyNaNs(&e.psyOuts[rep][g])
					switch {
					case short && nans == 0:
						filled++
					case !short && nans == shortPsyOutputs:
						skipped++
					default:
						t.Fatalf("frame %d granule %d rep %d: reference channel block type %d, %d of %d short outputs left untouched",
							f-1, g, rep, e.bt[g][refCh], nans, shortPsyOutputs)
					}
				}
			}
		}
		if poison {
			// The frames the drain codes are not inspected above; poisoning
			// before it still sends a read of a still-poisoned output into
			// the stream.
			poisonShortPsy(e)
		}
		stream, err := e.EncodeFrame(stream, nil) // drain
		if err != nil {
			t.Fatalf("drain: EncodeFrame: %v", err)
		}
		if poison && (filled == 0 || skipped == 0 || shape.mixed == 0 || shape.msShort == 0) {
			t.Fatalf("program no longer exercises the gate: filled=%d skipped=%d mismatched granules=%d M/S frames with a short granule=%d",
				filled, skipped, shape.mixed, shape.msShort)
		}
		return stream
	}

	if clean, poisoned := encode(false), encode(true); !bytes.Equal(clean, poisoned) {
		t.Fatalf("poisoning the skipped short-path outputs changed the stream: a skipped output was read")
	}
}
