package pcm

import "encoding/binary"

// Xing/Info tag layout constants. The flags word selects which of the
// frames/bytes/toc/quality fields follow, in this fixed order, each
// big-endian.
const (
	xingFlagFrames  = 1 << 0
	xingFlagBytes   = 1 << 1
	xingFlagTOC     = 1 << 2
	xingFlagQuality = 1 << 3

	xingMagicLen = 4 // "Xing" or "Info"
	xingFlagsLen = 4
	xingTOCLen   = 100
	xingFieldLen = 4 // frames, bytes, and quality are each one big-endian uint32
)

// xingHeader holds the fields parsed from a Xing/Info VBR tag frame. Such a
// frame occupies the position of the stream's first audio frame but carries
// metadata, not audio: its payload is never decoded as samples.
type xingHeader struct {
	frames  uint32    // count of real AUDIO frames, excluding this tag frame; 0 if flag absent
	bytes   uint32    // total stream bytes; 0 if flag absent
	toc     [100]byte // seek table (byte-percentage per time-percentage); valid only if hasTOC
	hasTOC  bool
	quality int  // 0..100; -1 if absent
	isInfo  bool // "Info" (CBR) vs "Xing" (VBR)

	// lameStart is the byte offset within the frame just past the Xing/Info
	// fields (magic, flags, and whichever of frames/bytes/toc/quality the
	// flags selected). A LAME extension tag, when present, begins here; it is
	// the xingEnd argument parseLAME expects.
	lameStart int
}

// sideInfoSize returns the MPEG side-information block size, in bytes, that
// immediately follows a frame's 4-byte header (and optional 2-byte CRC).
// MPEG1 is disambiguated from MPEG2/2.5 by sample rate: the two version's
// rate sets are disjoint (MPEG1: 32000/44100/48000; MPEG2/2.5: everything
// below), so sampleRate >= 32000 iff MPEG1.
func sideInfoSize(sampleRate, channels int) int {
	mpeg1 := sampleRate >= 32000
	mono := channels == 1
	switch {
	case mpeg1 && !mono:
		return 32
	case !mpeg1 && mono:
		return 9
	default: // mpeg1 && mono, or MPEG2/2.5 stereo
		return 17
	}
}

// samplesPerFrame returns the Layer III samples-per-channel count for a
// frame at the given sample rate: 1152 for MPEG1, 576 for MPEG2/2.5.
func samplesPerFrame(sampleRate int) int {
	if sampleRate >= 32000 {
		return 1152
	}
	return 576
}

// parseXing inspects one frame's raw bytes (frame[0:4] is its header) and
// reports whether it is a Xing/Info tag frame. sampleRate and channels come
// from that same frame's already-decoded header (mp3.FrameInfo), which
// parseXing uses only to size the side-information block; it does not
// re-parse the header bits itself.
//
// It bounds-checks every read against len(frame) and returns (nil, false)
// rather than panicking whenever frame is too short for a field its own
// flags word claims is present, or for the magic and flags themselves.
func parseXing(frame []byte, sampleRate, channels int) (*xingHeader, bool) {
	const headerLen = 4
	if len(frame) < headerLen {
		return nil, false
	}
	crcBytes := 0
	if frame[1]&0x01 == 0 { // protection bit clear: frame is CRC-protected, 2 extra bytes
		crcBytes = 2
	}
	off := headerLen + crcBytes + sideInfoSize(sampleRate, channels)

	if off+xingMagicLen > len(frame) {
		return nil, false
	}
	var isInfo bool
	switch string(frame[off : off+xingMagicLen]) {
	case "Xing":
		isInfo = false
	case "Info":
		isInfo = true
	default:
		return nil, false
	}
	off += xingMagicLen

	if off+xingFlagsLen > len(frame) {
		return nil, false
	}
	flags := binary.BigEndian.Uint32(frame[off:])
	off += xingFlagsLen

	xh := &xingHeader{isInfo: isInfo, quality: -1}

	if flags&xingFlagFrames != 0 {
		if off+xingFieldLen > len(frame) {
			return nil, false
		}
		xh.frames = binary.BigEndian.Uint32(frame[off:])
		off += xingFieldLen
	}
	if flags&xingFlagBytes != 0 {
		if off+xingFieldLen > len(frame) {
			return nil, false
		}
		xh.bytes = binary.BigEndian.Uint32(frame[off:])
		off += xingFieldLen
	}
	if flags&xingFlagTOC != 0 {
		if off+xingTOCLen > len(frame) {
			return nil, false
		}
		copy(xh.toc[:], frame[off:off+xingTOCLen])
		xh.hasTOC = true
		off += xingTOCLen
	}
	if flags&xingFlagQuality != 0 {
		if off+xingFieldLen > len(frame) {
			return nil, false
		}
		xh.quality = int(binary.BigEndian.Uint32(frame[off:]))
		off += xingFieldLen
	}

	xh.lameStart = off
	return xh, true
}
