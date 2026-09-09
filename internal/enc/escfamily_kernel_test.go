package enc

import (
	"math/rand/v2"
	"slices"
	"testing"
)

// escFamilyAccumScalarOld reproduces the pre-issue-66 escape-family accumulation
// exactly (accumEscFamilyFlat for a non-escaping pair, accumEscFamilyCost
// otherwise), as the differential oracle for escFamilyAccumGo. It mirrors
// bigValuesPrefixCost's former inner pair loop.
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

// baseArrays fills fixed-size [288]int32 base16/base24 arrays for the first n
// pairs, the shape escFamilyPrefix and escFamilyPrefixGo consume.
func baseArrays(ax, ay *[288]int32, n int) (base16, base24 [288]int32) {
	b16, b24 := escFamilyBaseFor(ax[:n], ay[:n])
	copy(base16[:], b16)
	copy(base24[:], b24)
	return
}

// randPartition builds a random big-values band partition into pb: nBands
// coding bands covering pairs [0, nPairs), with pb[0]=0, non-decreasing, and
// pb[nBands]=nPairs. Duplicate cut points produce zero-width bands, exercising
// the kernel's empty-band store (a band whose pair run is empty must still write
// the unchanged running accumulator to its prefixCost row).
func randPartition(r *rand.Rand, pb *[40]int, nBands, nPairs int) {
	pb[0] = 0
	for i := 1; i < nBands; i++ {
		pb[i] = r.IntN(nPairs + 1)
	}
	pb[nBands] = nPairs
	slices.Sort(pb[0 : nBands+1])
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

// TestEscFamilyAccumGoMatchesScalar pins escFamilyAccumGo (the per-run
// primitive escFamilyPrefixGo builds on) bit-for-bit to the former
// accumEscFamilyFlat/accumEscFamilyCost path over randomized pair runs spanning
// every branch, so the folded uniform formula (issue #66) is proven
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

// firstPrefixDiff returns the first (row, col) where two prefixCost matrices
// differ, or (-1, -1) if equal, for legible failure messages.
func firstPrefixDiff(a, b *[40][32]int32) (row, col int) {
	for r := range a {
		for c := range a[r] {
			if a[r][c] != b[r][c] {
				return r, c
			}
		}
	}
	return -1, -1
}

// TestEscFamilyPrefixParity sweeps the fused dispatcher (AVX2/NEON on the
// default build, pure Go under -tags noasm) against escFamilyPrefixGo across
// random band partitions and magnitude runs. Both prefixCost matrices are
// pre-seeded with the SAME distinct sentinel in every cell, so the equality
// check catches not only a wrong escape-column value but any stray write: the
// kernel must touch only rows 1..nBands, columns 16..31, and leave everything
// else (columns 0..15, row 0, rows past nBands) exactly as seeded.
func TestEscFamilyPrefixParity(t *testing.T) {
	r := rand.New(rand.NewPCG(42, 7))
	for iter := range 4000 {
		nPairs := r.IntN(289)    // 0..288
		nBands := 1 + r.IntN(39) // 1..39 (chooseRegions reaches 39 on a long granule over a short layout)
		var pb [40]int
		randPartition(r, &pb, nBands, nPairs)

		var ax, ay [288]int32
		for p := range nPairs {
			ax[p] = randMag(r)
			ay[p] = randMag(r)
		}
		base16, base24 := baseArrays(&ax, &ay, nPairs)

		var pcGo, pcK [40][32]int32
		s := int32(1)
		for row := range pcGo {
			for col := range pcGo[row] {
				pcGo[row][col] = s
				pcK[row][col] = s
				s++
			}
		}
		escFamilyPrefixGo(&pcGo, &pb, nBands, &ax, &ay, &base16, &base24)
		escFamilyPrefix(&pcK, &pb, nBands, &ax, &ay, &base16, &base24)
		if pcGo != pcK {
			row, col := firstPrefixDiff(&pcGo, &pcK)
			t.Fatalf("iter %d nBands=%d nPairs=%d: prefixCost[%d][%d] kernel=%d go=%d",
				iter, nBands, nPairs, row, col, pcK[row][col], pcGo[row][col])
		}
	}
}

// TestEscFamilyPrefixExtremes pins the kernel at every magnitude boundary that
// flips a branch (0, the escMaxDirect escape edge, and the largest maxVal
// impossible-cost edge), cross-producted over both lanes and split across a
// partition that includes leading and interior zero-width bands, against
// escFamilyPrefixGo.
func TestEscFamilyPrefixExtremes(t *testing.T) {
	mags := []int32{0, 1, 14, 15, 16, 100, 8205, 8206, 8207, 9000}
	var ax, ay [288]int32
	n := 0
	for _, a := range mags {
		for _, b := range mags {
			ax[n], ay[n] = a, b
			n++
		}
	}
	base16, base24 := baseArrays(&ax, &ay, n)

	// nBands=6 with two zero-width bands (0..0 leading, 7..7 interior).
	pb := [40]int{0, 0, 7, 7, 20, 55, n}
	const nBands = 6

	var pcGo, pcK [40][32]int32
	escFamilyPrefixGo(&pcGo, &pb, nBands, &ax, &ay, &base16, &base24)
	escFamilyPrefix(&pcK, &pb, nBands, &ax, &ay, &base16, &base24)
	if pcGo != pcK {
		row, col := firstPrefixDiff(&pcGo, &pcK)
		t.Fatalf("prefixCost[%d][%d] kernel=%d go=%d", row, col, pcK[row][col], pcGo[row][col])
	}
}

// evenPartition fills pb with nBands roughly equal bands covering [0, nPairs).
func evenPartition(pb *[40]int, nBands, nPairs int) {
	for i := range nBands + 1 {
		pb[i] = i * nPairs / nBands
	}
	pb[nBands] = nPairs
}

// TestEscFamilyPrefixAllocFree pins the fused dispatcher to zero heap
// allocations, matching the encoder's steady-state guarantee.
func TestEscFamilyPrefixAllocFree(t *testing.T) {
	var ax, ay [288]int32
	for i := range ax {
		ax[i] = int32((i * 7) % 9000)
		ay[i] = int32((i * 13) % 9000)
	}
	base16, base24 := baseArrays(&ax, &ay, 288)
	var pb [40]int
	const nBands = 21
	evenPartition(&pb, nBands, 288)
	var pc [40][32]int32
	if n := testing.AllocsPerRun(200, func() {
		escFamilyPrefix(&pc, &pb, nBands, &ax, &ay, &base16, &base24)
	}); n != 0 {
		t.Fatalf("escFamilyPrefix allocated %v times per run, want 0", n)
	}
}

// BenchmarkEscFamilyPrefix isolates the fused kernel against the pure-Go
// reference on a full 288-pair run partitioned into 21 long coding bands (the
// big-values worst case), so the fusion's per-call and snapshot savings are
// visible without the surrounding recode loop. The distribution is escape-heavy
// broadband, the regime where the escape-family cost dominates.
func BenchmarkEscFamilyPrefix(b *testing.B) {
	r := rand.New(rand.NewPCG(3, 4))
	var ax, ay [288]int32
	for p := range 288 {
		ax[p] = randMag(r)
		ay[p] = randMag(r)
	}
	base16, base24 := baseArrays(&ax, &ay, 288)
	var pb [40]int
	const nBands = 21
	evenPartition(&pb, nBands, 288)

	b.Run("kernel", func(b *testing.B) {
		var pc [40][32]int32
		for b.Loop() {
			escFamilyPrefix(&pc, &pb, nBands, &ax, &ay, &base16, &base24)
		}
	})
	b.Run("go", func(b *testing.B) {
		var pc [40][32]int32
		for b.Loop() {
			escFamilyPrefixGo(&pc, &pb, nBands, &ax, &ay, &base16, &base24)
		}
	})
}
