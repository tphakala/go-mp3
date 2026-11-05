//go:build !noasm

#include "textflag.h"

// func quantizeNEON(xr *[576]float64, ix *[576]int32, steps *[39]float64, widths *[39]int, nBands int, rounding, limit, zeroMax float64)
// Each line computes sqrt(t*sqrt(t)) + rounding, clamps it to limit with FMINNM (a NaN takes the
// limit, as the scalar comparison does), truncates it to int32 and negates it when xr is negative:
// the same comparison, truncation and negation as quantizeGranuleGo. A pair whose t are both below
// zeroMax (FCMGT is false for a NaN) is stored as zeros without the square roots. A pair with one t
// below zeroMax stores that zero and runs the other line through the same steps with scalar
// instructions (pairlow, pairhi), so it pays for one square-root chain instead of two.
TEXT ·quantizeNEON(SB), NOSPLIT, $0-64
	MOVD   xr+0(FP), R0
	MOVD   ix+8(FP), R1
	MOVD   steps+16(FP), R2
	MOVD   widths+24(FP), R3
	MOVD   nBands+32(FP), R4
	MOVD   $rounding+40(FP), R5
	VLD1R  (R5), [V29.D2]
	MOVD   $limit+48(FP), R5
	VLD1R  (R5), [V30.D2]
	MOVD   $zeroMax+56(FP), R5
	VLD1R  (R5), [V27.D2]
	MOVD   $0x8000000000000000, R5
	VDUP   R5, V31.D2
	CBZ    R4, done

band:
	VLD1R  (R2), [V28.D2]
	MOVD   (R3), R6

pair:
	CBZ    R6, next
	VLD1.P 16(R0), [V0.D2]
	VFABS  V0.D2, V2.D2
	VFMUL  V28.D2, V2.D2, V2.D2
	VFCMGT V2.D2, V27.D2, V6.D2
	VUMAXV V6.S4, V7
	FMOVS  F7, R7
	CBNZW  R7, pairlow
	VFSQRT V2.D2, V4.D2
	VFMUL  V4.D2, V2.D2, V4.D2
	VFSQRT V4.D2, V4.D2
	VFADD  V29.D2, V4.D2, V4.D2
	VFMINNM V30.D2, V4.D2, V4.D2
	VFCMLT $(0.0), V0.D2, V6.D2
	VAND   V31.B16, V6.B16, V6.B16
	VEOR   V6.B16, V4.B16, V4.B16
	VFCVTZS V4.D2, V4.D2
	VXTN   V4.D2, V4.S2
	VST1.P [V4.S2], 8(R1)
	SUB    $2, R6
	B      pair

pairlow:
	VUMINV V6.S4, V7
	FMOVS  F7, R7
	CBNZW  R7, pairzero
	VMOV   V6.D[0], R7
	CBNZ   R7, pairhi
	FSQRTD F2, F4
	FMULD  F4, F2, F4
	FSQRTD F4, F4
	FADDD  F29, F4, F4
	FMINNMD F30, F4, F4
	FCVTZSDW F4, R8
	FCMPD  $(0.0), F0
	CNEGW  MI, R8, R8
	MOVW   R8, (R1)
	MOVW   ZR, 4(R1)
	ADD    $8, R1
	SUB    $2, R6
	B      pair

pairhi:
	VDUP   V2.D[1], V8.D2
	VDUP   V0.D[1], V9.D2
	FSQRTD F8, F4
	FMULD  F4, F8, F4
	FSQRTD F4, F4
	FADDD  F29, F4, F4
	FMINNMD F30, F4, F4
	FCVTZSDW F4, R8
	FCMPD  $(0.0), F9
	CNEGW  MI, R8, R8
	MOVW   ZR, (R1)
	MOVW   R8, 4(R1)
	ADD    $8, R1
	SUB    $2, R6
	B      pair

pairzero:
	MOVD.P ZR, 8(R1)
	SUB    $2, R6
	B      pair

next:
	ADD    $8, R2
	ADD    $8, R3
	SUBS   $1, R4, R4
	BNE    band

done:
	RET
