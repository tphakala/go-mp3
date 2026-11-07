//go:build !noasm

#include "go_asm.h"
#include "textflag.h"

// func prefixCostNEON(ix *[576]int32, pb *[40]int, nBands int, prefixCost *[40][32]int32, rows *[256][32]int32, bound *[32]int32)
// Fills prefixCost[1..nBands] with the running big-values cost of every one of
// the 32 table slots: row k+1 is the sum over pairs [0, pb[k+1]) of the cost
// of that pair under each slot. It adds one pairCostRows row per pair, so the
// per-table costing the Go reference does (bigValuesPrefixCostGo) becomes one
// table lookup and eight vector adds.
//
// Per pair, with x = |ix[2i]| and y = |ix[2i+1]|:
//   - x|y < 16 (both magnitudes at most 15): add rows[x*16+y], a 128-byte row
//     (32 slots x 4 bytes). The row address is rows + ((x<<4 | y) << 7).
//   - otherwise (the escape path): add the row of min(x,15) and min(y,15), with
//     impossibleCost in every slot whose bound is not above max(x,y). The
//     clamped row is exact for every slot that can represent the pair, see
//     pairCostRows. The unsigned x|y < 16 test (BHS) sends a magnitude above
//     15 to the escape path without a second compare.
//
// Register roles:
//   R8 = ix cursor (8 bytes per pair), R1 = pb cursor (8-byte stride, one int
//   per band edge), R2 = bands left, R3 = prefixCost output cursor (128-byte
//   stride), R4 = rows base, R5 = bound base (advanced by the first load),
//   R6 = impossibleCost, R7 = current pair index (running across bands),
//   R10 = pb[k+1], the pair index this band ends at, R11 = x, R12 = y,
//   R13/R14 = x|y, max(x,y) and row address scratch.
//   V0-V7 = the 32 running sums (slots 0-3 in V0 ... slots 28-31 in V7),
//   V8-V15 = the row being added, V16-V23 = bound[0..31] (the smallest
//   magnitude each slot cannot represent), V24 = impossibleCost broadcast,
//   V25 = max(x,y) broadcast, V26 = compare mask, then the selected row.
// Go reverses the operand order of VCMGT and VBSL: "VCMGT V25, V16, V26" is
// V26 = V16 > V25 (bound > max(x,y)), and "VBSL V24, V8, V26" is
// V26 = (V26 & V8) | (^V26 & V24), the row where the slot can represent the
// pair, else impossibleCost.
//
// Preconditions: |ix[i]| <= maxQuant (the quantizer guarantees it); nBands <=
// 39; pb[0] == 0 and is never read; pb[1..nBands] are non-decreasing with
// pb[nBands] <= 288 (the pair count of ix). Rows 1..nBands of prefixCost are
// written, row 0 never is, so the caller passes it zeroed. A band with
// pb[k+1] == pb[k] stores the unchanged running sums.
TEXT ·prefixCostNEON(SB), NOSPLIT, $0-48
	MOVD ix+0(FP), R8  // ix cursor
	MOVD pb+8(FP), R1  // pb cursor, starts at pb[0]
	MOVD nBands+16(FP), R2  // bands left
	MOVD prefixCost+24(FP), R3  // output cursor
	MOVD rows+32(FP), R4  // pairCostRows
	MOVD bound+40(FP), R5  // pairCostBound
	VEOR V0.B16, V0.B16, V0.B16  // running sums start at zero
	VEOR V1.B16, V1.B16, V1.B16
	VEOR V2.B16, V2.B16, V2.B16
	VEOR V3.B16, V3.B16, V3.B16
	VEOR V4.B16, V4.B16, V4.B16
	VEOR V5.B16, V5.B16, V5.B16
	VEOR V6.B16, V6.B16, V6.B16
	VEOR V7.B16, V7.B16, V7.B16
	VLD1.P 64(R5), [V16.S4, V17.S4, V18.S4, V19.S4]  // bound[0:16], R5 += 64
	VLD1   (R5), [V20.S4, V21.S4, V22.S4, V23.S4]  // bound[16:32]
	MOVW   $const_impossibleCost, R6  // impossibleCost from go_asm.h
	VDUP   R6, V24.S4  // all 4 lanes
	MOVD   $0, R7  // pair index = 0 (pb[0] == 0)
	ADD    $128, R3  // output cursor to row 1 (row 0 is never written)
	CBZ    R2, done  // nBands == 0: nothing to write

band:
	ADD  $8, R1  // next band edge, pb[k+1] (int is 8 bytes)
	MOVD (R1), R10  // R10 = end pair index of this band
	CMP  R10, R7
	BGE  store  // empty band: store the unchanged sums

pair:
	MOVW (R8), R11  // ix[2i], sign-extended
	MOVW 4(R8), R12  // ix[2i+1]
	ADD  $8, R8
	CMP  $0, R11
	CNEG LT, R11, R11  // abs
	CMP  $0, R12
	CNEG LT, R12, R12
	ORR  R11, R12, R13
	CMP  $16, R13  // x|y >= 16 (unsigned) means a magnitude above 15
	BHS  escape
	LSL  $4, R11, R13  // x*16
	ADD  R12, R13, R13
	ADD  R13<<7, R4, R13  // row address: rows + (x*16+y)*128
	VLD1.P 64(R13), [V8.S4, V9.S4, V10.S4, V11.S4]  // row slots 0-15
	VLD1   (R13), [V12.S4, V13.S4, V14.S4, V15.S4]  // row slots 16-31
	VADD   V8.S4, V0.S4, V0.S4
	VADD   V9.S4, V1.S4, V1.S4
	VADD   V10.S4, V2.S4, V2.S4
	VADD   V11.S4, V3.S4, V3.S4
	VADD   V12.S4, V4.S4, V4.S4
	VADD   V13.S4, V5.S4, V5.S4
	VADD   V14.S4, V6.S4, V6.S4
	VADD   V15.S4, V7.S4, V7.S4
	ADD  $1, R7
	CMP  R10, R7
	BLT  pair

store:
	VST1.P [V0.S4, V1.S4, V2.S4, V3.S4], 64(R3)  // store row k+1, advance to the next
	VST1.P [V4.S4, V5.S4, V6.S4, V7.S4], 64(R3)
	SUBS   $1, R2, R2
	BNE    band

done:
	RET

escape:
	CMP  R12, R11  // flags for R11 - R12
	CSEL GT, R11, R12, R13  // R13 = max(x, y)
	MOVD $15, R14  // escMaxDirect, the clamp
	CMP  R14, R11
	CSEL GT, R14, R11, R11  // x = min(x, 15)
	CMP  R14, R12
	CSEL GT, R14, R12, R12  // y = min(y, 15)
	VDUP R13, V25.S4  // max(x,y) in all lanes
	LSL  $4, R11, R14
	ADD  R12, R14, R14
	ADD  R14<<7, R4, R14
	VLD1.P 64(R14), [V8.S4, V9.S4, V10.S4, V11.S4]
	VLD1   (R14), [V12.S4, V13.S4, V14.S4, V15.S4]
	VCMGT  V25.S4, V16.S4, V26.S4  // V26 = bound > max(x,y), slots 0-3
	VBSL   V24.B16, V8.B16, V26.B16  // row where the slot can represent it, else impossibleCost
	VADD   V26.S4, V0.S4, V0.S4
	VCMGT  V25.S4, V17.S4, V26.S4
	VBSL   V24.B16, V9.B16, V26.B16
	VADD   V26.S4, V1.S4, V1.S4
	VCMGT  V25.S4, V18.S4, V26.S4
	VBSL   V24.B16, V10.B16, V26.B16
	VADD   V26.S4, V2.S4, V2.S4
	VCMGT  V25.S4, V19.S4, V26.S4
	VBSL   V24.B16, V11.B16, V26.B16
	VADD   V26.S4, V3.S4, V3.S4
	VCMGT  V25.S4, V20.S4, V26.S4
	VBSL   V24.B16, V12.B16, V26.B16
	VADD   V26.S4, V4.S4, V4.S4
	VCMGT  V25.S4, V21.S4, V26.S4
	VBSL   V24.B16, V13.B16, V26.B16
	VADD   V26.S4, V5.S4, V5.S4
	VCMGT  V25.S4, V22.S4, V26.S4
	VBSL   V24.B16, V14.B16, V26.B16
	VADD   V26.S4, V6.S4, V6.S4
	VCMGT  V25.S4, V23.S4, V26.S4
	VBSL   V24.B16, V15.B16, V26.B16
	VADD   V26.S4, V7.S4, V7.S4
	ADD  $1, R7
	CMP  R10, R7
	BLT  pair
	B    store
