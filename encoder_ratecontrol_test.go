package mp3_test

import (
	"bytes"
	"io"
	"math"
	"testing"

	mp3 "github.com/tphakala/go-mp3"
	mp3pcm "github.com/tphakala/go-mp3/pcm"
)

// rcEncode encodes n loud-noise frames of stereo 44.1kHz/128kbps under the
// given rate-control mode and drains, returning the whole stream. Loud noise
// drives the rate loop into escalation, where the two modes can differ.
func rcEncode(t *testing.T, cfg mp3.EncoderConfig, n int, seed uint64) []byte {
	t.Helper()
	e, err := mp3.NewEncoder(cfg)
	if err != nil {
		t.Fatalf("NewEncoder(%+v): %v", cfg, err)
	}
	s := seed
	var stream []byte
	for range n {
		frame := planarNoise(&s, cfg.Channels, mp3.FrameSize, 0.9)
		if stream, err = e.EncodeFrame(stream, frame); err != nil {
			t.Fatalf("EncodeFrame: %v", err)
		}
	}
	if stream, err = e.EncodeFrame(stream, nil); err != nil {
		t.Fatalf("drain EncodeFrame: %v", err)
	}
	return stream
}

// TestEncoderRateControlDefaultIsExact confirms leaving RateControl unset
// selects the exact path. Two independent checks: the zero value of
// RateControl must BE RateControlExact (a const reorder that made Fast the
// zero value would silently flip every existing caller, and byte-equality
// below would not catch it because fast and exact agree on most streams), and
// a default-config stream must be byte-for-byte identical to an explicit
// RateControlExact stream (the plumbing routes the default through the exact
// path).
func TestEncoderRateControlDefaultIsExact(t *testing.T) {
	var zero mp3.EncoderConfig
	if zero.RateControl != mp3.RateControlExact {
		t.Fatalf("zero-value RateControl = %v, want RateControlExact; the default would not be exact", zero.RateControl)
	}

	base := mp3.EncoderConfig{SampleRate: 44100, Channels: 2, Bitrate: 128000}
	def := rcEncode(t, base, 8, 1)

	withExact := base
	withExact.RateControl = mp3.RateControlExact
	exact := rcEncode(t, withExact, 8, 1)

	if !bytes.Equal(def, exact) {
		t.Fatalf("default (%d bytes) and explicit RateControlExact (%d bytes) differ; the default must route through the exact path", len(def), len(exact))
	}
}

// TestEncoderRateControlFastRoundTrip confirms RateControlFast is plumbed
// through the public API end to end: it produces a valid CBR stream of the
// same length as exact (CBR is bitrate-fixed regardless of mode) that decodes
// cleanly through the pcm layer.
func TestEncoderRateControlFastRoundTrip(t *testing.T) {
	base := mp3.EncoderConfig{SampleRate: 44100, Channels: 2, Bitrate: 128000}

	exactCfg := base
	exactCfg.RateControl = mp3.RateControlExact
	exact := rcEncode(t, exactCfg, 12, 7)

	fastCfg := base
	fastCfg.RateControl = mp3.RateControlFast
	fast := rcEncode(t, fastCfg, 12, 7)

	if len(exact) != len(fast) {
		t.Fatalf("CBR stream length differs by mode: exact=%d fast=%d", len(exact), len(fast))
	}

	d, err := mp3pcm.NewDecoder(bytes.NewReader(fast))
	if err != nil {
		t.Fatalf("pcm.NewDecoder(fast stream): %v", err)
	}
	pcmData, err := io.ReadAll(d)
	if err != nil {
		t.Fatalf("decoding fast stream: %v", err)
	}
	if len(pcmData) == 0 {
		t.Fatal("fast stream decoded to zero samples")
	}
}

// TestRateControlString covers the RateControl stringer, including the
// out-of-range fallback.
func TestRateControlString(t *testing.T) {
	cases := []struct {
		rc   mp3.RateControl
		want string
	}{
		{mp3.RateControlExact, "RateControlExact"},
		{mp3.RateControlFast, "RateControlFast"},
		{mp3.RateControl(99), "RateControl(99)"},
	}
	for _, c := range cases {
		if got := c.rc.String(); got != c.want {
			t.Errorf("RateControl(%d).String() = %q, want %q", int(c.rc), got, c.want)
		}
	}
}

// TestPCMRateControlFastPlumbed proves pcm.Config.RateControl reaches the root
// encoder: the pcm one-shot under Fast equals the root encoder under Fast fed
// the same samples, and (positive control) Fast differs from Exact on this
// input, so the test cannot pass if the field were dropped.
func TestPCMRateControlFastPlumbed(t *testing.T) {
	const nch, frames, extra = 2, 12, 77
	s := uint64(5)
	src := planarNoise(&s, nch, frames*mp3.FrameSize+extra, 0.9)

	// S16 interleaved input, and the float view of exactly those samples.
	pcmBytes := make([]byte, 0, len(src[0])*nch*2)
	flt := make([][]float32, nch)
	for c := range flt {
		flt[c] = make([]float32, len(src[0]))
	}
	for i := range src[0] {
		for c := range nch {
			v := int16(max(-32768, min(32767, math.Round(float64(src[c][i])*32768))))
			pcmBytes = append(pcmBytes, byte(v), byte(uint16(v)>>8))
			flt[c][i] = float32(v) / 32768
		}
	}

	encodeRoot := func(rc mp3.RateControl) []byte {
		e, err := mp3.NewEncoder(mp3.EncoderConfig{SampleRate: 44100, Channels: nch, Bitrate: 32000, RateControl: rc})
		if err != nil {
			t.Fatal(err)
		}
		var out []byte
		for off := 0; off < len(flt[0]); off += mp3.FrameSize {
			end := min(off+mp3.FrameSize, len(flt[0]))
			chunk := make([][]float32, nch)
			for c := range chunk {
				chunk[c] = flt[c][off:end]
			}
			if out, err = e.EncodeFrame(out, chunk); err != nil {
				t.Fatal(err)
			}
		}
		if out, err = e.EncodeFrame(out, nil); err != nil {
			t.Fatal(err)
		}
		return out
	}
	encodePCM := func(rc mp3.RateControl) []byte {
		var buf bytes.Buffer
		cfg := mp3pcm.Config{SampleRate: 44100, Channels: nch, Bitrate: 32000, OmitGaplessTag: true, RateControl: rc}
		if err := mp3pcm.EncodeInterleaved(&buf, cfg, pcmBytes); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}

	rootFast, rootExact := encodeRoot(mp3.RateControlFast), encodeRoot(mp3.RateControlExact)
	if bytes.Equal(rootFast, rootExact) {
		t.Fatal("positive control failed: Fast and Exact agree on this input; lengthen or change it")
	}
	if got := encodePCM(mp3.RateControlFast); !bytes.Equal(got, rootFast) {
		t.Fatalf("pcm Fast (%d bytes) differs from root Fast (%d bytes)", len(got), len(rootFast))
	}
	if got := encodePCM(mp3.RateControlExact); !bytes.Equal(got, rootExact) {
		t.Fatalf("pcm Exact (%d bytes) differs from root Exact (%d bytes)", len(got), len(rootExact))
	}
	// Zero value is Exact.
	var zero bytes.Buffer
	cfg := mp3pcm.Config{SampleRate: 44100, Channels: nch, Bitrate: 32000, OmitGaplessTag: true}
	if err := mp3pcm.EncodeInterleaved(&zero, cfg, pcmBytes); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(zero.Bytes(), rootExact) {
		t.Fatal("zero-value pcm.Config differs from explicit RateControlExact")
	}
}
