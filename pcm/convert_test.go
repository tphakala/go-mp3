package pcm

import (
	"encoding/binary"
	"testing"
)

func TestConvertF32toS16(t *testing.T) {
	cases := []struct {
		name string
		in   float32
		want int16
	}{
		{"zero", 0.0, 0},
		{"positive full scale clamps", 1.0, 32767},
		{"negative full scale", -1.0, -32768},
		{"half positive", 0.5, 16384},
		{"rounds toward the ceiling but stays in range", 32767.4 / 32768, 32767},
		{"below the floor clamps", -32768.6 / 32768, -32768},
	}

	dst := make([]int16, 1)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			convertF32toS16(dst, []float32{tc.in})
			if dst[0] != tc.want {
				t.Errorf("convertF32toS16(%v) = %d, want %d", tc.in, dst[0], tc.want)
			}
		})
	}
}

// TestConvertF32toS16RoundHalfAwayFromZero pins the rounding mode: exactly
// .5 rounds away from zero in both directions, not to-even.
func TestConvertF32toS16RoundHalfAwayFromZero(t *testing.T) {
	src := []float32{2.5 / 32768, -2.5 / 32768}
	want := []int16{3, -3}
	dst := make([]int16, len(src))
	convertF32toS16(dst, src)
	for i := range want {
		if dst[i] != want[i] {
			t.Errorf("convertF32toS16[%d] = %d, want %d", i, dst[i], want[i])
		}
	}
}

func TestS16Float32ConversionRoundTripIdentity(t *testing.T) {
	// deinterleaveS16 (encode direction) composed with convertF32toS16 (decode
	// direction) must be the identity on every representable int16, proving the
	// two scales are exact inverses independent of the MP3 codec.
	src := make([]byte, 65536*2)
	for i := range 65536 {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(int16(i-32768)))
	}
	planar := make([][]float32, 1)
	planar[0] = make([]float32, 65536)
	deinterleaveS16(planar, src, 65536, 1)

	got := make([]int16, 65536)
	convertF32toS16(got, planar[0])
	for i := range 65536 {
		want := int16(i - 32768)
		if got[i] != want {
			t.Fatalf("round trip mismatch at %d: got %d want %d", i, got[i], want)
		}
	}
}
