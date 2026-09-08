package enc

import (
	"math/rand/v2"
	"testing"
)

// escFamilyAccumScalarOld reproduces the pre-issue-66 escape-family accumulation
// exactly (accumEscFamilyFlat for a non-escaping pair, accumEscFamilyCost
// otherwise), as the differential oracle for escFamilyAccumGo and the SIMD
// kernels. It mirrors bigValuesPrefixCost's former inner pair loop.
func escFamilyAccumScalarOld(ax, ay []int32) (acc16, acc24 [8]int) {
	for p := range ax {
		x, y := ax[p], ay[p]
		if x < escMaxDirect && y < escMaxDirect {
			accumEscFamilyFlat(&acc16, table16Codes, x, y)
			accumEscFamilyFlat(&acc24, table24Codes, x, y)
			continue
		}
		accumEscFamilyCost(&acc16, &escFam16Linbits, &escFam16MaxVal, table16Codes, x, y)
		accumEscFamilyCost(&acc24, &escFam24Linbits, &escFam24MaxVal, table24Codes, x, y)
	}
	return
}

// escFamilyBaseFor precomputes the per-pair shared codeword+sign costs that
// escFamilyAccumGo and the kernels consume (the codebook gather kept out of the
// vector path). Mirrors bigValuesPrefixCost's base16/base24 pre-pass.
func escFamilyBaseFor(ax, ay []int32) (base16, base24 []int32) {
	base16 = make([]int32, len(ax))
	base24 = make([]int32, len(ax))
	for p := range ax {
		x, y := ax[p], ay[p]
		cx, cy := x, y
		if cx > escMaxDirect {
			cx = escMaxDirect
		}
		if cy > escMaxDirect {
			cy = escMaxDirect
		}
		var sign int32
		if x != 0 {
			sign++
		}
		if y != 0 {
			sign++
		}
		base16[p] = int32(table16Codes[int(cx)*escTableDim+int(cy)].len) + sign
		base24[p] = int32(table24Codes[int(cx)*escTableDim+int(cy)].len) + sign
	}
	return
}

// randMag draws a big-values magnitude weighted to exercise every branch of the
// escape-family cost: the dense direct range 0..14, the escape boundary at
// escMaxDirect (15), moderate escapes, the largest maxVal boundary (8206, from
// linbits 13), and magnitudes beyond it where every table is impossibleCost.
func randMag(r *rand.Rand) int32 {
	switch r.IntN(5) {
	case 0:
		return int32(r.IntN(15)) // direct, non-escaping
	case 1:
		return int32(14 + r.IntN(4)) // straddle escMaxDirect (14..17)
	case 2:
		return int32(15 + r.IntN(256)) // moderate escapes
	case 3:
		return int32(8200 + r.IntN(12)) // straddle the maxVal 8206 boundary
	default:
		return int32(r.IntN(9000)) // full range incl. all-impossible
	}
}

func escFamilyAccumGoWrap(ax, ay []int32) (acc16, acc24 [8]int32) {
	base16, base24 := escFamilyBaseFor(ax, ay)
	escFamilyAccumGo(&acc16, &acc24, ax, ay, base16, base24)
	return
}

// BenchmarkEscFamilyAccum isolates the compiled kernel against the pure-Go
// reference on a full 288-pair run (the big-values worst case), so the per-pair
// vector win is visible without the surrounding recode loop. The distribution is
// escape-heavy broadband, the regime where accumEscFamilyCost dominates.
func BenchmarkEscFamilyAccum(b *testing.B) {
	r := rand.New(rand.NewPCG(3, 4))
	ax := make([]int32, 288)
	ay := make([]int32, 288)
	for p := range 288 {
		ax[p] = randMag(r)
		ay[p] = randMag(r)
	}
	base16, base24 := escFamilyBaseFor(ax, ay)
	b.Run("kernel", func(b *testing.B) {
		var a16, a24 [8]int32
		for b.Loop() {
			a16, a24 = [8]int32{}, [8]int32{}
			escFamilyAccum(&a16, &a24, ax, ay, base16, base24)
		}
	})
	b.Run("go", func(b *testing.B) {
		var a16, a24 [8]int32
		for b.Loop() {
			a16, a24 = [8]int32{}, [8]int32{}
			escFamilyAccumGo(&a16, &a24, ax, ay, base16, base24)
		}
	})
}

// TestEscFamilyAccumGoMatchesScalar pins escFamilyAccumGo bit-for-bit to the
// former accumEscFamilyFlat/accumEscFamilyCost path over randomized pair runs
// spanning every branch, so the folded uniform formula (issue #66) is proven
// output-neutral independently of the full-encode golden gate.
func TestEscFamilyAccumGoMatchesScalar(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for iter := range 4000 {
		n := 1 + r.IntN(288)
		ax := make([]int32, n)
		ay := make([]int32, n)
		for p := range n {
			ax[p] = randMag(r)
			ay[p] = randMag(r)
		}
		want16, want24 := escFamilyAccumScalarOld(ax, ay)
		got16, got24 := escFamilyAccumGoWrap(ax, ay)
		for j := range 8 {
			if int(got16[j]) != want16[j] {
				t.Fatalf("iter %d: acc16[%d] = %d, want %d", iter, j, got16[j], want16[j])
			}
			if int(got24[j]) != want24[j] {
				t.Fatalf("iter %d: acc24[%d] = %d, want %d", iter, j, got24[j], want24[j])
			}
		}
	}
}

// TestEscFamilyAccumParity sweeps the compiled dispatcher (AVX2/NEON on the
// default build, pure Go under -tags noasm) against escFamilyAccumGo across
// every run length that exercises the vector body and the sub-width fallback,
// with non-zero seeded accumulators so the kernel's in-place load/accumulate/
// store is checked, not just a from-zero pass.
func TestEscFamilyAccumParity(t *testing.T) {
	r := rand.New(rand.NewPCG(42, 7))
	for n := 1; n <= 300; n++ {
		ax := make([]int32, n)
		ay := make([]int32, n)
		for range 20 {
			for p := range n {
				ax[p] = randMag(r)
				ay[p] = randMag(r)
			}
			base16, base24 := escFamilyBaseFor(ax, ay)
			var s16, s24 [8]int32
			for j := range 8 {
				s16[j] = int32(r.Uint32()) >> 12
				s24[j] = int32(r.Uint32()) >> 12
			}
			g16, g24 := s16, s24
			k16, k24 := s16, s24
			escFamilyAccumGo(&g16, &g24, ax, ay, base16, base24)
			escFamilyAccum(&k16, &k24, ax, ay, base16, base24)
			if g16 != k16 || g24 != k24 {
				t.Fatalf("n=%d: kernel acc16=%v acc24=%v, want acc16=%v acc24=%v", n, k16, k24, g16, g24)
			}
		}
	}
}

// TestEscFamilyAccumExtremes pins the kernel at every magnitude boundary that
// flips a branch: 0, the escMaxDirect (15) escape edge, and the largest maxVal
// (8206, linbits 13) impossible-cost edge, cross-producted over both lanes, and
// cross-checks the whole chain (kernel, Go reference, and the former scalar
// path) agree.
func TestEscFamilyAccumExtremes(t *testing.T) {
	mags := []int32{0, 1, 14, 15, 16, 100, 8205, 8206, 8207, 9000}
	ax := make([]int32, 0, len(mags)*len(mags))
	ay := make([]int32, 0, len(mags)*len(mags))
	for _, a := range mags {
		for _, b := range mags {
			ax = append(ax, a)
			ay = append(ay, b)
		}
	}
	base16, base24 := escFamilyBaseFor(ax, ay)
	var g16, g24, k16, k24 [8]int32
	escFamilyAccumGo(&g16, &g24, ax, ay, base16, base24)
	escFamilyAccum(&k16, &k24, ax, ay, base16, base24)
	o16, o24 := escFamilyAccumScalarOld(ax, ay)
	for j := range 8 {
		if k16[j] != g16[j] || k24[j] != g24[j] {
			t.Fatalf("j=%d: kernel (%d,%d) != go ref (%d,%d)", j, k16[j], k24[j], g16[j], g24[j])
		}
		if int(k16[j]) != o16[j] || int(k24[j]) != o24[j] {
			t.Fatalf("j=%d: kernel (%d,%d) != old scalar (%d,%d)", j, k16[j], k24[j], o16[j], o24[j])
		}
	}
}

// TestEscFamilyAccumAllocFree pins the dispatcher to zero heap allocations,
// matching the encoder's steady-state guarantee.
func TestEscFamilyAccumAllocFree(t *testing.T) {
	ax := make([]int32, 288)
	ay := make([]int32, 288)
	for i := range ax {
		ax[i] = int32((i * 7) % 9000)
		ay[i] = int32((i * 13) % 9000)
	}
	base16, base24 := escFamilyBaseFor(ax, ay)
	var acc16, acc24 [8]int32
	if n := testing.AllocsPerRun(200, func() {
		acc16 = [8]int32{}
		acc24 = [8]int32{}
		escFamilyAccum(&acc16, &acc24, ax, ay, base16, base24)
	}); n != 0 {
		t.Fatalf("escFamilyAccum allocated %v times per run, want 0", n)
	}
}
