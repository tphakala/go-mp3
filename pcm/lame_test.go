package pcm

import "testing"

// buildLAMETag returns a LAME extension tag: the 9-byte "LAME3.100" version
// string, then filler up to the delay/padding field at offset 21, into which
// delay (12 bits) and padding (12 bits) are packed big-endian across 3 bytes.
func buildLAMETag(delay, padding int) []byte {
	b := make([]byte, 36) // covers the full standard LAME tag with margin
	copy(b, "LAME3.100")
	b[lameDelayOffset] = byte(delay >> 4)
	b[lameDelayOffset+1] = byte((delay&0x0f)<<4 | (padding>>8)&0x0f)
	b[lameDelayOffset+2] = byte(padding & 0xff)
	return b
}

func TestParseLAMESynthetic(t *testing.T) {
	t.Run("exact delay and padding", func(t *testing.T) {
		// 1105 = 0x451, 699 = 0x2bb: both use a nonzero low nibble of delay and
		// a nonzero high nibble of padding, so a mis-shifted pack fails loudly.
		const wantDelay, wantPadding = 1105, 699
		frame := append([]byte{0xff, 0xfb, 0x90, 0x64}, buildLAMETag(wantDelay, wantPadding)...)
		xingEnd := 4
		delay, padding, ok := parseLAME(frame, xingEnd)
		if !ok {
			t.Fatal("parseLAME returned ok=false, want true")
		}
		if delay != wantDelay {
			t.Errorf("delay = %d, want %d", delay, wantDelay)
		}
		if padding != wantPadding {
			t.Errorf("padding = %d, want %d", padding, wantPadding)
		}
	})

	t.Run("common 576/576", func(t *testing.T) {
		frame := append([]byte{0xff, 0xfb}, buildLAMETag(576, 576)...)
		delay, padding, ok := parseLAME(frame, 2)
		if !ok {
			t.Fatal("parseLAME returned ok=false, want true")
		}
		if delay != 576 || padding != 576 {
			t.Errorf("(delay, padding) = (%d, %d), want (576, 576)", delay, padding)
		}
	})

	t.Run("no LAME magic", func(t *testing.T) {
		frame := make([]byte, 64)
		copy(frame[4:], "Xtra") // something other than LAME at xingEnd
		delay, padding, ok := parseLAME(frame, 4)
		if ok || delay != 0 || padding != 0 {
			t.Fatalf("parseLAME = (%d, %d, %v), want (0, 0, false) with no magic", delay, padding, ok)
		}
	})

	t.Run("truncated after magic", func(t *testing.T) {
		// Magic present, but the frame ends before the delay/padding field.
		frame := append([]byte{0xff, 0xfb, 0x90, 0x64}, "LAME3.10"...) // < offset 21+3
		delay, padding, ok := parseLAME(frame, 4)
		if ok || delay != 0 || padding != 0 {
			t.Fatalf("parseLAME = (%d, %d, %v), want (0, 0, false) when truncated", delay, padding, ok)
		}
	})

	t.Run("magic at frame edge", func(t *testing.T) {
		// xingEnd points so close to the end that not even the 4-byte magic
		// fits: must not panic, must report absent.
		frame := []byte{0xff, 0xfb, 'L', 'A'}
		if _, _, ok := parseLAME(frame, 2); ok {
			t.Fatal("parseLAME returned ok=true when the magic runs past the frame end")
		}
	})

	t.Run("negative and out-of-range xingEnd", func(t *testing.T) {
		frame := append([]byte{0xff, 0xfb}, buildLAMETag(576, 576)...)
		if _, _, ok := parseLAME(frame, -1); ok {
			t.Fatal("parseLAME returned ok=true for a negative xingEnd")
		}
		if _, _, ok := parseLAME(frame, len(frame)+10); ok {
			t.Fatal("parseLAME returned ok=true for an xingEnd past the frame end")
		}
	})
}

// TestParseLAMEViaXing threads parseXing's reported lameStart into parseLAME,
// the exact wiring the decoder uses: a synthetic Info tag frame with a LAME
// extension appended right after the Xing fields.
func TestParseLAMEViaXing(t *testing.T) {
	const off = 36 // MPEG1 stereo side-info end (no CRC)
	toc := make([]byte, xingTOCLen)
	frame := buildTagFrame(off, false, "Info",
		xingFlagFrames|xingFlagBytes|xingFlagTOC|xingFlagQuality,
		316, 132492, toc, 57)
	frame = append(frame, buildLAMETag(576, 1234)...)

	xh, ok := parseXing(frame, 44100, 2)
	if !ok {
		t.Fatal("parseXing returned ok=false, want true")
	}
	delay, padding, lok := parseLAME(frame, xh.lameStart)
	if !lok {
		t.Fatalf("parseLAME returned ok=false at lameStart=%d, want true", xh.lameStart)
	}
	if delay != 576 || padding != 1234 {
		t.Errorf("(delay, padding) = (%d, %d), want (576, 1234)", delay, padding)
	}
}
