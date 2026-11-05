//go:build !noasm

#include "textflag.h"

// DETMATHCONST loads the constants PLOG and PEXP2 use: V16 = 0, V17 = 1,
// V18 = 0.5 (also the biased exponent of a Frexp mantissa), V19 = 2^-1022,
// V20 = 2^52, V21 = 1022, V22 = 52, V23 = the mantissa bits,
// V24 = sqrtHalf, V25 = ln2Hi, V26 = ln2Lo, V27 = 2^52 + 1023.
#define DETMATHCONST \
	VEOR V16.B16, V16.B16, V16.B16; \
	VMOVQ $0x3ff0000000000000, $0x3ff0000000000000, V17; \
	VMOVQ $0x3fe0000000000000, $0x3fe0000000000000, V18; \
	VMOVQ $0x0010000000000000, $0x0010000000000000, V19; \
	VMOVQ $0x4330000000000000, $0x4330000000000000, V20; \
	VMOVQ $0x408ff00000000000, $0x408ff00000000000, V21; \
	VMOVQ $0x404a000000000000, $0x404a000000000000, V22; \
	VMOVQ $0x000fffffffffffff, $0x000fffffffffffff, V23; \
	VMOVQ $0x3fe6a09e667f3bcd, $0x3fe6a09e667f3bcd, V24; \
	VMOVQ $0x3fe62e42fefa0000, $0x3fe62e42fefa0000, V25; \
	VMOVQ $0x3d7cf79abc9e3b3a, $0x3d7cf79abc9e3b3a, V26; \
	VMOVQ $0x43300000000003ff, $0x43300000000003ff, V27

// PLOG replaces each lane of V2 (positive and finite) with plog of it,
// using V5-V13 and R5, after DETMATHCONST. The steps are those of plog and
// math.Frexp: a subnormal is first scaled by 2^52 (exact), the biased exponent
// is converted to float64 (exact), the mantissa is recentred into
// [sqrtHalf, sqrt2), and the series and the Cody-Waite reconstruction run in
// plog's order, every product and sum separately rounded.
#define PLOG \
	VFCMGT V2.D2, V19.D2, V5.D2; \
	VFMUL  V20.D2, V2.D2, V6.D2; \
	VBIF   V5.B16, V2.B16, V6.B16; \
	VUSHR  $52, V6.D2, V7.D2; \
	VSCVTF V7.D2, V7.D2; \
	VFSUB  V21.D2, V7.D2, V7.D2; \
	VAND   V22.B16, V5.B16, V8.B16; \
	VFSUB  V8.D2, V7.D2, V7.D2; \
	VAND   V23.B16, V6.B16, V6.B16; \
	VORR   V18.B16, V6.B16, V6.B16; \
	VFCMGT V6.D2, V24.D2, V8.D2; \
	VFADD  V6.D2, V6.D2, V9.D2; \
	VBIT   V8.B16, V9.B16, V6.B16; \
	VAND   V17.B16, V8.B16, V9.B16; \
	VFSUB  V9.D2, V7.D2, V7.D2; \
	VFSUB  V17.D2, V6.D2, V9.D2; \
	VFADD  V17.D2, V6.D2, V10.D2; \
	VFDIV  V10.D2, V9.D2, V9.D2; \
	VFMUL  V9.D2, V9.D2, V10.D2; \
	MOVD   $·plogPoly(SB), R5; \
	FMOVD  96(R5), F11; \
	VDUP   V11.D[0], V11.D2; \
	VFMUL  V10.D2, V11.D2, V11.D2; \
	FMOVD  88(R5), F12; \
	VDUP   V12.D[0], V12.D2; \
	VFADD  V12.D2, V11.D2, V11.D2; \
	VFMUL  V10.D2, V11.D2, V11.D2; \
	FMOVD  80(R5), F12; \
	VDUP   V12.D[0], V12.D2; \
	VFADD  V12.D2, V11.D2, V11.D2; \
	VFMUL  V10.D2, V11.D2, V11.D2; \
	FMOVD  72(R5), F12; \
	VDUP   V12.D[0], V12.D2; \
	VFADD  V12.D2, V11.D2, V11.D2; \
	VFMUL  V10.D2, V11.D2, V11.D2; \
	FMOVD  64(R5), F12; \
	VDUP   V12.D[0], V12.D2; \
	VFADD  V12.D2, V11.D2, V11.D2; \
	VFMUL  V10.D2, V11.D2, V11.D2; \
	FMOVD  56(R5), F12; \
	VDUP   V12.D[0], V12.D2; \
	VFADD  V12.D2, V11.D2, V11.D2; \
	VFMUL  V10.D2, V11.D2, V11.D2; \
	FMOVD  48(R5), F12; \
	VDUP   V12.D[0], V12.D2; \
	VFADD  V12.D2, V11.D2, V11.D2; \
	VFMUL  V10.D2, V11.D2, V11.D2; \
	FMOVD  40(R5), F12; \
	VDUP   V12.D[0], V12.D2; \
	VFADD  V12.D2, V11.D2, V11.D2; \
	VFMUL  V10.D2, V11.D2, V11.D2; \
	FMOVD  32(R5), F12; \
	VDUP   V12.D[0], V12.D2; \
	VFADD  V12.D2, V11.D2, V11.D2; \
	VFMUL  V10.D2, V11.D2, V11.D2; \
	FMOVD  24(R5), F12; \
	VDUP   V12.D[0], V12.D2; \
	VFADD  V12.D2, V11.D2, V11.D2; \
	VFMUL  V10.D2, V11.D2, V11.D2; \
	FMOVD  16(R5), F12; \
	VDUP   V12.D[0], V12.D2; \
	VFADD  V12.D2, V11.D2, V11.D2; \
	VFMUL  V10.D2, V11.D2, V11.D2; \
	FMOVD  8(R5), F12; \
	VDUP   V12.D[0], V12.D2; \
	VFADD  V12.D2, V11.D2, V11.D2; \
	VFMUL  V10.D2, V11.D2, V11.D2; \
	FMOVD  0(R5), F12; \
	VDUP   V12.D[0], V12.D2; \
	VFADD  V12.D2, V11.D2, V11.D2; \
	VFMUL  V11.D2, V9.D2, V11.D2; \
	VFMUL  V26.D2, V7.D2, V12.D2; \
	VFADD  V11.D2, V12.D2, V12.D2; \
	VFMUL  V25.D2, V7.D2, V13.D2; \
	VFADD  V12.D2, V13.D2, V2.D2

// PEXP2 replaces each lane of V5 with pexp2 of it, using V6-V9 and R5, after
// DETMATHCONST, for lanes with -1022 <= floor(x + 0.5) <= 1023
// (thresholdsNEON passes x in [-5.2, -1.8]). k and f are formed as in pexp2,
// the series runs in its Horner order, and math.Ldexp(p, k) is the product
// with 2^k built in the exponent field, which is exact for every k in that
// range.
#define PEXP2 \
	VFADD   V18.D2, V5.D2, V6.D2; \
	VFRINTM V6.D2, V6.D2; \
	VFSUB   V6.D2, V5.D2, V7.D2; \
	MOVD    $·pexp2Poly(SB), R5; \
	FMOVD   112(R5), F8; \
	VDUP    V8.D[0], V8.D2; \
	VFMUL   V7.D2, V8.D2, V8.D2; \
	FMOVD   104(R5), F9; \
	VDUP    V9.D[0], V9.D2; \
	VFADD   V9.D2, V8.D2, V8.D2; \
	VFMUL   V7.D2, V8.D2, V8.D2; \
	FMOVD   96(R5), F9; \
	VDUP    V9.D[0], V9.D2; \
	VFADD   V9.D2, V8.D2, V8.D2; \
	VFMUL   V7.D2, V8.D2, V8.D2; \
	FMOVD   88(R5), F9; \
	VDUP    V9.D[0], V9.D2; \
	VFADD   V9.D2, V8.D2, V8.D2; \
	VFMUL   V7.D2, V8.D2, V8.D2; \
	FMOVD   80(R5), F9; \
	VDUP    V9.D[0], V9.D2; \
	VFADD   V9.D2, V8.D2, V8.D2; \
	VFMUL   V7.D2, V8.D2, V8.D2; \
	FMOVD   72(R5), F9; \
	VDUP    V9.D[0], V9.D2; \
	VFADD   V9.D2, V8.D2, V8.D2; \
	VFMUL   V7.D2, V8.D2, V8.D2; \
	FMOVD   64(R5), F9; \
	VDUP    V9.D[0], V9.D2; \
	VFADD   V9.D2, V8.D2, V8.D2; \
	VFMUL   V7.D2, V8.D2, V8.D2; \
	FMOVD   56(R5), F9; \
	VDUP    V9.D[0], V9.D2; \
	VFADD   V9.D2, V8.D2, V8.D2; \
	VFMUL   V7.D2, V8.D2, V8.D2; \
	FMOVD   48(R5), F9; \
	VDUP    V9.D[0], V9.D2; \
	VFADD   V9.D2, V8.D2, V8.D2; \
	VFMUL   V7.D2, V8.D2, V8.D2; \
	FMOVD   40(R5), F9; \
	VDUP    V9.D[0], V9.D2; \
	VFADD   V9.D2, V8.D2, V8.D2; \
	VFMUL   V7.D2, V8.D2, V8.D2; \
	FMOVD   32(R5), F9; \
	VDUP    V9.D[0], V9.D2; \
	VFADD   V9.D2, V8.D2, V8.D2; \
	VFMUL   V7.D2, V8.D2, V8.D2; \
	FMOVD   24(R5), F9; \
	VDUP    V9.D[0], V9.D2; \
	VFADD   V9.D2, V8.D2, V8.D2; \
	VFMUL   V7.D2, V8.D2, V8.D2; \
	FMOVD   16(R5), F9; \
	VDUP    V9.D[0], V9.D2; \
	VFADD   V9.D2, V8.D2, V8.D2; \
	VFMUL   V7.D2, V8.D2, V8.D2; \
	FMOVD   8(R5), F9; \
	VDUP    V9.D[0], V9.D2; \
	VFADD   V9.D2, V8.D2, V8.D2; \
	VFMUL   V7.D2, V8.D2, V8.D2; \
	FMOVD   0(R5), F9; \
	VDUP    V9.D[0], V9.D2; \
	VFADD   V9.D2, V8.D2, V8.D2; \
	VFADD   V27.D2, V6.D2, V6.D2; \
	VSHL    $52, V6.D2, V6.D2; \
	VFMUL   V6.D2, V8.D2, V5.D2

// UNPRED computes the next two bins of unpredNEON, advancing every pointer by
// one group, with V16 = 0 and V17 = 1: V3 = r, V8 = r1, V9 = r2m, V10 = rp,
// V11 = the no-history mask, V6/V7 = cos/sin 2f1, V4/V5 = cos/sin f2.
#define UNPRED \
	VLD1.P 16(R0), [V0.D2]; \
	VLD1.P 16(R1), [V1.D2]; \
	VFMUL  V0.D2, V0.D2, V2.D2; \
	VFMUL  V1.D2, V1.D2, V3.D2; \
	VFADD  V3.D2, V2.D2, V2.D2; \
	VST1.P [V2.D2], 16(R4); \
	VFSQRT V2.D2, V3.D2; \
	VLD1.P 16(R2), [V4.D2]; \
	VLD1.P 16(R3), [V5.D2]; \
	VFMUL  V4.D2, V4.D2, V6.D2; \
	VFMUL  V5.D2, V5.D2, V7.D2; \
	VFADD  V7.D2, V6.D2, V8.D2; \
	VFSUB  V7.D2, V6.D2, V6.D2; \
	VFDIV  V8.D2, V6.D2, V6.D2; \
	VFADD  V4.D2, V4.D2, V7.D2; \
	VFMUL  V5.D2, V7.D2, V7.D2; \
	VFDIV  V8.D2, V7.D2, V7.D2; \
	VFSQRT V8.D2, V8.D2; \
	VLD1.P 16(R8), [V4.D2]; \
	VLD1.P 16(R9), [V5.D2]; \
	VFMUL  V4.D2, V4.D2, V9.D2; \
	VFMUL  V5.D2, V5.D2, V10.D2; \
	VFADD  V10.D2, V9.D2, V9.D2; \
	VFSQRT V9.D2, V9.D2; \
	VFDIV  V9.D2, V4.D2, V4.D2; \
	VFDIV  V9.D2, V5.D2, V5.D2; \
	VFADD  V8.D2, V8.D2, V10.D2; \
	VFSUB  V9.D2, V10.D2, V10.D2; \
	VFCMEQ V16.D2, V8.D2, V11.D2; \
	VFCMEQ V16.D2, V9.D2, V12.D2; \
	VORR   V12.B16, V11.B16, V11.B16; \
	VFCMEQ V16.D2, V3.D2, V12.D2; \
	VFCMEQ V16.D2, V10.D2, V13.D2; \
	VAND   V13.B16, V12.B16, V12.B16; \
	VORR   V12.B16, V11.B16, V11.B16; \
	VFMUL  V4.D2, V6.D2, V2.D2; \
	VFMUL  V5.D2, V7.D2, V12.D2; \
	VFADD  V12.D2, V2.D2, V2.D2; \
	VFMUL  V4.D2, V7.D2, V7.D2; \
	VFMUL  V5.D2, V6.D2, V6.D2; \
	VFSUB  V6.D2, V7.D2, V7.D2; \
	VFMUL  V2.D2, V10.D2, V2.D2; \
	VFSUB  V2.D2, V0.D2, V2.D2; \
	VFMUL  V7.D2, V10.D2, V7.D2; \
	VFSUB  V7.D2, V1.D2, V7.D2; \
	VFMUL  V2.D2, V2.D2, V2.D2; \
	VFMUL  V7.D2, V7.D2, V7.D2; \
	VFADD  V7.D2, V2.D2, V2.D2; \
	VFSQRT V2.D2, V2.D2; \
	VFABS  V10.D2, V10.D2; \
	VFADD  V10.D2, V3.D2, V10.D2; \
	VFDIV  V10.D2, V2.D2, V2.D2; \
	VFCMGT V17.D2, V2.D2, V12.D2; \
	VBIT   V12.B16, V17.B16, V2.B16; \
	VBIT   V11.B16, V17.B16, V2.B16; \
	VST1.P [V2.D2], 16(R6)

// func unpredNEON(re, im *[1024]float64, prevRe, prevIm *[2][513]float64, r2, cw *[513]float64)
// R8 and R9 walk prevRe[1] and prevIm[1], 4104 bytes after prevRe[0] and
// prevIm[0].
TEXT ·unpredNEON(SB), NOSPLIT, $0-48
	MOVD re+0(FP), R10
	MOVD im+8(FP), R11
	MOVD prevRe+16(FP), R12
	MOVD prevIm+24(FP), R13
	MOVD r2+32(FP), R14
	MOVD cw+40(FP), R15
	VEOR V16.B16, V16.B16, V16.B16
	VMOVQ $0x3ff0000000000000, $0x3ff0000000000000, V17
	MOVD R10, R0
	MOVD R11, R1
	MOVD R12, R2
	MOVD R13, R3
	ADD  $4104, R12, R8
	ADD  $4104, R13, R9
	MOVD R14, R4
	MOVD R15, R6
	MOVD $256, R7

unpredLoop:
	UNPRED
	SUBS $1, R7
	BNE  unpredLoop

	// 513 is not a multiple of two: bins 511 and 512 cover the last one.
	ADD $4088, R10, R0
	ADD $4088, R11, R1
	ADD $4088, R12, R2
	ADD $4088, R13, R3
	ADD $8192, R12, R8
	ADD $8192, R13, R9
	ADD $4088, R14, R4
	ADD $4088, R15, R6
	UNPRED
	RET

// func thresholdsNEON(e, ct, ecb, cbs, nb *[psyMaxParts]float64, nbPrev *[2][psyMaxParts]float64, sprdT *[psyMaxParts][psyMaxParts]float64, norm, qthr *[psyMaxParts]float64, n int, tmn, nmt, rpelev2 float64)
// The spreading pass accumulates ecb (V0-V3) and cbs (V4-V7) of eight
// partitions over bb in order, one rounded product and one rounded sum per
// term. The threshold pass takes two partitions at a time: V0 = ecb,
// V3 = (ecb > 0), V4 = (cbb <= 0), V2 = tb, V5 = the pexp2 argument and then
// bc, V14 = raw and then nb. A sprdT row is 496 bytes, and nbPrev[1] starts
// 496 bytes after nbPrev[0].
TEXT ·thresholdsNEON(SB), NOSPLIT, $0-104
	MOVD e+0(FP), R0
	MOVD ct+8(FP), R1
	MOVD ecb+16(FP), R2
	MOVD cbs+24(FP), R3
	MOVD nb+32(FP), R4
	MOVD nbPrev+40(FP), R6
	MOVD sprdT+48(FP), R7
	MOVD norm+56(FP), R8
	MOVD qthr+64(FP), R9
	MOVD n+72(FP), R10

	SUB $8, R10, R11
	LSL $3, R11
	MOVD $0, R12

spreadBlock:
	VEOR V0.B16, V0.B16, V0.B16
	VEOR V1.B16, V1.B16, V1.B16
	VEOR V2.B16, V2.B16, V2.B16
	VEOR V3.B16, V3.B16, V3.B16
	VEOR V4.B16, V4.B16, V4.B16
	VEOR V5.B16, V5.B16, V5.B16
	VEOR V6.B16, V6.B16, V6.B16
	VEOR V7.B16, V7.B16, V7.B16
	ADD  R12, R7, R13
	MOVD R0, R14
	MOVD R1, R15
	MOVD R10, R19

spreadTerm:
	VLD1R.P 8(R14), [V8.D2]
	VLD1R.P 8(R15), [V9.D2]
	VLD1    (R13), [V10.D2, V11.D2, V12.D2, V13.D2]
	VFMUL   V8.D2, V10.D2, V14.D2
	VFADD   V14.D2, V0.D2, V0.D2
	VFMUL   V9.D2, V10.D2, V14.D2
	VFADD   V14.D2, V4.D2, V4.D2
	VFMUL   V8.D2, V11.D2, V14.D2
	VFADD   V14.D2, V1.D2, V1.D2
	VFMUL   V9.D2, V11.D2, V14.D2
	VFADD   V14.D2, V5.D2, V5.D2
	VFMUL   V8.D2, V12.D2, V14.D2
	VFADD   V14.D2, V2.D2, V2.D2
	VFMUL   V9.D2, V12.D2, V14.D2
	VFADD   V14.D2, V6.D2, V6.D2
	VFMUL   V8.D2, V13.D2, V14.D2
	VFADD   V14.D2, V3.D2, V3.D2
	VFMUL   V9.D2, V13.D2, V14.D2
	VFADD   V14.D2, V7.D2, V7.D2
	ADD     $496, R13
	SUBS    $1, R19
	BNE     spreadTerm

	ADD  R12, R2, R13
	VST1 [V0.D2, V1.D2, V2.D2, V3.D2], (R13)
	ADD  R12, R3, R13
	VST1 [V4.D2, V5.D2, V6.D2, V7.D2], (R13)
	CMP  R11, R12
	BEQ  spreadDone
	ADD  $64, R12
	CMP  R11, R12
	BLE  spreadBlock
	MOVD R11, R12
	B    spreadBlock

spreadDone:
	DETMATHCONST
	VMOVQ $0xbfd322d0e5604189, $0xbfd322d0e5604189, V28
	VMOVQ $0x3fdb851eb851eb85, $0x3fdb851eb851eb85, V29
	VMOVQ $0xbfd542a5a12e1c5a, $0xbfd542a5a12e1c5a, V30
	MOVD  $tmn+80(FP), R20
	MOVD  $nmt+88(FP), R21
	MOVD  $rpelev2+96(FP), R22
	VLD1R (R22), [V31.D2]
	SUB   $2, R10, R11
	LSL   $3, R11
	MOVD  $0, R12

toneGroup:
	ADD    R12, R2, R13
	VLD1   (R13), [V0.D2]
	ADD    R12, R3, R13
	VLD1   (R13), [V1.D2]
	VFDIV  V0.D2, V1.D2, V2.D2
	VFCMGT V16.D2, V0.D2, V3.D2
	VFCMGE V2.D2, V16.D2, V4.D2
	VFCMGT V17.D2, V2.D2, V5.D2
	VBIT   V5.B16, V17.B16, V2.B16
	PLOG
	VFMUL  V29.D2, V2.D2, V5.D2
	VFSUB  V5.D2, V28.D2, V2.D2
	VFCMGT V2.D2, V16.D2, V5.D2
	VBIT   V5.B16, V16.B16, V2.B16
	VFCMGT V17.D2, V2.D2, V5.D2
	VBIT   V5.B16, V17.B16, V2.B16
	VBIT   V4.B16, V17.B16, V2.B16
	VAND   V3.B16, V2.B16, V2.B16
	VLD1R  (R20), [V6.D2]
	VFMUL  V6.D2, V2.D2, V5.D2
	VFSUB  V2.D2, V17.D2, V6.D2
	VLD1R  (R21), [V7.D2]
	VFMUL  V7.D2, V6.D2, V6.D2
	VFADD  V6.D2, V5.D2, V5.D2
	VFMUL  V30.D2, V5.D2, V5.D2
	PEXP2
	ADD    R12, R8, R13
	VLD1   (R13), [V6.D2]
	VFMUL  V6.D2, V0.D2, V14.D2
	VFMUL  V5.D2, V14.D2, V14.D2
	ADD    R12, R6, R13
	VLD1   (R13), [V6.D2]
	VFADD  V6.D2, V6.D2, V6.D2
	ADD    $496, R13
	VLD1   (R13), [V7.D2]
	VFMUL  V31.D2, V7.D2, V7.D2
	VFCMGT V14.D2, V6.D2, V8.D2
	VBIF   V8.B16, V6.B16, V14.B16
	VFCMGT V14.D2, V7.D2, V8.D2
	VBIF   V8.B16, V7.B16, V14.B16
	ADD    R12, R9, R13
	VLD1   (R13), [V6.D2]
	VFCMGT V14.D2, V6.D2, V8.D2
	VBIT   V8.B16, V6.B16, V14.B16
	ADD    R12, R4, R13
	VST1   [V14.D2], (R13)
	CMP    R11, R12
	BEQ    toneDone
	ADD    $16, R12
	CMP    R11, R12
	BLE    toneGroup
	MOVD   R11, R12
	B      toneGroup

toneDone:
	RET

// func shortThresholdsNEON(eS, nbS *[psyMaxPartsS]float64, sprdT *[psyMaxPartsS][psyMaxPartsS]float64, norm, qthr *[psyMaxPartsS]float64, n int, bc float64)
// V0-V3 accumulate ecb of eight partitions over bb in order; a sprdT row is
// 352 bytes.
TEXT ·shortThresholdsNEON(SB), NOSPLIT, $0-56
	MOVD  eS+0(FP), R0
	MOVD  nbS+8(FP), R1
	MOVD  sprdT+16(FP), R2
	MOVD  norm+24(FP), R3
	MOVD  qthr+32(FP), R4
	MOVD  n+40(FP), R6
	FMOVD bc+48(FP), F31
	VDUP  V31.D[0], V31.D2

	SUB  $8, R6, R7
	LSL  $3, R7
	MOVD $0, R8

shortBlock:
	VEOR V0.B16, V0.B16, V0.B16
	VEOR V1.B16, V1.B16, V1.B16
	VEOR V2.B16, V2.B16, V2.B16
	VEOR V3.B16, V3.B16, V3.B16
	ADD  R8, R2, R9
	MOVD R0, R10
	MOVD R6, R11

shortTerm:
	VLD1R.P 8(R10), [V8.D2]
	VLD1    (R9), [V10.D2, V11.D2, V12.D2, V13.D2]
	VFMUL   V8.D2, V10.D2, V14.D2
	VFADD   V14.D2, V0.D2, V0.D2
	VFMUL   V8.D2, V11.D2, V14.D2
	VFADD   V14.D2, V1.D2, V1.D2
	VFMUL   V8.D2, V12.D2, V14.D2
	VFADD   V14.D2, V2.D2, V2.D2
	VFMUL   V8.D2, V13.D2, V14.D2
	VFADD   V14.D2, V3.D2, V3.D2
	ADD     $352, R9
	SUBS    $1, R11
	BNE     shortTerm

	ADD    R8, R3, R9
	VLD1   (R9), [V10.D2, V11.D2, V12.D2, V13.D2]
	ADD    R8, R4, R9
	VLD1   (R9), [V4.D2, V5.D2, V6.D2, V7.D2]
	VFMUL  V10.D2, V0.D2, V0.D2
	VFMUL  V31.D2, V0.D2, V0.D2
	VFCMGT V0.D2, V4.D2, V8.D2
	VBIT   V8.B16, V4.B16, V0.B16
	VFMUL  V11.D2, V1.D2, V1.D2
	VFMUL  V31.D2, V1.D2, V1.D2
	VFCMGT V1.D2, V5.D2, V8.D2
	VBIT   V8.B16, V5.B16, V1.B16
	VFMUL  V12.D2, V2.D2, V2.D2
	VFMUL  V31.D2, V2.D2, V2.D2
	VFCMGT V2.D2, V6.D2, V8.D2
	VBIT   V8.B16, V6.B16, V2.B16
	VFMUL  V13.D2, V3.D2, V3.D2
	VFMUL  V31.D2, V3.D2, V3.D2
	VFCMGT V3.D2, V7.D2, V8.D2
	VBIT   V8.B16, V7.B16, V3.B16
	ADD    R8, R1, R9
	VST1   [V0.D2, V1.D2, V2.D2, V3.D2], (R9)
	CMP    R7, R8
	BEQ    shortDone
	ADD    $64, R8
	CMP    R7, R8
	BLE    shortBlock
	MOVD   R7, R8
	B      shortBlock

shortDone:
	RET

// func plogsNEON(dst, src *float64, n int)
TEXT ·plogsNEON(SB), NOSPLIT, $0-24
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD n+16(FP), R2
	DETMATHCONST
	SUB  $2, R2, R3
	LSL  $3, R3
	MOVD $0, R4

plogsGroup:
	ADD  R4, R1, R6
	VLD1 (R6), [V2.D2]
	PLOG
	ADD  R4, R0, R6
	VST1 [V2.D2], (R6)
	CMP  R3, R4
	BEQ  plogsDone
	ADD  $16, R4
	CMP  R3, R4
	BLE  plogsGroup
	MOVD R3, R4
	B    plogsGroup

plogsDone:
	RET
