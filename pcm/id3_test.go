package pcm

import (
	"bufio"
	"bytes"
	"testing"
)

// id3v2Header builds a 10-byte ID3v2.3 header declaring a body of bodySize
// bytes, with the given flags byte, using synchsafe size encoding (7 bits
// per byte).
func id3v2Header(flags byte, bodySize int) []byte {
	b := make([]byte, 10)
	copy(b, "ID3")
	b[3] = 0x03 // version major
	b[4] = 0x00 // version revision
	b[5] = flags
	b[6] = byte((bodySize >> 21) & 0x7f)
	b[7] = byte((bodySize >> 14) & 0x7f)
	b[8] = byte((bodySize >> 7) & 0x7f)
	b[9] = byte(bodySize & 0x7f)
	return b
}

func TestSkipID3v2(t *testing.T) {
	const bodyN = 37

	// no-tag input: a plausible MPEG frame sync, longer than a header so the
	// 10-byte peek succeeds and the "ID3" check is what rejects it.
	noTag := append([]byte{0xff, 0xfb, 0x90, 0x00}, make([]byte, 12)...)

	withBody := append(id3v2Header(0x00, bodyN), make([]byte, bodyN+8)...)

	// footer flag (0x10) adds a second 10-byte trailer after the body.
	withFooter := append(id3v2Header(0x10, bodyN), make([]byte, bodyN+10+8)...)

	cases := []struct {
		name string
		in   []byte
		want int64
	}{
		{"no tag", noTag, 0},
		{"short stream", []byte{0x01, 0x02}, 0},
		{"empty stream", nil, 0},
		{"header plus body", withBody, 10 + bodyN},
		{"footer flag adds ten", withFooter, 10 + bodyN + 10},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			br := bufio.NewReader(bytes.NewReader(tc.in))
			got, err := skipID3v2(br)
			if err != nil {
				t.Fatalf("skipID3v2: unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("skipID3v2 = %d, want %d", got, tc.want)
			}
		})
	}
}
