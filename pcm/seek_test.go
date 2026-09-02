package pcm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"testing"

	mp3 "github.com/tphakala/go-mp3"
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

// readAllFloat32 decodes the whole stream r with WithF32 and
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
					assertF32Equal(t, float32BytesFromLE(t, gotBytes), whole[target*int64(ch):],
						fmt.Sprintf("post-seek decode at %d", target))
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
			assertF32Equal(t, float32BytesFromLE(t, gotBytes), whole[target*int64(ch):],
				fmt.Sprintf("post-seek decode after leading garbage at %d", target))
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
	// The same saturated target again, now over a warmed walk that resumes at the
	// EOF frame the first one stopped on: it must land identically.
	if again, aerr := d.SeekToSample(math.MaxInt64); aerr != nil || again != wantEnd {
		t.Fatalf("repeat SeekToSample(MaxInt64) = (%d, %v), want (%d, nil)", again, aerr, wantEnd)
	}
	var scratch [16]byte
	if n, rerr := d.Read(scratch[:]); n != 0 || !errors.Is(rerr, io.EOF) {
		t.Fatalf("post-seek Read = (%d, %v), want (0, io.EOF)", n, rerr)
	}
}

// TestSeekOverflowSaturatesFromParsedTags reaches the same saturate guard as
// TestSeekOverflowSaturates above, but with every precondition established by
// the decoder's own parsing rather than by assigning gaplessStart directly: the
// head delay comes from the fixture's real LAME extension, and the length stays
// unknown because the Info tag's frame count is zeroed (as in
// TestGaplessHeadOnlyTrim) and the source refuses SeekEnd, so the CBR fallback
// cannot supply one either. That is what the white-box test cannot show, and why
// both are kept: the overflow state is reachable through the public API alone,
// from bytes a real encoder could emit.
func TestSeekOverflowSaturatesFromParsedTags(t *testing.T) {
	mod := zeroInfoFrameCount(t, readFixture(t, sine48mono128))

	// Ground truth for the landing point: the walk's !reached branch reports
	// avail*spf - gaplessStart, which is exactly the playable sample count of this
	// head-trimmed, tail-open stream.
	full, ch, total := readAllFloat32(t, noLenSeeker{bytes.NewReader(mod)})
	if total != 0 {
		t.Fatalf("precondition: TotalSamples = %d, want 0 (zeroed frame count, no length probe)", total)
	}

	d, err := NewDecoder(noLenSeeker{bytes.NewReader(mod)}, WithF32())
	if err != nil {
		t.Fatalf("NewDecoder(WithF32): %v", err)
	}
	// The three preconditions of the overflow branch, every one of them parsed
	// rather than set: seekable (else SeekToSample reports ErrSeekUnsupported
	// before the arithmetic), unknown length (else the clamp returns first), and a
	// non-zero head delay (else the addition cannot overflow at all).
	if d.seeker == nil {
		t.Fatal("precondition: source reported non-seekable; the seek would never reach the guard")
	}
	if d.info.TotalSamples != 0 {
		t.Fatalf("precondition: TotalSamples = %d, want 0 (unknown length)", d.info.TotalSamples)
	}
	if d.gaplessStart != sine48mDelay+lameDecoderDelay {
		t.Fatalf("precondition: gaplessStart = %d, want %d (the fixture's parsed LAME delay plus the decoder delay)",
			d.gaplessStart, sine48mDelay+lameDecoderDelay)
	}

	// math.MaxInt64 + gaplessStart wraps int64 negative. Without the guard that
	// negative rawTarget yields a negative intra-frame offset and panics on a
	// negative slice index while priming; saturating instead sends the frame walk
	// to EOF, so the seek lands at the true stream end.
	landed, err := d.SeekToSample(math.MaxInt64)
	if err != nil {
		t.Fatalf("SeekToSample(MaxInt64): %v", err)
	}
	if wantEnd := int64(len(full) / ch); landed != wantEnd {
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
	// A second seek must fail the same way. The walk gave up on frame 0, so it
	// cached nothing beyond that frame's own offset and cannot resume onto a
	// frame it never sized; the clean decode below then proves both failed walks
	// left the source position restored.
	if _, err := d.SeekToSample(total / 2); !errors.Is(err, ErrSeekUnsupported) {
		t.Fatalf("repeat SeekToSample on a free-format stream = %v, want ErrSeekUnsupported", err)
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

	// S16 ground truth for the warm backward seek below (this decoder emits the
	// default S16), taken from the trailer-carrying stream rather than the clean
	// one. The two are not interchangeable: the header walk counts all 58 frames
	// (a header needs no confirmation), but the decode drops the last one,
	// because confirming a frame needs the header that follows it and the "TAG"
	// trailer supplies none. That gap is pre-existing behaviour, orthogonal to
	// the seek path; what matters here is that the warm seek reproduces the same
	// bytes a full decode of this stream produces.
	wantS16 := decodeAllBytes(t, bytes.NewReader(stream))

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

	// Warm walks over the same trailer. The first seek stopped on the "TAG"
	// header without caching an offset past it, so a repeat must land at the same
	// end, and a backward seek into the audio must still decode bit-exact.
	if again, aerr := d.SeekToSample(trueEnd * 4); aerr != nil || again != trueEnd {
		t.Fatalf("repeat SeekToSample past end = (%d, %v), want (%d, nil)", again, aerr, trueEnd)
	}
	back := trueEnd / 4
	if landedBack, berr := d.SeekToSample(back); berr != nil || landedBack != back {
		t.Fatalf("backward SeekToSample(%d) = (%d, %v), want (%d, nil)", back, landedBack, berr, back)
	}
	gotS16, err := io.ReadAll(d)
	if err != nil {
		t.Fatalf("ReadAll after the warm backward seek: %v", err)
	}
	if want := wantS16[back*int64(ch)*bytesPerS16Sample:]; !bytes.Equal(gotS16, want) {
		t.Fatalf("warm backward seek decoded %d bytes, want %d (bit-exact tail of the clean decode)", len(gotS16), len(want))
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

// maxWarmSeeks bounds the source Seek calls a warm (cache-resumed)
// SeekToSample may make: one to capture the source position, one header probe
// at the landing frame, and one inside reseek to rebind the decode path there.
// The extra slack keeps the assertion from being brittle about an added probe
// while still being far below a re-walk from audioStart, which costs one Seek
// per frame (dozens for these fixtures).
const maxWarmSeeks = 4

// minColdSeeks is the sanity floor for a cold walk's Seek count in the cache
// tests: enough to prove the walk really is one Seek per frame, so a warm count
// below maxWarmSeeks is meaningful rather than an artifact of a tiny stream.
const minColdSeeks = 20

// assertF32Equal fails the test unless got and want are bit-identical float32
// slices, reporting the first differing sample. Bit comparison (not ==) is the
// point: a post-seek decode must reproduce the from-the-start decode exactly.
func assertF32Equal(t *testing.T, got, want []float32, ctx string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: sample count = %d, want %d", ctx, len(got), len(want))
	}
	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s: sample %d = %v (bits %#x), want %v (bits %#x)",
				ctx, i, got[i], math.Float32bits(got[i]), want[i], math.Float32bits(want[i]))
		}
	}
}

// TestSeekWarmWalkSkipsReWalk pins the point of the frame-offset cache: the
// header walk must resume from the highest frame it already established rather
// than re-walking from audioStart. A cold seek pays one source Seek per frame;
// a repeat, a backward, or a forward seek into already-walked ground must then
// cost only the constant handful (position capture, one header probe at the
// landing frame, one reseek). The fresh-decoder case at the end covers the
// partial resume: a forward seek past the cached range pays only the delta.
func TestSeekWarmWalkSkipsReWalk(t *testing.T) {
	raw := readFixture(t, sine44s128)
	_, _, total := readAllFloat32(t, bytes.NewReader(raw))
	if total == 0 {
		t.Fatalf("%s has no known TotalSamples; the seek test needs one", sine44s128)
	}

	cs := &countingSeeker{rs: bytes.NewReader(raw)}
	d, err := NewDecoder(cs, WithF32())
	if err != nil {
		t.Fatalf("NewDecoder(WithF32): %v", err)
	}
	// seekCount measures only the seek itself: construction (and any earlier
	// seek) has already happened, so the counter is zeroed first.
	seekCount := func(d *Decoder, cs *countingSeeker, target int64) int {
		t.Helper()
		cs.seeks = 0
		landed, err := d.SeekToSample(target)
		if err != nil {
			t.Fatalf("SeekToSample(%d): %v", target, err)
		}
		if landed != target {
			t.Fatalf("SeekToSample(%d) landed on %d", target, landed)
		}
		return cs.seeks
	}

	cold := seekCount(d, cs, total*3/4)
	if cold < minColdSeeks {
		t.Fatalf("cold seek made only %d source Seeks; the fixture is too short for this test to mean anything", cold)
	}

	for _, tc := range []struct {
		name   string
		target int64
	}{
		{"repeat", total * 3 / 4},
		{"backward", total / 4},
		{"forward-into-cache", total / 2},
	} {
		if got := seekCount(d, cs, tc.target); got > maxWarmSeeks {
			t.Errorf("%s seek made %d source Seeks, want <= %d (the walk re-walked from audioStart instead of resuming from the cache)",
				tc.name, got, maxWarmSeeks)
		}
	}

	// A forward seek beyond the cached range must pay only the delta from the
	// highest cached frame, not the whole walk.
	fresh := &countingSeeker{rs: bytes.NewReader(raw)}
	d2, err := NewDecoder(fresh, WithF32())
	if err != nil {
		t.Fatalf("NewDecoder(fresh): %v", err)
	}
	seekCount(d2, fresh, total/4)
	if resume := seekCount(d2, fresh, total*3/4); resume >= cold {
		t.Errorf("forward seek past the cached range made %d source Seeks, want fewer than the %d of a full cold walk", resume, cold)
	}
}

// TestSeekWarmCacheBitExact is the headline equivalence guard: one decoder
// driven through repeat, backward, forward, frame-0, and last-sample targets
// must land on exactly the requested sample every time and emit bit-exact the
// same float32 tail as decoding the whole stream from the start and slicing.
// The first seek in the sequence is the cold walk, every later one resumes from
// the cache, so this compares cold against warm directly. It runs on a VBR
// stream (varying frame sizes, the demanding case for offset arithmetic), an
// Info-tag CBR stream (with a gapless window), and a tag-less CBR stream.
func TestSeekWarmCacheBitExact(t *testing.T) {
	for _, fixture := range []string{sine44sVBR, sine44s128, sine44s32} {
		t.Run(fixture, func(t *testing.T) {
			raw := readFixture(t, fixture)
			whole, ch, total := readAllFloat32(t, bytes.NewReader(raw))
			if total == 0 {
				t.Fatalf("%s has no known TotalSamples; the seek test needs one", fixture)
			}

			d, err := NewDecoder(bytes.NewReader(raw), WithF32())
			if err != nil {
				t.Fatalf("NewDecoder(WithF32): %v", err)
			}
			// Cold first, then repeat, backward, repeat-again, forward, frame 0,
			// and the last sample: every warm-walk shape the cache can take.
			for _, target := range []int64{total * 3 / 4, total / 4, total / 2, total / 2, total * 3 / 4, 0, total - 1} {
				landed, err := d.SeekToSample(target)
				if err != nil {
					t.Fatalf("SeekToSample(%d): %v", target, err)
				}
				if landed != target {
					t.Fatalf("SeekToSample(%d) landed on %d", target, landed)
				}
				gotBytes, err := io.ReadAll(d)
				if err != nil {
					t.Fatalf("ReadAll after seek to %d: %v", target, err)
				}
				assertF32Equal(t, float32BytesFromLE(t, gotBytes), whole[target*int64(ch):],
					fmt.Sprintf("post-seek decode at %d", target))
			}
		})
	}
}

// TestSeekCacheInvalidatedByReset asserts Reset drops the offset cache. The
// decoder first warms the cache on one stream, then rebinds to a different one
// with a different geometry and audioStart. Surviving offsets would send the
// walk to the wrong bytes, so the landing and the decoded tail are checked
// against the new stream's ground truth; the Seek count is checked too, so a
// stale cache that happened to land correctly still fails.
func TestSeekCacheInvalidatedByReset(t *testing.T) {
	rawA := readFixture(t, sine44s128)
	rawB := readFixture(t, sine44s32)
	_, _, totalA := readAllFloat32(t, bytes.NewReader(rawA))
	wholeB, chB, totalB := readAllFloat32(t, bytes.NewReader(rawB))
	if totalA == 0 || totalB == 0 {
		t.Fatalf("fixtures need known TotalSamples; got %d and %d", totalA, totalB)
	}

	d, err := NewDecoder(bytes.NewReader(rawA), WithF32())
	if err != nil {
		t.Fatalf("NewDecoder(WithF32): %v", err)
	}
	if _, err := d.SeekToSample(totalA / 2); err != nil {
		t.Fatalf("warming seek on the first stream: %v", err)
	}

	cs := &countingSeeker{rs: bytes.NewReader(rawB)}
	if err := d.Reset(cs, WithF32()); err != nil {
		t.Fatalf("Reset onto the second stream: %v", err)
	}

	target := totalB / 2
	cs.seeks = 0
	landed, err := d.SeekToSample(target)
	if err != nil {
		t.Fatalf("SeekToSample(%d) after Reset: %v", target, err)
	}
	if landed != target {
		t.Fatalf("SeekToSample(%d) after Reset landed on %d (stale offsets from the previous stream)", target, landed)
	}
	if cs.seeks <= maxWarmSeeks {
		t.Errorf("seek after Reset made only %d source Seeks, want a full cold walk (the stale offset cache survived Reset)", cs.seeks)
	}
	gotBytes, err := io.ReadAll(d)
	if err != nil {
		t.Fatalf("ReadAll after post-Reset seek: %v", err)
	}
	assertF32Equal(t, float32BytesFromLE(t, gotBytes), wholeB[target*int64(chB):], "post-Reset seek decode")
}

// TestSeekWarmCacheMidFrameEOF covers the warm walk over a stream cut off
// inside its final frame, the path where the walk sizes a frame whose bytes are
// not all there and then meets EOF one frame later. A cold decoder doing a
// single seek is the reference; a warm decoder repeating the past-end seek and
// then seeking backward must land identically and decode byte-identical PCM,
// including the terminal truncation error. The source hides its length
// (noLenSeeker), so TotalSamples stays 0 and the length clamp cannot
// short-circuit the walk.
func TestSeekWarmCacheMidFrameEOF(t *testing.T) {
	raw := readFixture(t, sine44s32) // tagless CBR, no gapless trim
	flen, ok := frameLength(raw)
	if !ok {
		t.Fatalf("first frame header in %s not decodable; fixture layout changed", sine44s32)
	}
	if len(raw) <= flen {
		t.Fatalf("fixture %s has only one frame", sine44s32)
	}
	// Cut the stream inside its last frame, so that frame's header is present
	// and sizable but its body is not.
	stream := bytes.Clone(raw[:len(raw)-flen/2])

	newDec := func() *Decoder {
		t.Helper()
		d, err := NewDecoder(noLenSeeker{bytes.NewReader(stream)}, WithF32())
		if err != nil {
			t.Fatalf("NewDecoder(WithF32): %v", err)
		}
		if d.info.TotalSamples != 0 {
			t.Fatalf("precondition: TotalSamples = %d, want 0 (unknown length, no clamp)", d.info.TotalSamples)
		}
		return d
	}

	const pastEnd = int64(1) << 40 // far past any sample this fixture holds

	// Cold reference: a fresh decoder per seek, so each walk starts empty.
	coldEnd, err := newDec().SeekToSample(pastEnd)
	if err != nil {
		t.Fatalf("cold SeekToSample(pastEnd): %v", err)
	}
	if coldEnd <= 0 {
		t.Fatalf("cold seek past the end landed on %d, want the stream end", coldEnd)
	}
	mid := coldEnd / 4
	coldDec := newDec()
	coldMid, err := coldDec.SeekToSample(mid)
	if err != nil {
		t.Fatalf("cold SeekToSample(%d): %v", mid, err)
	}
	coldOut, coldErr := readAllFromDecoder(coldDec)

	// Warm: one decoder, so the second and third walks resume from the cache
	// the first one built while running off the end of the truncated stream.
	warm := newDec()
	if got, err := warm.SeekToSample(pastEnd); err != nil || got != coldEnd {
		t.Fatalf("first warm SeekToSample(pastEnd) = (%d, %v), want (%d, nil)", got, err, coldEnd)
	}
	if got, err := warm.SeekToSample(pastEnd); err != nil || got != coldEnd {
		t.Fatalf("repeat SeekToSample(pastEnd) = (%d, %v), want (%d, nil)", got, err, coldEnd)
	}
	got, err := warm.SeekToSample(mid)
	if err != nil {
		t.Fatalf("warm SeekToSample(%d): %v", mid, err)
	}
	if got != coldMid {
		t.Fatalf("warm seek landed on %d, want %d (the cold landing)", got, coldMid)
	}
	warmOut, warmErr := readAllFromDecoder(warm)
	if (coldErr == nil) != (warmErr == nil) {
		t.Fatalf("warm read error = %v, cold read error = %v; they must agree", warmErr, coldErr)
	}
	if errors.Is(coldErr, mp3.ErrCorruptStream) != errors.Is(warmErr, mp3.ErrCorruptStream) {
		t.Fatalf("warm read error = %v, cold read error = %v; truncation must be reported the same either way", warmErr, coldErr)
	}
	if !bytes.Equal(warmOut, coldOut) {
		t.Fatalf("warm post-seek decode differs from the cold one: got %d bytes, want %d", len(warmOut), len(coldOut))
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

// countingSeeker wraps an io.ReadSeeker and counts Seek calls, so the seek
// offset-cache tests can assert that a warm walk skips the per-frame header
// probes a cold walk from audioStart has to make. Tests zero seeks immediately
// before the operation they measure.
type countingSeeker struct {
	rs    io.ReadSeeker
	seeks int
}

func (c *countingSeeker) Read(p []byte) (int, error) { return c.rs.Read(p) }

func (c *countingSeeker) Seek(offset int64, whence int) (int64, error) {
	c.seeks++
	return c.rs.Seek(offset, whence)
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
