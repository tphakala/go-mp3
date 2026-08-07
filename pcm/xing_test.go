package pcm

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// readVectorFixture reads a fetched vector fixture from testdata/vectors.
// That directory is gitignored (ISO- and LAME-derived conformance material
// is never committed; see CLAUDE.md), so a plain checkout has nothing
// there until scripts/fetch-vectors.sh populates it. This mirrors the
// MP3_REQUIRE_DUMPS convention in internal/dec (dumps_test.go,
// conformance_test.go): local runs skip silently when the corpus is
// absent, while MP3_REQUIRE_DUMPS (set where the corpus is fetched, e.g.
// CI's oracle job) turns the absence into a hard failure so the case is
// proven to actually run somewhere.
func readVectorFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("..", "testdata", "vectors", name)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if os.Getenv("MP3_REQUIRE_DUMPS") != "" {
				t.Fatalf("required vector fixture missing: %s (run scripts/fetch-vectors.sh)", path)
			}
			t.Skipf("vector fixture not found (run scripts/fetch-vectors.sh first): %s", path)
		}
		t.Fatalf("read vector fixture %s: %v", path, err)
	}
	return b
}

// put32 appends a big-endian uint32 to buf and returns the result.
func put32(buf []byte, v uint32) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return append(buf, b[:]...)
}

// buildTagFrame assembles a synthetic frame: a 4-byte header (byte[1] bit0
// clear when crcProtected, matching the wire encoding where 0 means
// CRC-protected), filler bytes up to off (the caller-computed side-info
// end), the magic, a flags word, and the fields flags selects, in the
// fixed frames/bytes/toc/quality order.
func buildTagFrame(off int, crcProtected bool, magic string, flags, frames, bytesTotal uint32, toc []byte, quality int32) []byte {
	buf := make([]byte, 4, off+256)
	buf[0] = 0xff
	buf[1] = 0xfb // protection bit (bit0) set: not CRC-protected, by default
	if crcProtected {
		buf[1] = 0xfa // bit0 clear: CRC-protected
	}
	buf = buf[:off] // zero-filled side-info region; parseXing must not care about its content
	buf = append(buf, magic...)
	buf = put32(buf, flags)
	if flags&xingFlagFrames != 0 {
		buf = put32(buf, frames)
	}
	if flags&xingFlagBytes != 0 {
		buf = put32(buf, bytesTotal)
	}
	if flags&xingFlagTOC != 0 {
		buf = append(buf, toc...)
	}
	if flags&xingFlagQuality != 0 {
		buf = put32(buf, uint32(quality))
	}
	return buf
}

// wantXing is the expected field set checkXingHeader compares a parsed
// xingHeader against. A nil toc skips the TOC content comparison (still
// checking hasTOC).
type wantXing struct {
	isInfo  bool
	frames  uint32
	bytes   uint32
	hasTOC  bool
	toc     []byte
	quality int
}

// checkXingHeader compares every field of a parsed xingHeader against want,
// reporting each mismatch. Factoring this out of the table-driven subtests
// keeps each one to a single call instead of five or six repeated ifs.
func checkXingHeader(t *testing.T, xh *xingHeader, want wantXing) {
	t.Helper()
	if xh.isInfo != want.isInfo {
		t.Errorf("isInfo = %v, want %v", xh.isInfo, want.isInfo)
	}
	if xh.frames != want.frames {
		t.Errorf("frames = %d, want %d", xh.frames, want.frames)
	}
	if xh.bytes != want.bytes {
		t.Errorf("bytes = %d, want %d", xh.bytes, want.bytes)
	}
	if xh.hasTOC != want.hasTOC {
		t.Errorf("hasTOC = %v, want %v", xh.hasTOC, want.hasTOC)
	}
	if want.toc != nil && !bytes.Equal(xh.toc[:], want.toc) {
		t.Error("toc content mismatch")
	}
	if xh.quality != want.quality {
		t.Errorf("quality = %d, want %d", xh.quality, want.quality)
	}
}

func TestParseXingSynthetic(t *testing.T) {
	toc := make([]byte, 100)
	for i := range toc {
		toc[i] = byte(i)
	}

	t.Run("MPEG1 stereo Xing with all fields", func(t *testing.T) {
		// MPEG1 (sampleRate>=32000), stereo: side info 32, no CRC.
		// offset = 4 + 0 + 32 = 36.
		frame := buildTagFrame(36, false, "Xing", 0x0f, 1234, 567890, toc, 57)
		xh, ok := parseXing(frame, 44100, 2)
		if !ok {
			t.Fatal("parseXing returned ok=false, want true")
		}
		checkXingHeader(t, xh, wantXing{isInfo: false, frames: 1234, bytes: 567890, hasTOC: true, toc: toc, quality: 57})
	})

	t.Run("MPEG1 mono Info frames only", func(t *testing.T) {
		// MPEG1, mono: side info 17, no CRC. offset = 4 + 0 + 17 = 21.
		frame := buildTagFrame(21, false, "Info", 0x01, 42, 0, nil, 0)
		xh, ok := parseXing(frame, 44100, 1)
		if !ok {
			t.Fatal("parseXing returned ok=false, want true")
		}
		checkXingHeader(t, xh, wantXing{isInfo: true, frames: 42, quality: -1})
	})

	t.Run("MPEG2 stereo Xing", func(t *testing.T) {
		// MPEG2/2.5 (sampleRate<32000), stereo: side info 17 (same numeric
		// size as MPEG1 mono above, but via the other branch), no CRC.
		// offset = 4 + 0 + 17 = 21.
		frame := buildTagFrame(21, false, "Xing", 0x03, 999, 88888, nil, 0)
		xh, ok := parseXing(frame, 24000, 2)
		if !ok {
			t.Fatal("parseXing returned ok=false, want true")
		}
		checkXingHeader(t, xh, wantXing{isInfo: false, frames: 999, bytes: 88888, quality: -1})
	})

	t.Run("CRC-protected frame shifts offset by 2", func(t *testing.T) {
		// MPEG1 stereo, CRC-protected: side info 32, +2 CRC bytes.
		// offset = 4 + 2 + 32 = 38.
		frame := buildTagFrame(38, true, "Xing", 0x01, 10, 0, nil, 0)
		xh, ok := parseXing(frame, 44100, 2)
		if !ok {
			t.Fatal("parseXing returned ok=false, want true (CRC offset not applied?)")
		}
		checkXingHeader(t, xh, wantXing{isInfo: false, frames: 10, quality: -1})
	})

	t.Run("CRC bit set but magic at the no-CRC offset is not found", func(t *testing.T) {
		// Header declares CRC-protected (offset should be 38), but the
		// magic is placed at the no-CRC offset (36) instead. This proves
		// the CRC bit actually drives the offset: an implementation that
		// hardcoded the no-CRC offset would wrongly find it here.
		frame := buildTagFrame(36, true, "Xing", 0x01, 10, 0, nil, 0)
		if _, ok := parseXing(frame, 44100, 2); ok {
			t.Fatal("parseXing returned ok=true when magic sits at the wrong (no-CRC) offset for a CRC-protected header, want false")
		}
	})

	t.Run("non-tag audio frame", func(t *testing.T) {
		frame := make([]byte, 100)
		frame[0], frame[1] = 0xff, 0xfb
		if _, ok := parseXing(frame, 44100, 2); ok {
			t.Fatal("parseXing returned ok=true for a frame with no Xing/Info magic")
		}
	})

	t.Run("truncated after flags word, no fields", func(t *testing.T) {
		// Magic and flags claim frames|bytes|toc|quality are all present,
		// but the frame ends right after the flags word: nothing to read.
		full := buildTagFrame(36, false, "Xing", 0x0f, 1, 1, toc, 1)
		truncated := full[:36+xingMagicLen+xingFlagsLen]
		if _, ok := parseXing(truncated, 44100, 2); ok {
			t.Fatal("parseXing returned ok=true for a truncated tag, want false")
		}
	})

	t.Run("truncated mid-flags-word", func(t *testing.T) {
		full := buildTagFrame(36, false, "Xing", 0x0f, 1, 1, toc, 1)
		truncated := full[:36+xingMagicLen+2] // 2 of the 4 flags bytes
		if _, ok := parseXing(truncated, 44100, 2); ok {
			t.Fatal("parseXing returned ok=true for a frame truncated mid-flags-word, want false")
		}
	})

	t.Run("truncated mid-TOC", func(t *testing.T) {
		full := buildTagFrame(36, false, "Xing", 0x0f, 1, 1, toc, 1)
		// Keep flags, frames, and bytes, but cut 1 byte into the 100-byte TOC.
		truncated := full[:36+xingMagicLen+xingFlagsLen+xingFieldLen+xingFieldLen+1]
		if _, ok := parseXing(truncated, 44100, 2); ok {
			t.Fatal("parseXing returned ok=true for a frame truncated mid-TOC, want false")
		}
	})

	t.Run("frame shorter than the header", func(t *testing.T) {
		if _, ok := parseXing([]byte{0xff, 0xfb}, 44100, 2); ok {
			t.Fatal("parseXing returned ok=true for a 2-byte frame, want false")
		}
	})

	t.Run("empty frame", func(t *testing.T) {
		if _, ok := parseXing(nil, 44100, 2); ok {
			t.Fatal("parseXing returned ok=true for a nil frame, want false")
		}
	})
}

// TestParseXingRealFixtures covers parseXing against the vbrtag fixtures
// purpose-built for this exact task (see
// testdata/vectors/l3-nonstandard-vbrtag-*.bit): all MPEG1 stereo (header
// bytes ff fb .. 64, verified byte[1] bit0=1 so no CRC), so side info 32,
// offset 36, matching the hand-built cases in TestParseXingSynthetic.
func TestParseXingRealFixtures(t *testing.T) {
	t.Run("full Info tag", func(t *testing.T) {
		raw := readVectorFixture(t, "l3-nonstandard-vbrtag-only.bit")
		xh, ok := parseXing(raw, 44100, 2)
		if !ok {
			t.Fatal("parseXing returned ok=false for l3-nonstandard-vbrtag-only.bit, want true")
		}
		// Verified directly against the fixture bytes: offset 0x2c = 00 00
		// 01 3c (frames), 0x30 = 00 02 05 8c (bytes, also the file's own
		// size), 0x98 = 00 00 00 39 (quality).
		checkXingHeader(t, xh, wantXing{isInfo: true, frames: 316, bytes: 132492, hasTOC: true, quality: 57})
	})

	t.Run("corrupted side-info still parses (offset is content-agnostic)", func(t *testing.T) {
		raw := readVectorFixture(t, "l3-nonstandard-vbrtag-corrupted.bit")
		xh, ok := parseXing(raw, 44100, 2)
		if !ok {
			t.Fatal("parseXing returned ok=false for l3-nonstandard-vbrtag-corrupted.bit, want true")
		}
		// Same tag fields as vbrtag-only.bit: side-info garbage before the
		// tag must not matter.
		checkXingHeader(t, xh, wantXing{isInfo: true, frames: 316, bytes: 132492, hasTOC: true, quality: 57})
	})

	t.Run("frames flag absent", func(t *testing.T) {
		raw := readVectorFixture(t, "l3-nonstandard-vbrtag-noframes.bit")
		xh, ok := parseXing(raw, 44100, 2)
		if !ok {
			t.Fatal("parseXing returned ok=false for l3-nonstandard-vbrtag-noframes.bit, want true")
		}
		checkXingHeader(t, xh, wantXing{isInfo: true, quality: -1})
	})

	t.Run("frames flag present but value zero", func(t *testing.T) {
		raw := readVectorFixture(t, "l3-nonstandard-vbrtag-empty.bit")
		xh, ok := parseXing(raw, 44100, 2)
		if !ok {
			t.Fatal("parseXing returned ok=false for l3-nonstandard-vbrtag-empty.bit, want true")
		}
		// Distinct from the noframes case: bytes/toc/quality are still
		// present (only the frames field itself was zeroed).
		checkXingHeader(t, xh, wantXing{isInfo: true, bytes: 132492, hasTOC: true, quality: 57})
	})

	t.Run("truncated tag (oob-read)", func(t *testing.T) {
		raw := readVectorFixture(t, "l3-nonstandard-vbrtag-oob-read.bit")
		if _, ok := parseXing(raw, 44100, 2); ok {
			t.Fatal("parseXing returned ok=true for l3-nonstandard-vbrtag-oob-read.bit (truncated mid-TOC), want false")
		}
	})
}

func TestSideInfoSize(t *testing.T) {
	cases := []struct {
		name                 string
		sampleRate, channels int
		want                 int
	}{
		{"MPEG1 stereo", 44100, 2, 32},
		{"MPEG1 mono", 48000, 1, 17},
		{"MPEG2 stereo", 24000, 2, 17},
		{"MPEG2 mono", 22050, 1, 9},
		{"MPEG2.5 stereo", 8000, 2, 17},
		{"MPEG2.5 mono", 11025, 1, 9},
		{"MPEG1 boundary rate stereo", 32000, 2, 32},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sideInfoSize(tc.sampleRate, tc.channels); got != tc.want {
				t.Errorf("sideInfoSize(%d, %d) = %d, want %d", tc.sampleRate, tc.channels, got, tc.want)
			}
		})
	}
}

func TestSamplesPerFrame(t *testing.T) {
	cases := []struct {
		sampleRate int
		want       int
	}{
		{44100, 1152},
		{48000, 1152},
		{32000, 1152},
		{24000, 576},
		{22050, 576},
		{16000, 576},
		{11025, 576},
		{8000, 576},
	}
	for _, tc := range cases {
		if got := samplesPerFrame(tc.sampleRate); got != tc.want {
			t.Errorf("samplesPerFrame(%d) = %d, want %d", tc.sampleRate, got, tc.want)
		}
	}
}
