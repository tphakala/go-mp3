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
