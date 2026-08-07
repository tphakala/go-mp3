package dec

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// dumpPath resolves an oracle dump artifact for a fixture the same way
// readDump does (tools/oracle/dumps/<fixture-basename>/<name>), so the
// full-stream PCM gate reads from the same tree as every stage differential.
func dumpPath(fixture, name string) string {
	return filepath.Join("..", "..", "tools", "oracle", "dumps", filepath.Base(fixture), name)
}

// readF32File reads a raw little-endian float32 file (the oracle's
// pcm.f32le, written by tools/oracle/mp3dump.c) into a []float32. It mirrors
// readDump's skip behavior: absent dumps skip locally so a checkout without
// a `task oracle:dump` run passes, but MP3_REQUIRE_DUMPS makes the absence a
// hard failure so CI proves the gate actually ran.
func readF32File(t *testing.T, path string) []float32 {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if os.Getenv("MP3_REQUIRE_DUMPS") != "" {
				t.Fatalf("dump required but not found: %s", path)
			}
			t.Skipf("dump not found (run `task oracle:dump` first): %s", path)
		}
		t.Fatalf("reading f32 file %s: %v", path, err)
	}
	if len(data)%4 != 0 {
		t.Fatalf("f32 file %s: length %d is not a multiple of 4", path, len(data))
	}

	out := make([]float32, len(data)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4 : i*4+4]))
	}
	return out
}

// compareBitExact fails t if got and want differ in length or anywhere in
// their float32 bit patterns, reporting the first differing sample index.
// Bit-pattern equality (not ==) so a NaN or a signed zero mismatch is caught.
func compareBitExact(t *testing.T, fx string, got, want []float32) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("%s: decoded %d samples, want %d", fx, len(got), len(want))
	}
	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s: pcm[%d] = %08x, want %08x", fx, i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}

// TestFullStreamMatchesOracle drives the stateful Decoder over every fixture
// (fixturePaths, which INCLUDES corrupt_bitflip.mp3 unlike the stateless
// replayFixtures) and byte-compares the concatenated float32 PCM against the
// oracle's pcm.f32le.
//
// The frame-advance loop is decodeAllFrames (conformance_test.go), which
// replicates tools/oracle/mp3dump.c:233-249 exactly, the same code that
// produced pcm.f32le and is therefore the gate's own reference: advance by
// info.FrameBytes (which already includes FrameOffset upstream, so adding
// FrameOffset again would desync), append only when the decoder returned
// samples, and stop when no further progress is possible. This deliberately
// differs from the plan's sketched `pos += FrameOffset + FrameBytes`, which
// would double-count the leading offset; matching mp3dump.c is what makes
// the comparison bit-exact.
func TestFullStreamMatchesOracle(t *testing.T) {
	for _, fx := range fixturePaths(t) {
		want := readF32File(t, dumpPath(fx, "pcm.f32le"))
		data := readFile(t, fx)

		d := NewDecoder()
		got := decodeAllFrames(d, data, nil)

		compareBitExact(t, fx, got, want)
	}
}
