// Package pcm is the high-level streaming PCM API for go-mp3: an MP3 stream
// (an io.Reader) in, interleaved little-endian PCM out through io.Reader and
// io.WriterTo. It wraps the frame-at-a-time mp3.Decoder with the buffering,
// ID3v2 skipping and format conversion a consumer that just wants samples
// would otherwise have to write by hand.
//
// It is shaped like the sibling pcm packages in go-flac, go-aac and go-wav so
// a consumer can switch codecs with the same call shape. The package name
// deliberately collides with those; import it with an alias:
//
//	import mp3pcm "github.com/tphakala/go-mp3/pcm"
//
// # Decoding
//
// NewDecoder reads far enough to establish the stream configuration, skipping a
// leading ID3v2 tag and syncing to the first audio frame, and returns a
// Decoder whose Info is populated:
//
//	d, err := mp3pcm.NewDecoder(r)
//	if err != nil {
//	    // no decodable MP3 frame
//	}
//	pcm, err := io.ReadAll(d) // or io.Copy(w, d) via WriteTo
//
// Read accepts any buffer size, including ones that do not align to a whole
// sample; leftover bytes resume on the next call. The default output is
// interleaved little-endian signed 16-bit PCM (2 bytes per sample), matching
// go-aac and go-wav.
//
// Pass WithF32 to NewDecoder (or to the DecodeInterleaved one-shots) to emit
// interleaved little-endian float32 (4 bytes per sample) instead.
//
// # Gapless trim
//
// A stream carrying a LAME gapless tag is trimmed to its playable extent, so
// the decoded output lines up sample for sample with what went into the
// encoder. The window is the one ffmpeg and mpg123 apply, not the tag's own
// two fields: the head trim is the tag's encoder delay PLUS the standard
// 529-sample Layer III synthesis delay, and the tail trim is the tag's
// padding LESS that same 529, floored at zero. Info reports the raw tag
// fields in EncoderDelay and EncoderPadding, so those two do not describe
// the applied window on their own. Info.TotalSamples is the playable count
// and always equals the number of samples per channel Read emits.
//
// # Encoding
//
// NewEncoder wraps the root mp3.Encoder as an io.WriteCloser that consumes
// interleaved little-endian signed 16-bit PCM and writes a CBR MP3 stream:
//
//	e, err := mp3pcm.NewEncoder(w, mp3pcm.Config{
//	    SampleRate: 44100,
//	    Channels:   2,
//	    Bitrate:    128000, // zero selects the 128 kb/s default
//	})
//	// e.Write(interleavedS16); e.Close()
//
// Write accepts any chunk size, buffering across calls; Close flushes the final
// partial frame and drains the encoder's one-frame lookahead. EncodeInterleaved
// is the one-shot form for a caller that already holds the whole buffer. The
// stream is tagless CBR (no LAME gapless tag), so a decoder does not trim the
// encoder's algorithmic delay: decoded output carries mp3.TotalDelay leading
// samples per channel, which a caller aligning back to the original input drops.
//
// # Reuse
//
// Decoder.Reset rebinds an existing Decoder to a new source, reusing its
// internal buffers and the wrapped mp3.Decoder, so a caller decoding many
// clips can pool decoders.
package pcm
