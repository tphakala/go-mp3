package pcm

import (
	"bufio"
	"errors"
	"io"
)

const (
	// id3HeaderSize is the fixed size of an ID3v2 tag header (and of the
	// optional footer): "ID3"/"3DI", two version bytes, one flags byte, and
	// a four-byte synchsafe size.
	id3HeaderSize = 10
	// id3FooterFlag is the bit in the flags byte that signals a 10-byte
	// footer follows the tag body (ID3v2.4).
	id3FooterFlag = 0x10
	// id3v1Size is the fixed size of an ID3v1 trailer, a "TAG" magic followed by
	// 125 bytes of fixed metadata fields, appended after the last audio frame.
	id3v1Size = 128
)

// skipID3v2 consumes a leading ID3v2 tag from br, if present, and returns the
// number of bytes skipped. When br does not begin with an ID3v2 tag (including
// a stream too short to hold the 10-byte header), it consumes nothing and
// returns (0, nil). It only returns a non-nil error when discarding a
// well-formed tag hits an underlying read failure.
func skipID3v2(br *bufio.Reader) (int64, error) {
	hdr, err := br.Peek(id3HeaderSize)
	if err != nil {
		// Fewer than a full header is available. A short stream simply has no
		// ID3v2 tag; report that rather than surfacing the peek's io.EOF.
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return 0, nil
		}
		return 0, err
	}
	if hdr[0] != 'I' || hdr[1] != 'D' || hdr[2] != '3' {
		return 0, nil
	}

	// Synchsafe size: the high bit of each of the four size bytes is zero, so
	// each contributes 7 bits. It counts the tag body only, excluding the
	// header (and any footer).
	size := int64(hdr[6]&0x7f)<<21 | int64(hdr[7]&0x7f)<<14 | int64(hdr[8]&0x7f)<<7 | int64(hdr[9]&0x7f)
	total := int64(id3HeaderSize) + size
	if hdr[5]&id3FooterFlag != 0 {
		total += id3HeaderSize
	}

	if _, err := br.Discard(int(total)); err != nil {
		return 0, err
	}
	return total, nil
}

// id3v1TrailerBytes reports the size of a trailing ID3v1 tag (128 bytes, magic
// "TAG") at the end of a seekable source, or 0 when it is absent, the stream is
// too short to hold one, or the probe fails. end is the source's byte length. It
// seeks to read the trailer and leaves the read position at the probe point; the
// caller is expected to restore its own position afterward (estimateCBRDuration
// does, via its final SeekStart). The trailer is metadata, not audio, so
// excluding it keeps a CBR frame-count estimate from being inflated by roughly
// one frame.
func id3v1TrailerBytes(rs io.ReadSeeker, end int64) int64 {
	if end < id3v1Size {
		return 0
	}
	if _, err := rs.Seek(end-id3v1Size, io.SeekStart); err != nil {
		return 0
	}
	var magic [3]byte
	if _, err := io.ReadFull(rs, magic[:]); err != nil {
		return 0
	}
	if magic[0] == 'T' && magic[1] == 'A' && magic[2] == 'G' {
		return id3v1Size
	}
	return 0
}
