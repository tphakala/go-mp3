package pcm

import (
	"bytes"
	"testing"

	mp3 "github.com/tphakala/go-mp3"
)

// TestRoundTripSNR encodes a clean tone, decodes it, aligns by the encoder's
// algorithmic delay (mp3.TotalDelay, since the encoder emits a tagless CBR
// stream with no LAME tag for the decoder to trim), and asserts the recovered
// audio matches the input over the aligned region above an SNR floor. This is a
// coarse "audio survived the round trip and is time-aligned" gate, not a precise
// fidelity measurement: a 1 kHz tone at 128 kbps reproduces well, so a
// misalignment or gross corruption collapses the SNR far below the floor.
func TestRoundTripSNR(t *testing.T) {
	cases := []struct {
		name  string
		cfg   Config
		floor float64 // minimum acceptable SNR in dB
	}{
		{"mono 44100", Config{SampleRate: 44100, Channels: 1, Bitrate: 128000}, 20},
		{"stereo 44100", Config{SampleRate: 44100, Channels: 2, Bitrate: 128000}, 20},
		{"mono 48000", Config{SampleRate: 48000, Channels: 1, Bitrate: 192000}, 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const nSamplesPerCh = mp3.FrameSize * 30
			pcm := genSineS16(nSamplesPerCh, tc.cfg.Channels, 1000, tc.cfg.SampleRate)

			var enc bytes.Buffer
			if err := EncodeInterleaved(&enc, tc.cfg, pcm); err != nil {
				t.Fatal(err)
			}
			decoded, info, err := DecodeInterleaved(bytes.NewReader(enc.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			if info.SampleRate != tc.cfg.SampleRate || info.Channels != tc.cfg.Channels {
				t.Fatalf("Info mismatch: got %+v", info)
			}

			ref := bytesToS16(pcm)
			got := bytesToS16(decoded)

			// Drop mp3.TotalDelay leading samples per channel from the decoded
			// stream to align it with the original input.
			skip := mp3.TotalDelay * tc.cfg.Channels
			if skip >= len(got) {
				t.Fatalf("decoded output too short to align: %d samples, delay skip %d", len(got), skip)
			}
			got = got[skip:]

			// Skip a short lead-in and trail-out where the tone envelope and MDCT
			// windowing overlap the frame edges; compare the stable interior.
			guard := mp3.FrameSize * tc.cfg.Channels
			if len(ref) <= 2*guard || len(got) <= 2*guard {
				t.Fatal("signal too short for guarded comparison")
			}
			n := len(ref) - 2*guard
			if m := len(got) - 2*guard; m < n {
				n = m
			}
			snr := snrDB(ref[guard:guard+n], got[guard:guard+n])
			if snr < tc.floor {
				t.Fatalf("round-trip SNR %.1f dB below floor %.1f dB", snr, tc.floor)
			}
		})
	}
}

// TestRoundTripDecodedLengthStructure pins the framing/drain accounting
// independently of fidelity: the decoded per-channel sample count must be at
// least the input length (no samples are lost) and at most two frames beyond
// ceil(input / FrameSize) frames (the encoder delay plus the drain/flush frames,
// allowing for CBR estimation trimming the final frames).
func TestRoundTripDecodedLengthStructure(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, Bitrate: 128000}
	const nSamplesPerCh = mp3.FrameSize * 8
	pcm := genSineS16(nSamplesPerCh, cfg.Channels, 1000, cfg.SampleRate)

	var enc bytes.Buffer
	if err := EncodeInterleaved(&enc, cfg, pcm); err != nil {
		t.Fatal(err)
	}
	decoded, _, err := DecodeInterleaved(bytes.NewReader(enc.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	gotPerCh := len(bytesToS16(decoded)) / cfg.Channels
	// Encoded frames: ceil(nSamplesPerCh / FrameSize) non-nil calls + 1 drain,
	// each decoding to FrameSize samples. gotPerCh must be within two frames of
	// that, allowing for CBR estimation trimming the final frames.
	minPerCh := nSamplesPerCh // at least the input length must survive
	if gotPerCh < minPerCh {
		t.Fatalf("decoded %d samples/ch, want at least %d", gotPerCh, minPerCh)
	}
	maxPerCh := (nSamplesPerCh/mp3.FrameSize + 2) * mp3.FrameSize
	if gotPerCh > maxPerCh {
		t.Fatalf("decoded %d samples/ch, want at most %d", gotPerCh, maxPerCh)
	}
}
