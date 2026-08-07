package mp3_test

import (
	"errors"
	"fmt"
	"log"
	"os"

	mp3 "github.com/tphakala/go-mp3"
)

// ExampleDecoder_DecodeFrame decodes an MP3 file frame by frame with the
// low-level Decoder and reports the stream's sample rate.
func ExampleDecoder_DecodeFrame() {
	data, err := os.ReadFile("testdata/fixtures/sine48m_128.mp3")
	if err != nil {
		log.Fatal(err)
	}

	d := mp3.NewDecoder()
	pcm := make([]float32, 1152*2) // up to 1152 samples/channel, interleaved

	var rate int
	pos := 0
	for pos < len(data) {
		n, info, err := d.DecodeFrame(data[pos:], pcm)
		if err != nil && !errors.Is(err, mp3.ErrUnsupported) {
			log.Fatal(err)
		}
		if n > 0 && rate == 0 {
			rate = info.SampleRate
		}
		if info.FrameBytes == 0 {
			break // no more frames
		}
		pos += info.FrameBytes
	}

	fmt.Println("sample rate:", rate)
	// Output: sample rate: 48000
}
