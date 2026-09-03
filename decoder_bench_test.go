package mp3_test

import (
	"os"
	"testing"

	mp3 "github.com/tphakala/go-mp3"
)

// benchFixtures spans the decoder's cost drivers: broadband stereo (the
// heaviest Huffman + IMDCT + synthesis load), a high-bitrate stereo tone, and
// a mono tone (half the granule work). Each is decoded end to end per
// iteration so the steady-state per-frame path dominates the profile.
var benchFixtures = []struct {
	name string
	path string
}{
	{"noise-stereo-192k-44100", "testdata/fixtures/noise32s_192.mp3"},
	{"sine-stereo-320k-44100", "testdata/fixtures/sine44s_320.mp3"},
	{"sine-mono-128k-48000", "testdata/fixtures/sine48m_128.mp3"},
}

// BenchmarkDecodeFrame decodes each fixture front to back per iteration,
// reporting ns/op across the whole file and allocs/op (expected zero in steady
// state). SetBytes reports decode throughput in the input's compressed bytes.
func BenchmarkDecodeFrame(b *testing.B) {
	for _, f := range benchFixtures {
		data, err := os.ReadFile(f.path)
		if err != nil {
			b.Fatalf("ReadFile %s: %v", f.path, err)
		}
		b.Run(f.name, func(b *testing.B) {
			d := mp3.NewDecoder()
			pcm := make([]float32, 1152*2)
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			for b.Loop() {
				d.Reset()
				pos := 0
				for pos < len(data) {
					_, fi, err := d.DecodeFrame(data[pos:], pcm)
					if err != nil {
						b.Fatalf("DecodeFrame at %d: %v", pos, err)
					}
					if fi.FrameBytes == 0 {
						break
					}
					pos += fi.FrameBytes
				}
			}
		})
	}
}
