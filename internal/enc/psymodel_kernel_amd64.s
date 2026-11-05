//go:build !noasm

#include "textflag.h"

// psyk holds the kernels' constants, each repeated across the four lanes.
DATA psyk<>+0x000(SB)/8, $0x3ff0000000000000
DATA psyk<>+0x008(SB)/8, $0x3ff0000000000000
DATA psyk<>+0x010(SB)/8, $0x3ff0000000000000
DATA psyk<>+0x018(SB)/8, $0x3ff0000000000000
DATA psyk<>+0x020(SB)/8, $0x3fe0000000000000
DATA psyk<>+0x028(SB)/8, $0x3fe0000000000000
DATA psyk<>+0x030(SB)/8, $0x3fe0000000000000
DATA psyk<>+0x038(SB)/8, $0x3fe0000000000000
DATA psyk<>+0x040(SB)/8, $0x7fffffffffffffff
DATA psyk<>+0x048(SB)/8, $0x7fffffffffffffff
DATA psyk<>+0x050(SB)/8, $0x7fffffffffffffff
DATA psyk<>+0x058(SB)/8, $0x7fffffffffffffff
DATA psyk<>+0x060(SB)/8, $0x0010000000000000
DATA psyk<>+0x068(SB)/8, $0x0010000000000000
DATA psyk<>+0x070(SB)/8, $0x0010000000000000
DATA psyk<>+0x078(SB)/8, $0x0010000000000000
DATA psyk<>+0x080(SB)/8, $0x4330000000000000
DATA psyk<>+0x088(SB)/8, $0x4330000000000000
DATA psyk<>+0x090(SB)/8, $0x4330000000000000
DATA psyk<>+0x098(SB)/8, $0x4330000000000000
DATA psyk<>+0x0a0(SB)/8, $0x408ff00000000000
DATA psyk<>+0x0a8(SB)/8, $0x408ff00000000000
DATA psyk<>+0x0b0(SB)/8, $0x408ff00000000000
DATA psyk<>+0x0b8(SB)/8, $0x408ff00000000000
DATA psyk<>+0x0c0(SB)/8, $0x404a000000000000
DATA psyk<>+0x0c8(SB)/8, $0x404a000000000000
DATA psyk<>+0x0d0(SB)/8, $0x404a000000000000
DATA psyk<>+0x0d8(SB)/8, $0x404a000000000000
DATA psyk<>+0x0e0(SB)/8, $0x000fffffffffffff
DATA psyk<>+0x0e8(SB)/8, $0x000fffffffffffff
DATA psyk<>+0x0f0(SB)/8, $0x000fffffffffffff
DATA psyk<>+0x0f8(SB)/8, $0x000fffffffffffff
DATA psyk<>+0x100(SB)/8, $0x3fe6a09e667f3bcd
DATA psyk<>+0x108(SB)/8, $0x3fe6a09e667f3bcd
DATA psyk<>+0x110(SB)/8, $0x3fe6a09e667f3bcd
DATA psyk<>+0x118(SB)/8, $0x3fe6a09e667f3bcd
DATA psyk<>+0x120(SB)/8, $0x3fe62e42fefa0000
DATA psyk<>+0x128(SB)/8, $0x3fe62e42fefa0000
DATA psyk<>+0x130(SB)/8, $0x3fe62e42fefa0000
DATA psyk<>+0x138(SB)/8, $0x3fe62e42fefa0000
DATA psyk<>+0x140(SB)/8, $0x3d7cf79abc9e3b3a
DATA psyk<>+0x148(SB)/8, $0x3d7cf79abc9e3b3a
DATA psyk<>+0x150(SB)/8, $0x3d7cf79abc9e3b3a
DATA psyk<>+0x158(SB)/8, $0x3d7cf79abc9e3b3a
DATA psyk<>+0x160(SB)/8, $0xbfd322d0e5604189
DATA psyk<>+0x168(SB)/8, $0xbfd322d0e5604189
DATA psyk<>+0x170(SB)/8, $0xbfd322d0e5604189
DATA psyk<>+0x178(SB)/8, $0xbfd322d0e5604189
DATA psyk<>+0x180(SB)/8, $0x3fdb851eb851eb85
DATA psyk<>+0x188(SB)/8, $0x3fdb851eb851eb85
DATA psyk<>+0x190(SB)/8, $0x3fdb851eb851eb85
DATA psyk<>+0x198(SB)/8, $0x3fdb851eb851eb85
DATA psyk<>+0x1a0(SB)/8, $0xbfd542a5a12e1c5a
DATA psyk<>+0x1a8(SB)/8, $0xbfd542a5a12e1c5a
DATA psyk<>+0x1b0(SB)/8, $0xbfd542a5a12e1c5a
DATA psyk<>+0x1b8(SB)/8, $0xbfd542a5a12e1c5a
DATA psyk<>+0x1c0(SB)/8, $0x43300000000003ff
DATA psyk<>+0x1c8(SB)/8, $0x43300000000003ff
DATA psyk<>+0x1d0(SB)/8, $0x43300000000003ff
DATA psyk<>+0x1d8(SB)/8, $0x43300000000003ff
GLOBL psyk<>(SB), RODATA|NOPTR, $0x1e0

#define ONE       psyk<>+0x000(SB) // 1
#define HALF      psyk<>+0x020(SB) // 0.5, also the biased exponent of a Frexp mantissa
#define ABSMASK   psyk<>+0x040(SB) // clears the sign bit
#define MINNORMAL psyk<>+0x060(SB) // 2^-1022, the smallest normal
#define TWO52     psyk<>+0x080(SB) // 2^52
#define C1022     psyk<>+0x0a0(SB) // 1022
#define C52       psyk<>+0x0c0(SB) // 52
#define MANTMASK  psyk<>+0x0e0(SB) // the mantissa bits
#define SQRTHALF  psyk<>+0x100(SB) // sqrtHalf
#define LN2HI     psyk<>+0x120(SB) // ln2Hi
#define LN2LO     psyk<>+0x140(SB) // ln2Lo
#define TBBASE    psyk<>+0x160(SB) // -0.299
#define TBSLOPE   psyk<>+0x180(SB) // 0.43
#define SNRSCALE  psyk<>+0x1a0(SB) // -psyLog2TenOver10
#define EXPBIAS   psyk<>+0x1c0(SB) // 2^52 + 1023

// PLOG replaces each lane of Y2 (positive and finite) with plog of it,
// using Y5-Y13. The steps are those of plog and math.Frexp: a subnormal is
// first scaled by 2^52 (exact), the biased exponent is read as an integer and
// converted through the 2^52 bias (exact), the mantissa is recentred into
// [sqrtHalf, sqrt2), and the series and the Cody-Waite reconstruction run in
// plog's order, every product and sum separately rounded.
#define PLOG \
	VCMPPD       $0x01, MINNORMAL, Y2, Y5; \
	VMULPD       TWO52, Y2, Y6; \
	VBLENDVPD    Y5, Y6, Y2, Y6; \
	VPSRLQ       $52, Y6, Y7; \
	VPOR         TWO52, Y7, Y7; \
	VSUBPD       TWO52, Y7, Y7; \
	VSUBPD       C1022, Y7, Y7; \
	VANDPD       C52, Y5, Y8; \
	VSUBPD       Y8, Y7, Y7; \
	VANDPD       MANTMASK, Y6, Y6; \
	VORPD        HALF, Y6, Y6; \
	VCMPPD       $0x01, SQRTHALF, Y6, Y8; \
	VADDPD       Y6, Y6, Y9; \
	VBLENDVPD    Y8, Y9, Y6, Y6; \
	VANDPD       ONE, Y8, Y9; \
	VSUBPD       Y9, Y7, Y7; \
	VSUBPD       ONE, Y6, Y9; \
	VADDPD       ONE, Y6, Y10; \
	VDIVPD       Y10, Y9, Y9; \
	VMULPD       Y9, Y9, Y10; \
	VBROADCASTSD ·plogPoly+96(SB), Y11; \
	VMULPD       Y10, Y11, Y11; \
	VBROADCASTSD ·plogPoly+88(SB), Y12; \
	VADDPD       Y12, Y11, Y11; \
	VMULPD       Y10, Y11, Y11; \
	VBROADCASTSD ·plogPoly+80(SB), Y12; \
	VADDPD       Y12, Y11, Y11; \
	VMULPD       Y10, Y11, Y11; \
	VBROADCASTSD ·plogPoly+72(SB), Y12; \
	VADDPD       Y12, Y11, Y11; \
	VMULPD       Y10, Y11, Y11; \
	VBROADCASTSD ·plogPoly+64(SB), Y12; \
	VADDPD       Y12, Y11, Y11; \
	VMULPD       Y10, Y11, Y11; \
	VBROADCASTSD ·plogPoly+56(SB), Y12; \
	VADDPD       Y12, Y11, Y11; \
	VMULPD       Y10, Y11, Y11; \
	VBROADCASTSD ·plogPoly+48(SB), Y12; \
	VADDPD       Y12, Y11, Y11; \
	VMULPD       Y10, Y11, Y11; \
	VBROADCASTSD ·plogPoly+40(SB), Y12; \
	VADDPD       Y12, Y11, Y11; \
	VMULPD       Y10, Y11, Y11; \
	VBROADCASTSD ·plogPoly+32(SB), Y12; \
	VADDPD       Y12, Y11, Y11; \
	VMULPD       Y10, Y11, Y11; \
	VBROADCASTSD ·plogPoly+24(SB), Y12; \
	VADDPD       Y12, Y11, Y11; \
	VMULPD       Y10, Y11, Y11; \
	VBROADCASTSD ·plogPoly+16(SB), Y12; \
	VADDPD       Y12, Y11, Y11; \
	VMULPD       Y10, Y11, Y11; \
	VBROADCASTSD ·plogPoly+8(SB), Y12; \
	VADDPD       Y12, Y11, Y11; \
	VMULPD       Y10, Y11, Y11; \
	VBROADCASTSD ·plogPoly+0(SB), Y12; \
	VADDPD       Y12, Y11, Y11; \
	VMULPD       Y11, Y9, Y11; \
	VMULPD       LN2LO, Y7, Y12; \
	VADDPD       Y11, Y12, Y12; \
	VMULPD       LN2HI, Y7, Y13; \
	VADDPD       Y12, Y13, Y2

// PEXP2 replaces each lane of Y5 with pexp2 of it, using Y6-Y9, for lanes
// with -1022 <= floor(x + 0.5) <= 1023 (thresholdsAVX2 passes x in
// [-5.2, -1.8]). k and f are formed as in pexp2, the series runs in its Horner
// order, and math.Ldexp(p, k) is the product with 2^k built in the exponent
// field, which is exact for every k in that range.
#define PEXP2 \
	VADDPD       HALF, Y5, Y6; \
	VROUNDPD     $0x09, Y6, Y6; \
	VSUBPD       Y6, Y5, Y7; \
	VBROADCASTSD ·pexp2Poly+112(SB), Y8; \
	VMULPD       Y7, Y8, Y8; \
	VBROADCASTSD ·pexp2Poly+104(SB), Y9; \
	VADDPD       Y9, Y8, Y8; \
	VMULPD       Y7, Y8, Y8; \
	VBROADCASTSD ·pexp2Poly+96(SB), Y9; \
	VADDPD       Y9, Y8, Y8; \
	VMULPD       Y7, Y8, Y8; \
	VBROADCASTSD ·pexp2Poly+88(SB), Y9; \
	VADDPD       Y9, Y8, Y8; \
	VMULPD       Y7, Y8, Y8; \
	VBROADCASTSD ·pexp2Poly+80(SB), Y9; \
	VADDPD       Y9, Y8, Y8; \
	VMULPD       Y7, Y8, Y8; \
	VBROADCASTSD ·pexp2Poly+72(SB), Y9; \
	VADDPD       Y9, Y8, Y8; \
	VMULPD       Y7, Y8, Y8; \
	VBROADCASTSD ·pexp2Poly+64(SB), Y9; \
	VADDPD       Y9, Y8, Y8; \
	VMULPD       Y7, Y8, Y8; \
	VBROADCASTSD ·pexp2Poly+56(SB), Y9; \
	VADDPD       Y9, Y8, Y8; \
	VMULPD       Y7, Y8, Y8; \
	VBROADCASTSD ·pexp2Poly+48(SB), Y9; \
	VADDPD       Y9, Y8, Y8; \
	VMULPD       Y7, Y8, Y8; \
	VBROADCASTSD ·pexp2Poly+40(SB), Y9; \
	VADDPD       Y9, Y8, Y8; \
	VMULPD       Y7, Y8, Y8; \
	VBROADCASTSD ·pexp2Poly+32(SB), Y9; \
	VADDPD       Y9, Y8, Y8; \
	VMULPD       Y7, Y8, Y8; \
	VBROADCASTSD ·pexp2Poly+24(SB), Y9; \
	VADDPD       Y9, Y8, Y8; \
	VMULPD       Y7, Y8, Y8; \
	VBROADCASTSD ·pexp2Poly+16(SB), Y9; \
	VADDPD       Y9, Y8, Y8; \
	VMULPD       Y7, Y8, Y8; \
	VBROADCASTSD ·pexp2Poly+8(SB), Y9; \
	VADDPD       Y9, Y8, Y8; \
	VMULPD       Y7, Y8, Y8; \
	VBROADCASTSD ·pexp2Poly+0(SB), Y9; \
	VADDPD       Y9, Y8, Y8; \
	VADDPD       EXPBIAS, Y6, Y6; \
	VPSLLQ       $52, Y6, Y6; \
	VMULPD       Y6, Y8, Y5

// UNPRED computes bins AX/8 to AX/8+3 of unpredAVX2 with Y13 = 0,
// Y14 = ABSMASK and Y15 = 1: Y3 = r, Y8 = r1, Y9 = r2m, Y10 = rp,
// Y11 = the no-history mask, Y6/Y7 = cos/sin 2f1, Y4/Y5 = cos/sin f2.
#define UNPRED \
	VMOVUPD   (SI)(AX*1), Y0; \
	VMOVUPD   (DI)(AX*1), Y1; \
	VMULPD    Y0, Y0, Y2; \
	VMULPD    Y1, Y1, Y3; \
	VADDPD    Y3, Y2, Y2; \
	VMOVUPD   Y2, (R10)(AX*1); \
	VSQRTPD   Y2, Y3; \
	VMOVUPD   (R8)(AX*1), Y4; \
	VMOVUPD   (R9)(AX*1), Y5; \
	VMULPD    Y4, Y4, Y6; \
	VMULPD    Y5, Y5, Y7; \
	VADDPD    Y7, Y6, Y8; \
	VSUBPD    Y7, Y6, Y6; \
	VDIVPD    Y8, Y6, Y6; \
	VADDPD    Y4, Y4, Y7; \
	VMULPD    Y5, Y7, Y7; \
	VDIVPD    Y8, Y7, Y7; \
	VSQRTPD   Y8, Y8; \
	VMOVUPD   4104(R8)(AX*1), Y4; \
	VMOVUPD   4104(R9)(AX*1), Y5; \
	VMULPD    Y4, Y4, Y9; \
	VMULPD    Y5, Y5, Y10; \
	VADDPD    Y10, Y9, Y9; \
	VSQRTPD   Y9, Y9; \
	VDIVPD    Y9, Y4, Y4; \
	VDIVPD    Y9, Y5, Y5; \
	VADDPD    Y8, Y8, Y10; \
	VSUBPD    Y9, Y10, Y10; \
	VCMPPD    $0x00, Y13, Y8, Y11; \
	VCMPPD    $0x00, Y13, Y9, Y12; \
	VORPD     Y12, Y11, Y11; \
	VCMPPD    $0x00, Y13, Y3, Y12; \
	VCMPPD    $0x00, Y13, Y10, Y2; \
	VANDPD    Y2, Y12, Y12; \
	VORPD     Y12, Y11, Y11; \
	VMULPD    Y4, Y6, Y2; \
	VMULPD    Y5, Y7, Y12; \
	VADDPD    Y12, Y2, Y2; \
	VMULPD    Y4, Y7, Y7; \
	VMULPD    Y5, Y6, Y6; \
	VSUBPD    Y6, Y7, Y7; \
	VMULPD    Y2, Y10, Y2; \
	VSUBPD    Y2, Y0, Y2; \
	VMULPD    Y7, Y10, Y7; \
	VSUBPD    Y7, Y1, Y7; \
	VMULPD    Y2, Y2, Y2; \
	VMULPD    Y7, Y7, Y7; \
	VADDPD    Y7, Y2, Y2; \
	VSQRTPD   Y2, Y2; \
	VANDPD    Y14, Y10, Y10; \
	VADDPD    Y10, Y3, Y10; \
	VDIVPD    Y10, Y2, Y2; \
	VCMPPD    $0x0E, Y15, Y2, Y12; \
	VBLENDVPD Y12, Y15, Y2, Y2; \
	VBLENDVPD Y11, Y15, Y2, Y2; \
	VMOVUPD   Y2, (R11)(AX*1)

// func unpredAVX2(re, im *[1024]float64, prevRe, prevIm *[2][513]float64, r2, cw *[513]float64)
// prevRe[1] and prevIm[1] start 4104 bytes after prevRe[0] and prevIm[0].
TEXT ·unpredAVX2(SB), NOSPLIT, $0-48
	MOVQ    re+0(FP), SI
	MOVQ    im+8(FP), DI
	MOVQ    prevRe+16(FP), R8
	MOVQ    prevIm+24(FP), R9
	MOVQ    r2+32(FP), R10
	MOVQ    cw+40(FP), R11
	VXORPD  Y13, Y13, Y13
	VMOVUPD ABSMASK, Y14
	VMOVUPD ONE, Y15
	XORQ    AX, AX

unpredLoop:
	UNPRED
	ADDQ $32, AX
	CMPQ AX, $4096
	JLT  unpredLoop

	// 513 is not a multiple of four: bins 509 to 512 cover the last one.
	MOVQ $4072, AX
	UNPRED
	VZEROUPPER
	RET

// func thresholdsAVX2(e, ct, ecb, cbs, nb *[psyMaxParts]float64, nbPrev *[2][psyMaxParts]float64, sprdT *[psyMaxParts][psyMaxParts]float64, norm, qthr *[psyMaxParts]float64, n int, tmn, nmt, rpelev2 float64)
// The spreading pass accumulates ecb (Y0, Y1) and cbs (Y2, Y3) of eight
// partitions over bb in order, one rounded product and one rounded sum per
// term. The threshold pass takes four partitions at a time: Y0 = ecb,
// Y3 = (ecb > 0), Y4 = (cbb <= 0), Y2 = tb, Y5 = the pexp2 argument and then
// bc, Y6 = raw and then nb. A sprdT row is 496 bytes, and nbPrev[1] starts
// 496 bytes after nbPrev[0].
TEXT ·thresholdsAVX2(SB), NOSPLIT, $0-104
	MOVQ e+0(FP), SI
	MOVQ ct+8(FP), DI
	MOVQ ecb+16(FP), R8
	MOVQ cbs+24(FP), R9
	MOVQ nb+32(FP), R10
	MOVQ nbPrev+40(FP), R11
	MOVQ sprdT+48(FP), R13
	MOVQ norm+56(FP), R14
	MOVQ qthr+64(FP), R15
	MOVQ n+72(FP), CX

	MOVQ CX, R12
	SUBQ $8, R12
	SHLQ $3, R12
	XORQ DX, DX

spreadBlock:
	VXORPD Y0, Y0, Y0
	VXORPD Y1, Y1, Y1
	VXORPD Y2, Y2, Y2
	VXORPD Y3, Y3, Y3
	LEAQ   (R13)(DX*1), BX
	XORQ   AX, AX

spreadTerm:
	VBROADCASTSD (SI)(AX*8), Y8
	VBROADCASTSD (DI)(AX*8), Y9
	VMOVUPD      (BX), Y10
	VMULPD       Y8, Y10, Y11
	VADDPD       Y11, Y0, Y0
	VMULPD       Y9, Y10, Y11
	VADDPD       Y11, Y2, Y2
	VMOVUPD      32(BX), Y10
	VMULPD       Y8, Y10, Y11
	VADDPD       Y11, Y1, Y1
	VMULPD       Y9, Y10, Y11
	VADDPD       Y11, Y3, Y3
	ADDQ         $496, BX
	INCQ         AX
	CMPQ         AX, CX
	JLT          spreadTerm

	VMOVUPD Y0, (R8)(DX*1)
	VMOVUPD Y1, 32(R8)(DX*1)
	VMOVUPD Y2, (R9)(DX*1)
	VMOVUPD Y3, 32(R9)(DX*1)
	CMPQ    DX, R12
	JEQ     spreadDone
	ADDQ    $64, DX
	CMPQ    DX, R12
	JLE     spreadBlock
	MOVQ    R12, DX
	JMP     spreadBlock

spreadDone:
	VXORPD  Y14, Y14, Y14
	VMOVUPD ONE, Y15
	ADDQ    $32, R12
	XORQ    DX, DX

toneGroup:
	VMOVUPD   (R8)(DX*1), Y0
	VMOVUPD   (R9)(DX*1), Y1
	VDIVPD    Y0, Y1, Y2
	VCMPPD    $0x0E, Y14, Y0, Y3
	VCMPPD    $0x02, Y14, Y2, Y4
	VCMPPD    $0x0E, Y15, Y2, Y5
	VBLENDVPD Y5, Y15, Y2, Y2
	PLOG
	VMULPD    TBSLOPE, Y2, Y5
	VMOVUPD   TBBASE, Y2
	VSUBPD    Y5, Y2, Y2
	VCMPPD    $0x01, Y14, Y2, Y5
	VBLENDVPD Y5, Y14, Y2, Y2
	VCMPPD    $0x0E, Y15, Y2, Y5
	VBLENDVPD Y5, Y15, Y2, Y2
	VBLENDVPD Y4, Y15, Y2, Y2
	VANDPD    Y3, Y2, Y2
	VBROADCASTSD tmn+80(FP), Y5
	VMULPD    Y5, Y2, Y5
	VSUBPD    Y2, Y15, Y6
	VBROADCASTSD nmt+88(FP), Y7
	VMULPD    Y7, Y6, Y6
	VADDPD    Y6, Y5, Y5
	VMULPD    SNRSCALE, Y5, Y5
	PEXP2
	VMULPD    (R14)(DX*1), Y0, Y6
	VMULPD    Y5, Y6, Y6
	VMOVUPD   (R11)(DX*1), Y7
	VADDPD    Y7, Y7, Y7
	VMOVUPD   496(R11)(DX*1), Y8
	VBROADCASTSD rpelev2+96(FP), Y9
	VMULPD    Y9, Y8, Y8
	VMINPD    Y7, Y6, Y6
	VMINPD    Y8, Y6, Y6
	VMOVUPD   (R15)(DX*1), Y7
	VCMPPD    $0x01, Y7, Y6, Y8
	VBLENDVPD Y8, Y7, Y6, Y6
	VMOVUPD   Y6, (R10)(DX*1)
	CMPQ      DX, R12
	JEQ       toneDone
	ADDQ      $32, DX
	CMPQ      DX, R12
	JLE       toneGroup
	MOVQ      R12, DX
	JMP       toneGroup

toneDone:
	VZEROUPPER
	RET

// func shortThresholdsAVX2(eS, nbS *[psyMaxPartsS]float64, sprdT *[psyMaxPartsS][psyMaxPartsS]float64, norm, qthr *[psyMaxPartsS]float64, n int, bc float64)
// Y0 and Y1 accumulate ecb of eight partitions over bb in order; a sprdT row
// is 352 bytes.
TEXT ·shortThresholdsAVX2(SB), NOSPLIT, $0-56
	MOVQ         eS+0(FP), SI
	MOVQ         nbS+8(FP), DI
	MOVQ         sprdT+16(FP), R13
	MOVQ         norm+24(FP), R14
	MOVQ         qthr+32(FP), R15
	MOVQ         n+40(FP), CX
	VBROADCASTSD bc+48(FP), Y12

	MOVQ CX, R12
	SUBQ $8, R12
	SHLQ $3, R12
	XORQ DX, DX

shortBlock:
	VXORPD Y0, Y0, Y0
	VXORPD Y1, Y1, Y1
	LEAQ   (R13)(DX*1), BX
	XORQ   AX, AX

shortTerm:
	VBROADCASTSD (SI)(AX*8), Y8
	VMULPD       (BX), Y8, Y10
	VADDPD       Y10, Y0, Y0
	VMULPD       32(BX), Y8, Y11
	VADDPD       Y11, Y1, Y1
	ADDQ         $352, BX
	INCQ         AX
	CMPQ         AX, CX
	JLT          shortTerm

	VMULPD    (R14)(DX*1), Y0, Y0
	VMULPD    Y12, Y0, Y0
	VMOVUPD   (R15)(DX*1), Y2
	VCMPPD    $0x01, Y2, Y0, Y3
	VBLENDVPD Y3, Y2, Y0, Y0
	VMOVUPD   Y0, (DI)(DX*1)
	VMULPD    32(R14)(DX*1), Y1, Y1
	VMULPD    Y12, Y1, Y1
	VMOVUPD   32(R15)(DX*1), Y2
	VCMPPD    $0x01, Y2, Y1, Y3
	VBLENDVPD Y3, Y2, Y1, Y1
	VMOVUPD   Y1, 32(DI)(DX*1)
	CMPQ      DX, R12
	JEQ       shortDone
	ADDQ      $64, DX
	CMPQ      DX, R12
	JLE       shortBlock
	MOVQ      R12, DX
	JMP       shortBlock

shortDone:
	VZEROUPPER
	RET

// func plogsAVX2(dst, src *float64, n int)
TEXT ·plogsAVX2(SB), NOSPLIT, $0-24
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ n+16(FP), R12
	SUBQ $4, R12
	SHLQ $3, R12
	XORQ DX, DX

plogsGroup:
	VMOVUPD (SI)(DX*1), Y2
	PLOG
	VMOVUPD Y2, (DI)(DX*1)
	CMPQ    DX, R12
	JEQ     plogsDone
	ADDQ    $32, DX
	CMPQ    DX, R12
	JLE     plogsGroup
	MOVQ    R12, DX
	JMP     plogsGroup

plogsDone:
	VZEROUPPER
	RET
