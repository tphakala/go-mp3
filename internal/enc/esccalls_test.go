package enc

import (
	"os"
	"testing"
)

// escSurveyEnv gates TestEscalationCallSurvey. The survey encodes 30 frames
// in each of seven escalation-heavy configurations (~575s total), which is
// exactly the kind of runtime issue #37 is trying to shrink, so it must not
// run in the default suite or CI. Set MP3_ESC_SURVEY=1 (with an extended
// -timeout) to run it on demand when re-sizing the cap, mirroring
// MP3_MASKING_FULLGRID.
const escSurveyEnv = "MP3_ESC_SURVEY"

// TestEscalationCallSurvey measures the real per-frame escTryBudget call
// count (escState.calls: outerLoop invocations plus memo hits) across the
// encoder's escalation-heavy configurations, now that the per-frame memo
// (PR #40) collapses redundant calls. Its output (run with -v) is the
// sizing evidence for maskEscalationMaxCalls (issue #37): the cap is a pure
// cost ceiling, so it should sit above every real workload's demand with
// headroom, but not at many times it. Env-gated (see escSurveyEnv) because
// it is slow; it is a diagnostic and re-sizing tool, not a per-run guard.
//
// White-box: reads e.esc.calls directly after each EncodeFrame call. The
// held-frame contract makes that exact for this loop shape: call 1 stashes
// and codes nothing, and every later non-drain call codes exactly one
// frame, whose escalateForMasking reset calls to 0 and left the final
// count behind. No drain is issued (a drain call codes two frames and
// would hide the first one's count).
func TestEscalationCallSurvey(t *testing.T) {
	if os.Getenv(escSurveyEnv) != "1" {
		t.Skip("skipping escalation call-count survey; set " + escSurveyEnv + "=1 (with an extended -timeout) to run it")
	}
	const frames = 30
	cases := []struct {
		name      string
		cfg       Config
		transient bool
	}{
		{"stereo-32k-32000-broadband", Config{SampleRate: 32000, Channels: 2, BitrateKbps: 32}, false},
		{"stereo-32k-44100-broadband", Config{SampleRate: 44100, Channels: 2, BitrateKbps: 32}, false},
		{"stereo-32k-48000-broadband", Config{SampleRate: 48000, Channels: 2, BitrateKbps: 32}, false},
		{"stereo-64k-44100-broadband", Config{SampleRate: 44100, Channels: 2, BitrateKbps: 64}, false},
		{"stereo-128k-44100-broadband", Config{SampleRate: 44100, Channels: 2, BitrateKbps: 128}, false},
		{"stereo-128k-44100-transient", Config{SampleRate: 44100, Channels: 2, BitrateKbps: 128}, true},
		{"mono-320k-44100-broadband", Config{SampleRate: 44100, Channels: 1, BitrateKbps: 320}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, err := New(tc.cfg)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			seed := uint64(1)
			dst := make([]byte, 0, 4096)
			maxCalls := 0
			for n := range frames {
				var samples [][]float32
				if tc.transient {
					samples = transientSamples(&seed, tc.cfg.Channels, 0.8)
				} else {
					samples = planarSamples(&seed, tc.cfg.Channels, 0.7)
				}
				if dst, err = e.EncodeFrame(dst[:0], samples); err != nil {
					t.Fatalf("EncodeFrame %d: %v", n, err)
				}
				if n >= 1 && e.esc.calls > maxCalls {
					maxCalls = e.esc.calls
				}
			}
			t.Logf("max escTryBudget calls per frame: %d (cap %d)", maxCalls, maskEscalationMaxCalls)
			if maxCalls >= maskEscalationMaxCalls {
				t.Logf("NOTE: cap-pinned; lowering the cap would change this workload's output")
			}
		})
	}
}
