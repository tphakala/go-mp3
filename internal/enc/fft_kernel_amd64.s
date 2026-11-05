//go:build !noasm

#include "textflag.h"

// func fftStagesAVX2(re, im, stageRe, stageIm []float64)
// Each butterfly rounds its two products separately before the add and subtract, then updates
// j-tr and j+tr, the same operation order as fftForward; the first two stages permute lanes within
// eight elements.
TEXT ·fftStagesAVX2(SB), NOSPLIT, $0-96
	MOVQ re_base+0(FP), SI
	MOVQ re_len+8(FP), CX
	MOVQ im_base+24(FP), DI
	MOVQ stageRe_base+48(FP), R8
	MOVQ stageIm_base+72(FP), R9
	SHLQ $3, CX

	VBROADCASTSD (R8), Y12
	VBROADCASTSD (R9), Y13
	XORQ AX, AX
first:
	VMOVUPD    (SI)(AX*1), Y0
	VMOVUPD    32(SI)(AX*1), Y1
	VMOVUPD    (DI)(AX*1), Y2
	VMOVUPD    32(DI)(AX*1), Y3
	VUNPCKLPD  Y1, Y0, Y4
	VUNPCKHPD  Y1, Y0, Y5
	VUNPCKLPD  Y3, Y2, Y6
	VUNPCKHPD  Y3, Y2, Y7
	VMULPD  Y12, Y5, Y8
	VMULPD  Y13, Y7, Y9
	VSUBPD  Y9, Y8, Y8
	VMULPD  Y13, Y5, Y10
	VMULPD  Y12, Y7, Y11
	VADDPD  Y11, Y10, Y10
	VSUBPD  Y8, Y4, Y5
	VSUBPD  Y10, Y6, Y7
	VADDPD  Y8, Y4, Y4
	VADDPD  Y10, Y6, Y6
	VUNPCKLPD  Y5, Y4, Y0
	VUNPCKHPD  Y5, Y4, Y1
	VUNPCKLPD  Y7, Y6, Y2
	VUNPCKHPD  Y7, Y6, Y3
	VMOVUPD    Y0, (SI)(AX*1)
	VMOVUPD    Y1, 32(SI)(AX*1)
	VMOVUPD    Y2, (DI)(AX*1)
	VMOVUPD    Y3, 32(DI)(AX*1)
	ADDQ       $64, AX
	CMPQ       AX, CX
	JLT        first

	VBROADCASTF128 8(R8), Y12
	VBROADCASTF128 8(R9), Y13
	XORQ AX, AX
second:
	VMOVUPD    (SI)(AX*1), Y0
	VMOVUPD    32(SI)(AX*1), Y1
	VMOVUPD    (DI)(AX*1), Y2
	VMOVUPD    32(DI)(AX*1), Y3
	VPERM2F128 $0x20, Y1, Y0, Y4
	VPERM2F128 $0x31, Y1, Y0, Y5
	VPERM2F128 $0x20, Y3, Y2, Y6
	VPERM2F128 $0x31, Y3, Y2, Y7
	VMULPD  Y12, Y5, Y8
	VMULPD  Y13, Y7, Y9
	VSUBPD  Y9, Y8, Y8
	VMULPD  Y13, Y5, Y10
	VMULPD  Y12, Y7, Y11
	VADDPD  Y11, Y10, Y10
	VSUBPD  Y8, Y4, Y5
	VSUBPD  Y10, Y6, Y7
	VADDPD  Y8, Y4, Y4
	VADDPD  Y10, Y6, Y6
	VPERM2F128 $0x20, Y5, Y4, Y0
	VPERM2F128 $0x31, Y5, Y4, Y1
	VPERM2F128 $0x20, Y7, Y6, Y2
	VPERM2F128 $0x31, Y7, Y6, Y3
	VMOVUPD    Y0, (SI)(AX*1)
	VMOVUPD    Y1, 32(SI)(AX*1)
	VMOVUPD    Y2, (DI)(AX*1)
	VMOVUPD    Y3, 32(DI)(AX*1)
	ADDQ       $64, AX
	CMPQ       AX, CX
	JLT        second

	MOVQ $32, R10
	MOVQ $24, R11
stage:
	CMPQ R10, CX
	JGE  done
	XORQ BX, BX
block:
	XORQ DX, DX
butterfly:
	LEAQ (BX)(DX*1), AX
	LEAQ (AX)(R10*1), R12
	LEAQ (R11)(DX*1), R13
	VMOVUPD (SI)(AX*1), Y0
	VMOVUPD (DI)(AX*1), Y1
	VMOVUPD (SI)(R12*1), Y2
	VMOVUPD (DI)(R12*1), Y3
	VMOVUPD (R8)(R13*1), Y12
	VMOVUPD (R9)(R13*1), Y13
	VMULPD  Y12, Y2, Y8
	VMULPD  Y13, Y3, Y9
	VSUBPD  Y9, Y8, Y8
	VMULPD  Y13, Y2, Y10
	VMULPD  Y12, Y3, Y11
	VADDPD  Y11, Y10, Y10
	VSUBPD  Y8, Y0, Y2
	VSUBPD  Y10, Y1, Y3
	VADDPD  Y8, Y0, Y0
	VADDPD  Y10, Y1, Y1
	VMOVUPD Y0, (SI)(AX*1)
	VMOVUPD Y1, (DI)(AX*1)
	VMOVUPD Y2, (SI)(R12*1)
	VMOVUPD Y3, (DI)(R12*1)
	ADDQ    $32, DX
	CMPQ    DX, R10
	JLT     butterfly
	LEAQ    (BX)(R10*2), BX
	CMPQ    BX, CX
	JLT     block
	ADDQ    R10, R11
	SHLQ    $1, R10
	JMP     stage

done:
	VZEROUPPER
	RET
