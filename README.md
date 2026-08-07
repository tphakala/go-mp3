# go-mp3

Pure-Go MP3 (MPEG-1/2/2.5 Layer III) decoder and, in later phases, encoder,
implemented without cgo. MIT licensed.

The decoder is usable today and is validated bit-exactly against a pinned
minimp3 C oracle across amd64 and arm64. The encoder is not implemented yet
(in progress) and will be an independent implementation from ISO/IEC 11172-3
and published literature. See PROVENANCE.md for licensing provenance.

## Decoding

`Decoder.DecodeFrame` decodes one MP3 frame at a time into interleaved
float32 PCM. Drive it in a loop, advancing by `FrameInfo.FrameBytes` after
each call:

```go
d := mp3.NewDecoder()
pcm := make([]float32, 1152*2) // up to 1152 samples/channel, interleaved

pos := 0
for pos < len(data) {
	n, info, err := d.DecodeFrame(data[pos:], pcm)
	if err != nil && !errors.Is(err, mp3.ErrUnsupported) {
		log.Fatal(err)
	}
	if n > 0 {
		// pcm[:n*info.Channels] holds this frame's samples.
	}
	if info.FrameBytes == 0 {
		break // no more data to search
	}
	pos += info.FrameBytes
}
```

`ErrUnsupported` marks a recognized Layer I or II frame, which this decoder
does not produce samples for; the caller can still advance past it using
`FrameBytes`. See the `DecodeFrame` doc comment for the full breakdown of
its return values.

## Streaming decoder (package pcm)

The `pcm` package wraps the frame API above into a plain `io.Reader`: give
it an MP3 stream and read interleaved PCM back, without driving the
frame-by-frame loop yourself. It skips a leading ID3v2 tag, recognizes a
Xing/Info/VBRI tag frame and a LAME gapless extension (excluding the tag
frame from the output and trimming the encoder's delay/padding), and
recovers from mid-stream garbage.

```go
import mp3pcm "github.com/tphakala/go-mp3/pcm"

d, err := mp3pcm.NewDecoder(r) // r is an io.Reader
if err != nil {
	log.Fatal(err)
}
fmt.Println(d.Info().SampleRate, d.Info().Channels, d.Info().Duration())

pcm, err := io.ReadAll(d) // interleaved little-endian S16 by default
// or: n, err := d.WriteTo(w)
```

Pass `mp3pcm.WithF32()` to `NewDecoder` for native interleaved little-endian
float32 output instead of the default 16-bit PCM. When the source is an
`io.Seeker`, `d.SeekToSample(n)` positions the decoder so the next `Read`
starts at per-channel sample `n` in the gapless-trimmed timeline.

The decoder is usable today; the encoder is not implemented yet.
