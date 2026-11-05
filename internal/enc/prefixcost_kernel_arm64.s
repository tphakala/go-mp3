//go:build !noasm

#include "go_asm.h"
#include "textflag.h"

// func prefixCostNEON(ix *[576]int32, pb *[40]int, nBands int, prefixCost *[40][32]int32, rows *[256][32]int32, bound *[32]int32)
// A pair with both magnitudes at most 15 adds its rows row. Otherwise it adds
// the row of its magnitudes clamped to 15, with impossibleCost in every slot
// whose bound is not above the larger magnitude.
TEXT ·prefixCostNEON(SB), NOSPLIT, $0-48
	MOVD ix+0(FP), R8
	MOVD pb+8(FP), R1
	MOVD nBands+16(FP), R2
	MOVD prefixCost+24(FP), R3
	MOVD rows+32(FP), R4
	MOVD bound+40(FP), R5
	VEOR V0.B16, V0.B16, V0.B16
	VEOR V1.B16, V1.B16, V1.B16
	VEOR V2.B16, V2.B16, V2.B16
	VEOR V3.B16, V3.B16, V3.B16
	VEOR V4.B16, V4.B16, V4.B16
	VEOR V5.B16, V5.B16, V5.B16
	VEOR V6.B16, V6.B16, V6.B16
	VEOR V7.B16, V7.B16, V7.B16
	VLD1.P 64(R5), [V16.S4, V17.S4, V18.S4, V19.S4]
	VLD1   (R5), [V20.S4, V21.S4, V22.S4, V23.S4]
	MOVW   $const_impossibleCost, R6
	VDUP   R6, V24.S4
	MOVD   $0, R7
	ADD    $128, R3
	CBZ    R2, done

band:
	ADD  $8, R1
	MOVD (R1), R10
	CMP  R10, R7
	BGE  store

pair:
	MOVW (R8), R11
	MOVW 4(R8), R12
	ADD  $8, R8
	CMP  $0, R11
	CNEG LT, R11, R11
	CMP  $0, R12
	CNEG LT, R12, R12
	ORR  R11, R12, R13
	CMP  $16, R13
	BHS  escape
	LSL  $4, R11, R13
	ADD  R12, R13, R13
	ADD  R13<<7, R4, R13
	VLD1.P 64(R13), [V8.S4, V9.S4, V10.S4, V11.S4]
	VLD1   (R13), [V12.S4, V13.S4, V14.S4, V15.S4]
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
	VST1.P [V0.S4, V1.S4, V2.S4, V3.S4], 64(R3)
	VST1.P [V4.S4, V5.S4, V6.S4, V7.S4], 64(R3)
	SUBS   $1, R2, R2
	BNE    band

done:
	RET

escape:
	CMP  R12, R11
	CSEL GT, R11, R12, R13
	MOVD $15, R14
	CMP  R14, R11
	CSEL GT, R14, R11, R11
	CMP  R14, R12
	CSEL GT, R14, R12, R12
	VDUP R13, V25.S4
	LSL  $4, R11, R14
	ADD  R12, R14, R14
	ADD  R14<<7, R4, R14
	VLD1.P 64(R14), [V8.S4, V9.S4, V10.S4, V11.S4]
	VLD1   (R14), [V12.S4, V13.S4, V14.S4, V15.S4]
	VCMGT  V25.S4, V16.S4, V26.S4
	VBSL   V24.B16, V8.B16, V26.B16
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
