package dec

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// fixturePathsF is the testing.F counterpart of fixturePaths: it returns every
// fixture under testdata/fixtures so the fuzz corpus can seed from real MP3
// streams. It includes corrupt_bitflip.mp3 (unlike replayFixtures) because the
// fuzz target only asserts no panic, so a stateful/strict-resync divergence is
// irrelevant here.
func fixturePathsF(f *testing.F) []string {
	f.Helper()
	matches, err := filepath.Glob(filepath.Join("..", "..", "testdata", "fixtures", "*.mp3"))
	if err != nil {
		f.Fatalf("globbing fixtures: %v", err)
	}
	return matches
}

// readFileF is the testing.F counterpart of readFile.
func readFileF(f *testing.F, path string) []byte {
	f.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		f.Fatalf("reading %s: %v", path, err)
	}
	return data
}

// craftedFuzzSeeds returns hand-built inputs that exercise decode paths the
// clean fixtures do not, so the seed corpus (run by plain `go test`) covers
// them deterministically without waiting for the fuzzer to rediscover them.
func craftedFuzzSeeds() [][]byte {
	// Fast-path free-format frame sized below the 4-byte header: the Step 0
	// panic guard. A raw byte stream cannot prime the decoder's sticky
	// free_format_bytes, so this seed cannot reach the guard through
	// DecodeFrame from cold state; TestFastPathFreeFormatNoPanic pins that path
	// directly. It is kept here anyway as a malformed-header seed.
	freeFormat := []byte{
		0xFF, 0xFB, 0x00, 0xFF, 0xFB, 0x00, 0x00,
		0, 0, 0, 0, 0, 0, 0, 0, 0,
	}
	return [][]byte{
		{},                       // empty
		{0xFF},                   // one sync byte
		{0xFF, 0xFB},             // partial header
		{0xFF, 0xFB, 0x90, 0x64}, // exactly a header, no payload
		freeFormat,
	}
}

// FuzzDecodeFrame drives DecodeFrame over arbitrary bytes and asserts it never
// panics, whatever the input. The seed corpus is every fixture plus truncated,
// bit-flipped, and hand-crafted malformed variants, so plain `go test` (which
// replays the seeds) already exercises the malformed-input paths; `go test
// -fuzz` explores beyond them.
//
// The frame walk mirrors mp3dec_decode_frame's caller contract exactly:
// advance by info.FrameBytes, which upstream already sets to i+frame_size
// (offset included, tools/oracle/minimp3.h:1741), and stop when it reports no
// progress. Adding FrameOffset on top, as the plan's sketch did, would
// double-count the skipped bytes and desync the walk.
func FuzzDecodeFrame(f *testing.F) {
	for _, fx := range fixturePathsF(f) {
		data := readFileF(f, fx)
		f.Add(data)

		// Truncated variants: a few deterministic lengths per fixture, so a
		// frame can end mid-payload and mid-header.
		for _, cut := range []int{7, 100, 500, len(data) / 3, len(data) / 2} {
			if cut > 0 && cut < len(data) {
				f.Add(bytes.Clone(data[:cut]))
			}
		}

		// Bit-flipped variant: flip one bit near the middle to corrupt a frame
		// body without necessarily breaking sync.
		if len(data) > 8 {
			flipped := bytes.Clone(data)
			flipped[len(flipped)/2] ^= 0x40
			f.Add(flipped)
		}
	}

	// A cut Layer III stream captured for fuzzing under testdata/vectors/fuzz.
	if fuzzCut := filepath.Join("..", "..", "testdata", "vectors", "fuzz", "l3-compl-cut.mp3"); fuzzCut != "" {
		if data, err := os.ReadFile(fuzzCut); err == nil {
			f.Add(data)
		}
	}

	for _, seed := range craftedFuzzSeeds() {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		d := NewDecoder()
		pcm := make([]float32, maxSamplesPerFrame)
		var info FrameInfo

		pos := 0
		for i := 0; i < 64 && pos < len(data); i++ { // bounded frames per input
			d.DecodeFrame(data[pos:], pcm, &info)
			if info.FrameBytes <= 0 {
				break
			}
			pos += info.FrameBytes
		}
	})
}
