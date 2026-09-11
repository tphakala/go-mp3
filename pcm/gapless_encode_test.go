package pcm

import (
	"bytes"
	"errors"
	"testing"

	mp3 "github.com/tphakala/go-mp3"
)

// TestStreamingWriteSeekerWritesTag: a streaming encode into a seekable sink
// writes the gapless tag, so the round trip is sample-accurate (the same result
// as the one-shot EncodeInterleaved).
func TestStreamingWriteSeekerWritesTag(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, Bitrate: 128000}
	const n = mp3.FrameSize*4 + 300 // a non-frame-aligned tail
	pcm := genSineS16(n, cfg.Channels, 1000, cfg.SampleRate)

	var m memWriteSeeker
	e, err := NewEncoder(&m, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Write(pcm); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	decoded, info, err := DecodeInterleaved(bytes.NewReader(m.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if info.TotalSamples != uint64(n) {
		t.Fatalf("Info.TotalSamples = %d, want the input length %d", info.TotalSamples, n)
	}
	if got := len(bytesToS16(decoded)) / cfg.Channels; got != n {
		t.Fatalf("decoded %d samples/ch, want exactly %d", got, n)
	}
}

// TestStreamingTaglessOnPlainWriter: a plain io.Writer cannot be back-patched,
// so the streaming encoder leaves the stream tagless even though tagging is on
// by default. A tagless stream is not gapless-trimmed, so its decode carries the
// algorithmic delay and runs longer than the input (unlike the seekable sink in
// TestStreamingWriteSeekerWritesTag, whose decode is exactly the input length).
func TestStreamingTaglessOnPlainWriter(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 1, Bitrate: 128000}
	const n = mp3.FrameSize * 4
	pcm := genSineS16(n, cfg.Channels, 1000, cfg.SampleRate)

	var buf bytes.Buffer // not an io.WriteSeeker
	e, err := NewEncoder(&buf, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Write(pcm); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	decoded, _, err := DecodeInterleaved(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(bytesToS16(decoded)) / cfg.Channels; got <= n {
		t.Fatalf("tagless decode = %d samples/ch, want more than the input %d (delay not trimmed)", got, n)
	}
}

// TestOmitGaplessTagOnWriteSeeker: OmitGaplessTag suppresses the tag even when
// the sink is seekable, so the decode again carries the untrimmed delay.
func TestOmitGaplessTagOnWriteSeeker(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 1, Bitrate: 128000, OmitGaplessTag: true}
	const n = mp3.FrameSize * 4
	pcm := genSineS16(n, cfg.Channels, 1000, cfg.SampleRate)

	var m memWriteSeeker
	e, err := NewEncoder(&m, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Write(pcm); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	decoded, _, err := DecodeInterleaved(bytes.NewReader(m.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(bytesToS16(decoded)) / cfg.Channels; got <= n {
		t.Fatalf("OmitGaplessTag decode = %d samples/ch, want more than the input %d (delay not trimmed)", got, n)
	}
}

// TestStreamingSeekPatchRestoresToStreamEnd guards the Close seek-patch: after
// rewriting the leading tag frame, the sink must be restored to the end of the
// STREAM (SeekCurrent), not the end of the FILE, so follow-on writes append
// correctly. The sink starts at position 0 in a buffer pre-sized well beyond the
// stream, so a file-end restore would land past the stream (at the buffer end)
// while the correct stream-end restore lands at streamLen.
func TestStreamingSeekPatchRestoresToStreamEnd(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 1, Bitrate: 128000}
	pcm := genSineS16(mp3.FrameSize*3, cfg.Channels, 1000, cfg.SampleRate)

	var oneshot bytes.Buffer
	if err := EncodeInterleaved(&oneshot, cfg, pcm); err != nil {
		t.Fatal(err)
	}
	streamLen := int64(oneshot.Len())

	m := &memWriteSeeker{buf: bytes.Repeat([]byte{0xAA}, oneshot.Len()+4096)}
	e, err := NewEncoder(m, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Write(pcm); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	if m.pos != streamLen {
		t.Fatalf("after Close, sink position = %d, want the stream end %d (a file-end restore is the bug)", m.pos, streamLen)
	}
	if !bytes.Equal(m.buf[:streamLen], oneshot.Bytes()) {
		t.Fatal("seekable-streamed bytes do not match the one-shot tagged stream")
	}
}

// TestStreamingSeekPatchMidFile exercises a sink positioned at a NONZERO offset
// before encoding (tagOff != 0), so the placeholder write, the drain-time end
// capture, and the totalBytes = endOff - tagOff subtraction all use a non-zero
// base. The prefix must be untouched, the stream after it must decode
// sample-accurately, and the tag's byte-count field must equal the stream length
// (which is only correct when tagOff is subtracted, not the file offset).
func TestStreamingSeekPatchMidFile(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, Bitrate: 128000}
	const n = mp3.FrameSize*3 + 77
	pcm := genSineS16(n, cfg.Channels, 1000, cfg.SampleRate)

	const prefix = 1234
	m := &memWriteSeeker{}
	if _, err := m.Write(bytes.Repeat([]byte{0x5A}, prefix)); err != nil {
		t.Fatal(err)
	}

	e, err := NewEncoder(m, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Write(pcm); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(m.buf[:prefix], bytes.Repeat([]byte{0x5A}, prefix)) {
		t.Fatal("prefix bytes were overwritten by the encoder")
	}
	stream := m.buf[prefix:]

	// The tag's byte-count field must be the stream length (endOff - tagOff).
	// A file-offset base (missing the tagOff subtraction) would overcount by the
	// prefix length.
	xh, ok := parseXing(stream, cfg.SampleRate, cfg.Channels)
	if !ok {
		t.Fatal("leading frame after the prefix is not an Info tag")
	}
	if int(xh.bytes) != len(stream) {
		t.Fatalf("tag bytes field = %d, want the stream length %d (tagOff not subtracted?)", xh.bytes, len(stream))
	}

	decoded, info, err := DecodeInterleaved(bytes.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if info.TotalSamples != uint64(n) {
		t.Fatalf("Info.TotalSamples = %d, want %d", info.TotalSamples, n)
	}
	if got := len(bytesToS16(decoded)) / cfg.Channels; got != n {
		t.Fatalf("decoded %d samples/ch, want exactly %d", got, n)
	}
}

// TestStreamingCloseDoesNotPatchAfterWriteError guards a data-integrity hazard:
// if a sink Write fails mid-stream (a transient error the caller ignores) but the
// Close drain then succeeds, Close must surface the latched error and must NOT
// back-patch a confident Info tag whose frame count and byte total would not
// match the bytes actually on the sink.
func TestStreamingCloseDoesNotPatchAfterWriteError(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, Bitrate: 128000}
	pcm := genSineS16(mp3.FrameSize*10, cfg.Channels, 1000, cfg.SampleRate)
	boom := errors.New("sink boom")
	// Fail one write after the placeholder frame plus about one audio frame, so
	// earlier frames, later frames, and the Close drain all still reach the sink.
	f := &flakyWriteSeeker{failAfterBytes: 500, err: boom}

	e, err := NewEncoder(f, cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a caller that ignores the Write error (the contract says to
	// abandon the stream) and calls Close anyway.
	_, _ = e.Write(pcm)
	if cerr := e.Close(); !errors.Is(cerr, boom) {
		t.Fatalf("Close = %v, want the latched sink error surfaced (a broken stream must not be finalized with a confident tag)", cerr)
	}
	if !f.failed {
		t.Fatal("the flaky sink never actually failed a write; adjust failAfterBytes")
	}
}
