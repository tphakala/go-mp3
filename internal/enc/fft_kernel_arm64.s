//go:build !noasm

#include "textflag.h"

// func fftStagesNEON(re, im, stageRe, stageIm []float64)
// Each butterfly rounds its two products separately before the add and subtract, then updates
// j-tr and j+tr, the same operation order as fftForward; the first stage permutes lanes within
// four elements. Separate FMUL and FADD/FSUB, never FMLA, as fftForward's float64 conversions block fusion.
TEXT ·fftStagesNEON(SB), NOSPLIT, $0-96
	MOVD re_base+0(FP), R0
	MOVD re_len+8(FP), R4
	MOVD im_base+24(FP), R1
	MOVD stageRe_base+48(FP), R2
	MOVD stageIm_base+72(FP), R3
	LSL  $3, R4, R4

	VLD1R (R2), [V4.D2]
	VLD1R (R3), [V5.D2]
	MOVD  R0, R10
	MOVD  R1, R12
	LSR   $5, R4, R7
first:
	VLD1  (R10), [V16.D2, V17.D2]
	VLD1  (R12), [V18.D2, V19.D2]
	VUZP1 V17.D2, V16.D2, V0.D2
	VUZP2 V17.D2, V16.D2, V2.D2
	VUZP1 V19.D2, V18.D2, V1.D2
	VUZP2 V19.D2, V18.D2, V3.D2
	VFMUL V4.D2, V2.D2, V6.D2
	VFMUL V5.D2, V3.D2, V7.D2
	VFSUB V7.D2, V6.D2, V6.D2
	VFMUL V5.D2, V2.D2, V8.D2
	VFMUL V4.D2, V3.D2, V9.D2
	VFADD V9.D2, V8.D2, V8.D2
	VFSUB V6.D2, V0.D2, V2.D2
	VFSUB V8.D2, V1.D2, V3.D2
	VFADD V6.D2, V0.D2, V0.D2
	VFADD V8.D2, V1.D2, V1.D2
	VZIP1 V2.D2, V0.D2, V16.D2
	VZIP2 V2.D2, V0.D2, V17.D2
	VZIP1 V3.D2, V1.D2, V18.D2
	VZIP2 V3.D2, V1.D2, V19.D2
	VST1.P [V16.D2, V17.D2], 32(R10)
	VST1.P [V18.D2, V19.D2], 32(R12)
	SUBS  $1, R7, R7
	BNE   first

	MOVD $16, R5
	MOVD $8, R6
stage:
	CMP  R4, R5
	BGE  done
	MOVD $0, R7
block:
	MOVD $0, R8
butterfly:
	ADD  R7, R8, R9
	ADD  R0, R9, R10
	ADD  R10, R5, R11
	ADD  R1, R9, R12
	ADD  R12, R5, R13
	ADD  R6, R8, R9
	ADD  R2, R9, R14
	ADD  R3, R9, R15
	VLD1 (R10), [V0.D2]
	VLD1 (R12), [V1.D2]
	VLD1 (R11), [V2.D2]
	VLD1 (R13), [V3.D2]
	VLD1 (R14), [V4.D2]
	VLD1 (R15), [V5.D2]
	VFMUL V4.D2, V2.D2, V6.D2
	VFMUL V5.D2, V3.D2, V7.D2
	VFSUB V7.D2, V6.D2, V6.D2
	VFMUL V5.D2, V2.D2, V8.D2
	VFMUL V4.D2, V3.D2, V9.D2
	VFADD V9.D2, V8.D2, V8.D2
	VFSUB V6.D2, V0.D2, V2.D2
	VFSUB V8.D2, V1.D2, V3.D2
	VFADD V6.D2, V0.D2, V0.D2
	VFADD V8.D2, V1.D2, V1.D2
	VST1 [V0.D2], (R10)
	VST1 [V1.D2], (R12)
	VST1 [V2.D2], (R11)
	VST1 [V3.D2], (R13)
	ADD  $16, R8
	CMP  R5, R8
	BLT  butterfly
	ADD  R5<<1, R7, R7
	CMP  R4, R7
	BLT  block
	ADD  R5, R6, R6
	LSL  $1, R5, R5
	B    stage

done:
	RET
