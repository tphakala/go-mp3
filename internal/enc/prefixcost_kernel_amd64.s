//go:build !noasm

#include "go_asm.h"
#include "textflag.h"

// func prefixCostAVX2(ix *[576]int32, pb *[40]int, nBands int, prefixCost *[40][32]int32, rows *[256][32]int32, bound *[32]int32)
// Fills prefixCost[1..nBands] with the running big-values cost of every one of
// the 32 table slots: row k+1 is the sum over pairs [0, pb[k+1]) of the cost
// of that pair under each slot. It adds one pairCostRows row per pair, so the
// per-table costing the Go reference does (bigValuesPrefixCostGo) becomes one
// table lookup and four vector adds.
//
// Per pair, with x = |ix[2i]| and y = |ix[2i+1]|:
//   - x|y < 16 (both magnitudes at most 15): add rows[x*16+y], a 128-byte row
//     (32 slots x 4 bytes). The row index is (x*16+y)<<7, SHLL $4 then SHLQ $7.
//   - otherwise (the escape path): add the row of min(x,15) and min(y,15), with
//     impossibleCost in every slot whose bound is not above max(x,y). The
//     clamped row is exact for every slot that can represent the pair, see
//     pairCostRows. The unsigned x|y < 16 test (JAE) sends a magnitude above
//     15 to the escape path without a second compare.
// The GPR to XMM moves are VMOVQ: a legacy-SSE MOVQ here runs with the YMM
// upper halves in use, which costs about 110 ns per escape pair on Zen 3
// (EPYC 7763).
//
// Register roles:
//   SI = ix base, BX = pb cursor (8-byte stride, one int per band edge),
//   CX = bands left, DI = prefixCost output row cursor (128-byte stride),
//   R8 = rows base, R9 = bound base, DX = current pair index (running across
//   bands), R10 = pb[k+1], the pair index this band ends at.
//   Y0-Y3 = the 32 running sums (slots 0-7, 8-15, 16-23, 24-31),
//   Y4-Y7 = bound[0..31] (the smallest magnitude each slot cannot represent),
//   Y8 = impossibleCost broadcast, Y9 = max(x,y) broadcast, Y10 = compare
//   mask, Y11 = selected row.
//   AX = x then the row index, R11 = y, R12 = sign mask, then x|y, then
//   max(x,y), R13 = the constant 15.
//
// Preconditions: |ix[i]| <= maxQuant (the quantizer guarantees it; the 32-bit
// abs and the signed clamp below do not handle math.MinInt32, which would pick
// a wrong row); nBands <= 39; pb[0] == 0 and is never read; pb[1..nBands] are
// non-decreasing with pb[nBands] <= 288 (the pair count of ix). Rows
// 1..nBands of prefixCost are written, row 0 never is, so the caller passes it
// zeroed. A band with pb[k+1] == pb[k] stores the unchanged running sums.
TEXT ·prefixCostAVX2(SB), NOSPLIT, $0-48
	MOVQ ix+0(FP), SI  // ix base
	MOVQ pb+8(FP), BX  // pb cursor, starts at pb[0]
	MOVQ nBands+16(FP), CX  // bands left
	MOVQ prefixCost+24(FP), DI  // output cursor, starts at row 0
	MOVQ rows+32(FP), R8  // pairCostRows
	MOVQ bound+40(FP), R9  // pairCostBound
	VPXOR   Y0, Y0, Y0  // running sums start at zero
	VPXOR   Y1, Y1, Y1
	VPXOR   Y2, Y2, Y2
	VPXOR   Y3, Y3, Y3
	VMOVDQU 0(R9), Y4  // bound[0:8]
	VMOVDQU 32(R9), Y5  // bound[8:16]
	VMOVDQU 64(R9), Y6  // bound[16:24]
	VMOVDQU 96(R9), Y7  // bound[24:32]
	MOVL    $const_impossibleCost, AX  // impossibleCost from go_asm.h
	VMOVQ   AX, X8
	VPBROADCASTD X8, Y8  // all 8 lanes
	XORQ    DX, DX  // pair index = 0 (pb[0] == 0)
	TESTQ   CX, CX
	JZ      done  // nBands == 0: nothing to write

band:
	ADDQ $8, BX  // next band edge, pb[k+1] (int is 8 bytes)
	ADDQ $128, DI  // next output row, prefixCost[k+1] (32 x 4 bytes)
	MOVQ (BX), R10  // R10 = end pair index of this band
	CMPQ DX, R10
	JGE  store  // empty band: store the unchanged sums

pair:
	MOVL (SI)(DX*8), AX  // ix[2i]; 8 bytes per pair
	MOVL 4(SI)(DX*8), R11  // ix[2i+1]
	MOVL AX, R12
	SARL $31, R12  // sign mask: -1 if negative, else 0
	XORL R12, AX  // abs: (v ^ m) - m
	SUBL R12, AX
	MOVL R11, R12
	SARL $31, R12
	XORL R12, R11
	SUBL R12, R11
	MOVL AX, R12
	ORL  R11, R12
	CMPL R12, $16  // x|y >= 16 (unsigned) means a magnitude above 15
	JAE  escape
	SHLL $4, AX  // x*16
	ADDL R11, AX  // x*16 + y
	SHLQ $7, AX  // byte offset of the row, 128 bytes per row
	VPADDD 0(R8)(AX*1), Y0, Y0  // add the row, slots 0-7
	VPADDD 32(R8)(AX*1), Y1, Y1
	VPADDD 64(R8)(AX*1), Y2, Y2
	VPADDD 96(R8)(AX*1), Y3, Y3
	INCQ DX
	CMPQ DX, R10
	JLT  pair

store:
	VMOVDQU Y0, 0(DI)
	VMOVDQU Y1, 32(DI)
	VMOVDQU Y2, 64(DI)
	VMOVDQU Y3, 96(DI)
	DECQ    CX
	JNZ     band

done:
	VZEROUPPER
	RET

escape:
	MOVL    AX, R12
	CMPL    R11, R12  // R12 = max(x, y), signed compare
	CMOVLGT R11, R12
	MOVL    $15, R13  // escMaxDirect, the clamp
	CMPL    AX, R13
	CMOVLGT R13, AX  // x = min(x, 15)
	CMPL    R11, R13
	CMOVLGT R13, R11  // y = min(y, 15)
	SHLL    $4, AX
	ADDL    R11, AX
	SHLQ    $7, AX
	VMOVQ   R12, X9  // VMOVQ, not MOVQ (see the header)
	VPBROADCASTD X9, Y9
	VPCMPGTD  Y9, Y4, Y10  // mask = bound > max(x,y), slots 0-7 (Go order is reversed: Y10 = Y4 > Y9)
	VPBLENDVB Y10, 0(R8)(AX*1), Y8, Y11  // row where the slot can represent it, else impossibleCost
	VPADDD    Y11, Y0, Y0
	VPCMPGTD  Y9, Y5, Y10
	VPBLENDVB Y10, 32(R8)(AX*1), Y8, Y11
	VPADDD    Y11, Y1, Y1
	VPCMPGTD  Y9, Y6, Y10
	VPBLENDVB Y10, 64(R8)(AX*1), Y8, Y11
	VPADDD    Y11, Y2, Y2
	VPCMPGTD  Y9, Y7, Y10
	VPBLENDVB Y10, 96(R8)(AX*1), Y8, Y11
	VPADDD    Y11, Y3, Y3
	INCQ DX
	CMPQ DX, R10
	JLT  pair
	JMP  store
