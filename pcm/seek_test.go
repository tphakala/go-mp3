package pcm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"testing"
)

const (
	// sine44sVBR is a real VBR stream (Xing tag with a TOC; frame sizes vary
	// from 104 to 626 bytes), 44.1 kHz stereo, with a LAME gapless window.
	sine44sVBR = fixturesDir + "/sine44s_vbr.mp3"
	// sine44s128 is an Info-tag (CBR) stream, 44.1 kHz stereo, with a LAME
	// gapless window.
	sine44s128 = fixturesDir + "/sine44s_128.mp3"
	// sine44s32 is a tag-less CBR stream, 16 kHz (MPEG2) stereo, 144-byte
	// frames and no gapless trim.
	sine44s32 = fixturesDir + "/sine44s_32.mp3"
)

// readAllFloat32 decodes the whole seekable stream at path with WithF32 and
// returns the playable (gapless-trimmed) samples plus the stream's channel
// count and per-channel total. It is the ground truth a post-seek decode is
// compared against.
func readAllFloat32(t *testing.T, r io.Reader) (samples []float32, channels int, total int64) {
	t.Helper()
	d, err := NewDecoder(r, WithF32())
	if err != nil {
		t.Fatalf("NewDecoder(WithF32): %v", err)
	}
	b, err := io.ReadAll(d)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return float32BytesFromLE(t, b), d.Info().Channels, int64(d.Info().TotalSamples)
}

// TestSeekToSampleWithTOC seeks each of a TOC-carrying VBR stream, an Info-tag
// CBR stream, and a tag-less CBR stream to several fractional positions, and
// asserts the decoder lands on exactly the requested sample and that every
// sample it then emits is bit-exact float32 equal to decoding the whole stream
// from the start and slicing from that sample. Fractional targets land
// mid-frame, so this also exercises the intra-frame leading-sample drop. The
// VBR fixture is the demanding case (a byte-based TOC map is only approximate
// for it); the CBR fixtures exercise the proportional/tag-less paths, which
// must land bit-exact too.
func TestSeekToSampleWithTOC(t *testing.T) {
	for _, fixture := range []string{sine44sVBR, sine44s128, sine44s32} {
		t.Run(fixture, func(t *testing.T) {
			whole, ch, total := readAllFloat32(t, mustOpen(t, fixture))
			if total == 0 {
				t.Fatalf("%s has no known TotalSamples; the seek test needs one", fixture)
			}

			for _, tc := range []struct {
				name     string
				num, den int64
			}{
				{"quarter", 1, 4},
				{"midpoint", 1, 2},
				{"three-quarter", 3, 4},
			} {
				t.Run(tc.name, func(t *testing.T) {
					target := total * tc.num / tc.den

					f := mustOpen(t, fixture)
					d, err := NewDecoder(f, WithF32())
					if err != nil {
						t.Fatalf("NewDecoder(WithF32): %v", err)
					}
					landed, err := d.SeekToSample(target)
					if err != nil {
						t.Fatalf("SeekToSample(%d): %v", target, err)
					}
					if landed != target {
						t.Fatalf("SeekToSample landed on %d, want %d", landed, target)
					}

					gotBytes, err := io.ReadAll(d)
					if err != nil {
						t.Fatalf("ReadAll after seek: %v", err)
					}
					got := float32BytesFromLE(t, gotBytes)
					want := whole[target*int64(ch):]
					if len(got) != len(want) {
						t.Fatalf("post-seek sample count = %d, want %d", len(got), len(want))
					}
					for i := range want {
						if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
							t.Fatalf("post-seek sample %d = %v (bits %#x), want %v (bits %#x)",
								i, got[i], math.Float32bits(got[i]), want[i], math.Float32bits(want[i]))
						}
					}
				})
			}
		})
	}
}

// TestSeekToSampleClamp asserts that seeking to or past the known playable
// length lands at the stream end (returning the total) and that the next read
// reports a clean io.EOF.
func TestSeekToSampleClamp(t *testing.T) {
	_, _, total := readAllFloat32(t, mustOpen(t, sine44sVBR))

	for _, target := range []int64{total, total + 1, total + 100000} {
		f := mustOpen(t, sine44sVBR)
		d, err := NewDecoder(f)
		if err != nil {
			t.Fatalf("NewDecoder: %v", err)
		}
		landed, err := d.SeekToSample(target)
		if err != nil {
			t.Fatalf("SeekToSample(%d): %v", target, err)
		}
		if landed != total {
			t.Fatalf("SeekToSample(%d) landed on %d, want %d (the total)", target, landed, total)
		}
		var scratch [64]byte
		if n, err := d.Read(scratch[:]); n != 0 || !errors.Is(err, io.EOF) {
			t.Fatalf("post-clamp Read = (%d, %v), want (0, io.EOF)", n, err)
		}
	}
}

// TestSeekNonSeekableSource asserts a bare (non-Seeker) source reports
// ErrSeekUnsupported and, being an argument/capability error, leaves the
// decoder usable for further reads.
func TestSeekNonSeekableSource(t *testing.T) {
	raw := readFixture(t, sine48mono128)
	d, err := NewDecoder(readerOnly{bytes.NewReader(raw)})
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if _, err := d.SeekToSample(1000); !errors.Is(err, ErrSeekUnsupported) {
		t.Fatalf("SeekToSample on a non-seeker = %v, want ErrSeekUnsupported", err)
	}
	// The decoder must remain readable: the capability error touched no state.
	var scratch [16]byte
	if _, err := d.Read(scratch[:]); err != nil {
		t.Fatalf("Read after ErrSeekUnsupported = %v, want the decoder still usable", err)
	}
}

// TestSeekNegative asserts a negative index reports ErrInvalidSeek without
// poisoning the decoder.
func TestSeekNegative(t *testing.T) {
	raw := readFixture(t, sine48mono128)
	d, err := NewDecoder(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if _, err := d.SeekToSample(-1); !errors.Is(err, ErrInvalidSeek) {
		t.Fatalf("SeekToSample(-1) = %v, want ErrInvalidSeek", err)
	}
	var scratch [16]byte
	if _, err := d.Read(scratch[:]); err != nil {
		t.Fatalf("Read after ErrInvalidSeek = %v, want the decoder still usable", err)
	}
}

// TestDecoderExcludesVBRITagFrame prepends a synthetic Fraunhofer VBRI tag
// frame to a tag-less audio stream and asserts pcm.Decoder treats it as
// metadata, never emitting its samples: the decoded audio must equal decoding
// the original stream on its own, and Info().TotalSamples must come from the
// VBRI frame count.
func TestDecoderExcludesVBRITagFrame(t *testing.T) {
	orig := readFixture(t, sine44s32)
	flen, ok := frameLength(orig[:4])
	if !ok {
		t.Fatalf("frameLength(first frame header) failed; fixture layout changed")
	}

	const vbriFrames = 58 // sine44s_32 holds 58 audio frames on disk
	tag := buildVBRITagFrame(orig[:4], flen, vbriFrames)
	constructed := append(bytes.Clone(tag), orig...)

	want := decodeAllBytes(t, bytes.NewReader(orig))
	got := decodeAllBytes(t, bytes.NewReader(constructed))
	if !bytes.Equal(got, want) {
		t.Fatalf("VBRI tag frame leaked into audio: got %d bytes, want %d (the tag-less decode)",
			len(got), len(want))
	}

	d, err := NewDecoder(bytes.NewReader(constructed))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	// sine44s_32 is 16 kHz (MPEG2), 576 samples/frame.
	if wantTotal := uint64(vbriFrames) * 576; d.Info().TotalSamples != wantTotal {
		t.Errorf("Info().TotalSamples = %d, want %d (from the VBRI frame count)",
			d.Info().TotalSamples, wantTotal)
	}
}

// buildVBRITagFrame builds a decodable frame of length flen whose 4-byte header
// is copied from a real frame (so mp3.DecodeFrame sizes it correctly), carrying
// a minimal Fraunhofer VBRI tag at the fixed offset 36. The protection bit is
// forced off so no CRC is expected of the otherwise-empty frame body.
func buildVBRITagFrame(header []byte, flen, frames int) []byte {
	buf := make([]byte, flen)
	copy(buf, header[:4])
	buf[1] |= 0x01 // clear protection: no CRC follows the header
	copy(buf[vbriOffset:], vbriMagic)
	p := vbriOffset + vbriMagicLen
	be16 := func(v uint16) {
		binary.BigEndian.PutUint16(buf[p:], v)
		p += 2
	}
	be32 := func(v uint32) {
		binary.BigEndian.PutUint32(buf[p:], v)
		p += 4
	}
	be16(1)                    // version
	be16(0)                    // delay
	be16(0)                    // quality
	be32(uint32(flen))         // bytes (unused by exclusion)
	be32(uint32(frames))       // frames
	be16(0)                    // tocEntries (empty TOC)
	be16(1)                    // tocScale
	be16(2)                    // entrySize (must be non-zero)
	be16(1)                    // entryFrames
	return buf
}

// readerOnly hides an io.Seeker behind a bare io.Reader so a decoder built on
// it reports the source as non-seekable.
type readerOnly struct{ r io.Reader }

func (ro readerOnly) Read(p []byte) (int, error) { return ro.r.Read(p) }

// mustOpen opens a fixture file (an io.ReadSeeker) and closes it at test end.
func mustOpen(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}
