package pcm

import (
	"bytes"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// fuzzFixturePaths returns every fixture under testdata/fixtures, mirroring
// internal/dec's fixturePathsF, so the pcm fuzz corpus seeds from the same
// real MP3 streams. It includes the two corrupt_* fixtures (unlike
// replayFixtures): this fuzz target only asserts no panic and no hang, so a
// resync/strict-decode divergence from a clean decode is irrelevant here.
func fuzzFixturePaths(f *testing.F) []string {
	f.Helper()
	matches, err := filepath.Glob(filepath.Join("..", "testdata", "fixtures", "*.mp3"))
	if err != nil {
		f.Fatalf("globbing fixtures: %v", err)
	}
	return matches
}

// maxFuzzOutputBytes bounds the PCM a single fuzz iteration is allowed to
// read out. It is far larger than any fixture or seed input could
// legitimately decode to, so it never trims a real decode; it exists only
// so a pathological input cannot make the harness allocate without limit.
const maxFuzzOutputBytes = 16 << 20 // 16 MiB

// FuzzStreamDecode drives pcm.Decoder, in both its default S16 output and
// WithF32, and SeekToSample, over arbitrary bytes, and asserts that neither
// construction, seeking, nor draining ever panics or hangs, whatever the
// input. Each iteration seeks twice, so both the cold frame-header walk and
// the warm one that resumes from the cached frame offsets run against
// malformed input. The seed corpus is every fixture plus truncated and
// bit-flipped variants, so plain `go test` (which replays the seeds) already
// exercises the malformed-input paths this decoder must survive; `go test
// -fuzz` explores beyond them.
//
// seekIdx is a second, independently mutated fuzz input, handed to
// SeekToSample RAW: it is never negated or bounded before use, so the
// fuzzer can reach both ErrInvalidSeek (a negative index) and the
// saturate-rather-than-overflow arithmetic SeekToSample documents (a huge
// index). This is safe specifically because seekIdx is never used as a
// slice length or count anywhere in this test; it flows straight into
// SeekToSample, which already handles the full int64 range. A fuzzer int
// that WAS going to be used as a length or count would need bounding (e.g.
// `% k`) before any negation or abs, since abs(math.MinInt64) is still
// negative and would panic a later make/index inside the harness itself,
// not the code under test; this target has no such derived value.
func FuzzStreamDecode(f *testing.F) {
	for _, fx := range fuzzFixturePaths(f) {
		data, err := os.ReadFile(fx)
		if err != nil {
			f.Fatalf("reading %s: %v", fx, err)
		}
		f.Add(data, int64(0))

		// Truncated variants: a few deterministic lengths, so a frame can end
		// mid-payload and mid-header.
		for _, cut := range []int{7, 100, 500, len(data) / 3, len(data) / 2} {
			if cut > 0 && cut < len(data) {
				f.Add(bytes.Clone(data[:cut]), int64(0))
			}
		}

		// Bit-flipped variant: flip one bit near the middle to corrupt a frame
		// body without necessarily breaking sync.
		if len(data) > 8 {
			flipped := bytes.Clone(data)
			flipped[len(flipped)/2] ^= 0x40
			f.Add(flipped, int64(0))
		}
	}

	// Seek-index extremes: a negative index (ErrInvalidSeek) and the two
	// int64 bounds, which must saturate rather than overflow the seek
	// arithmetic (rawTarget := sampleIndex + gaplessStart, targetFrame :=
	// rawTarget / spf, and so on in seek.go). Paired with empty data, these
	// only reach ErrSeekUnsupported/never construct at all (bytes.Reader over
	// nothing fails NewDecoder), so they alone never actually run
	// SeekToSample's clamp/overflow-saturate arithmetic against a live
	// Decoder in the plain `go test` seed replay; the pairing below with a
	// real fixture is what exercises it there.
	f.Add([]byte{}, int64(-1))
	f.Add([]byte{}, int64(math.MinInt64))
	f.Add([]byte{}, int64(math.MaxInt64))

	// A real, constructible stream paired with the seek-index extremes, so
	// SeekToSample's saturate-rather-than-overflow arithmetic runs against a
	// live Decoder even under plain `go test` (which only replays seeds,
	// never mutates them): sampleIndex >= 0 and TotalSamples known clamps to
	// seekToEnd; unknown total instead reaches rawTarget's own overflow
	// check. sine44s_32.mp3 is a small, tag-less, exact-CBR fixture already
	// relied on elsewhere in this package (robustness_test.go,
	// decoder_test.go) for constructing cleanly. It is committed, so a read
	// failure means a broken checkout, not an optional seed: fail loudly
	// rather than quietly dropping the seeds these edges depend on.
	liveStream, err := os.ReadFile(sine44s32)
	if err != nil {
		f.Fatalf("reading %s: %v", sine44s32, err)
	}
	f.Add(bytes.Clone(liveStream), int64(math.MaxInt64))
	f.Add(bytes.Clone(liveStream), int64(math.MinInt64))
	f.Add(bytes.Clone(liveStream), int64(-1))

	f.Fuzz(func(t *testing.T, data []byte, seekIdx int64) {
		for _, f32 := range [...]bool{false, true} {
			var opts []Option
			if f32 {
				opts = append(opts, WithF32())
			}

			// bytes.NewReader implements io.ReadSeeker, so this already
			// exercises the seekable construction path (a CBR duration probe
			// on NewDecoder, and a real SeekToSample below), not just the
			// plain streaming one.
			d, err := NewDecoder(bytes.NewReader(data), opts...)
			if err != nil {
				continue // failing to construct is fine; a panic is not
			}

			// Any outcome is acceptable here: a successful seek,
			// ErrInvalidSeek, ErrSeekUnsupported (a free-format stream), or a
			// latched decode error from priming. Only a panic is not.
			_, _ = d.SeekToSample(seekIdx)

			// A second, nearer seek on the same decoder, so the fuzzer drives
			// the warm frame-offset walk (resuming from the offsets the first
			// seek cached) and not only the cold one. Halving keeps the target
			// inside the range the first walk may have covered, which is where
			// the resume arithmetic actually applies; the outcome is again
			// unconstrained, only a panic or hang would be a finding.
			_, _ = d.SeekToSample(seekIdx / 2)

			// Bounded drain: any read error (including a latched seek or
			// decode failure) is fine; a panic or hang is not.
			_, _ = io.ReadAll(io.LimitReader(d, maxFuzzOutputBytes))
		}
	})
}
