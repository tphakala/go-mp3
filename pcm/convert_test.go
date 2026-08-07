package pcm

import "testing"

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
