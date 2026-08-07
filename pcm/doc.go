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
// # Reuse
//
// Decoder.Reset rebinds an existing Decoder to a new source, reusing its
// internal buffers and the wrapped mp3.Decoder, so a caller decoding many
// clips can pool decoders.
package pcm
