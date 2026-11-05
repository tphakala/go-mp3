//go:build !noasm

#include "textflag.h"

// func quantizeAVX2(xr *[576]float64, ix *[576]int32, steps *[39]float64, widths *[39]int, nBands int, rounding, limit, zeroMax float64)
// Each line computes sqrt(t*sqrt(t)) + rounding, clamps it to limit, truncates it to int32 and
// negates it when xr is negative: the same comparison, truncation and negation as quantizeGranuleGo.
// A group whose t are all below zeroMax (the ordered compare is false for a NaN) is stored as zeros
// without the square roots.
TEXT ·quantizeAVX2(SB), NOSPLIT, $0-64
	MOVQ         xr+0(FP), SI
	MOVQ         ix+8(FP), DI
	MOVQ         steps+16(FP), R8
	MOVQ         widths+24(FP), R9
	MOVQ         nBands+32(FP), CX
	VBROADCASTSD rounding+40(FP), Y13
	VBROADCASTSD limit+48(FP), Y12
	VBROADCASTSD zeroMax+56(FP), Y9
	VPCMPEQQ     Y15, Y15, Y15
	VPSRLQ       $1, Y15, Y15
	VPCMPEQQ     Y10, Y10, Y10
	VPSLLQ       $63, Y10, Y10
	VXORPD       Y11, Y11, Y11
	TESTQ        CX, CX
	JZ           done

band:
	VBROADCASTSD (R8), Y14
	MOVQ         (R9), DX

quad:
	CMPQ        DX, $4
	JLT         pair
	VMOVUPD     (SI), Y0
	VANDPD      Y15, Y0, Y1
	VMULPD      Y14, Y1, Y1
	VCMPPD      $1, Y9, Y1, Y3
	VMOVMSKPD   Y3, AX
	CMPL        AX, $15
	JEQ         quadzero
	VSQRTPD     Y1, Y2
	VMULPD      Y2, Y1, Y2
	VSQRTPD     Y2, Y2
	VADDPD      Y13, Y2, Y2
	VMINPD      Y12, Y2, Y2
	VCMPPD      $1, Y11, Y0, Y3
	VANDPD      Y10, Y3, Y3
	VXORPD      Y3, Y2, Y2
	VCVTTPD2DQY Y2, X4
	VMOVDQU     X4, (DI)
	ADDQ        $32, SI
	ADDQ        $16, DI
	SUBQ        $4, DX
	JMP         quad

quadzero:
	VMOVDQU     X11, (DI)
	ADDQ        $32, SI
	ADDQ        $16, DI
	SUBQ        $4, DX
	JMP         quad

pair:
	TESTQ       DX, DX
	JZ          next
	VMOVUPD     (SI), X0
	VANDPD      X15, X0, X1
	VMULPD      X14, X1, X1
	VCMPPD      $1, X9, X1, X3
	VMOVMSKPD   X3, AX
	CMPL        AX, $3
	JEQ         pairzero
	VSQRTPD     X1, X2
	VMULPD      X2, X1, X2
	VSQRTPD     X2, X2
	VADDPD      X13, X2, X2
	VMINPD      X12, X2, X2
	VCMPPD      $1, X11, X0, X3
	VANDPD      X10, X3, X3
	VXORPD      X3, X2, X2
	VCVTTPD2DQX X2, X4
	VMOVQ       X4, (DI)
	ADDQ        $16, SI
	ADDQ        $8, DI
	JMP         next

pairzero:
	MOVQ        $0, (DI)
	ADDQ        $16, SI
	ADDQ        $8, DI

next:
	ADDQ $8, R8
	ADDQ $8, R9
	DECQ CX
	JNZ  band

done:
	VZEROUPPER
	RET
