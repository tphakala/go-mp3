package bits_test

import (
	"bytes"
	"testing"

	"github.com/tphakala/go-mp3/internal/bits"
)

// TestWriterKnownBytes locks in the MSB-first packing convention against
// hand-computed output: WriteBits(0xFFF, 12) followed by WriteBits(0x5, 4)
// fills exactly two bytes, and a header-like sequence of odd-sized fields
// (mirroring an MPEG frame header's 11+2+2+1+4+2+1+1+2+2+1+1+2 bit layout)
// packs to bytes computed by hand.
func TestWriterKnownBytes(t *testing.T) {
	var buf []byte
	w := bits.NewWriter(buf)
	w.WriteBits(0xFFF, 12)
	w.WriteBits(0x5, 4)
	got := w.Flush()
	want := []byte{0xFF, 0xF5}
	if !bytes.Equal(got, want) {
		t.Fatalf("Flush() = %#v, want %#v", got, want)
	}

	// Header-like sequence: field widths 11,2,2,1,4,2,1,1,2,2,1,1,2 (32 bits
	// total, mirroring an MPEG frame header's layout), values chosen so the
	// hand-computed bytes are easy to verify.
	//
	//   11 bits: 0b111_1111_1111 (sync, all ones)
	//    2 bits: 0b11
	//    2 bits: 0b01
	//    1 bit : 0b0
	//    4 bits: 0b1001
	//    2 bits: 0b10
	//    1 bit : 0b1
	//    1 bit : 0b0
	//    2 bits: 0b11
	//    2 bits: 0b00
	//    1 bit : 0b1
	//    1 bit : 0b0
	//    2 bits: 0b10
	//
	// Concatenated bitstream (32 bits), grouped into bytes:
	//   11111111 11111010 10011010 11001010
	//   0xFF     0xFA     0x9A     0xCA
	hw := bits.NewWriter(nil)
	hw.WriteBits(0b111_1111_1111, 11)
	hw.WriteBits(0b11, 2)
	hw.WriteBits(0b01, 2)
	hw.WriteBits(0b0, 1)
	hw.WriteBits(0b1001, 4)
	hw.WriteBits(0b10, 2)
	hw.WriteBits(0b1, 1)
	hw.WriteBits(0b0, 1)
	hw.WriteBits(0b11, 2)
	hw.WriteBits(0b00, 2)
	hw.WriteBits(0b1, 1)
	hw.WriteBits(0b0, 1)
	hw.WriteBits(0b10, 2)
	hgot := hw.Flush()
	hwant := []byte{0xFF, 0xFA, 0x9A, 0xCA}
	if !bytes.Equal(hgot, hwant) {
		t.Fatalf("header Flush() = %#v, want %#v", hgot, hwant)
	}
}

// TestWriterWideWrites covers the top of WriteBits's documented n range,
// n in [25, 32], which TestWriterReaderRoundTrip cannot reach because
// Reader.Bits itself caps at 24 bits per call. These assertions compare
// against hand-derived byte sequences directly, not a Reader round trip.
func TestWriterWideWrites(t *testing.T) {
	// n == 32, all ones: every bit set packs to four 0xFF bytes.
	w := bits.NewWriter(nil)
	w.WriteBits(0xFFFFFFFF, 32)
	got := w.Flush()
	want := []byte{0xFF, 0xFF, 0xFF, 0xFF}
	if !bytes.Equal(got, want) {
		t.Fatalf("WriteBits(0xFFFFFFFF, 32): Flush() = %#v, want %#v", got, want)
	}

	// n == 32, mixed value: a byte-aligned value packs to its four bytes
	// in order, MSB-first, with no shifting needed to verify by hand.
	w = bits.NewWriter(nil)
	w.WriteBits(0x12345678, 32)
	got = w.Flush()
	want = []byte{0x12, 0x34, 0x56, 0x78}
	if !bytes.Equal(got, want) {
		t.Fatalf("WriteBits(0x12345678, 32): Flush() = %#v, want %#v", got, want)
	}

	// n == 29 (inside the uncovered 25..31 range): value 0x10000001 has
	// only its top bit (bit 28) and bottom bit (bit 0) set within the low
	// 29 bits, so the packed bitstream is a single 1, 27 zeros, and a
	// single 1, followed by 3 zero-padding bits from Flush (32-29=3):
	//   1 0000000 00000000 00000000 1000
	// Grouped into bytes: 10000000 00000000 00000000 00001000
	//                     0x80     0x00     0x00     0x08
	w = bits.NewWriter(nil)
	w.WriteBits(0x10000001, 29)
	got = w.Flush()
	want = []byte{0x80, 0x00, 0x00, 0x08}
	if !bytes.Equal(got, want) {
		t.Fatalf("WriteBits(0x10000001, 29): Flush() = %#v, want %#v", got, want)
	}
}

// TestWriterReaderRoundTrip writes 10k deterministic (v, n) pairs and reads
// them back through bits.Reader, the load-bearing correctness property: bits
// written by Writer must read back identically through Reader. The generator
// is a fixed LCG (not math/rand, whose stream is not guaranteed stable across
// Go versions), so the test is fully reproducible.
func TestWriterReaderRoundTrip(t *testing.T) {
	const count = 10000
	type pair struct {
		v uint32
		n int
	}
	pairs := make([]pair, count)

	var seed uint64 = 1
	next := func() uint64 {
		seed = seed*6364136223846793005 + 1442695040888963407
		return seed
	}

	w := bits.NewWriter(nil)
	for i := range pairs {
		n := int(uint(next()>>32) % 25) // n in [0, 24]
		v := uint32(next() >> 32)
		pairs[i] = pair{v: v, n: n}
		w.WriteBits(v, n)
	}
	buf := w.Flush()

	r := bits.NewReader(buf)
	for i, p := range pairs {
		mask := uint32(0)
		if p.n > 0 {
			mask = ^uint32(0) >> uint(32-p.n)
		}
		want := p.v & mask
		got := r.Bits(p.n)
		if got != want {
			t.Fatalf("pair %d: Bits(%d) = %#x, want %#x", i, p.n, got, want)
		}
	}
	if r.Overrun() {
		t.Fatal("round trip must not overrun")
	}
}

// TestWriterFlushPadding checks that Flush zero-pads the final partial byte:
// frame stuffing relies on the padding bits being zero, not garbage.
func TestWriterFlushPadding(t *testing.T) {
	w := bits.NewWriter(nil)
	w.WriteBits(0x1FFF, 13) // 13 ones, low 13 bits of 0x1FFF are all 1
	got := w.Flush()
	if len(got) != 2 {
		t.Fatalf("len(Flush()) = %d, want 2", len(got))
	}
	// 13 bits: 11111111 11111xxx, trailing 3 bits must be zero.
	if got[1]&0x07 != 0 {
		t.Fatalf("trailing bits = %#08b, want zero padding", got[1])
	}
	if got[0] != 0xFF || got[1] != 0xF8 {
		t.Fatalf("Flush() = %#v, want [0xFF 0xF8]", got)
	}
}

// TestWriterAppendsToPrefix checks that NewWriter over a non-empty slice
// preserves the existing prefix bytes and appends new bits after them.
func TestWriterAppendsToPrefix(t *testing.T) {
	prefix := []byte{0xAB, 0xCD}
	w := bits.NewWriter(prefix)
	w.WriteBits(0xEF, 8)
	got := w.Flush()
	want := []byte{0xAB, 0xCD, 0xEF}
	if !bytes.Equal(got, want) {
		t.Fatalf("Flush() = %#v, want %#v", got, want)
	}
}

// TestWriterPanicsOnBadN checks that WriteBits panics for n outside its
// documented [0, 32] range, guarding against the unbounded byte-drain loop
// a negative n would otherwise trigger.
func TestWriterPanicsOnBadN(t *testing.T) {
	panics := func(n int) (didPanic bool) {
		defer func() {
			if recover() != nil {
				didPanic = true
			}
		}()
		w := bits.NewWriter(nil)
		w.WriteBits(0, n)
		return false
	}

	if !panics(-1) {
		t.Fatal("WriteBits(_, -1) did not panic, want panic")
	}
	if !panics(33) {
		t.Fatal("WriteBits(_, 33) did not panic, want panic")
	}
}

// TestWriterBitsWritten checks the running bit count, including the
// no-op n=0 case.
func TestWriterBitsWritten(t *testing.T) {
	w := bits.NewWriter(nil)
	if w.BitsWritten() != 0 {
		t.Fatalf("BitsWritten() = %d, want 0", w.BitsWritten())
	}
	w.WriteBits(0, 0)
	if w.BitsWritten() != 0 {
		t.Fatalf("BitsWritten() after n=0 = %d, want 0", w.BitsWritten())
	}
	w.WriteBits(0x3, 2)
	if w.BitsWritten() != 2 {
		t.Fatalf("BitsWritten() = %d, want 2", w.BitsWritten())
	}
	w.WriteBits(0xFF, 8)
	if w.BitsWritten() != 10 {
		t.Fatalf("BitsWritten() = %d, want 10", w.BitsWritten())
	}
	w.WriteBits(0, 0)
	if w.BitsWritten() != 10 {
		t.Fatalf("BitsWritten() after trailing n=0 = %d, want 10", w.BitsWritten())
	}
}
