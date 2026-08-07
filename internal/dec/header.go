// Package dec implements the MP3 (MPEG-1/2/2.5 Layer III) decoder core,
// ported unit-by-unit from the pinned minimp3 (CC0-1.0, see
// tools/oracle/minimp3.h and PROVENANCE.md) and gated by differential
// tests against that C oracle.
package dec

// Constants mirroring tools/oracle/minimp3.h (HDR_SIZE, MAX_FRAME_SYNC_MATCHES,
// MAX_FREE_FORMAT_FRAME_SIZE).
const (
	hdrSize                = 4
	maxFrameSyncMatches    = 10
	maxFreeFormatFrameSize = 2304
)

// The hdr_* accessors below mirror the HDR_* bit-field macros in
// tools/oracle/minimp3.h exactly: h is a 4-byte MPEG frame header, and
// each accessor reads a fixed set of bits from it. Every function in this
// file assumes len(h) >= 4, exactly as upstream assumes at least HDR_SIZE
// readable bytes; callers are responsible for that invariant, mirroring
// the pin (no defensive bounds checks upstream, none added here).

func hdrGetLayer(h []byte) int       { return int(h[1]>>1) & 3 }
func hdrGetBitrate(h []byte) int     { return int(h[2] >> 4) }
func hdrGetSampleRate(h []byte) int  { return int(h[2]>>2) & 3 }
func hdrTestPadding(h []byte) bool   { return h[2]&0x2 != 0 }
func hdrTestNotMPEG25(h []byte) bool { return h[1]&0x10 != 0 }
func hdrIsFrame576(h []byte) bool    { return h[1]&14 == 2 }
func hdrIsLayer1(h []byte) bool      { return h[1]&6 == 6 }

// hdrIsFreeFormat mirrors HDR_IS_FREE_FORMAT.
func hdrIsFreeFormat(h []byte) bool { return h[2]&0xF0 == 0 }

// hdrIsCRC mirrors HDR_IS_CRC.
func hdrIsCRC(h []byte) bool { return h[1]&1 == 0 }

// hdrTestMPEG1 mirrors HDR_TEST_MPEG1.
func hdrTestMPEG1(h []byte) bool { return h[1]&0x8 != 0 }

// hdrValid mirrors hdr_valid (tools/oracle/minimp3.h).
func hdrValid(h []byte) bool {
	return h[0] == 0xff &&
		(h[1]&0xF0 == 0xf0 || h[1]&0xFE == 0xe2) &&
		hdrGetLayer(h) != 0 &&
		hdrGetBitrate(h) != 15 &&
		hdrGetSampleRate(h) != 3
}

// hdrCompare mirrors hdr_compare: whether a and b are headers from frames
// of the same MPEG stream (same version, layer, sample rate, and free
// format-ness), with b additionally required to be a valid header.
func hdrCompare(a, b []byte) bool {
	return hdrValid(b) &&
		(a[1]^b[1])&0xFE == 0 &&
		(a[2]^b[2])&0x0C == 0 &&
		hdrIsFreeFormat(a) == hdrIsFreeFormat(b)
}

// hdrHalfrate mirrors the static const halfrate table in hdr_bitrate_kbps.
var hdrHalfrate = [2][3][15]uint8{
	{
		{0, 4, 8, 12, 16, 20, 24, 28, 32, 40, 48, 56, 64, 72, 80},
		{0, 4, 8, 12, 16, 20, 24, 28, 32, 40, 48, 56, 64, 72, 80},
		{0, 16, 24, 28, 32, 40, 48, 56, 64, 72, 80, 88, 96, 112, 128},
	},
	{
		{0, 16, 20, 24, 28, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160},
		{0, 16, 24, 28, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192},
		{0, 16, 32, 48, 64, 80, 96, 112, 128, 144, 160, 176, 192, 208, 224},
	},
}

// hdrBitrateKbps mirrors hdr_bitrate_kbps.
func hdrBitrateKbps(h []byte) uint32 {
	mpeg1 := 0
	if hdrTestMPEG1(h) {
		mpeg1 = 1
	}
	return 2 * uint32(hdrHalfrate[mpeg1][hdrGetLayer(h)-1][hdrGetBitrate(h)])
}

// hdrHz mirrors the static const g_hz table in hdr_sample_rate_hz.
var hdrHz = [3]uint32{44100, 48000, 32000}

// hdrSampleRateHz mirrors hdr_sample_rate_hz.
func hdrSampleRateHz(h []byte) uint32 {
	hz := hdrHz[hdrGetSampleRate(h)]
	if !hdrTestMPEG1(h) {
		hz >>= 1
	}
	if !hdrTestNotMPEG25(h) {
		hz >>= 1
	}
	return hz
}

// hdrFrameSamples mirrors hdr_frame_samples.
func hdrFrameSamples(h []byte) uint32 {
	if hdrIsLayer1(h) {
		return 384
	}
	if hdrIsFrame576(h) {
		return 1152 >> 1
	}
	return 1152
}

// hdrFrameBytes mirrors hdr_frame_bytes.
func hdrFrameBytes(h []byte, freeFormatSize int) int {
	frameBytes := int(hdrFrameSamples(h)) * int(hdrBitrateKbps(h)) * 125 / int(hdrSampleRateHz(h))
	if hdrIsLayer1(h) {
		frameBytes &^= 3 // slot align
	}
	if frameBytes != 0 {
		return frameBytes
	}
	return freeFormatSize
}

// hdrPadding mirrors hdr_padding.
func hdrPadding(h []byte) int {
	if !hdrTestPadding(h) {
		return 0
	}
	if hdrIsLayer1(h) {
		return 4
	}
	return 1
}

// matchFrame mirrors mp3d_match_frame. Upstream takes (hdr, mp3_bytes,
// frame_bytes); the Go port drops mp3_bytes since len(hdr) carries it: the
// caller always passes the tail slice starting at the candidate header,
// exactly as upstream's hdr pointer is already advanced to that position.
func matchFrame(hdr []byte, frameBytes int) bool {
	pos := 0
	for nmatch := 0; nmatch < maxFrameSyncMatches; nmatch++ {
		pos += hdrFrameBytes(hdr[pos:], frameBytes) + hdrPadding(hdr[pos:])
		if pos+hdrSize > len(hdr) {
			return nmatch > 0
		}
		if !hdrCompare(hdr, hdr[pos:]) {
			return false
		}
	}
	return true
}

// findFrame mirrors mp3d_find_frame. Upstream takes (mp3, mp3_bytes,
// free_format_bytes, ptr_frame_bytes); the Go port drops mp3_bytes since
// len(mp3) carries it.
func findFrame(mp3 []byte, freeFormatBytes *int, ptrFrameBytes *int) int {
	mp3Bytes := len(mp3)
	for i := 0; i < mp3Bytes-hdrSize; i++ {
		h := mp3[i:]
		if hdrValid(h) {
			frameBytes := hdrFrameBytes(h, *freeFormatBytes)
			frameAndPadding := frameBytes + hdrPadding(h)

			for k := hdrSize; frameBytes == 0 && k < maxFreeFormatFrameSize && i+2*k < mp3Bytes-hdrSize; k++ {
				if hdrCompare(h, h[k:]) {
					fb := k - hdrPadding(h)
					nextfb := fb + hdrPadding(h[k:])
					if i+k+nextfb+hdrSize > mp3Bytes || !hdrCompare(h, h[k+nextfb:]) {
						continue
					}
					frameAndPadding = k
					frameBytes = fb
					*freeFormatBytes = fb
				}
			}
			if (frameBytes != 0 && i+frameAndPadding <= mp3Bytes && matchFrame(h, frameBytes)) ||
				(i == 0 && frameAndPadding == mp3Bytes) {
				*ptrFrameBytes = frameAndPadding
				return i
			}
			*freeFormatBytes = 0
		}
	}
	*ptrFrameBytes = 0
	return mp3Bytes
}
