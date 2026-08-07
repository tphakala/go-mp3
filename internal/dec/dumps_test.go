package dec

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// dumpRecord is one record from an oracle dump file: a tag, and its
// payload interpreted both ways. A .dump file does not self-describe
// whether its payload is float32 or int32, so readDump fills both
// fields from the same raw bytes and the caller picks the one it needs.
type dumpRecord struct {
	Tag uint32
	F32 []float32
	I32 []int32
}

// readDump parses an oracle dump file written by tools/oracle/mp3dump.c:
// repeated records of tag uint32-LE, count uint32-LE, payload count*4
// bytes. It skips the test if the dump is absent, so CI without a local
// `task oracle:dump` run skips cleanly instead of failing.
func readDump(t *testing.T, fixture, stage string) []dumpRecord {
	t.Helper()

	// dump-all.sh names each fixture's dump directory after the full
	// fixture filename, extension included (e.g. dumps/beep22s_64.mp3/),
	// so the lookup key is filepath.Base(fixture), not fixture with its
	// extension stripped: stripping the extension left the fixture's
	// "../../testdata/fixtures/" directory prefix baked into base, which
	// filepath.Join then walked back out of via ".." components, landing
	// on a path outside tools/oracle/dumps entirely.
	base := filepath.Base(fixture)
	path := filepath.Join("..", "..", "tools", "oracle", "dumps", base, stage+".dump")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("dump not found (run `task oracle:dump` first): %s", path)
		}
		t.Fatalf("reading dump %s: %v", path, err)
	}

	return parseDump(t, path, data)
}

// parseDump decodes the record stream shared by readDump and its
// self-test below.
func parseDump(t *testing.T, path string, data []byte) []dumpRecord {
	t.Helper()

	var records []dumpRecord
	for len(data) > 0 {
		if len(data) < 8 {
			t.Fatalf("dump %s: truncated record header (%d bytes left)", path, len(data))
		}
		tag := binary.LittleEndian.Uint32(data[0:4])
		count := binary.LittleEndian.Uint32(data[4:8])
		data = data[8:]

		payloadLen := int(count) * 4
		if len(data) < payloadLen {
			t.Fatalf("dump %s: truncated payload for tag %d (want %d bytes, have %d)", path, tag, payloadLen, len(data))
		}
		payload := data[:payloadLen]
		data = data[payloadLen:]

		rec := dumpRecord{Tag: tag, F32: make([]float32, count), I32: make([]int32, count)}
		for i := range int(count) {
			bits := binary.LittleEndian.Uint32(payload[i*4 : i*4+4])
			rec.F32[i] = math.Float32frombits(bits)
			rec.I32[i] = int32(bits)
		}
		records = append(records, rec)
	}
	return records
}

// fixturePaths returns every fixture under testdata/fixtures, relative
// to the repo root the same way readDump resolves dump files.
func fixturePaths(t *testing.T) []string {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join("..", "..", "testdata", "fixtures", "*.mp3"))
	if err != nil {
		t.Fatalf("globbing fixtures: %v", err)
	}
	return matches
}

// replayFixtures returns fixturePaths(t) minus corrupt_bitflip.mp3, for
// differential tests (Tasks 5-9) that replay frame-by-frame decode state
// from scratch on every call, the way findFrame/matchFrame and their
// successors do.
//
// corrupt_bitflip.mp3 is excluded because its oracle dumps come from
// mp3dec_decode_frame, which is stateful: dec persists across the whole
// file, and once a frame decodes successfully, the next call takes a
// "fast path" that trusts dec->header and only re-validates it against
// the single next frame's header (hdr_compare), never calling
// mp3d_find_frame/mp3d_match_frame at all. mp3d_match_frame is much
// stricter (it requires MAX_FRAME_SYNC_MATCHES consecutive frame headers
// to agree), so on a stream with corruption scattered every few frames
// (see scripts/gen-fixtures.sh) the two can legitimately disagree: the
// fast path accepts a frame the strict from-scratch search would reject
// because a later, corrupted frame falls within its lookahead window.
// Verified against the pin directly (a standalone C program calling
// mp3d_find_frame/mp3d_match_frame outside of mp3dec_decode_frame agrees
// with this package's Go port bit-for-bit), so the divergence is real
// upstream behavior, not a porting bug.
//
// Bit-exact coverage of corrupt_bitflip.mp3's resync behavior is deferred
// to whichever task ports mp3dec_decode_frame's stateful Decoder: it must
// replicate the fast-path header cache to match the oracle's pcm.f32le
// output there, and its differential test should include this fixture.
func replayFixtures(t *testing.T) []string {
	t.Helper()

	var out []string
	for _, fx := range fixturePaths(t) {
		if filepath.Base(fx) == "corrupt_bitflip.mp3" {
			continue
		}
		out = append(out, fx)
	}
	return out
}

// TestReadDumpSelfTest crafts a small dump file with two records (one
// f32, one i32 payload) and asserts readDump's parsing, so package dec
// has a real passing test before Task 5 adds decoder code.
func TestReadDumpSelfTest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "self-test.dump")

	var buf []byte
	writeRecord := func(tag uint32, values []uint32) {
		hdr := make([]byte, 8)
		binary.LittleEndian.PutUint32(hdr[0:4], tag)
		binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(values)))
		buf = append(buf, hdr...)
		for _, v := range values {
			var b [4]byte
			binary.LittleEndian.PutUint32(b[:], v)
			buf = append(buf, b[:]...)
		}
	}

	f32Values := []float32{1.5, -2.25, 0}
	f32Bits := make([]uint32, len(f32Values))
	for i, v := range f32Values {
		f32Bits[i] = math.Float32bits(v)
	}
	writeRecord(1, f32Bits)

	i32Values := []int32{42, -7}
	i32Bits := make([]uint32, len(i32Values))
	for i, v := range i32Values {
		i32Bits[i] = uint32(v)
	}
	writeRecord(2, i32Bits)

	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatalf("writing self-test dump: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading self-test dump: %v", err)
	}
	records := parseDump(t, path, data)

	if len(records) != 2 {
		t.Fatalf("len(records) = %d, want 2", len(records))
	}

	if records[0].Tag != 1 {
		t.Fatalf("records[0].Tag = %d, want 1", records[0].Tag)
	}
	if len(records[0].F32) != len(f32Values) {
		t.Fatalf("len(records[0].F32) = %d, want %d", len(records[0].F32), len(f32Values))
	}
	for i, want := range f32Values {
		if records[0].F32[i] != want {
			t.Fatalf("records[0].F32[%d] = %v, want %v", i, records[0].F32[i], want)
		}
	}

	if records[1].Tag != 2 {
		t.Fatalf("records[1].Tag = %d, want 2", records[1].Tag)
	}
	if len(records[1].I32) != len(i32Values) {
		t.Fatalf("len(records[1].I32) = %d, want %d", len(records[1].I32), len(i32Values))
	}
	for i, want := range i32Values {
		if records[1].I32[i] != want {
			t.Fatalf("records[1].I32[%d] = %v, want %v", i, records[1].I32[i], want)
		}
	}
}
