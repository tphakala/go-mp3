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
