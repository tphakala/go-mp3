package pcm_test

import (
	"bytes"
	"fmt"

	mp3pcm "github.com/tphakala/go-mp3/pcm"
)

func ExampleEncodeInterleaved() {
	// 1152 stereo samples of silence (S16LE): 1152 * 2 channels * 2 bytes.
	pcm := make([]byte, 1152*2*2)
	var out bytes.Buffer
	if err := mp3pcm.EncodeInterleaved(&out, mp3pcm.Config{
		SampleRate: 44100,
		Channels:   2,
		Bitrate:    128000,
	}, pcm); err != nil {
		panic(err)
	}
	fmt.Println(out.Len() > 0)
	// Output: true
}

func ExampleDecodeInterleaved() {
	// Encode a short stream, then decode it back to interleaved S16 PCM.
	pcm := make([]byte, 1152*2*2)
	var enc bytes.Buffer
	cfg := mp3pcm.Config{SampleRate: 44100, Channels: 2, Bitrate: 128000}
	if err := mp3pcm.EncodeInterleaved(&enc, cfg, pcm); err != nil {
		panic(err)
	}
	out, info, err := mp3pcm.DecodeInterleaved(bytes.NewReader(enc.Bytes()))
	if err != nil {
		panic(err)
	}
	fmt.Println(info.SampleRate, info.Channels, len(out) > 0)
	// Output: 44100 2 true
}
