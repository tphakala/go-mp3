package pcm

import (
	"encoding/binary"

	mp3 "github.com/tphakala/go-mp3"
)

// This file is the WRITE side of the Xing/Info + LAME gapless tag, the mirror
// of the parse side in xing.go and lame.go. A leading Info (the CBR variant of
// the Xing tag) frame carrying a LAME extension lets a decoder trim the
// encoder's algorithmic delay and the final-frame zero padding, so an encode
// then decode round trip is sample-accurate. The MPEG-1 Layer III header field
// tables below are ISO/IEC 11172-3 constants, the same kind of layout knowledge
// sideInfoSize and samplesPerFrame already keep in this package.

// mpeg1BitrateIndex maps a legal MPEG-1 Layer III CBR bitrate in kbps to its
// 4-bit header bitrate index (1..14). The 14 rates are the same set the root
// encoder validates, so a kbps reaching here is always present.
var mpeg1BitrateIndex = map[int]int{
	32: 1, 40: 2, 48: 3, 56: 4, 64: 5, 80: 6, 96: 7, 112: 8,
	128: 9, 160: 10, 192: 11, 224: 12, 256: 13, 320: 14,
}

// mpeg1SampleRateIndex maps a legal MPEG-1 sample rate in Hz to its 2-bit
// header sample-rate index.
var mpeg1SampleRateIndex = map[int]int{44100: 0, 48000: 1, 32000: 2}

// lameTagVersion fills the first 6 bytes of the LAME extension's 9-byte
// encoder-version field; the remaining 3 bytes stay zero from the frame buffer.
// The first four bytes are "LAME": that is the Info Tag format's magic (the
// public LAME/Info tag specification), and it is the prefix a gapless-aware
// decoder looks for before it honors the encoder delay/padding fields. It is
// NOT a claim to be the LAME encoder; the remaining bytes ("go") mark this
// pure-Go encoder and are ignored for gapless. TestGaplessCompatFfmpegMpg123
// verifies that ffmpeg and mpg123 both trim a stream tagged this way to exactly
// the input length.
const lameTagVersion = "LAMEgo"

// infoFrameLen returns the byte length of the leading Info tag frame for the
// given CBR configuration: one MPEG-1 Layer III frame with the padding bit
// clear, i.e. the same 144000*kbps/sampleRate the internal frame layer uses.
func infoFrameLen(sampleRate, kbps int) int {
	return 144000 * kbps / sampleRate
}

// buildInfoFrameHeader packs the 4-byte MPEG-1 Layer III header for the Info
// tag frame: sync, MPEG-1, Layer III, no CRC (protection bit set), the bitrate
// and sample-rate indices, padding bit clear, and the channel mode (mono=3,
// stereo=0). It carries no audio, so mode-extension/copyright/emphasis stay 0.
// The no-CRC choice matches this encoder's own audio frames and the crcBytes==0
// path parseXing takes for it.
func buildInfoFrameHeader(sampleRate, kbps, channels int) [4]byte {
	bi := mpeg1BitrateIndex[kbps]
	sri := mpeg1SampleRateIndex[sampleRate]
	mode := 0 // stereo channel-mode bits; a silent tag frame codes no audio
	if channels == 1 {
		mode = 3 // single channel (mono)
	}
	return [4]byte{
		0xFF,
		0xFB, // 111 (sync) 11 (MPEG-1) 01 (Layer III) 1 (no CRC)
		byte(bi<<4 | sri<<2), // padding and private bits clear
		byte(mode << 6),      // mode-ext, copyright, original, emphasis all 0
	}
}

// buildInfoFrame assembles the complete leading Info + LAME tag frame. The
// frame decodes as silence (all-zero side information, no main data) and
// parseXing detects and excludes it from the audio, so it costs one frame of
// stream position and no audible content. audioFrames is the count of real
// audio frames that follow (the value parseXing multiplies by samplesPerFrame
// to recover the pre-trim length); totalBytes is the whole stream size
// including this frame; delay/padding are the LAME encoder delay and end
// padding a decoder trims (see lamePadding).
//
// Invariant: the smallest supported frame (32 kbps at 48 kHz stereo, 96 bytes)
// comfortably holds the tag, which ends at 4 + 32 (side info) + 16 (Info magic,
// flags, frames, bytes) + 24 (LAME magic through the delay/padding field) = 76.
func buildInfoFrame(sampleRate, kbps, channels int, audioFrames, totalBytes int64, delay, padding int) []byte {
	frame := make([]byte, infoFrameLen(sampleRate, kbps))
	h := buildInfoFrameHeader(sampleRate, kbps, channels)
	copy(frame, h[:])

	// The side-information block stays zero: the tag frame carries no audio
	// (no main data), so it decodes as silence and parseXing excludes it.
	off := 4 + sideInfoSize(sampleRate, channels)

	copy(frame[off:], "Info") // CBR variant of the Xing tag
	off += xingMagicLen
	binary.BigEndian.PutUint32(frame[off:], xingFlagFrames|xingFlagBytes)
	off += xingFlagsLen
	binary.BigEndian.PutUint32(frame[off:], toU32(audioFrames))
	off += xingFieldLen
	binary.BigEndian.PutUint32(frame[off:], toU32(totalBytes))
	off += xingFieldLen

	// LAME extension begins at lameStart (== off). Write the magic/version,
	// then pack the two 12-bit fields into the 3 bytes at lameDelayOffset,
	// exactly as parseLAME reads them. Every other LAME field stays zero.
	copy(frame[off:], lameTagVersion)
	p := off + lameDelayOffset
	frame[p] = byte(delay >> 4)
	frame[p+1] = byte((delay&0x0f)<<4 | (padding>>8)&0x0f)
	frame[p+2] = byte(padding & 0xff)

	return frame
}

// lamePadding returns the LAME end-padding field for a stream that coded
// audioFrames frames from nInput real samples per channel. It is the single
// source of truth for the write-side arithmetic, the mirror of the decoder's
// gaplessTrim: the total decoded length is audioFrames*FrameSize, of which
// EncoderDelay leads and this padding trails, so a gapless decoder recovers
// exactly nInput samples (total - EncoderDelay - padding). Clamped to the
// field's 12-bit range; in practice it is [624, 1775] and never reaches the
// clamp. The inputs are int64 so a multi-hour stream cannot overflow the
// arithmetic on a 32-bit build.
func lamePadding(audioFrames, nInput int64) int {
	p := audioFrames*mp3.FrameSize - nInput - mp3.EncoderDelay
	switch {
	case p < 0:
		return 0
	case p > 4095:
		return 4095
	default:
		return int(p) //nolint:gosec // G115: 0 <= p <= 4095 in this branch.
	}
}

// toU32 clamps a non-negative count to the uint32 range for a big-endian tag
// field. The count inputs (audio frames, total bytes) are int64 so a multi-hour
// or multi-gigabyte stream cannot wrap on a 32-bit build; the clamp only guards
// the pathological case so the field stays well-formed rather than wrapping.
func toU32(v int64) uint32 {
	if v < 0 {
		return 0
	}
	if v > 0xffffffff {
		return 0xffffffff
	}
	return uint32(v) //nolint:gosec // G115: bounded to [0, math.MaxUint32] above.
}
