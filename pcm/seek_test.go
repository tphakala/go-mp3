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
	// sine44sFree is a free-format stream (Xing/LAME tag, but audio frames carry
	// no bitrate index) at 44.1 kHz stereo. Its frames cannot be sized from their
	// headers, so the seek frame-header walk cannot position it.
	sine44sFree = fixturesDir + "/sine44s_free168.mp3"
)

// seekFraction names a fractional position in a stream for the seek tests.
type seekFraction struct {
	name     string
	num, den int64
}

// seekFractions are the fractional seek targets the seek tests share.
var seekFractions = []seekFraction{
	{"quarter", 1, 4},
	{"midpoint", 1, 2},
	{"three-quarter", 3, 4},
}

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

			for _, tc := range seekFractions {
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

// TestSeekAfterLeadingGarbage prefixes a tag-less CBR stream with leading
// garbage (no sync word). T6 makes such a stream decode by resyncing to the
// first real frame; this asserts the seek path stays consistent. audioStart
// must advance past the garbage so the frame-header walk starts at the first
// real frame (otherwise frameOffsets reads a garbage byte as a header,
// frameLength fails, and SeekToSample latches "undecodable frame header"), and
// firstFrameBytes must exclude the garbage so the CBR total is right and the
// target is not wrongly clamped. Landing must be bit-exact vs a clean decode.
func TestSeekAfterLeadingGarbage(t *testing.T) {
	raw := readFixture(t, sine44s32)
	whole, ch, total := readAllFloat32(t, bytes.NewReader(raw))
	if total == 0 {
		t.Fatalf("%s has no known TotalSamples; the seek test needs one", sine44s32)
	}

	garbage := make([]byte, 1000) // zero bytes: no frame sync anywhere
	stream := make([]byte, 0, len(garbage)+len(raw))
	stream = append(stream, garbage...)
	stream = append(stream, raw...)

	for _, tc := range seekFractions {
		t.Run(tc.name, func(t *testing.T) {
			target := total * tc.num / tc.den

			d, err := NewDecoder(bytes.NewReader(stream), WithF32())
			if err != nil {
				t.Fatalf("NewDecoder(WithF32): %v", err)
			}
			if got := int64(d.Info().TotalSamples); got != total {
				t.Fatalf("TotalSamples with leading garbage = %d, want %d (garbage not excluded from the CBR span)", got, total)
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
					t.Fatalf("post-seek sample %d bits %#x, want %#x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
				}
			}
		})
	}
}

// TestSeekAfterLeadingGarbageZeroFirstFrame covers the case the raw-fixture test
// above misses: the first confirmed post-garbage frame decodes with n == 0.
// plainCBRFrames strips the fixture's own frame 0, so the first remaining frame
// is reservoir-dependent and yields no samples; with leading garbage, its
// fi.FrameOffset (the garbage length) must still be folded into audioStart before
// the n == 0 skip consumes it, or audioStart stays 0 (pointing into the garbage),
// TotalSamples is inflated, and SeekToSample latches "undecodable frame header".
//
// It cannot assert "bit-exact vs decode-from-start-and-slice" the way the raw
// test does: a leading n == 0 frame makes the CBR TotalSamples (which counts that
// frame) exceed the emitted sample count, so SeekToSample's frame index is offset
// by one frame from the sliced timeline even on a clean decode (a pre-existing
// limitation, orthogonal to this fix). Instead it asserts the garbage stream is
// indistinguishable from the clean one: identical TotalSamples and identical seek
// landing and output, which isolates exactly the leading-garbage handling.
func TestSeekAfterLeadingGarbageZeroFirstFrame(t *testing.T) {
	base := plainCBRFrames(t, sine44s32)

	clean, err := NewDecoder(bytes.NewReader(base))
	if err != nil {
		t.Fatalf("NewDecoder(clean): %v", err)
	}
	total := int64(clean.Info().TotalSamples)
	if total == 0 {
		t.Fatalf("plainCBRFrames(%s) has no TotalSamples", sine44s32)
	}

	garbage := make([]byte, 1000) // zero bytes: no frame sync anywhere
	stream := make([]byte, 0, len(garbage)+len(base))
	stream = append(stream, garbage...)
	stream = append(stream, base...)

	garb, err := NewDecoder(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("NewDecoder(garbage): %v", err)
	}
	if got := int64(garb.Info().TotalSamples); got != total {
		t.Fatalf("TotalSamples with leading garbage + n==0 first frame = %d, want %d (garbage not excluded)", got, total)
	}

	seekRead := func(src []byte, target int64) (int64, []byte) {
		t.Helper()
		d, err := NewDecoder(bytes.NewReader(src), WithF32())
		if err != nil {
			t.Fatalf("NewDecoder(WithF32): %v", err)
		}
		landed, err := d.SeekToSample(target)
		if err != nil {
			t.Fatalf("SeekToSample(%d): %v", target, err)
		}
		out, err := io.ReadAll(d)
		if err != nil {
			t.Fatalf("ReadAll after seek: %v", err)
		}
		return landed, out
	}

	for _, tc := range seekFractions {
		t.Run(tc.name, func(t *testing.T) {
			target := total * tc.num / tc.den
			wantLanded, wantOut := seekRead(base, target)
			gotLanded, gotOut := seekRead(stream, target)
			if gotLanded != wantLanded {
				t.Fatalf("seek landed = %d, want %d (clean-stream landing)", gotLanded, wantLanded)
			}
			if !bytes.Equal(gotOut, wantOut) {
				t.Fatalf("post-seek output differs from clean stream: got %d bytes, want %d", len(gotOut), len(wantOut))
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

// TestSeekOverflowSaturates models the one state that reaches the rawTarget
// addition with the length clamp bypassed: a stream with a real LAME encoder
// delay (gaplessStart > 0) whose total length could not be determined
// (TotalSamples == 0, here because the source refuses SeekEnd so the CBR probe
// fails). Seeking math.MaxInt64 then computes sampleIndex + gaplessStart, which
// overflows int64 to a negative rawTarget; without the saturation guard that
// negative target drives a negative slice index panic during priming. The guard
// must saturate instead, so the frame walk runs to EOF and the seek lands at the
// true stream end.
//
// gaplessStart and the unknown length are forced here (white-box) because the
// only stream that reaches this state naturally is a LAME-delay stream whose
// Xing tag omits the frame count and whose length cannot be probed; no fixture
// carries that combination.
func TestSeekOverflowSaturates(t *testing.T) {
	raw := readFixture(t, sine44s32) // tagless CBR, header-walkable, 58 frames
	full, ch, _ := readAllFloat32(t, bytes.NewReader(raw))
	const forcedDelay = 576

	d, err := NewDecoder(noLenSeeker{bytes.NewReader(raw)})
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if d.info.TotalSamples != 0 {
		t.Fatalf("precondition: TotalSamples = %d, want 0 (unknown length)", d.info.TotalSamples)
	}
	d.gaplessStart = forcedDelay // model a LAME head delay on an unknown-length stream

	landed, err := d.SeekToSample(math.MaxInt64)
	if err != nil {
		t.Fatalf("SeekToSample(MaxInt64): %v", err)
	}
	wantEnd := int64(len(full)/ch) - forcedDelay // true end, less the forced head delay
	if landed != wantEnd {
		t.Fatalf("landed = %d, want %d (the true stream end)", landed, wantEnd)
	}
	var scratch [16]byte
	if n, rerr := d.Read(scratch[:]); n != 0 || !errors.Is(rerr, io.EOF) {
		t.Fatalf("post-seek Read = (%d, %v), want (0, io.EOF)", n, rerr)
	}
}

// TestSeekFreeFormatUnsupported seeks a free-format stream, whose frames carry
// no bitrate index and so cannot be sized by the frame-header walk. The walk
// fails on the very first audio frame, which SeekToSample must report as the
// non-poisoning ErrSeekUnsupported (a capability error, like a non-seekable
// source) rather than latching a sticky error. The decoder must stay fully
// usable: a full decode after the failed seek matches a clean decode.
func TestSeekFreeFormatUnsupported(t *testing.T) {
	raw := readFixture(t, sine44sFree)
	want := decodeAllBytes(t, bytes.NewReader(raw))

	d, err := NewDecoder(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	total := int64(d.Info().TotalSamples)
	if total == 0 {
		t.Fatalf("%s has no TotalSamples; the target cannot be kept below the clamp", sine44sFree)
	}
	// A target below the known total so the length clamp does not short-circuit
	// the walk; the walk itself must then fail on the free-format first frame.
	if _, err := d.SeekToSample(total / 2); !errors.Is(err, ErrSeekUnsupported) {
		t.Fatalf("SeekToSample on a free-format stream = %v, want ErrSeekUnsupported", err)
	}
	got, err := readAllFromDecoder(d)
	if err != nil {
		t.Fatalf("Read after ErrSeekUnsupported: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("decode after failed seek differs from clean decode: got %d bytes, want %d", len(got), len(want))
	}
}

// TestSeekTrailerLandsAtEnd exercises the i>0 branch of the header walk: after
// walking at least one real frame it meets a non-frame header (an ID3v1 "TAG"
// trailer past the last audio frame). That is treated like EOF, so the seek
// lands at the stream end rather than latching an undecodable-header error. The
// stream length is made unknown (the source refuses SeekEnd, so no clamp) and
// the target is large, driving the walk off the end of the audio into the
// trailer.
func TestSeekTrailerLandsAtEnd(t *testing.T) {
	raw := readFixture(t, sine44s32) // tagless CBR, 58 frames, no gapless trim
	full, ch, _ := readAllFloat32(t, bytes.NewReader(raw))
	trueEnd := int64(len(full) / ch)

	stream := make([]byte, 0, len(raw)+id3v1Size)
	stream = append(stream, raw...)
	stream = append(stream, id3v1Tag()...)

	d, err := NewDecoder(noLenSeeker{bytes.NewReader(stream)})
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if d.info.TotalSamples != 0 {
		t.Fatalf("precondition: TotalSamples = %d, want 0 (unknown length, no clamp)", d.info.TotalSamples)
	}
	// A target well past the audio so the walk steps off the last frame and meets
	// the "TAG" trailer, which is not a frame header.
	landed, err := d.SeekToSample(trueEnd * 4)
	if err != nil {
		t.Fatalf("SeekToSample past end with trailer: %v", err)
	}
	if landed != trueEnd {
		t.Fatalf("landed = %d, want %d (the stream end)", landed, trueEnd)
	}
	var scratch [16]byte
	if n, rerr := d.Read(scratch[:]); n != 0 || !errors.Is(rerr, io.EOF) {
		t.Fatalf("post-seek Read = (%d, %v), want (0, io.EOF)", n, rerr)
	}
}

// TestSeekMidFailureLatchesAndClears drives the sticky-error contract of
// SeekToSample: a mid-seek failure that is neither a capability nor an argument
// error (here a source that fails a Seek during the frame-header walk) latches
// d.err, so a later Read returns that same error; a subsequent successful seek
// then clears it and normal reads resume. A corrupt frame header no longer
// triggers this path (it now lands at end), so the failure is injected at the
// source instead.
func TestSeekMidFailureLatchesAndClears(t *testing.T) {
	raw := readFixture(t, sine44s32)
	_, _, total := readAllFloat32(t, bytes.NewReader(raw))
	if total == 0 {
		t.Fatalf("%s has no TotalSamples", sine44s32)
	}

	gate := &gateSeeker{rs: bytes.NewReader(raw)}
	d, err := NewDecoder(gate)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	// Fail the frame-header walk's seeks with a non-EOF error so SeekToSample
	// latches it. The target stays below total so the length clamp does not
	// short-circuit the walk.
	gate.fail = true
	if _, err := d.SeekToSample(total / 2); !errors.Is(err, errInjectedSeek) {
		t.Fatalf("SeekToSample with injected seek failure = %v, want errInjectedSeek", err)
	}
	// The error is sticky: a later Read returns it, not a clean decode.
	var scratch [64]byte
	if _, err := d.Read(scratch[:]); !errors.Is(err, errInjectedSeek) {
		t.Fatalf("Read after latched seek failure = %v, want errInjectedSeek", err)
	}

	// A subsequent successful seek clears the latch and reads resume.
	gate.fail = false
	if _, err := d.SeekToSample(total / 4); err != nil {
		t.Fatalf("recovery SeekToSample: %v", err)
	}
	if n, err := d.Read(scratch[:]); n == 0 || err != nil {
		t.Fatalf("Read after recovery = (%d, %v), want data and no error", n, err)
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
	buf[1] |= 0x01 // protection bit = 1: no CRC, so no 2-byte CRC follows the header
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
	be16(1)              // version
	be16(0)              // delay
	be16(0)              // quality
	be32(uint32(flen))   // bytes (unused by exclusion)
	be32(uint32(frames)) // frames
	be16(0)              // tocEntries (empty TOC)
	be16(1)              // tocScale
	be16(2)              // entrySize (must be non-zero)
	be16(1)              // entryFrames
	return buf
}

// readerOnly hides an io.Seeker behind a bare io.Reader so a decoder built on
// it reports the source as non-seekable.
type readerOnly struct{ r io.Reader }

func (ro readerOnly) Read(p []byte) (int, error) { return ro.r.Read(p) }

// noLenSeeker is seekable for repositioning but refuses SeekEnd, so a decoder on
// it cannot probe the stream length and leaves TotalSamples at 0. That bypasses
// SeekToSample's length clamp and exercises the header-walk-to-end paths.
type noLenSeeker struct{ *bytes.Reader }

func (n noLenSeeker) Seek(offset int64, whence int) (int64, error) {
	if whence == io.SeekEnd {
		return 0, errors.New("pcm_test: seek end unsupported")
	}
	return n.Reader.Seek(offset, whence)
}

// errInjectedSeek is the sentinel a gateSeeker returns while armed, so the
// sticky-latch test can match it with errors.Is.
var errInjectedSeek = errors.New("pcm_test: injected seek failure")

// gateSeeker wraps a ReadSeeker and, while fail is set, returns errInjectedSeek
// from every Seek. It lets a test inject a mid-seek failure and then clear it,
// to verify SeekToSample's sticky-error latch and its recovery on a later
// successful seek.
type gateSeeker struct {
	rs   io.ReadSeeker
	fail bool
}

func (g *gateSeeker) Read(p []byte) (int, error) { return g.rs.Read(p) }

func (g *gateSeeker) Seek(offset int64, whence int) (int64, error) {
	if g.fail {
		return 0, errInjectedSeek
	}
	return g.rs.Seek(offset, whence)
}

// mustOpen opens a fixture file (an io.ReadSeeker) and closes it at test end.
func mustOpen(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Errorf("close %s: %v", path, err)
		}
	})
	return f
}
