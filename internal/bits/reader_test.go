package bits_test

import (
	"testing"

	"github.com/tphakala/go-mp3/internal/bits"
)

func TestReaderBits(t *testing.T) {
	buf := []byte{0b1011_0011, 0b0101_1110, 0xFF}
	r := bits.NewReader(buf)
	for _, tc := range []struct {
		n    int
		want uint32
	}{
		{3, 0b101}, {5, 0b10011}, {8, 0b0101_1110}, {4, 0xF},
	} {
		if got := r.Bits(tc.n); got != tc.want {
			t.Fatalf("Bits(%d) = %#b, want %#b", tc.n, got, tc.want)
		}
	}
	if r.Pos() != 20 {
		t.Fatalf("Pos() = %d, want 20", r.Pos())
	}
	r.Bits(24) // crosses limit
	if !r.Overrun() {
		t.Fatal("expected overrun")
	}
	if r.Overrun() && r.Bits(8) != 0 {
		t.Fatal("post-overrun reads must return 0")
	}
}

// TestReaderExactLimit locks in the two documented boundary cases: a read
// ending exactly at the limit succeeds without latching overrun, and Bits(0)
// at the limit returns 0 without panicking (the degenerate case byteAt guards).
func TestReaderExactLimit(t *testing.T) {
	r := bits.NewReader([]byte{0xAB})
	if got := r.Bits(8); got != 0xAB {
		t.Fatalf("Bits(8) = %#x, want 0xAB", got)
	}
	if r.Overrun() {
		t.Fatal("read ending exactly at limit must not overrun")
	}
	if got := r.Bits(0); got != 0 {
		t.Fatalf("Bits(0) at limit = %#x, want 0", got)
	}
	if r.Overrun() {
		t.Fatal("Bits(0) at limit must not overrun")
	}
}
