package pcm

import (
	"encoding/binary"
	"testing"
)

// put16 appends a big-endian uint16 to buf and returns the result.
func put16(buf []byte, v uint16) []byte {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], v)
	return append(buf, b[:]...)
}

// buildVBRIFrame assembles a synthetic frame carrying a Fraunhofer VBRI tag at
// the fixed offset 36 (a 4-byte header plus 32 filler bytes), followed by the
// fixed VBRI header fields and a TOC of len(toc) entries, each entrySize bytes
// wide, big-endian. It does not model a decodable audio payload; parseVBRI
// only inspects the tag region.
func buildVBRIFrame(version, delay, quality uint16, bytesTotal, frames uint32, tocScale, entrySize, entryFrames uint16, toc []uint16) []byte {
	buf := make([]byte, vbriOffset, vbriOffset+vbriHeaderLen+len(toc)*int(entrySize))
	buf[0] = 0xff
	buf[1] = 0xfb
	buf = append(buf, vbriMagic...)
	buf = put16(buf, version)
	buf = put16(buf, delay)
	buf = put16(buf, quality)
	buf = put32(buf, bytesTotal)
	buf = put32(buf, frames)
	buf = put16(buf, uint16(len(toc)))
	buf = put16(buf, tocScale)
	buf = put16(buf, entrySize)
	buf = put16(buf, entryFrames)
	for _, e := range toc {
		if entrySize == 1 {
			buf = append(buf, byte(e))
		} else {
			buf = put16(buf, e)
		}
	}
	return buf
}

// buildVBRIFrameWide builds a VBRI frame with a 2-entry, 4-byte-wide TOC, used
// to exercise the entrySize>2 path (validated but not captured into []uint16).
func buildVBRIFrameWide(t *testing.T) []byte {
	t.Helper()
	buf := make([]byte, vbriOffset)
	buf[0] = 0xff
	buf[1] = 0xfb
	buf = append(buf, vbriMagic...)
	buf = put16(buf, 1)                                   // version
	buf = put16(buf, 0)                                   // delay
	buf = put16(buf, 0)                                   // quality
	buf = put32(buf, 100)                                 // bytes
	buf = put32(buf, 10)                                  // frames
	buf = put16(buf, 2)                                   // tocEntries
	buf = put16(buf, 1)                                   // tocScale
	buf = put16(buf, 4)                                   // entrySize (4 bytes/entry)
	buf = put16(buf, 1)                                   // entryFrames
	buf = append(buf, 0, 0, 0x01, 0x00, 0, 0, 0x02, 0x00) // entries 0 and 1 (4 bytes each)
	return buf
}

// wantVBRI is the field set checkVBRIHeader compares a parsed vbriHeader
// against. Fields are in the same order buildVBRIFrame takes them.
type wantVBRI struct {
	version     uint16
	delay       uint16
	quality     uint16
	bytes       uint32
	frames      uint32
	tocScale    uint16
	entrySize   uint16
	entryFrames uint16
	toc         []uint16
}

// checkVBRIHeader compares every captured field of a parsed vbriHeader against
// want, keeping each subtest to a single call.
func checkVBRIHeader(t *testing.T, vh *vbriHeader, want wantVBRI) {
	t.Helper()
	if vh.version != want.version {
		t.Errorf("version = %d, want %d", vh.version, want.version)
	}
	if vh.delay != want.delay {
		t.Errorf("delay = %d, want %d", vh.delay, want.delay)
	}
	if vh.quality != want.quality {
		t.Errorf("quality = %d, want %d", vh.quality, want.quality)
	}
	if vh.bytes != want.bytes {
		t.Errorf("bytes = %d, want %d", vh.bytes, want.bytes)
	}
	if vh.frames != want.frames {
		t.Errorf("frames = %d, want %d", vh.frames, want.frames)
	}
	if vh.tocScale != want.tocScale {
		t.Errorf("tocScale = %d, want %d", vh.tocScale, want.tocScale)
	}
	if vh.entrySize != want.entrySize {
		t.Errorf("entrySize = %d, want %d", vh.entrySize, want.entrySize)
	}
	if vh.entryFrames != want.entryFrames {
		t.Errorf("entryFrames = %d, want %d", vh.entryFrames, want.entryFrames)
	}
	if len(vh.toc) != len(want.toc) {
		t.Fatalf("len(toc) = %d, want %d", len(vh.toc), len(want.toc))
	}
	for i := range want.toc {
		if vh.toc[i] != want.toc[i] {
			t.Errorf("toc[%d] = %d, want %d", i, vh.toc[i], want.toc[i])
		}
	}
}

func TestParseVBRIValid(t *testing.T) {
	t.Run("2-byte TOC", func(t *testing.T) {
		toc := []uint16{100, 200, 300, 400}
		frame := buildVBRIFrame(1, 576, 80, 123456, 789, 1, 2, 197, toc)
		vh, ok := parseVBRI(frame)
		if !ok {
			t.Fatal("parseVBRI returned ok=false, want true")
		}
		checkVBRIHeader(t, vh, wantVBRI{1, 576, 80, 123456, 789, 1, 2, 197, toc})
	})

	t.Run("1-byte TOC", func(t *testing.T) {
		toc := []uint16{5, 250, 128}
		frame := buildVBRIFrame(1, 0, 0, 42, 7, 1, 1, 1, toc)
		vh, ok := parseVBRI(frame)
		if !ok {
			t.Fatal("parseVBRI returned ok=false, want true")
		}
		checkVBRIHeader(t, vh, wantVBRI{1, 0, 0, 42, 7, 1, 1, 1, toc})
	})

	t.Run("empty TOC", func(t *testing.T) {
		frame := buildVBRIFrame(1, 0, 0, 100, 10, 1, 2, 1, nil)
		vh, ok := parseVBRI(frame)
		if !ok {
			t.Fatal("parseVBRI returned ok=false for an empty TOC, want true")
		}
		checkVBRIHeader(t, vh, wantVBRI{1, 0, 0, 100, 10, 1, 2, 1, nil})
	})

	t.Run("wide TOC entry validated but not captured", func(t *testing.T) {
		// entrySize 4 cannot fit a []uint16; the tag is still accepted (its
		// frames/bytes remain usable) with toc left nil, and the 4-byte-per-
		// entry TOC region is bounds-validated against the frame length.
		vh, ok := parseVBRI(buildVBRIFrameWide(t))
		if !ok {
			t.Fatal("parseVBRI returned ok=false for a 4-byte-entry TOC, want true")
		}
		if vh.entrySize != 4 {
			t.Errorf("entrySize = %d, want 4", vh.entrySize)
		}
		if vh.toc != nil {
			t.Errorf("toc = %v, want nil (4-byte entries are not captured)", vh.toc)
		}
	})
}

func TestParseVBRIRejects(t *testing.T) {
	t.Run("wrong magic", func(t *testing.T) {
		frame := buildVBRIFrame(1, 0, 0, 100, 10, 1, 2, 1, []uint16{1})
		copy(frame[vbriOffset:], "XBRI")
		if _, ok := parseVBRI(frame); ok {
			t.Fatal("parseVBRI returned ok=true for a non-VBRI magic, want false")
		}
	})

	t.Run("magic at wrong offset", func(t *testing.T) {
		// A VBRI magic one byte early must not be accepted: the offset is fixed.
		frame := buildVBRIFrame(1, 0, 0, 100, 10, 1, 2, 1, []uint16{1})
		copy(frame[vbriOffset:], "\x00BRI")
		copy(frame[vbriOffset-1:], "VBRI")
		if _, ok := parseVBRI(frame); ok {
			t.Fatal("parseVBRI returned ok=true for a magic at offset 35, want false")
		}
	})

	t.Run("zero entry size", func(t *testing.T) {
		frame := buildVBRIFrame(1, 0, 0, 100, 10, 1, 0, 1, nil)
		if _, ok := parseVBRI(frame); ok {
			t.Fatal("parseVBRI returned ok=true for a zero entry size, want false")
		}
	})

	t.Run("truncated before header", func(t *testing.T) {
		frame := buildVBRIFrame(1, 0, 0, 100, 10, 1, 2, 1, nil)
		truncated := frame[:vbriOffset+vbriHeaderLen-1] // one byte short of the fixed header
		if _, ok := parseVBRI(truncated); ok {
			t.Fatal("parseVBRI returned ok=true for a truncated header, want false")
		}
	})

	t.Run("truncated mid-TOC", func(t *testing.T) {
		frame := buildVBRIFrame(1, 0, 0, 100, 10, 1, 2, 1, []uint16{10, 20, 30})
		truncated := frame[:len(frame)-1] // drops the last TOC byte
		if _, ok := parseVBRI(truncated); ok {
			t.Fatal("parseVBRI returned ok=true for a frame truncated mid-TOC, want false")
		}
	})

	t.Run("short frame", func(t *testing.T) {
		if _, ok := parseVBRI([]byte{0xff, 0xfb}); ok {
			t.Fatal("parseVBRI returned ok=true for a 2-byte frame, want false")
		}
		if _, ok := parseVBRI(nil); ok {
			t.Fatal("parseVBRI returned ok=true for a nil frame, want false")
		}
	})
}
