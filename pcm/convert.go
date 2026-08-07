package pcm

import "math"

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
