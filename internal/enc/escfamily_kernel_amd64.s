//go:build !noasm

#include "textflag.h"

// func escFamilyPrefixAVX2(prefixCost *[40][32]int32, pb *[40]int, nBands int, ax, ay, base16, base24 *[288]int32)
//
// Fused escape-family prefix-cost kernel (issue #68). Walks coding bands
// 0..nBands-1: for each band it accumulates the escape-family cost of that
// band's big-values pairs into the two per-family 8-lane accumulators (acc16,
// tables 16..23, in Y0; acc24, tables 24..31, in Y1; resident across the whole
// run and zeroed once), then writes the CUMULATIVE accumulators into
// prefixCost[k+1][16..23] and [24..31] as two contiguous 8-lane stores at the
// 128-byte row stride. Band k covers pairs [pb[k], pb[k+1]); pb[0] is 0, so the
// pair pointers advance continuously and only pb[k+1] is read. A zero-width band
// stores the unchanged accumulators. Columns 0..15 and row 0 are never touched,
// so the caller's non-escape table costs survive.
//
// Per-pair cost is the bit-exact mirror of escFamilyAccumGo, per table j:
//   impossibleCost                 if ax>maxv[j] || ay>maxv[j]
//   base + escCount*linbits[j]     otherwise
// with escCount = (ax>=15) + (ay>=15). The common non-escaping pair (both
// magnitudes < 15) takes the fast path acc += broadcast(base). base16/base24 are
// the caller's Go pre-pass. The per-pair body is identical to the former
// per-band kernel; only the band loop and in-place stores wrap it.
TEXT ·escFamilyPrefixAVX2(SB), NOSPLIT, $0-56
	MOVQ nBands+16(FP), DX
	TESTQ DX, DX
	JLE ret                              // nBands <= 0: nothing to do
	VPXOR Y0, Y0, Y0                     // acc16 = 0
	VPXOR Y1, Y1, Y1                     // acc24 = 0
	VMOVDQU ·escFam16MaxVal(SB), Y2      // per-table maxVal, family 16
	VMOVDQU ·escFam24MaxVal(SB), Y3      // per-table maxVal, family 24
	VMOVDQU ·escFam16LinbitsI32(SB), Y4  // per-table linbits, family 16
	VMOVDQU ·escFam24LinbitsI32(SB), Y5  // per-table linbits, family 24
	VPBROADCASTD ·escFamImpossible(SB), Y6

	MOVQ pb+8(FP), SI
	ADDQ $8, SI                          // &pb[1] (only pb[k+1] is read)
	MOVQ ax+24(FP), R8
	MOVQ ay+32(FP), R9
	MOVQ base16+40(FP), R10
	MOVQ base24+48(FP), R11
	MOVQ prefixCost+0(FP), R14
	ADDQ $128, R14                       // &prefixCost[1] (row stride 32*4)
	XORQ R12, R12                        // p = 0
	XORQ BX, BX                          // k = 0

band_loop:
	MOVQ (SI), R13                       // end = pb[k+1]
pair_loop:
	CMPQ R12, R13
	JGE band_store
	MOVL (R8), AX                        // ax[p]
	MOVL (R9), CX                        // ay[p]
	CMPL AX, $15
	JGE slow
	CMPL CX, $15
	JGE slow
	// fast path: both < escMaxDirect -> addend is just base for every table
	VPBROADCASTD (R10), Y7               // base16[p]
	VPADDD Y7, Y0, Y0
	VPBROADCASTD (R11), Y8               // base24[p]
	VPADDD Y8, Y1, Y1
	JMP pair_next
slow:
	VPBROADCASTD (R10), Y7               // addend16 = base16
	VPBROADCASTD (R11), Y8               // addend24 = base24
	CMPL AX, $15
	JL noEscX
	VPADDD Y4, Y7, Y7                    // ax escapes: += linbits
	VPADDD Y5, Y8, Y8
noEscX:
	CMPL CX, $15
	JL noEscY
	VPADDD Y4, Y7, Y7                    // ay escapes: += linbits
	VPADDD Y5, Y8, Y8
noEscY:
	VPBROADCASTD (R8), Y9                // bax
	VPBROADCASTD (R9), Y10               // bay
	// family 16: mask where ax>maxv16 || ay>maxv16, then select impossibleCost
	VPCMPGTD Y2, Y9, Y11                 // bax > maxv16
	VPCMPGTD Y2, Y10, Y12                // bay > maxv16
	VPOR Y12, Y11, Y11
	VPBLENDVB Y11, Y6, Y7, Y7            // mask ? impossible : addend16
	VPADDD Y7, Y0, Y0
	// family 24
	VPCMPGTD Y3, Y9, Y11                 // bax > maxv24
	VPCMPGTD Y3, Y10, Y12                // bay > maxv24
	VPOR Y12, Y11, Y11
	VPBLENDVB Y11, Y6, Y8, Y8            // mask ? impossible : addend24
	VPADDD Y8, Y1, Y1
pair_next:
	ADDQ $4, R8
	ADDQ $4, R9
	ADDQ $4, R10
	ADDQ $4, R11
	INCQ R12
	JMP pair_loop
band_store:
	VMOVDQU Y0, 64(R14)                  // prefixCost[k+1][16..23]
	VMOVDQU Y1, 96(R14)                  // prefixCost[k+1][24..31]
	ADDQ $8, SI                          // &pb[k+2]
	ADDQ $128, R14                       // next prefixCost row
	INCQ BX
	CMPQ BX, DX
	JL band_loop
ret:
	VZEROUPPER
	RET
