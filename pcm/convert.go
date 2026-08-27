package pcm

import (
	"encoding/binary"
	"math"
)

// S16 quantization constants. The decoder emits float32 in [-1, 1]; full-scale
// +1.0 maps to 32768, one past the int16 ceiling, so it clamps to 32767.
const (
	s16Scale = 32768.0 // +1.0 float -> 32768 before clamping
	s16Max   = 32767   // math.MaxInt16
	s16Min   = -32768  // math.MinInt16
)

// convertF32toS16 quantizes interleaved float32 samples in [-1, 1] to signed
// 16-bit PCM, writing len(src) results into dst (which must have room). Each
// sample is scaled by 32768 in float64, rounded half away from zero, then
// clamped to the int16 range, so full-scale +1.0 saturates at 32767 rather
// than wrapping.
func convertF32toS16(dst []int16, src []float32) {
	for i, s := range src {
		v := math.Round(float64(s) * s16Scale)
		switch {
		case v >= s16Max:
			dst[i] = s16Max
		case v <= s16Min:
			dst[i] = s16Min
		default:
			dst[i] = int16(v)
		}
	}
}

// f32FromS16Scale maps a signed 16-bit sample to float32 in [-1, 1). It is the
// exact inverse of the s16Scale path in convertF32toS16: that path multiplies a
// float by 32768 and rounds, so dividing an int16 by 32768 here round-trips
// every in-range sample back to itself. It matches FFmpeg's and go-aac's
// integer-to-float PCM conversion, so the encoder sees the values a C encoder
// would.
const f32FromS16Scale = 1.0 / 32768.0

// deinterleaveS16 splits n interleaved little-endian signed 16-bit inter-channel
// samples from src into the per-channel float32 slices dst[0..channels-1],
// scaling each to [-1, 1) by f32FromS16Scale. src must hold at least
// n*channels*2 bytes and each dst[c] at least n elements.
func deinterleaveS16(dst [][]float32, src []byte, n, channels int) {
	for c := range channels {
		d := dst[c][:n]
		for i := range n {
			v := int16(binary.LittleEndian.Uint16(src[(i*channels+c)*2:]))
			d[i] = float32(v) * f32FromS16Scale
		}
	}
}
