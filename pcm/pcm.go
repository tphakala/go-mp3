package pcm

import (
	"fmt"

	mp3 "github.com/tphakala/go-mp3"
)

// Config controls the pcm encoder output. It is a flat struct mirroring the
// sibling pcm.Config in go-flac, go-aac and go-opus: every field's zero value
// is documented. Input PCM is always interleaved little-endian signed 16-bit
// (S16LE), so there is no bit-depth field.
type Config struct {
	// SampleRate is the input sample rate in Hz: 32000, 44100, or 48000.
	// Required; there is no zero default.
	SampleRate int
	// Channels is 1 (mono) or 2 (stereo). Required; there is no zero default.
	// For stereo the encoder selects L/R or M/S joint stereo per frame; both
	// decode to 2 channels.
	Channels int
	// Bitrate is the CBR target in bits per second for the whole stream: one of
	// the 14 MPEG-1 Layer III rates (32000, 40000, 48000, 56000, 64000, 80000,
	// 96000, 112000, 128000, 160000, 192000, 224000, 256000, 320000). Zero
	// selects mp3.DefaultBitrate (128000).
	Bitrate int
}

// validate reports the first config problem, or nil. SampleRate and Channels
// carry go-mp3/pcm-prefixed messages here. The exact 14-rate CBR set is owned by
// the root mp3 package and is not duplicated: a non-zero Bitrate is checked for
// sign only here, and its membership in the CBR set is validated by
// mp3.NewEncoder / mp3.Encoder.Reset (whose go-mp3-prefixed error is returned
// unwrapped from Reset), so the two layers cannot drift.
func (c Config) validate() error {
	switch c.SampleRate {
	case 32000, 44100, 48000:
	default:
		return fmt.Errorf("go-mp3/pcm: unsupported sample rate %d (supported: 32000, 44100, 48000)", c.SampleRate)
	}
	if c.Channels != 1 && c.Channels != 2 {
		return fmt.Errorf("go-mp3/pcm: unsupported channel count %d (supported: 1, 2)", c.Channels)
	}
	if c.Bitrate < 0 {
		return fmt.Errorf("go-mp3/pcm: negative bitrate %d", c.Bitrate)
	}
	return nil
}

// toEncoderConfig validates c and maps it to the root mp3.EncoderConfig. A zero
// Bitrate is passed through as zero, which the root encoder maps to
// mp3.DefaultBitrate.
func (c Config) toEncoderConfig() (mp3.EncoderConfig, error) {
	if err := c.validate(); err != nil {
		return mp3.EncoderConfig{}, err
	}
	return mp3.EncoderConfig{
		SampleRate: c.SampleRate,
		Channels:   c.Channels,
		Bitrate:    c.Bitrate,
	}, nil
}
