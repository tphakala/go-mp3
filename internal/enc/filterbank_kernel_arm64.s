//go:build !noasm

#include "textflag.h"

// PARTIALSTEP multiplies the eight samples at R10 by the eight window values at R11 and adds the
// products to the partial sums in V0-V3, then advances R10 and R11 by 64 samples.
#define PARTIALSTEP \
	VLD1  (R10), [V4.D2, V5.D2, V6.D2, V7.D2]; \
	VLD1  (R11), [V8.D2, V9.D2, V10.D2, V11.D2]; \
	ADD   $512, R10; \
	ADD   $512, R11; \
	VFMUL V8.D2, V4.D2, V4.D2; \
	VFMUL V9.D2, V5.D2, V5.D2; \
	VFMUL V10.D2, V6.D2, V6.D2; \
	VFMUL V11.D2, V7.D2, V7.D2; \
	VFADD V4.D2, V0.D2, V0.D2; \
	VFADD V5.D2, V1.D2, V1.D2; \
	VFADD V6.D2, V2.D2, V2.D2; \
	VFADD V7.D2, V3.D2, V3.D2

// func subbandNEON(x, window *[512]float64, matrixT *[64][32]float64, out *[32]float64)
// The partial sums accumulate x*window from zero in k order and each subband accumulates matrixT*y
// from zero in j order, separate FMUL and FADD, the same per-term rounding as window, partialSums
// and matrixStep.
TEXT ·subbandNEON(SB), 0, $512-32
	MOVD x+0(FP), R0
	MOVD window+8(FP), R1
	MOVD matrixT+16(FP), R2
	MOVD out+24(FP), R3
	MOVD $y-512(SP), R4
	MOVD R4, R7
	MOVD $8, R5

partial:
	MOVD R0, R10
	MOVD R1, R11
	VEOR V0.B16, V0.B16, V0.B16
	VEOR V1.B16, V1.B16, V1.B16
	VEOR V2.B16, V2.B16, V2.B16
	VEOR V3.B16, V3.B16, V3.B16
	PARTIALSTEP
	PARTIALSTEP
	PARTIALSTEP
	PARTIALSTEP
	PARTIALSTEP
	PARTIALSTEP
	PARTIALSTEP
	PARTIALSTEP
	VST1.P [V0.D2, V1.D2, V2.D2, V3.D2], 64(R7)
	ADD    $64, R0
	ADD    $64, R1
	SUBS   $1, R5, R5
	BNE    partial

	MOVD $2, R5

half:
	MOVD R4, R12
	MOVD R2, R11
	MOVD $64, R6
	VEOR V0.B16, V0.B16, V0.B16
	VEOR V1.B16, V1.B16, V1.B16
	VEOR V2.B16, V2.B16, V2.B16
	VEOR V3.B16, V3.B16, V3.B16
	VEOR V4.B16, V4.B16, V4.B16
	VEOR V5.B16, V5.B16, V5.B16
	VEOR V6.B16, V6.B16, V6.B16
	VEOR V7.B16, V7.B16, V7.B16

matrix:
	VLD1R.P 8(R12), [V8.D2]
	VLD1.P  64(R11), [V16.D2, V17.D2, V18.D2, V19.D2]
	VLD1    (R11), [V20.D2, V21.D2, V22.D2, V23.D2]
	ADD     $192, R11
	VFMUL V8.D2, V16.D2, V16.D2
	VFMUL V8.D2, V17.D2, V17.D2
	VFMUL V8.D2, V18.D2, V18.D2
	VFMUL V8.D2, V19.D2, V19.D2
	VFMUL V8.D2, V20.D2, V20.D2
	VFMUL V8.D2, V21.D2, V21.D2
	VFMUL V8.D2, V22.D2, V22.D2
	VFMUL V8.D2, V23.D2, V23.D2
	VFADD V16.D2, V0.D2, V0.D2
	VFADD V17.D2, V1.D2, V1.D2
	VFADD V18.D2, V2.D2, V2.D2
	VFADD V19.D2, V3.D2, V3.D2
	VFADD V20.D2, V4.D2, V4.D2
	VFADD V21.D2, V5.D2, V5.D2
	VFADD V22.D2, V6.D2, V6.D2
	VFADD V23.D2, V7.D2, V7.D2
	SUBS    $1, R6, R6
	BNE     matrix
	VST1.P  [V0.D2, V1.D2, V2.D2, V3.D2], 64(R3)
	VST1.P  [V4.D2, V5.D2, V6.D2, V7.D2], 64(R3)
	ADD     $128, R2
	SUBS    $1, R5, R5
	BNE     half
	RET
