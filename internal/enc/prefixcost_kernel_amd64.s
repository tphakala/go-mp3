//go:build !noasm

#include "go_asm.h"
#include "textflag.h"

// func prefixCostAVX2(ix *[576]int32, pb *[40]int, nBands int, prefixCost *[40][32]int32, rows *[256][32]int32, bound *[32]int32)
// A pair with both magnitudes at most 15 adds its rows row. Otherwise it adds
// the row of its magnitudes clamped to 15, with impossibleCost in every slot
// whose bound is not above the larger magnitude. The GPR to XMM moves are
// VMOVQ: a legacy-SSE MOVQ here runs with the YMM upper halves in use, which
// costs about 110 ns per escape pair on Zen 3 (EPYC 7763).
TEXT ·prefixCostAVX2(SB), NOSPLIT, $0-48
	MOVQ ix+0(FP), SI
	MOVQ pb+8(FP), BX
	MOVQ nBands+16(FP), CX
	MOVQ prefixCost+24(FP), DI
	MOVQ rows+32(FP), R8
	MOVQ bound+40(FP), R9
	VPXOR   Y0, Y0, Y0
	VPXOR   Y1, Y1, Y1
	VPXOR   Y2, Y2, Y2
	VPXOR   Y3, Y3, Y3
	VMOVDQU 0(R9), Y4
	VMOVDQU 32(R9), Y5
	VMOVDQU 64(R9), Y6
	VMOVDQU 96(R9), Y7
	MOVL    $const_impossibleCost, AX
	VMOVQ   AX, X8
	VPBROADCASTD X8, Y8
	XORQ    DX, DX
	TESTQ   CX, CX
	JZ      done

band:
	ADDQ $8, BX
	ADDQ $128, DI
	MOVQ (BX), R10
	CMPQ DX, R10
	JGE  store

pair:
	MOVL (SI)(DX*8), AX
	MOVL 4(SI)(DX*8), R11
	MOVL AX, R12
	SARL $31, R12
	XORL R12, AX
	SUBL R12, AX
	MOVL R11, R12
	SARL $31, R12
	XORL R12, R11
	SUBL R12, R11
	MOVL AX, R12
	ORL  R11, R12
	CMPL R12, $16
	JAE  escape
	SHLL $4, AX
	ADDL R11, AX
	SHLQ $7, AX
	VPADDD 0(R8)(AX*1), Y0, Y0
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
	CMPL    R11, R12
	CMOVLGT R11, R12
	MOVL    $15, R13
	CMPL    AX, R13
	CMOVLGT R13, AX
	CMPL    R11, R13
	CMOVLGT R13, R11
	SHLL    $4, AX
	ADDL    R11, AX
	SHLQ    $7, AX
	VMOVQ   R12, X9
	VPBROADCASTD X9, Y9
	VPCMPGTD  Y9, Y4, Y10
	VPBLENDVB Y10, 0(R8)(AX*1), Y8, Y11
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
