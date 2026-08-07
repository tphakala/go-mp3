package pcm

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"

	mp3 "github.com/tphakala/go-mp3"
)

// plainCBRFrames returns a fixture's audio frames with its leading Xing/Info
// tag frame stripped, i.e. a tag-less CBR stream. Without a Xing/LAME tag the
// decoder arms no gapless tail-trim, so every frame is emitted and an appended
// or truncated tail is actually reached (a tag-armed tail-trim would end the
// decode before it).
func plainCBRFrames(t *testing.T, path string) []byte {
	t.Helper()
	raw := readFixture(t, path)
	br := bufio.NewReader(bytes.NewReader(raw))
	idLen, err := skipID3v2(br)
	if err != nil {
		t.Fatalf("skipID3v2: %v", err)
	}
	body := raw[idLen:]
	flen, ok := frameLength(body)
	if !ok {
		t.Fatalf("first frame header in %s not decodable", path)
	}
	if flen >= len(body) {
		t.Fatalf("fixture %s has only one frame", path)
	}
	return body[flen:]
}

// id3v1Tag returns a minimal 128-byte ID3v1 trailer: the "TAG" magic followed
// by zero-filled fields. It carries no frame sync word, so a decoder must treat
// it as a clean end, not audio and not a truncated frame.
func id3v1Tag() []byte {
	tag := make([]byte, id3v1Size)
	copy(tag, "TAG")
	return tag
}

// countingReader counts the bytes actually read through it. It is deliberately
// not an io.Seeker, so a Decoder wrapping it takes the non-seekable path (no CBR
// length probe), which keeps the byte count a faithful measure of how far the
// decoder scanned.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// readAllFromDecoder drains d, returning the decoded bytes and the terminal
// error (nil on a clean io.EOF). Unlike decodeAllBytes it does not fatal on a
// non-EOF error, so a test can assert on it.
func readAllFromDecoder(d *Decoder) ([]byte, error) {
	var out []byte
	buf := make([]byte, 4096)
	for {
		n, err := d.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return out, err
		}
	}
}

// TestStreamingLeadingGarbage feeds 1000 bytes of leading garbage before a real
// Xing/Info+LAME stream. The decoder must resync to the first real frame and
// parse its tag at fi.FrameOffset (not offset 0), so both Info and the full PCM
// output match a clean decode of the same stream.
func TestStreamingLeadingGarbage(t *testing.T) {
	const path = fixturesDir + "/sine48m_128.mp3"
	raw := readFixture(t, path)

	clean, err := NewDecoder(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("NewDecoder(clean): %v", err)
	}
	wantInfo := clean.Info()
	cleanOut, err := readAllFromDecoder(clean)
	if err != nil {
		t.Fatalf("read clean: %v", err)
	}

	garbage := make([]byte, 1000) // zero bytes: no frame sync anywhere
	stream := make([]byte, 0, len(garbage)+len(raw))
	stream = append(stream, garbage...)
	stream = append(stream, raw...)

	d, err := NewDecoder(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("NewDecoder(garbage-prefixed): %v", err)
	}
	if got := d.Info(); got != wantInfo {
		t.Errorf("Info after leading garbage = %+v, want %+v", got, wantInfo)
	}
	out, err := readAllFromDecoder(d)
	if err != nil {
		t.Fatalf("read garbage-prefixed: %v", err)
	}
	if !bytes.Equal(out, cleanOut) {
		t.Errorf("output after leading garbage differs: got %d bytes, want %d", len(out), len(cleanOut))
	}
}

// TestStreamingResyncBudget appends a garbage run far larger than the resync
// budget after valid audio. The decoder must give up with ErrCorruptStream
// after a bounded scan, never reading the whole garbage run or hanging.
func TestStreamingResyncBudget(t *testing.T) {
	plain := plainCBRFrames(t, fixturesDir+"/sine44s_128.mp3")
	garbage := make([]byte, 8*resyncBudgetBytes) // zero bytes: no frame sync anywhere
	stream := make([]byte, 0, len(plain)+len(garbage))
	stream = append(stream, plain...)
	stream = append(stream, garbage...)

	cr := &countingReader{r: bytes.NewReader(stream)}
	d, err := NewDecoder(cr)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if _, err := readAllFromDecoder(d); !errors.Is(err, mp3.ErrCorruptStream) {
		t.Fatalf("read error = %v, want mp3.ErrCorruptStream", err)
	}

	limit := int64(len(plain)) + resyncBudgetBytes + resyncWindowBytes + 2*readerBufSize
	if cr.n > limit {
		t.Errorf("read %d bytes before giving up, want <= %d (unbounded scan)", cr.n, limit)
	}
	if cr.n >= int64(len(stream)) {
		t.Errorf("read the entire %d-byte garbage run; the scan is not bounded", len(stream))
	}
}

// TestStreamingResyncDiscardAndRetain drives the discard-and-retain branch that
// TestStreamingLeadingGarbage (1000 bytes) never reaches: more than
// resyncWindowBytes of leading garbage, so a full resync window is all garbage
// and its head is discarded while a frame-sized tail is retained. The garbage
// length is derived so the first real frame's header lands in the unconfirmable
// tail of a resync window (its body plus the following header overruns the
// window, so DecodeFrame cannot confirm it in place). The retained tail must
// carry that header across the discard boundary to the next window, where it is
// confirmed and decoded. The recovered audio must be bit-exact to a clean
// decode.
//
// What this pins: the discard-and-retain path preserves a boundary-straddling
// frame. Dropping the retained tail (retaining nothing) makes the resync skip
// past that frame and corrupts the output, and this test goes RED for that
// regression (verified by mutation: discard := len(d.frameBuf)).
//
// What this does NOT pin: the exact size of the retained tail. With a fixed-size
// CBR fixture, retaining maxFrameBytes vs maxFrameBytes+frameHeaderSize, and
// over-retaining (discard := resyncRetainBytes), are all byte-identical here:
// the frame header never lands in the last frameHeaderSize bytes of a window,
// and over-retaining only keeps a superset. Pinning the exact hdrSize margin
// needs a near-maxFrameBytes free-format frame straddling a 4-byte gap, which no
// current fixture provides; that discrimination is a T7/fuzz carry-forward.
func TestStreamingResyncDiscardAndRetain(t *testing.T) {
	raw := plainCBRFrames(t, fixturesDir+"/sine44s_32.mp3")
	clean := decodeAllBytes(t, bytes.NewReader(raw))

	frameLen, ok := frameLength(raw)
	if !ok {
		t.Fatalf("first frame header not decodable")
	}
	// Land the first real frame's header inside the unconfirmable tail of the
	// second resync window. Windows the resync advances over end at multiples of
	// resyncWindowBytes, and a header within a frame length of a window's end
	// cannot be confirmed in place; half a frame in keeps a margin on both sides
	// of that tail. This straddles the discard boundary, so only a retained tail
	// carries it across.
	garbageLen := 2*resyncWindowBytes - frameLen/2
	if garbageLen <= resyncWindowBytes || garbageLen >= resyncBudgetBytes {
		t.Fatalf("derived garbage length %d outside the resync window/budget range", garbageLen)
	}
	garbage := make([]byte, garbageLen)
	stream := make([]byte, 0, len(garbage)+len(raw))
	stream = append(stream, garbage...)
	stream = append(stream, raw...)

	got := decodeAllBytes(t, bytes.NewReader(stream))
	if !bytes.Equal(got, clean) {
		t.Fatalf("audio after %d bytes of garbage differs from clean decode: got %d bytes, want %d",
			garbageLen, len(got), len(clean))
	}
}

// TestStreamingTruncatedFrameAtEOF cuts a valid stream mid-final-frame, leaving
// a valid frame header whose declared length overruns the remaining bytes. That
// is a truncated frame, which must surface as ErrCorruptStream, not a clean end.
func TestStreamingTruncatedFrameAtEOF(t *testing.T) {
	plain := plainCBRFrames(t, fixturesDir+"/sine44s_128.mp3")
	flen, ok := frameLength(plain)
	if !ok {
		t.Fatalf("frame header not decodable")
	}
	truncated := plain[:len(plain)-flen/2] // drop half the final frame's body

	d, err := NewDecoder(bytes.NewReader(truncated))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if _, err := readAllFromDecoder(d); !errors.Is(err, mp3.ErrCorruptStream) {
		t.Fatalf("read error = %v, want mp3.ErrCorruptStream", err)
	}
}

// TestStreamingTrailingID3v1Clean appends a 128-byte ID3v1 trailer to a valid
// stream. The trailer carries no frame sync, so it is trailing non-frame bytes,
// not a truncated frame: the decode must end cleanly (no ErrCorruptStream).
func TestStreamingTrailingID3v1Clean(t *testing.T) {
	plain := plainCBRFrames(t, fixturesDir+"/sine44s_128.mp3")
	stream := make([]byte, 0, len(plain)+id3v1Size)
	stream = append(stream, plain...)
	stream = append(stream, id3v1Tag()...)

	d, err := NewDecoder(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if _, err := readAllFromDecoder(d); err != nil {
		t.Fatalf("trailing ID3v1 tag must decode cleanly, got %v", err)
	}
}

// TestStreamingID3v1TrailerDuration checks that a trailing 128-byte ID3v1 tag is
// excluded from the CBR byte span, so it does not inflate the estimated frame
// count. The audio is trimmed to the largest remainder modulo the frame size,
// so a wrongly-counted trailer would roll the frame count up by exactly one.
func TestStreamingID3v1TrailerDuration(t *testing.T) {
	plain := plainCBRFrames(t, fixturesDir+"/sine44s_128.mp3")
	flen, ok := frameLength(plain)
	if !ok {
		t.Fatalf("frame header not decodable")
	}
	// Land base's length one byte short of a frame boundary so that adding a
	// mis-counted 128-byte trailer crosses into the next frame.
	base := plain[:len(plain)-len(plain)%flen-1]

	stream := make([]byte, 0, len(base)+id3v1Size)
	stream = append(stream, base...)
	stream = append(stream, id3v1Tag()...)

	d, err := NewDecoder(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	spf := uint64(samplesPerFrame(d.Info().SampleRate))
	wantFrames := uint64(len(base) / flen)
	if got := d.Info().TotalSamples; got != wantFrames*spf {
		t.Errorf("TotalSamples with ID3v1 trailer = %d, want %d (%d frames * %d); trailer not excluded",
			got, wantFrames*spf, wantFrames, spf)
	}
}

// flipMiddleByte returns a copy of raw with its middle byte inverted.
func flipMiddleByte(raw []byte) []byte {
	out := bytes.Clone(raw)
	if len(out) > 0 {
		out[len(out)/2] ^= 0xFF
	}
	return out
}

// TestStreamingNoPanicFuzzSeeds decodes fixtures, the corrupt fixtures, and
// truncated / bit-flipped / garbage-appended variants of each. None may panic;
// a construction or decode error is acceptable.
func TestStreamingNoPanicFuzzSeeds(t *testing.T) {
	seeds := []string{
		"sine44s_128.mp3", "sine44s_32.mp3", "noise32s_192.mp3", "sine48m_128.mp3",
		"sine44s_free168.mp3", "corrupt_bitflip.mp3", "corrupt_truncated.mp3",
	}
	for _, name := range seeds {
		raw := readFixture(t, fixturesDir+"/"+name)
		garbaged := make([]byte, 0, len(raw)+4096)
		garbaged = append(garbaged, raw...)
		garbaged = append(garbaged, make([]byte, 4096)...)
		variants := map[string][]byte{
			"raw":       raw,
			"truncated": raw[:len(raw)/2],
			"bitflip":   flipMiddleByte(raw),
			"garbaged":  garbaged,
		}
		for label, data := range variants {
			t.Run(name+"/"+label, func(t *testing.T) {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("panic decoding %s/%s: %v", name, label, r)
					}
				}()
				d, err := NewDecoder(bytes.NewReader(data))
				if err != nil {
					return // failing to construct is fine; a panic is not
				}
				_, _ = readAllFromDecoder(d) // any error is fine; a panic is not
			})
		}
	}
}
