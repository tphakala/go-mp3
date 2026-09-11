package pcm

import (
	"bytes"
	"testing"

	mp3 "github.com/tphakala/go-mp3"
)

// TestRoundTripSNR encodes a clean tone, decodes it, and asserts the recovered
// audio matches the input over a guarded interior above an SNR floor. Because
// EncodeInterleaved now writes a Xing/Info + LAME gapless tag by default, the
// decoder trims the encoder's algorithmic delay and the final padding itself,
// so the decoded output lines up with the input WITHOUT any manual delay skip
// and has exactly the input length. This is a coarse "audio survived the round
// trip and is time-aligned" gate, not a precise fidelity measurement: a 1 kHz
// tone at 128 kbps reproduces well, so a misalignment or gross corruption
// collapses the SNR far below the floor.
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
			t.Parallel()
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

			// The gapless tag makes the round trip sample-accurate: the decoded
			// per-channel length equals the input length, no delay skip needed.
			if len(got) != len(ref) {
				t.Fatalf("decoded %d samples, want exactly the input length %d", len(got), len(ref))
			}

			// Skip a short lead-in and trail-out where the tone envelope and MDCT
			// windowing overlap the frame edges; compare the stable interior.
			guard := mp3.FrameSize * tc.cfg.Channels
			if len(ref) <= 2*guard {
				t.Fatal("signal too short for guarded comparison")
			}
			n := len(ref) - 2*guard
			snr := snrDB(ref[guard:guard+n], got[guard:guard+n])
			if snr < tc.floor {
				t.Fatalf("round-trip SNR %.1f dB below floor %.1f dB", snr, tc.floor)
			}
		})
	}
}

// TestRoundTripSampleAccurate pins the gapless accounting: with the tag written
// (the default), the decoded per-channel sample count must equal the input
// length exactly, and Info.TotalSamples must report that same playable length.
func TestRoundTripSampleAccurate(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		// n is chosen to exercise a non-frame-aligned tail (the final partial
		// frame is zero-padded and the padding field must account for it).
		n int
	}{
		{"mono 44100 aligned", Config{SampleRate: 44100, Channels: 1, Bitrate: 128000}, mp3.FrameSize * 8},
		{"stereo 44100 partial tail", Config{SampleRate: 44100, Channels: 2, Bitrate: 128000}, mp3.FrameSize*8 + 517},
		{"mono 32000 partial tail", Config{SampleRate: 32000, Channels: 1, Bitrate: 64000}, mp3.FrameSize*5 + 1},
		{"stereo 48000 aligned", Config{SampleRate: 48000, Channels: 2, Bitrate: 320000}, mp3.FrameSize * 4},
		// Bitrate 0 selects the default (128 kbps): exercises resolvedKbps's
		// zero-default in the tag-sizing path.
		{"mono 44100 default bitrate", Config{SampleRate: 44100, Channels: 1}, mp3.FrameSize*3 + 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pcm := genSineS16(tc.n, tc.cfg.Channels, 1000, tc.cfg.SampleRate)

			var enc bytes.Buffer
			if err := EncodeInterleaved(&enc, tc.cfg, pcm); err != nil {
				t.Fatal(err)
			}
			decoded, info, err := DecodeInterleaved(bytes.NewReader(enc.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			gotPerCh := len(bytesToS16(decoded)) / tc.cfg.Channels
			if gotPerCh != tc.n {
				t.Fatalf("decoded %d samples/ch, want exactly the input length %d", gotPerCh, tc.n)
			}
			if info.TotalSamples != uint64(tc.n) {
				t.Fatalf("Info.TotalSamples = %d, want the input length %d", info.TotalSamples, tc.n)
			}
		})
	}
}

// TestRoundTripTaglessStructure covers the opt-out path: with OmitGaplessTag the
// stream carries no tag, so the decoder does not trim, and the decoded length is
// the input plus the encoder delay and drain/flush frames, exactly as before the
// tag existed. The per-channel count must be at least the input length (no
// samples lost) and at most two frames beyond ceil(input / FrameSize).
func TestRoundTripTaglessStructure(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, Bitrate: 128000, OmitGaplessTag: true}
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
	// A tagless stream is not gapless-trimmed, so the raw decoded length carries
	// the algorithmic delay and the drain/flush frames.
	gotPerCh := len(bytesToS16(decoded)) / cfg.Channels
	// Strictly greater than the input: a tagless decode always carries the
	// algorithmic delay, so an equal count would mean a tag slipped in and got
	// trimmed (a broken OmitGaplessTag guard). This must fail in that case.
	if gotPerCh <= nSamplesPerCh {
		t.Fatalf("tagless decode = %d samples/ch, want strictly more than the input %d", gotPerCh, nSamplesPerCh)
	}
	maxPerCh := (nSamplesPerCh/mp3.FrameSize + 2) * mp3.FrameSize
	if gotPerCh > maxPerCh {
		t.Fatalf("decoded %d samples/ch, want at most %d", gotPerCh, maxPerCh)
	}
}
