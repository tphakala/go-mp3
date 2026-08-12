package bits

// Writer is an MSB-first bit writer, the mirror of Reader: bits written by
// Writer read back identically through Reader. It appends to a caller-owned
// byte slice, accumulating pending bits MSB-aligned and draining full bytes
// as they complete, the same bit convention Reader.Bits consumes.
type Writer struct {
	buf  []byte // destination; WriteBits appends to it
	acc  uint64 // pending bits, MSB-aligned within the low nacc bits
	nacc uint   // number of pending bits in acc (0..7 between calls)
	bits int    // total bits written since NewWriter
}

// NewWriter creates a Writer that appends to buf, starting at len(buf). Any
// existing bytes in buf are preserved as a prefix.
func NewWriter(buf []byte) Writer {
	return Writer{buf: buf}
}

// WriteBits writes the low n bits of v, MSB-first, masking v to its low n
// bits (callers may pass wider values; the mask is part of the contract,
// matching how side-info fields are packed). n must be in [0, 32].
func (w *Writer) WriteBits(v uint32, n int) {
	if n == 0 {
		return
	}
	mask := uint32(1)<<uint(n) - 1
	w.acc = w.acc<<uint(n) | uint64(v&mask)
	w.nacc += uint(n)
	w.bits += n

	for w.nacc >= 8 {
		w.nacc -= 8
		w.buf = append(w.buf, byte(w.acc>>w.nacc))
	}
}

// BitsWritten returns the total number of bits written since NewWriter.
func (w *Writer) BitsWritten() int { return w.bits }

// Flush zero-pads any pending partial byte and returns the extended buf.
// Frame stuffing relies on the padding bits being zero. After Flush, the
// Writer is spent; create a new one per frame.
func (w *Writer) Flush() []byte {
	if w.nacc > 0 {
		pad := 8 - w.nacc
		w.buf = append(w.buf, byte(w.acc<<pad))
		w.nacc = 0
	}
	return w.buf
}
