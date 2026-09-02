# go-mp3

Pure-Go MP3 (MPEG-1/2/2.5 Layer III) decoder and MPEG-1 Layer III encoder,
implemented without cgo. MIT licensed.

The decoder is usable today and is validated bit-exactly against a pinned
minimp3 C oracle across amd64 and arm64. The encoder is an independent
implementation derived from ISO/IEC 11172-3 and published literature, never
from LAME, Shine, or other GPL/LGPL encoders; see PROVENANCE.md for licensing
provenance. It is a fixed-bitrate CBR encoder with a psychoacoustic model, a
bit reservoir, per-frame M/S joint stereo, and attack-driven short blocks
(block switching); quality at a given bitrate still lags a fully tuned
encoder like LAME, and further tuning is planned and measured against LAME
(see Quality measurement against LAME below). VBR is not planned.

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

## Encoding

`Encoder.EncodeFrame` feeds planar float32 PCM into the encoder, one
`FrameSize`-sample frame per call, and appends zero or more complete MP3
frames to the output: it holds a one-frame PCM lookahead for attack
detection and block switching, so the first call typically appends nothing,
and audio passed to call `n` comes back out no earlier than call `n+1`.
Configure a CBR bitrate (or leave it zero for the 128 kb/s default), then
drive it in a loop and drain at the end:

```go
e, err := mp3.NewEncoder(mp3.EncoderConfig{
	SampleRate: 44100,
	Channels:   2,
	Bitrate:    128000, // or leave 0 for the 128 kb/s default
})
if err != nil {
	log.Fatal(err)
}

var stream []byte
for pos := 0; pos < len(left); pos += mp3.FrameSize {
	end := min(pos+mp3.FrameSize, len(left))
	frame := [][]float32{left[pos:end], right[pos:end]}
	stream, err = e.EncodeFrame(stream, frame)
	if err != nil {
		log.Fatal(err)
	}
}
stream, err = e.EncodeFrame(stream, nil) // drain: flushes the held frame plus the final frame
if err != nil {
	log.Fatal(err)
}
```

Only the final frame of a stream may be shorter than `FrameSize` (1152
samples per channel); `EncodeFrame` zero-pads it and finalizes the stream,
so any further non-nil call returns `ErrEncoderFinalized` until `Reset`. The
nil drain call is always legal, including right after a short final frame:
it codes whichever real frame the encoder is still holding for its
lookahead (if any), then the final silence flush frame, so `N` non-nil
calls plus drain always total exactly `N+1` emitted frames.

Status: it produces valid, standard-compliant CBR MPEG-1 Layer III streams
(32/44.1/48 kHz, mono or stereo, the 14 legal CBR bitrates) with a
psychoacoustic model, a bit reservoir, per-frame M/S joint stereo, and
attack-driven short blocks, but quality at a given bitrate still lags a
fully tuned encoder like LAME; further tuning is planned and is measured
with the quality harness described below. VBR is not planned.

The encoded stream is tagless (no Xing/LAME header), so the `pcm` decoder
below applies no gapless trim to it: the decoded output carries
`mp3.TotalDelay` (1057) leading samples of algorithmic delay, measured per
channel, unchanged by the one-frame lookahead above (it shifts when frames
come back from `EncodeFrame`, not where they land in the decoded stream).
`Encoder.TotalDelay()` and `Encoder.Delay()` return `mp3.TotalDelay` and
`mp3.EncoderDelay` respectively for callers that only hold an `*Encoder`.
For the interleaved output that means discarding the first
`TotalDelay * Channels` sample values (not `TotalDelay` values) to align
the decoded audio back to the original input.

## Quality measurement against LAME

`task quality` (or `go run ./tools/quality`) encodes a deterministic
synthetic corpus (tones, pink noise, click trains, sweeps, bird-like chirps,
a speech-like program, and three stereo programs) through both this encoder
and the `lame` binary at the same CBR bitrates, decodes both through this
project's `pcm` decoder, aligns them by normalized cross-correlation, and
writes
`tools/quality/out/report.md` plus a JSON twin. LAME is used strictly as a
black-box binary (see PROVENANCE.md). The Go-native metrics are SNR,
band-limited SNR (bins at or below 16 kHz, so LAME's bitrate-dependent
lowpass is not counted as noise there), segmental SNR, log-spectral
distance, a pre-echo measure around detected attacks, and effective
bandwidth. When `visqol` (Google ViSQOL v3) and `peaq-odg` (a GstPEAQ front
end) are on PATH, ViSQOL MOS-LQO and PEAQ ODG columns are added; both are
optional, and both score at 48 kHz, so `ffmpeg` must also be on PATH for any
grid that is not already 48 kHz (the default is 44.1 kHz); without it those
two columns stay `n/a`. The `lame` binary itself is required: the harness
refuses to run without it, so install it or pass `-lame`.

Flags reach the tool after `--` when going through task (`task quality --
-bitrates 128`), or directly with `go run`. `-corpus DIR` adds your own WAV
files, each measured only at its own sample rate, so include that rate in
`-rates` or the file is skipped. `-bitrates`, `-rates` and `-programs` shape
the grid, `-seconds` sets the program length, `-keep` retains the
intermediate MP3 and WAV files under the work directory, `-lame`, `-visqol`
and `-peaq` take explicit binary paths, and `-out` and `-json` redirect the
two reports. The default grid is the 11 synthetic programs at 44.1 kHz and
128/192/256/320 kbps, 6 seconds each. `task quality:smoke` runs the same
end-to-end check CI runs.

## Streaming decoder (package pcm)

The `pcm` package wraps the frame API above into a plain `io.Reader`: give
it an MP3 stream and read interleaved PCM back, without driving the
frame-by-frame loop yourself. It skips a leading ID3v2 tag, recognizes a
Xing/Info/VBRI tag frame and a LAME gapless extension (excluding the tag
frame from the output and trimming the encoder delay plus the standard
529-sample decoder delay at the head, and the padding less that same 529 at
the tail, the same window ffmpeg and mpg123 apply, so the output lines up
sample for sample with the encoder's input), and recovers from mid-stream
garbage.

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

The decoder is usable today. The encoder is a usable CBR encoder with a
psychoacoustic model, bit reservoir, M/S joint stereo, and short blocks,
though quality still lags a fully tuned encoder like LAME; see the Encoding
and Quality measurement sections above.
