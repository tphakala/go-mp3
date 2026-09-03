package mp3_test

import (
	"bytes"
	"io"
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
