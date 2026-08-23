package enc

import (
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// clickTrainMono builds nFrames mono frames of silence with one loud burst
// (stored-LCG noise, amplitude amp) at burstFrame: the basic transient
// program these behavioral gates drive the real encoder with.
func clickTrainMono(nFrames, burstFrame int, amp float32) [][]float32 {
	seed := uint64(0xC1CC)
	out := make([][]float32, nFrames)
	for f := range nFrames {
		buf := make([]float32, 1152)
		if f == burstFrame {
			for i := range buf {
				buf[i] = float32(testsignal.LCGSigned(&seed)) * amp
			}
		}
		out[f] = buf
	}
	return out
}

// granuleLog is one recorded granule-channel from a diagHook-instrumented
// encode: nominal stream position (frame-major, granule-minor within
// frame), channel, decided block type, and whether it satisfied masking
// (over == 0).
type granuleLog struct {
	frame, g, ch int
	blockType    int
	over         int
}

// runLoggingBlockTypes drives e through samples (one EncodeFrame call per
// entry) plus a drain, recording every coded granule-channel's block type
// and masking over-count via SetDiagHookPin. frame counts stream position
// (0-based, incrementing on every g==0,ch==0 call), so it runs ahead of
// the nominal input frame index by the held-frame lookahead's usual
// off-by-one/two accounting; callers that care about that alignment
// account for it themselves. The final TWO logged frames are always the
// drain's (the real last-held frame, then the silence flush frame).
func runLoggingBlockTypes(t *testing.T, e *Encoder, samples [][]float32) []granuleLog {
	t.Helper()
	var log []granuleLog
	frame := -1
	e.SetDiagHookPin(func(g, ch int, diag DiagGranule) {
		if g == 0 && ch == 0 {
			frame++
		}
		lay := layoutFor(diag.BlockType, e.srIndex)
		_, _, over := maskingMetrics(&diag.Noise, &diag.XminXr, lay)
		log = append(log, granuleLog{frame: frame, g: g, ch: ch, blockType: diag.BlockType, over: over})
	})
	defer e.SetDiagHookPin(nil)

	var dst []byte
	var err error
	for _, s := range samples {
		dst, err = e.EncodeFrame(dst, [][]float32{s})
		if err != nil {
			t.Fatalf("EncodeFrame: %v", err)
		}
	}
	if _, err = e.EncodeFrame(dst, nil); err != nil {
		t.Fatalf("drain EncodeFrame: %v", err)
	}
	return log
}

// TestEncoderSwitchesOnTransients drives a click-train program through the
// real encoder and requires block-switching to actually engage: granules
// stream-far from the burst stay long, and at least one granule near the
// burst is not long (design decisions 9/10, wired into codeFrame in this
// task). The two channels of a correlated stereo version (identical L/R
// content) must agree on block type at every granule, and M/S must be
// retained on at least one of the near-burst frames (design decision 13:
// agreement is a precondition for M/S, not a guarantee, but identical
// content trivially satisfies it and correlated content is exactly the
// case M/S already favors).
func TestEncoderSwitchesOnTransients(t *testing.T) {
	const nFrames = 14
	const burstFrame = 7

	t.Run("mono", func(t *testing.T) {
		e := mustEncoder(t, Config{SampleRate: 44100, Channels: 1, BitrateKbps: 128})
		log := runLoggingBlockTypes(t, e, clickTrainMono(nFrames, burstFrame, 0.8))

		sawNonLongNearBurst := false
		for _, l := range log {
			near := l.frame >= burstFrame-2 && l.frame <= burstFrame+2
			if !near && l.blockType != blockLong {
				t.Errorf("frame %d g %d: blockType = %d, want blockLong (far from the burst at frame %d)",
					l.frame, l.g, l.blockType, burstFrame)
			}
			if near && l.blockType != blockLong {
				sawNonLongNearBurst = true
			}
		}
		if !sawNonLongNearBurst {
			t.Errorf("no non-long block type seen anywhere near the burst (frame %d): block switching never engaged", burstFrame)
		}
	})

	t.Run("correlated stereo", func(t *testing.T) {
		e := mustEncoder(t, Config{SampleRate: 44100, Channels: 2, BitrateKbps: 128})
		mono := clickTrainMono(nFrames, burstFrame, 0.8)
		samples := make([][]float32, nFrames)
		for f := range nFrames {
			samples[f] = mono[f] // reused read-only by both channels below
		}

		var log []granuleLog
		frame := -1
		msSeen := false
		e.SetDiagHookPin(func(g, ch int, diag DiagGranule) {
			if g == 0 && ch == 0 {
				frame++
			}
			log = append(log, granuleLog{frame: frame, g: g, ch: ch, blockType: diag.BlockType})
			if frame >= burstFrame-2 && frame <= burstFrame+2 && e.msFrame {
				msSeen = true
			}
		})

		var dst []byte
		var err error
		for f := range nFrames {
			dst, err = e.EncodeFrame(dst, [][]float32{samples[f], samples[f]})
			if err != nil {
				t.Fatalf("EncodeFrame: %v", err)
			}
		}
		if _, err = e.EncodeFrame(dst, nil); err != nil {
			t.Fatalf("drain: %v", err)
		}

		byFrameGranule := make(map[[2]int][2]int) // [frame,g] -> [ch0 bt, ch1 bt]
		for _, l := range log {
			key := [2]int{l.frame, l.g}
			v := byFrameGranule[key]
			v[l.ch] = l.blockType
			byFrameGranule[key] = v
		}
		for key, v := range byFrameGranule {
			if v[0] != v[1] {
				t.Errorf("frame %d g %d: channels disagree on block type (%d vs %d) for identical L/R content",
					key[0], key[1], v[0], v[1])
			}
		}
		if !msSeen {
			t.Errorf("M/S never retained near the burst (frame %d) for identical L/R content", burstFrame)
		}
	})
}

// TestEncoderNeverSwitchesOnTones requires a steady multi-tone program and
// a pure-silence program to produce only long blocks (design decisions
// 9/10: attackDetect's ratio+floor test must never fire on either). Frame
// 0 is excluded from the tone case only: attackDetect's zero initial carry
// legitimately calls a stream's abrupt silence-to-full-amplitude opening
// an attack (design decision 9's own rationale, confirmed in depth while
// investigating a related TestEncoderMaskingContract regression, Inc7 Task
// B2), a cold-start artifact with no bearing on steady-state behavior;
// silence never trips the floor at any frame, including frame 0.
func TestEncoderNeverSwitchesOnTones(t *testing.T) {
	const nFrames = 12

	t.Run("multitone", func(t *testing.T) {
		e := mustEncoder(t, Config{SampleRate: 44100, Channels: 1, BitrateKbps: 128})
		tone := testsignal.MultiTone(44100, nFrames*1152, 0, 0.7)
		samples := make([][]float32, nFrames)
		for f := range nFrames {
			buf := make([]float32, 1152)
			for i := range buf {
				buf[i] = float32(tone[f*1152+i])
			}
			samples[f] = buf
		}
		log := runLoggingBlockTypes(t, e, samples)
		for _, l := range log {
			if l.frame == 0 {
				continue // cold-start onset, see doc comment
			}
			if l.blockType != blockLong {
				t.Errorf("frame %d g %d: blockType = %d, want blockLong (steady tone)", l.frame, l.g, l.blockType)
			}
		}
	})

	t.Run("silence", func(t *testing.T) {
		e := mustEncoder(t, Config{SampleRate: 44100, Channels: 1, BitrateKbps: 128})
		samples := make([][]float32, nFrames)
		for f := range nFrames {
			samples[f] = make([]float32, 1152)
		}
		log := runLoggingBlockTypes(t, e, samples)
		for _, l := range log {
			if l.blockType != blockLong {
				t.Errorf("frame %d g %d: blockType = %d, want blockLong (silence)", l.frame, l.g, l.blockType)
			}
		}
	})
}

// TestEncoderChannelDisagreementForcesLR drives a stereo program with a
// burst in channel 0 only (channel 1 stays silent throughout): the two
// channels must disagree on block type at the burst, and design decision
// 13's veto must force every such frame to L/R (msFrame == false),
// regardless of what the four-way PE comparison alone would have picked.
func TestEncoderChannelDisagreementForcesLR(t *testing.T) {
	const nFrames = 12
	const burstFrame = 6

	e := mustEncoder(t, Config{SampleRate: 44100, Channels: 2, BitrateKbps: 128})
	ch0 := clickTrainMono(nFrames, burstFrame, 0.8)

	sawDisagreement := false
	var dst []byte
	var err error
	for f := range nFrames {
		silentCh1 := make([]float32, 1152)
		dst, err = e.EncodeFrame(dst, [][]float32{ch0[f], silentCh1})
		if err != nil {
			t.Fatalf("frame %d: EncodeFrame: %v", f, err)
		}
		if e.bt[0][0] != e.bt[0][1] || e.bt[1][0] != e.bt[1][1] {
			sawDisagreement = true
			if e.msFrame {
				t.Errorf("call %d: channels disagree on block type (bt=%v) but msFrame = true, want false (decision 13 veto)", f, e.bt)
			}
		}
	}
	if _, err = e.EncodeFrame(dst, nil); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if !sawDisagreement {
		t.Fatal("channels never disagreed on block type: test setup did not exercise the veto")
	}
}

// TestEncoderPreEchoBound is the basic pre-echo bound Phase 4 increment 7's
// roadmap names (deep pre-echo shaping is Phase 5 scope): for a click-train
// program, the granule immediately preceding each short-block ("attack")
// granule must itself satisfy the masking contract (over == 0), so the
// encoder is not left coding a pre-echo-vulnerable long or start granule
// at a budget too tight for its own threshold right before a transient.
// The drain's two frames are excluded (an EncodeFrame(nil) artifact against
// stale/silence content, not a real pre-echo scenario, the same boundary
// this package's masking tests already exclude).
func TestEncoderPreEchoBound(t *testing.T) {
	const nFrames = 14
	const burstFrame = 7

	e := mustEncoder(t, Config{SampleRate: 44100, Channels: 1, BitrateKbps: 128})
	log := runLoggingBlockTypes(t, e, clickTrainMono(nFrames, burstFrame, 0.8))

	// Flatten into pure granule order (mono: one channel only), dropping
	// the drain's trailing two logged frames (the last held real frame
	// re-logged at drain time, then the silence flush frame): N input
	// calls plus drain yield N+1 logged frames total (indices 0..N), so
	// the drain's two are the final two indices.
	maxFrame := -1
	for _, l := range log {
		if l.frame > maxFrame {
			maxFrame = l.frame
		}
	}
	drainFrame := maxFrame - 1
	var seq []granuleLog
	for _, l := range log {
		if l.frame >= drainFrame {
			continue
		}
		seq = append(seq, l)
	}

	checked := 0
	for i := 1; i < len(seq); i++ {
		if seq[i].blockType != blockShort {
			continue
		}
		prev := seq[i-1]
		checked++
		if prev.over != 0 {
			t.Errorf("granule before an attack (frame %d g %d, type %d) has over = %d, want 0 (pre-echo bound)",
				prev.frame, prev.g, prev.blockType, prev.over)
		}
	}
	if checked == 0 {
		t.Fatal("no short-block (attack) granule found: test setup did not exercise a transient")
	}
}
