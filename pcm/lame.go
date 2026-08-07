package pcm

// LAME extension tag layout, appended to a Xing/Info tag frame immediately
// after the Xing fields. See the public LAME info-tag specification.
const (
	lameMagic    = "LAME"
	lameMagicLen = 4
	// lameDelayOffset is the byte offset, measured from the start of the LAME
	// magic, of the 3-byte field that packs the encoder delay (12 bits) and
	// padding (12 bits). The intervening bytes are the 9-byte encoder version
	// string (bytes 0..8, e.g. "LAME3.100"), a revision/VBR-method byte (9), a
	// lowpass byte (10), a 4-byte replay-gain peak (11..14), two 2-byte
	// replay-gain fields (15..18), an encoding-flags byte (19), and a
	// bitrate/ABR byte (20). The delay/padding field occupies bytes 21..23.
	lameDelayOffset = 21
	lameDelayLen    = 3
)

// parseLAME reads the encoder delay and padding from a LAME extension that
// begins at xingEnd (the byte offset just past a Xing/Info tag's fields, i.e.
// xingHeader.lameStart). It returns (0, 0, false) when the "LAME" magic is
// absent at xingEnd or the frame is too short for the delay/padding field; it
// bounds-checks every read and never panics.
//
// The delay and padding are two big-endian 12-bit fields packed into three
// bytes b0 b1 b2: delay = b0<<4 | b1>>4, padding = (b1&0x0f)<<8 | b2.
func parseLAME(frame []byte, xingEnd int) (delay, padding int, ok bool) {
	if xingEnd < 0 || xingEnd+lameMagicLen > len(frame) {
		return 0, 0, false
	}
	if string(frame[xingEnd:xingEnd+lameMagicLen]) != lameMagic {
		return 0, 0, false
	}
	p := xingEnd + lameDelayOffset
	if p+lameDelayLen > len(frame) {
		return 0, 0, false
	}
	b0 := int(frame[p])
	b1 := int(frame[p+1])
	b2 := int(frame[p+2])
	delay = b0<<4 | b1>>4
	padding = (b1&0x0f)<<8 | b2
	return delay, padding, true
}
