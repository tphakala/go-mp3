package dec

import (
	"os"
	"testing"
)

// readFile reads a fixture file's bytes, failing the test on error.
func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return data
}

// Hand-crafted 4-byte headers, verified byte-by-byte against the pin's
// hdr_* bit layout in tools/oracle/minimp3.h.
var (
	// MPEG-1 Layer III, 44100 Hz, 128 kbps, no CRC, no padding, joint
	// stereo. Also the literal first 4 bytes of testdata/fixtures/sine44s_128.mp3.
	mpeg1L3_44100_128 = []byte{0xFF, 0xFB, 0x90, 0x64}
	// Same, with the padding bit (h[2] bit 0x2) set.
	mpeg1L3_44100_128_padded = []byte{0xFF, 0xFB, 0x92, 0x64}
	// MPEG-2 Layer III, 24000 Hz, 32 kbps, no CRC, no padding, stereo.
	mpeg2L3_24000_32 = []byte{0xFF, 0xF3, 0x44, 0x00}
	// Same, with padding.
	mpeg2L3_24000_32_padded = []byte{0xFF, 0xF3, 0x46, 0x00}
	// MPEG-1 Layer III, free format (bitrate index 0), 44100 Hz.
	mpeg1L3FreeFormat = []byte{0xFF, 0xFB, 0x00, 0x64}
	// MPEG-1 Layer I (upstream hdr_valid accepts L1/L2 as syncable).
	mpeg1L1_44100 = []byte{0xFF, 0xFF, 0x90, 0x64}
)

func TestHdrValid(t *testing.T) {
	tests := []struct {
		name string
		h    []byte
		want bool
	}{
		{"MPEG-1 L3 44100/128", mpeg1L3_44100_128, true},
		{"MPEG-2 L3 24000/32", mpeg2L3_24000_32, true},
		{"MPEG-1 free format L3", mpeg1L3FreeFormat, true},
		{"MPEG-1 Layer I accepted as syncable", mpeg1L1_44100, true},
		{"bad sync byte", []byte{0xFE, 0xFB, 0x90, 0x64}, false},
		{"bad second sync byte", []byte{0xFF, 0x00, 0x90, 0x64}, false},
		// h[1]=0xF1: bits7-4=1111 (passes the fast sync check), layer
		// bits (bit2,bit1)=00 -> HDR_GET_LAYER=0 (reserved).
		{"reserved layer (0)", []byte{0xFF, 0xF1, 0x90, 0x64}, false},
		{"reserved bitrate index (15)", []byte{0xFF, 0xFB, 0xF0, 0x64}, false},
		{"reserved sample rate index (3)", []byte{0xFF, 0xFB, 0x9C, 0x64}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hdrValid(tt.h); got != tt.want {
				t.Errorf("hdrValid(%08b) = %v, want %v", tt.h, got, tt.want)
			}
		})
	}
}

func TestHdrFrameBytes(t *testing.T) {
	tests := []struct {
		name           string
		h              []byte
		freeFormatSize int
		want           int
	}{
		// 1152*128*125/44100 = 417 (floor); the classic 128kbps/44.1kHz
		// "417 or 418" alternation, where padding supplies the +1.
		{"MPEG-1 44100/128 no padding", mpeg1L3_44100_128, 0, 417},
		// 576*32*125/24000 = 96 exactly (MPEG-2 LSF frame, 576 samples/frame).
		{"MPEG-2 24000/32 no padding", mpeg2L3_24000_32, 0, 96},
		{"free format falls back to hint (0)", mpeg1L3FreeFormat, 0, 0},
		{"free format falls back to hint (626)", mpeg1L3FreeFormat, 626, 626},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hdrFrameBytes(tt.h, tt.freeFormatSize); got != tt.want {
				t.Errorf("hdrFrameBytes(%08b, %d) = %d, want %d", tt.h, tt.freeFormatSize, got, tt.want)
			}
		})
	}
}

// TestHdrFrameBytesPlusPadding covers the padding-math half of Step 2:
// hdr_frame_bytes and hdr_padding are always summed by callers (see
// mp3d_match_frame, mp3d_find_frame) to get the actual bytes to advance.
func TestHdrFrameBytesPlusPadding(t *testing.T) {
	tests := []struct {
		name string
		h    []byte
		want int
	}{
		{"MPEG-1 44100/128 with padding", mpeg1L3_44100_128_padded, 417 + 1},
		{"MPEG-2 24000/32 with padding", mpeg2L3_24000_32_padded, 96 + 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hdrFrameBytes(tt.h, 0) + hdrPadding(tt.h)
			if got != tt.want {
				t.Errorf("hdrFrameBytes+hdrPadding(%08b) = %d, want %d", tt.h, got, tt.want)
			}
		})
	}
}

// TestFindFrameMatchesOracle is the differential test: it walks every
// committed fixture frame by frame using findFrame, exactly mirroring the
// oracle's "frames" dump (offset, frame_bytes, sample_rate_hz) recorded by
// the DUMPI hook added to mp3dec_decode_frame. See task-5-brief.md Step 3.
//
// Uses replayFixtures, not fixturePaths: see its doc comment for why
// corrupt_bitflip.mp3 is excluded here and deferred to Task 10.
func TestFindFrameMatchesOracle(t *testing.T) {
	for _, fx := range replayFixtures(t) {
		recs := readDump(t, fx, "frames")
		data := readFile(t, fx)
		pos, free := 0, 0
		for _, rec := range recs {
			var fb int
			off := findFrame(data[pos:], &free, &fb)
			got := [3]int32{int32(off), int32(fb), int32(hdrSampleRateHz(data[pos+off:]))}
			want := [3]int32{rec.I32[0], rec.I32[1], rec.I32[2]}
			if got != want {
				t.Fatalf("%s: frame mismatch got %v want %v", fx, got, want)
			}
			pos += off + fb
		}
	}
}
