// Package bits provides an MSB-first bit reader matching the bit-position
// and overrun semantics of minimp3's bs_t/get_bits (see tools/oracle/minimp3.h,
// CC0-1.0), which every decode unit in this package reads its bitstream
// through.
package bits

// Reader is an MSB-first bit reader. Position and limit are tracked in
// bits, not bytes, mirroring upstream bs_t.
type Reader struct {
	buf   []byte
	pos   int
	limit int
	over  bool
}

// NewReader creates a Reader over buf with the limit set to the full
// buffer length in bits (8*len(buf)), mirroring upstream bs_init.
func NewReader(buf []byte) Reader {
	return NewReaderBits(buf, 0, 8*len(buf))
}

// NewReaderBits creates a Reader over buf starting at posBits with an
// explicit bit limit, for callers that seek into a shared buffer (e.g.
// the bit reservoir) rather than starting at byte 0.
func NewReaderBits(buf []byte, posBits, limitBits int) Reader {
	return Reader{buf: buf, pos: posBits, limit: limitBits}
}

// byteAt returns buf[i], or 0 if i falls outside buf. Upstream get_bits
// can dereference one byte past bs->limit>>3 when n is 0 exactly at the
// limit (s==0 makes p point at buf+bytes); minimp3 buffers carry slack
// bytes for that, but a Go slice does not, so out-of-bounds bytes read
// as 0 instead of panicking. Every byte touched by a real (n>0, within
// limit) read stays in bounds, so this only matters for that degenerate
// n==0 case, whose result is shifted out and discarded anyway.
func (r *Reader) byteAt(i int) byte {
	if i >= 0 && i < len(r.buf) {
		return r.buf[i]
	}
	return 0
}

// Bits reads the next n bits (n in [0,24]) MSB-first and advances the
// position, porting upstream get_bits exactly: the position always
// advances by n first, and a read that pushes it past the limit returns
// 0 and latches Overrun. Since pos never moves backward on its own,
// every read after that also lands past the limit and returns 0 too.
func (r *Reader) Bits(n int) uint32 {
	s := uint(r.pos & 7)
	shl := n + int(s)
	byteIdx := r.pos >> 3
	r.pos += n
	if r.pos > r.limit {
		r.over = true
		return 0
	}

	var cache uint32
	next := uint32(r.byteAt(byteIdx)) & (255 >> s)
	byteIdx++
	for {
		shl -= 8
		if shl <= 0 {
			break
		}
		cache |= next << uint(shl)
		next = uint32(r.byteAt(byteIdx))
		byteIdx++
	}
	return cache | (next >> uint(-shl))
}

// Pos returns the current bit position.
func (r *Reader) Pos() int { return r.pos }

// SetPos sets the bit position directly, e.g. to rewind or seek within
// the buffer.
func (r *Reader) SetPos(bits int) { r.pos = bits }

// Overrun reports whether any read has ever advanced the position past
// the limit.
func (r *Reader) Overrun() bool { return r.over }
