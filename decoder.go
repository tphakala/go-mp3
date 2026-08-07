package mp3

import (
	"errors"

	"github.com/tphakala/go-mp3/internal/dec"
)

// Sentinel errors returned by this package's decoding functions.
var (
	// ErrCorruptStream is reserved for the Phase 2 streaming layer (package
	// pcm), which can distinguish a genuinely corrupt payload from a normal
	// resync-skippable frame across a whole stream. DecodeFrame does not
	// return it: at the single-frame granularity, a frame that is valid but
	// produces no samples looks identical to a corrupt one, so returning
	// this error here would be a guess. It exists now so Phase 2 code can
	// reference mp3.ErrCorruptStream, the same way go-flac's pcm package
	// imports flac.ErrCRCMismatch.
	ErrCorruptStream = errors.New("go-mp3: corrupt stream")

	// ErrUnsupported is returned by DecodeFrame when it recognizes a valid
	// frame header for a layer this decoder does not decode (Layer I or II;
	// this library decodes Layer III only).
	ErrUnsupported = errors.New("go-mp3: unsupported stream")
)

// FrameInfo carries the facts DecodeFrame determined about the frame it
// just processed.
type FrameInfo struct {
	// Channels is the number of audio channels (1 or 2).
	Channels int
	// SampleRate is the sample rate in samples per second.
	SampleRate int
	// Layer is the MPEG audio layer (1, 2, or 3) of a recognized frame
	// header, or 0 if DecodeFrame found no frame header in data at all.
	// This decoder produces samples only for Layer 3.
	Layer int
	// Bitrate is the frame's bitrate in kilobits per second.
	Bitrate int
	// FrameBytes is the total number of bytes consumed by this call,
	// including any leading bytes skipped before frame sync (FrameOffset).
	// A caller advances its read position by FrameBytes to reach the next
	// frame. FrameBytes is 0 only when data was empty; see DecodeFrame for
	// the full breakdown of what a zero Channels/Layer/SampleRate paired
	// with a nonzero FrameBytes means.
	FrameBytes int
	// FrameOffset is the number of bytes skipped before the frame's sync
	// word was found.
	FrameOffset int
}

// Decoder decodes MPEG-1/2/2.5 Layer III (MP3) audio frame by frame. It
// carries state between calls (bit reservoir, filterbank and overlap
// memory, and a cached header for fast resync), so frames from the same
// stream must be decoded in order with the same Decoder. A Decoder is not
// safe for concurrent use; use one per goroutine.
type Decoder struct {
	d *dec.Decoder
}

// NewDecoder returns a new Decoder with no stream state.
func NewDecoder() *Decoder {
	return &Decoder{d: dec.NewDecoder()}
}

// DecodeFrame decodes the first MP3 frame found in data into pcm as
// interleaved float32 samples in [-1, 1] (up to 1152 samples per channel,
// so pcm must have room for 1152*Channels), and returns the number of
// samples decoded per channel along with the frame's FrameInfo.
//
// A caller drives a stream by looping: decode, then advance its read
// position by info.FrameBytes, stopping once info.FrameBytes is 0 (or once
// the position reaches the end of the buffered data, whichever comes
// first: a search that finds nothing consumes the rest of data in one
// call, exactly like TestFullStreamMatchesOracle's loop in internal/dec).
// The return values combine as follows:
//
//   - n > 0: a Layer III frame was decoded; pcm[:n*info.Channels] holds
//     the interleaved samples, and err is nil.
//   - n == 0, info.FrameBytes == 0: data was empty; there is nothing left
//     to search. err is nil; this is the stream-end signal, not an error.
//   - n == 0, info.FrameBytes > 0, info.Layer == 0: no frame header was
//     recognized anywhere in data, so it was all treated as skippable
//     bytes (e.g. leading garbage, an ID3 tag, or trailing padding). This
//     is normal, not an error; err is nil, and the caller advances by
//     info.FrameBytes as usual (a streaming caller would instead append
//     more data and retry, since nothing conclusive was found).
//   - n == 0, info.FrameBytes > 0, info.Layer == 3: a Layer III frame was
//     found and consumed but produced no samples. This is normal during
//     MP3 resync (e.g. right after a stream cut) and is not an error; err
//     is nil. The caller still advances by info.FrameBytes.
//   - n == 0, info.FrameBytes > 0, info.Layer is 1 or 2: a valid Layer I
//     or II frame was found and sized, but this decoder does not decode
//     it. err is ErrUnsupported; the caller can still advance by
//     info.FrameBytes to skip over it.
func (d *Decoder) DecodeFrame(data []byte, pcm []float32) (n int, info FrameInfo, err error) {
	var di dec.FrameInfo
	samples := d.d.DecodeFrame(data, pcm, &di)

	info = FrameInfo{
		Channels:    di.Channels,
		SampleRate:  di.SampleRateHz,
		Layer:       di.Layer,
		Bitrate:     di.BitrateKbps,
		FrameBytes:  di.FrameBytes,
		FrameOffset: di.FrameOffset,
	}

	if samples == 0 && di.FrameBytes > 0 && di.Layer != 0 && di.Layer != 3 {
		err = ErrUnsupported
	}
	return samples, info, err
}

// Reset clears all stream state, so the next DecodeFrame call behaves as if
// d were freshly returned by NewDecoder. Call it before decoding an
// unrelated stream with the same Decoder.
func (d *Decoder) Reset() {
	d.d.Reset()
}
