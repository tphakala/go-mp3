package dec

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestFastPathFreeFormatNoPanic pins the Step 0 guard: a fast-path frame whose
// computed size is smaller than the 4-byte header must not panic.
//
// The fast path (DecodeFrame, decode.go) sizes a repeat frame from the cached
// header as hdrFrameBytes(mp3, freeFormatBytes)+hdrPadding(mp3). A sticky
// free-format size below hdrSize (reachable: mp3d_find_frame can latch
// free_format_bytes = k-padding = 3, see findFrame) makes frameSize = 3, so
// the old code sliced mp3[i+hdrSize : i+frameSize] = mp3[4:3], an inverted
// bound that panics. Upstream bs_inits a negative limit and limps to the
// overrun check, returning 0 without crashing (tools/oracle/minimp3.h:1753,
// get_bits always advances pos then returns 0 past the negative limit); the
// guard matches that observable outcome: no samples, FrameBytes advanced so
// the caller still makes progress, header cleared so the next call resyncs.
func TestFastPathFreeFormatNoPanic(t *testing.T) {
	// FF FB 00: MPEG-1 Layer III, free format (bitrate index 0), 44100 Hz, no
	// padding. Byte 3 repeats FF FB 00 so hdrCompare(mp3, mp3[3:]) holds, which
	// is what keeps the fast path from zeroing frameSize and falling back to
	// findFrame.
	mp3 := []byte{
		0xFF, 0xFB, 0x00,
		0xFF, 0xFB, 0x00, 0x00,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	}

	d := &Decoder{}
	d.header = [4]byte{0xFF, 0xFB, 0x00, 0x00} // cached header matches mp3
	d.freeFormatBytes = 3                      // sticky free-format size < hdrSize

	pcm := make([]float32, maxSamplesPerFrame)
	var info FrameInfo

	n := d.DecodeFrame(mp3, pcm, &info) // must not panic

	if n != 0 {
		t.Fatalf("samples = %d, want 0 for a sub-header-size frame", n)
	}
	if info.FrameBytes <= 0 {
		t.Fatalf("FrameBytes = %d, want > 0 so the caller advances", info.FrameBytes)
	}
	if d.header[0] != 0 {
		t.Fatalf("header[0] = %#x, want 0 (state cleared for resync)", d.header[0])
	}
}

// TestFastPathFreeFormatQueryMode documents and locks query-mode (pcm == nil)
// behavior for the same undersized fast-path free-format frame as
// TestFastPathFreeFormatNoPanic above: it must not panic, and it must return
// the header's nominal frame sample count, not 0.
//
// Upstream returns hdr_frame_samples(hdr) in query mode
// (tools/oracle/minimp3.h:1748-1751), before bs_init ever runs, so the
// frameSize < hdrSize guard in DecodeFrame (which sits after the pcm == nil
// return, by design; see decode.go) is never reached in query mode. A
// reviewer bot flagged the early return as skipping the guard as if it were
// a bug; it is deliberate oracle fidelity, and this test pins the observable
// behavior it produces.
func TestFastPathFreeFormatQueryMode(t *testing.T) {
	// Same crafted input as TestFastPathFreeFormatNoPanic: FF FB 00 repeated
	// at byte 3 so the fast path latches a sticky free-format size (3) below
	// hdrSize (4).
	mp3 := []byte{
		0xFF, 0xFB, 0x00,
		0xFF, 0xFB, 0x00, 0x00,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	}

	d := &Decoder{}
	d.header = [4]byte{0xFF, 0xFB, 0x00, 0x00} // cached header matches mp3
	d.freeFormatBytes = 3                      // sticky free-format size < hdrSize

	want := int(hdrFrameSamples(d.header[:]))

	var info FrameInfo
	n := d.DecodeFrame(mp3, nil, &info) // query mode: must not panic

	if n != want {
		t.Fatalf("query-mode samples = %d, want %d (hdr_frame_samples, per the oracle's pre-bs_init return)", n, want)
	}
}

// TestMonoStereoFlagNoPanic pins the intensity/MS-stereo mono guard (l3Decode,
// decode.go): a malformed single-channel frame carrying a spurious joint-stereo
// flag must not panic.
//
// hdrTestIStereo (scalefactors.go) tests only the I_STEREO mode-extension bit
// (hdr[3]&0x10); it does not verify the channel mode. A crafted mono frame
// (mode bits 0b11, hdr[3]&0xC0==0xC0) with that bit set makes the stereo
// dispatch select intensity stereo, whose l3IntensityStereo reads gr[1] for the
// MPEG-2 shift bit (stereo.go:193). A mono granule's gr has length 1, so that
// access panicked ("index out of range [1] with length 1") before the nch==2
// guard was added. Upstream survives the same input only because its gr_info is
// a fixed-size C array (gr_info[4]), reading a stale struct instead of
// overrunning; the Go port's length-nch slice needs the explicit guard. A valid
// mono stream never signals stereo, so the guard changes no valid output.
//
// The input is reachable from the public mp3.NewDecoder().DecodeFrame, hence
// from pcm.Decoder; Decoder.DecodeFrame here is that same entry point.
func TestMonoStereoFlagNoPanic(t *testing.T) {
	// FF FB 38 FF ...: FF FB is the MPEG-1 Layer III sync; byte 3 = FF sets the
	// channel mode to mono (0b11) and the I_STEREO mode-extension bit (0x10) at
	// once, the exact combination that steered a mono granule into the
	// intensity-stereo path.
	data := []byte("\xff\xfb8\xff\x000A00070000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000")

	d := &Decoder{}
	pcm := make([]float32, maxSamplesPerFrame)
	var info FrameInfo

	mustNotPanic(t, "mono frame with spurious stereo flag", func() {
		d.DecodeFrame(data, pcm, &info)
	})
}

// The frame walk here reuses decodeFullStream (conformance_test.go), which
// mirrors tools/oracle/mp3dump.c:235-249 exactly (advance by info.FrameBytes,
// append n*channels only when a frame decoded, stop when no progress is
// possible); its extra return values (intensity-stereo/mixed-block/layer3 flags)
// are irrelevant here and discarded.

// fillDeterministic writes a splitmix64 keystream into b. It is used instead of
// math/rand so the "random" garbage is byte-stable across Go versions and
// platforms (the oracle dump of a mutated file must stay reproducible) and does
// not drag in a weak-RNG lint exception.
func fillDeterministic(b []byte, seed uint64) {
	x := seed
	for i := range b {
		x += 0x9e3779b97f4a7c15
		z := x
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		z ^= z >> 31
		b[i] = byte(z)
	}
}

// prependGarbage returns n deterministic leading garbage bytes followed by data.
// The decoder (and the oracle) must skip the garbage via resync and then decode
// the intact stream that follows.
func prependGarbage(data []byte, n int, seed uint64) []byte {
	out := make([]byte, n+len(data))
	fillDeterministic(out[:n], seed)
	copy(out[n:], data)
	return out
}

// cutHole returns data with a contiguous chunk removed from its interior. The
// hole corrupts the frame(s) it lands in and forces a resync, while leaving the
// stream head and tail intact so the natural end is not truncated (which would
// otherwise trip the reservoir-boundary divergence documented on
// TestRobustnessRecoveryMatchesOracle).
func cutHole(data []byte, start, length int) []byte {
	if start < 0 || length <= 0 || start+length >= len(data) {
		return bytes.Clone(data)
	}
	out := make([]byte, 0, len(data)-length)
	out = append(out, data[:start]...)
	out = append(out, data[start+length:]...)
	return out
}

// holeParams derives a deterministic interior hole for a stream: start ~40% in,
// length ~one 128 kbps/44.1 kHz frame (417 bytes), which reliably straddles a
// frame boundary while keeping the tail intact.
func holeParams(n int) (start, length int) {
	return n * 2 / 5, 417
}

// recoveryFixtures is the curated set of clean single-config Layer III streams
// used for the oracle-differential recovery tests. corrupt_*, free-format, and
// VBR fixtures are excluded: their decode already exercises resync/edge paths,
// and mutating them on top would muddy what the differential is asserting.
func recoveryFixtures() []string {
	names := []string{
		"sine44s_128.mp3",
		"noise32s_192.mp3",
		"sine48m_mono128.mp3",
		"beep22s_64.mp3",
		"sine44s_320.mp3",
	}
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = filepath.Join("..", "..", "testdata", "fixtures", n)
	}
	return out
}

// mutatedInput is one deterministically mutated stream: name is the basename it
// is materialized under (tools/oracle/mutated/<name>) and dumped as, data is the
// mutated bytes.
type mutatedInput struct {
	name string
	data []byte
}

// recoveryMutations builds the (a) leading-garbage and (b) mid-stream-hole
// variants of every recoveryFixtures entry. These are the inputs whose Go
// decode is byte-compared against the oracle's decode of the same bytes.
func recoveryMutations(t *testing.T) []mutatedInput {
	t.Helper()

	fixtures := recoveryFixtures()
	out := make([]mutatedInput, 0, len(fixtures)*2)
	for _, fx := range fixtures {
		data := readFile(t, fx)
		base := filepath.Base(fx)

		out = append(out, mutatedInput{
			name: "garble-" + base,
			data: prependGarbage(data, 1000, 0x6d70336a),
		})

		start, length := holeParams(len(data))
		out = append(out, mutatedInput{
			name: "hole-" + base,
			data: cutHole(data, start, length),
		})
	}
	return out
}

// mutatedDir is where TestGenerateMutatedInputs materializes mutated streams for
// the oracle harness to dump; dump-all.sh globs it. It is gitignored.
func mutatedPath(name string) string {
	return filepath.Join("..", "..", "tools", "oracle", "mutated", name)
}

// TestGenerateMutatedInputs materializes the recovery mutations under
// tools/oracle/mutated so tools/oracle/dump-all.sh can dump them with the C
// oracle. It only runs when MP3_GEN_MUTATED is set (dump-all.sh sets it before
// building/dumping), so a plain `go test` never writes into the source tree.
func TestGenerateMutatedInputs(t *testing.T) {
	if os.Getenv("MP3_GEN_MUTATED") == "" {
		t.Skip("set MP3_GEN_MUTATED=1 to (re)materialize mutated oracle inputs")
	}

	dir := filepath.Join("..", "..", "tools", "oracle", "mutated")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	for _, m := range recoveryMutations(t) {
		if err := os.WriteFile(filepath.Join(dir, m.name), m.data, 0o644); err != nil {
			t.Fatalf("writing %s: %v", m.name, err)
		}
	}
}

// TestRobustnessNoPanic asserts DecodeFrame never panics on any fixture stream
// mutated three ways: (a) 1000 leading garbage bytes, (b) an interior hole, and
// (c) truncation at every length across the first 200 bytes. It covers every
// fixture (including corrupt_* and free-format), since a panic is a panic
// regardless of resync semantics.
func TestRobustnessNoPanic(t *testing.T) {
	for _, fx := range fixturePaths(t) {
		data := readFile(t, fx)
		base := filepath.Base(fx)

		// (a) leading garbage
		mustNotPanic(t, base+" garble", func() {
			decodeFullStream(prependGarbage(data, 1000, 0x1234abcd))
		})

		// (b) interior hole
		start, length := holeParams(len(data))
		mustNotPanic(t, base+" hole", func() {
			decodeFullStream(cutHole(data, start, length))
		})

		// (c) truncation at every length across the first 200 bytes
		limit := min(200, len(data))
		for k := 0; k <= limit; k++ {
			mustNotPanic(t, base+" trunc", func() {
				decodeFullStream(data[:k])
			})
		}
	}
}

// mustNotPanic runs fn and turns any panic into a test failure that names the
// offending case, instead of a bare stack trace.
func mustNotPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panic on %s: %v", name, r)
		}
	}()
	fn()
}

// TestRobustnessRecoveryMatchesOracle proves the decoder recovers from leading
// garbage and an interior hole exactly as the C oracle does: for each mutation
// it byte-compares Go's decode of the mutated bytes against the oracle's
// pcm.f32le for those same bytes. The oracle defines correct recovery.
//
// Both mutations keep the stream's natural tail intact, so neither hits the
// bit-reservoir-boundary divergence that a truncated tail would (there,
// internal/bits.Reader returns deterministic 0 past its limit while upstream
// reads raw scratch bytes; the Go behavior is the correct, safer one and is
// exercised for no-panic only, by the (c) truncations above). Consequently the
// comparison here is full-length; if a future mutation did truncate at a
// reservoir boundary, its comparison would need scoping to the last
// fully-available frame rather than being forced to false parity.
//
// It follows the MP3_REQUIRE_DUMPS convention: absent dumps skip locally and
// fail in CI. The oracle inputs come from TestGenerateMutatedInputs (run by
// dump-all.sh), so the regenerated bytes must match what was dumped; a mismatch
// means the mutation drifted from the materialized files and is failed loudly.
func TestRobustnessRecoveryMatchesOracle(t *testing.T) {
	for _, m := range recoveryMutations(t) {
		want := readF32File(t, dumpPath(mutatedPath(m.name), "pcm.f32le"))

		// Drift guard: the file the oracle actually dumped must equal the bytes
		// we regenerate here, or the comparison is against the wrong input.
		if onDisk, err := os.ReadFile(mutatedPath(m.name)); err == nil {
			if !bytes.Equal(onDisk, m.data) {
				t.Fatalf("%s: materialized oracle input differs from regenerated bytes; re-run `task oracle:dump`", m.name)
			}
		} else if !os.IsNotExist(err) {
			t.Fatalf("reading %s: %v", mutatedPath(m.name), err)
		} else if os.Getenv("MP3_REQUIRE_DUMPS") != "" {
			t.Fatalf("mutated oracle input missing but dump present: %s", mutatedPath(m.name))
		}

		got, _, _, _ := decodeFullStream(m.data)
		compareBitExact(t, m.name, got, want)
	}
}
