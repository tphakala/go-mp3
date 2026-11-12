package enc

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// burst is one LCG-noise burst of a burstProgram: n samples at amplitude amp
// starting at sample off of frame, on channel ch, or on every channel with
// independent samples when ch is burstEach.
type burst struct {
	frame, ch, off, n int
	amp               float32
}

const burstEach = -1

// burstProgram builds nFrames frames of nch-channel planar input: LCG noise at
// amplitude floor (silence when floor is 0) with the listed bursts written over
// it. Golden-input discipline: stored LCG values and one float32 multiply per
// sample, no libm. The draw order is part of the frozen goldens: the floor is
// drawn first, frame by frame and channel by channel, then the bursts in list
// order (channel by channel for burstEach), all from the one seed.
func burstProgram(seed uint64, nch, nFrames int, floor float32, bursts []burst) [][][]float32 {
	out := make([][][]float32, nFrames)
	for f := range out {
		out[f] = make([][]float32, nch)
		for ch := range nch {
			buf := make([]float32, 1152)
			if floor != 0 {
				for i := range buf {
					buf[i] = float32(testsignal.LCGSigned(&seed)) * floor
				}
			}
			out[f][ch] = buf
		}
	}
	for _, b := range bursts {
		for ch := range nch {
			if b.ch != burstEach && b.ch != ch {
				continue
			}
			for i := b.off; i < b.off+b.n; i++ {
				out[b.frame][ch][i] = float32(testsignal.LCGSigned(&seed)) * b.amp
			}
		}
	}
	return out
}

// shortShapesProgram is a 17-frame stereo program over a quiet noise floor
// whose bursts give the window state machine (blockTypeFor) every short-run
// input shape: a burst inside the stream's first granule, bursts in two
// consecutive frames, a burst on channel 0 only and one on channel 1 only,
// and, from frame 10 on, identical channels with a single burst and then two
// consecutive ones.
func shortShapesProgram() [][][]float32 {
	const identicalFrom = 10
	p := burstProgram(777, 2, 17, 0.015625, []burst{
		{0, burstEach, 256, 150, 0.875},
		{2, burstEach, 400, 300, 0.875},
		{3, burstEach, 400, 300, 0.875},
		{6, 0, 400, 300, 0.875},
		{8, 1, 400, 300, 0.875},
		{11, 0, 400, 300, 0.875},
		{13, 0, 400, 300, 0.875},
		{14, 0, 400, 300, 0.875},
	})
	for f := identicalFrom; f < len(p); f++ {
		copy(p[f][1], p[f][0])
	}
	return p
}

// clickTrainProgram is silence with a 96-sample full-scale burst at the start
// of every granule after the first, independent per channel.
func clickTrainProgram(nch, nFrames int) [][][]float32 {
	bursts := make([]burst, 0, 2*nFrames)
	for pos := 576; pos+96 <= nFrames*1152; pos += 576 {
		bursts = append(bursts, burst{pos / 1152, burstEach, pos % 1152, 96, 1.0})
	}
	return burstProgram(5, nch, nFrames, 0, bursts)
}

// transientProgram is nFrames frames of transientSamples: a burst in the first
// 192 samples of every frame and silence elsewhere.
func transientProgram(nch, nFrames int) [][][]float32 {
	seed := uint64(1)
	p := make([][][]float32, nFrames)
	for f := range p {
		p[f] = transientSamples(&seed, nch, 0.8)
	}
	return p
}

// shortPathShape counts, over the coded frames of one encode, the block-type
// shapes the short-path goldens exist to pin.
type shortPathShape struct {
	shortGC int // granule-channels coded as short blocks
	mixed   int // granules whose two channels have different block types
	msShort int // M/S frames holding a short granule
	keptSsg int // granule-channels whose kept coding has a nonzero subblock_gain
}

// observe adds the frame e coded last.
func (s *shortPathShape) observe(e *Encoder) {
	hasShort := false
	for g := range 2 {
		for ch := range e.nch {
			if e.bt[g][ch] == blockShort {
				s.shortGC++
				hasShort = true
			}
			if e.gr[g][ch].sf.subblockGain != [3]int{} {
				s.keptSsg++
			}
		}
		if e.nch == 2 && e.bt[g][0] != e.bt[g][1] {
			s.mixed++
		}
	}
	if e.msFrame && hasShort {
		s.msShort++
	}
}

// TestEncodeGoldenShortPaths freezes sha256(full encoded stream, drained) for
// programs that drive short-block granules through the outer loop and the
// masking escalation: stereo short runs, channels with different block types,
// M/S frames holding short granules, runs that bridge a quiet granule and
// continue, a stream-start attack, and low and high bitrates on all three
// sample rates. Never re-freeze on an arm64/amd64 mismatch (a determinism bug
// to fix).
//
// Each row also asserts the block-type shapes it exists for, read from the
// encoder after every coded frame, so a re-frozen hash cannot hide a program
// that stopped reaching them. The frame coded from the last input is coded
// inside the drain call and is not observed; no shape depends on it.
func TestEncodeGoldenShortPaths(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		frames  [][][]float32
		wantHex string
		// Shapes the row must reach at least once.
		mixed, msShort, keptSsg bool
	}{
		{"44100_2ch_128kbps_shapes", Config{SampleRate: 44100, Channels: 2, BitrateKbps: 128}, shortShapesProgram(),
			"8287c510e2eab3fae2a5e83d29b6c857e0eaf4090b71d5e3f175843dd6a53d85", true, true, false},
		{"48000_2ch_32kbps_shapes", Config{SampleRate: 48000, Channels: 2, BitrateKbps: 32}, shortShapesProgram(),
			"76d5d2ee88389c014db9a860d89038cf9a90edb05c2d218b5edf234db2b19db8", true, true, false},
		{"32000_2ch_64kbps_shapes", Config{SampleRate: 32000, Channels: 2, BitrateKbps: 64}, shortShapesProgram(),
			"ae6c010ca7a855a8dd60a6a7880514a7e04abf77b64363de0162b491c83cf80d", true, true, false},
		{"48000_2ch_320kbps_clicks", Config{SampleRate: 48000, Channels: 2, BitrateKbps: 320}, clickTrainProgram(2, 8),
			"95cdfdd3497fe66cb4fc3690b8929d8e17fb7fcf47827bc32f7eae9bdd770081", false, true, true},
		{"44100_1ch_320kbps_clicks", Config{SampleRate: 44100, Channels: 1, BitrateKbps: 320}, clickTrainProgram(1, 8),
			"dffd54e89eeec1a198dfde07ed18f12d3c31e8799605c5a499f1bb043f87fe71", false, false, true},
		{"48000_2ch_32kbps_transient", Config{SampleRate: 48000, Channels: 2, BitrateKbps: 32}, transientProgram(2, 8),
			"49f24966ba42b53875ebee4e7782c379cff0464fc173fdf10ef57b2bc8da918c", false, true, false},
		{"44100_1ch_64kbps_transient", Config{SampleRate: 44100, Channels: 1, BitrateKbps: 64}, transientProgram(1, 8),
			"62fc7077b1812329df4b2160cd0a2e56e6ebddb43efe9ed9090c0bb6e9fa4304", false, false, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := mustEncoder(t, c.cfg)
			var stream []byte
			var shape shortPathShape
			for f, fr := range c.frames {
				coded := e.frames
				var err error
				stream, err = e.EncodeFrame(stream, fr)
				if err != nil {
					t.Fatalf("frame %d: EncodeFrame: %v", f, err)
				}
				if e.frames == coded+1 {
					shape.observe(e)
				}
			}
			stream, err := e.EncodeFrame(stream, nil) // drain
			if err != nil {
				t.Fatalf("drain: EncodeFrame: %v", err)
			}

			sum := sha256.Sum256(stream)
			if got := hex.EncodeToString(sum[:]); got != c.wantHex {
				t.Errorf("sha256 = %s, want %s", got, c.wantHex)
			}
			t.Logf("shape: %+v", shape)
			if shape.shortGC == 0 {
				t.Errorf("no short granule coded: the row no longer reaches the short-block path")
			}
			if c.mixed && shape.mixed == 0 {
				t.Errorf("no granule with mismatched channel block types")
			}
			if c.msShort && shape.msShort == 0 {
				t.Errorf("no M/S frame with a short granule")
			}
			if c.keptSsg && shape.keptSsg == 0 {
				t.Errorf("no kept coding with a nonzero subblock_gain")
			}
		})
	}
}
