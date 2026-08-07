package pcm

import "encoding/binary"

// VBRI is the Fraunhofer variable-bitrate tag. Unlike a Xing/Info tag (whose
// offset depends on the MPEG version and channel mode via the side-info size),
// a VBRI tag sits at a FIXED offset: 32 bytes after the start of the 4-byte
// frame header, i.e. at frame[36]. All multi-byte fields are big-endian.
const (
	vbriOffset   = 36 // 4-byte frame header + 32 fixed bytes
	vbriMagic    = "VBRI"
	vbriMagicLen = 4
	// vbriHeaderLen is the size of the fixed VBRI header, from the magic
	// through the frames-per-TOC-entry field, before the variable-length TOC:
	// magic(4) + version(2) + delay(2) + quality(2) + bytes(4) + frames(4) +
	// tocEntries(2) + tocScale(2) + entrySize(2) + entryFrames(2) = 26.
	vbriHeaderLen = 26
)

// vbriHeader holds the fields parsed from a Fraunhofer VBRI tag frame. Like a
// Xing/Info tag frame, a VBRI frame carries stream metadata, not audio, so its
// payload is never decoded as samples.
type vbriHeader struct {
	version     uint16
	delay       uint16
	quality     uint16
	bytes       uint32 // total stream size in bytes
	frames      uint32 // count of audio frames
	tocEntries  uint16 // number of TOC entries
	tocScale    uint16 // scale factor applied to TOC values
	entrySize   uint16 // bytes per TOC entry
	entryFrames uint16 // frames represented by each TOC entry
	// toc holds the seek table when each entry is 1 or 2 bytes wide. Wider
	// entries are bounds-validated (so a malformed tag is still rejected) but
	// left uncaptured, since a []uint16 cannot represent them; toc stays nil in
	// that case. The seek TOC consumer (a later task) revisits wider entries if
	// a real fixture ever needs them.
	toc []uint16
}

// parseVBRI inspects a frame for a Fraunhofer VBRI tag at the fixed offset 36
// and returns its parsed header. It bounds-checks every read against
// len(frame) and returns (nil, false) rather than panicking on a short frame,
// a wrong magic, a zero-width TOC entry, or a TOC that overruns the frame.
func parseVBRI(frame []byte) (*vbriHeader, bool) {
	if vbriOffset+vbriHeaderLen > len(frame) {
		return nil, false
	}
	if string(frame[vbriOffset:vbriOffset+vbriMagicLen]) != vbriMagic {
		return nil, false
	}

	p := vbriOffset + vbriMagicLen
	vh := &vbriHeader{}
	vh.version = binary.BigEndian.Uint16(frame[p:])
	p += 2
	vh.delay = binary.BigEndian.Uint16(frame[p:])
	p += 2
	vh.quality = binary.BigEndian.Uint16(frame[p:])
	p += 2
	vh.bytes = binary.BigEndian.Uint32(frame[p:])
	p += 4
	vh.frames = binary.BigEndian.Uint32(frame[p:])
	p += 4
	vh.tocEntries = binary.BigEndian.Uint16(frame[p:])
	p += 2
	vh.tocScale = binary.BigEndian.Uint16(frame[p:])
	p += 2
	vh.entrySize = binary.BigEndian.Uint16(frame[p:])
	p += 2
	vh.entryFrames = binary.BigEndian.Uint16(frame[p:])
	p += 2

	if vh.entrySize == 0 {
		return nil, false // a zero-width entry cannot describe a TOC
	}
	// The TOC (tocEntries entries, entrySize bytes each) must fit in the frame.
	// Compute in uint64 so the product cannot overflow int on a 32-bit build
	// (both fields are uint16, so the product can exceed math.MaxInt32).
	tocLen := uint64(vh.tocEntries) * uint64(vh.entrySize)
	if uint64(p)+tocLen > uint64(len(frame)) {
		return nil, false
	}
	// Capture the TOC only when each entry fits a uint16 (entrySize 1 or 2).
	// Wider entries are already bounds-validated above; toc stays nil.
	if vh.entrySize == 1 || vh.entrySize == 2 {
		vh.toc = make([]uint16, vh.tocEntries)
		for i := range int(vh.tocEntries) {
			off := p + i*int(vh.entrySize)
			if vh.entrySize == 1 {
				vh.toc[i] = uint16(frame[off])
			} else {
				vh.toc[i] = binary.BigEndian.Uint16(frame[off:])
			}
		}
	}
	return vh, true
}
