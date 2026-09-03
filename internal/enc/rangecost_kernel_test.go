package enc

import (
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// TestSubMinReduceParity sweeps random int32 row pairs through the compiled
// kernel (AVX2/NEON on the default build, pure Go under -tags noasm) against the
// pure-Go reference across every length that exercises the vector blocks and the
// scalar tail residue (n mod 8 on AVX2, n mod 4 on NEON), including sub-width
// inputs that fall back to the reference. n = 32 is the production width.
func TestSubMinReduceParity(t *testing.T) {
	var seed uint32 = 12345
	next := func() int32 { seed = seed*1664525 + 1013904223; return int32(seed) }
	for n := 1; n <= 40; n++ {
		a := make([]int32, n)
		b := make([]int32, n)
		for range 300 {
			for i := range a {
				a[i] = next() >> 8
				b[i] = next() >> 8
			}
			want := subMinReduceGo(a, b)
			got := subMinReduce(a, b)
			if got != want {
				t.Fatalf("n=%d a=%v b=%v: subMinReduce=%d want=%d", n, a, b, got, want)
			}
		}
	}
}

// TestSubMinReduceExtremes checks the kernel at the int32 boundaries, where a
// wrapping subtraction would diverge from the reference if the asm used the
// wrong width.
func TestSubMinReduceExtremes(t *testing.T) {
	cases := [][2][]int32{
		{{minI32, maxI32, 0, -1, 5, 6, 7, 8}, {maxI32, minI32, 0, 1, -5, -6, -7, -8}},
		{{maxI32, maxI32, maxI32, maxI32}, {minI32, 1, -1, maxI32}},
	}
	for i, c := range cases {
		if got, want := subMinReduce(c[0], c[1]), subMinReduceGo(c[0], c[1]); got != want {
			t.Fatalf("case %d: subMinReduce=%d want=%d", i, got, want)
		}
	}
}

const (
	minI32 = -1 << 31
	maxI32 = 1<<31 - 1
)

// TestSubMinReduceAllocFree pins the kernel to zero heap allocations, matching
// the encoder's steady-state guarantee.
func TestSubMinReduceAllocFree(t *testing.T) {
	a := make([]int32, 32)
	b := make([]int32, 32)
	for i := range a {
		a[i] = int32(i * 7)
		b[i] = int32(i*3 - 4)
	}
	if n := testing.AllocsPerRun(200, func() { _ = subMinReduce(a, b) }); n != 0 {
		t.Fatalf("subMinReduce allocated %v times per run, want 0", n)
	}
}

// TestRangeCostMinMatchesScalar is the load-bearing invariant of the SIMD
// integration: the fused 32-wide reduction (rangeCostMin, including the invalid
// columns' all-impossible ramp) must return exactly the minimum the scalar
// rangeCost finds over validBigTables, for every range of every realistic
// prefix-cost table. If the two ever diverged, chooseRegions' memo search
// (rangeCostMin) and its final costing (rangeCost) would disagree and the coded
// bit count would be wrong.
func TestRangeCostMinMatchesScalar(t *testing.T) {
	layouts := []*bandLayout{
		&layoutLong[0], &layoutLong[1], &layoutLong[2],
		&layoutShort[0], &layoutShort[1], &layoutShort[2],
	}
	var seed uint64 = 777
	next := func() float64 { return testsignal.LCG(&seed) }
	scales := []int32{1, 2, 16, 255, maxQuant}

	for _, lay := range layouts {
		for _, scale := range scales {
			for trial := range 30 {
				var ix [576]int32
				for i := range ix {
					frac := float64(576-i) / 576
					v := int32(next() * float64(scale) * frac * frac)
					if next() < 0.5 {
						v = -v
					}
					ix[i] = v
				}
				part := partitionSpectrum(&ix)
				pb := pairBoundaries(lay, part.bigValues)
				var pc [40][32]int32
				bigValuesPrefixCost(&ix, &pb, lay, &pc)
				for a := 0; a <= lay.nBands; a++ {
					for b := a; b <= lay.nBands; b++ {
						cost, _ := rangeCost(&pc, &pb, a, b)
						if m := rangeCostMin(&pc, &pb, a, b); m != cost {
							t.Fatalf("lay.short=%v scale=%d trial=%d (a=%d b=%d): rangeCost=%d rangeCostMin=%d",
								lay.short, scale, trial, a, b, cost, m)
						}
					}
				}
			}
		}
	}
}
