//go:build !noasm

#include "textflag.h"

// func escFamilyAccumNEON(acc16, acc24 *[8]int32, ax, ay, base16, base24 []int32)
//
// arm64 NEON counterpart of escFamilyAccumAVX2. Each family's 8 table lanes live
// in two 4-wide int32 vectors (lo tables 0..3, hi tables 4..7): acc16 in V0/V1,
// acc24 in V2/V3, resident across the whole run and written back once. Per pair,
// per table: impossibleCost if ax>maxv[j] || ay>maxv[j], else
// base + escCount*linbits[j]. The common non-escaping pair (both magnitudes
// < 15) takes a scalar-gated fast path of pure broadcast-adds. Bit-exact mirror
// of escFamilyAccumGo. Requires len==n for all slices; n>=0 safe.
TEXT ·escFamilyAccumNEON(SB), NOSPLIT, $0-112
	MOVD acc16+0(FP), R0
	MOVD acc24+8(FP), R1
	VLD1 (R0), [V0.S4, V1.S4]            // acc16 (in/out)
	VLD1 (R1), [V2.S4, V3.S4]            // acc24 (in/out)
	MOVD $·escFam16MaxVal(SB), R9
	VLD1 (R9), [V4.S4, V5.S4]            // maxv16 lo/hi
	MOVD $·escFam24MaxVal(SB), R9
	VLD1 (R9), [V6.S4, V7.S4]            // maxv24 lo/hi
	MOVD $·escFam16LinbitsI32(SB), R9
	VLD1 (R9), [V8.S4, V9.S4]            // linb16 lo/hi
	MOVD $·escFam24LinbitsI32(SB), R9
	VLD1 (R9), [V10.S4, V11.S4]          // linb24 lo/hi
	MOVD $·escFamImpossible(SB), R9
	VLD1R (R9), [V12.S4]                 // impossibleCost, all lanes

	MOVD ax_base+16(FP), R2
	MOVD ay_base+40(FP), R3
	MOVD base16_base+64(FP), R4
	MOVD base24_base+88(FP), R5
	MOVD ax_len+24(FP), R6
	CBZ R6, ef_store
ef_loop:
	MOVW (R2), R7                        // ax[p]
	MOVW (R3), R8                        // ay[p]
	CMP $15, R7
	BGE ef_slow
	CMP $15, R8
	BGE ef_slow
	// fast path: both < escMaxDirect -> addend is base for every table
	VLD1R (R4), [V13.S4]
	VADD V13.S4, V0.S4, V0.S4
	VADD V13.S4, V1.S4, V1.S4
	VLD1R (R5), [V14.S4]
	VADD V14.S4, V2.S4, V2.S4
	VADD V14.S4, V3.S4, V3.S4
	B ef_next
ef_slow:
	VLD1R (R4), [V13.S4]                 // addend16 lo = base16
	VLD1R (R4), [V14.S4]                 // addend16 hi = base16
	VLD1R (R5), [V15.S4]                 // addend24 lo = base24
	VLD1R (R5), [V16.S4]                 // addend24 hi = base24
	CMP $15, R7
	BLT ef_noEscX
	VADD V8.S4, V13.S4, V13.S4           // ax escapes: += linbits
	VADD V9.S4, V14.S4, V14.S4
	VADD V10.S4, V15.S4, V15.S4
	VADD V11.S4, V16.S4, V16.S4
ef_noEscX:
	CMP $15, R8
	BLT ef_noEscY
	VADD V8.S4, V13.S4, V13.S4           // ay escapes: += linbits
	VADD V9.S4, V14.S4, V14.S4
	VADD V10.S4, V15.S4, V15.S4
	VADD V11.S4, V16.S4, V16.S4
ef_noEscY:
	VDUP R7, V17.S4                      // bax
	VDUP R8, V18.S4                      // bay
	// family 16 lo: mask = (bax>maxv16lo)|(bay>maxv16lo); addend = mask?imp:addend
	VCMGT V4.S4, V17.S4, V19.S4
	VCMGT V4.S4, V18.S4, V20.S4
	VORR V20.B16, V19.B16, V19.B16
	VBSL V13.B16, V12.B16, V19.B16       // V19 = mask?imp:addend16lo
	VADD V19.S4, V0.S4, V0.S4
	// family 16 hi
	VCMGT V5.S4, V17.S4, V20.S4
	VCMGT V5.S4, V18.S4, V21.S4
	VORR V21.B16, V20.B16, V20.B16
	VBSL V14.B16, V12.B16, V20.B16       // V20 = mask?imp:addend16hi
	VADD V20.S4, V1.S4, V1.S4
	// family 24 lo
	VCMGT V6.S4, V17.S4, V19.S4
	VCMGT V6.S4, V18.S4, V21.S4
	VORR V21.B16, V19.B16, V19.B16
	VBSL V15.B16, V12.B16, V19.B16       // V19 = mask?imp:addend24lo
	VADD V19.S4, V2.S4, V2.S4
	// family 24 hi
	VCMGT V7.S4, V17.S4, V20.S4
	VCMGT V7.S4, V18.S4, V21.S4
	VORR V21.B16, V20.B16, V20.B16
	VBSL V16.B16, V12.B16, V20.B16       // V20 = mask?imp:addend24hi
	VADD V20.S4, V3.S4, V3.S4
ef_next:
	ADD $4, R2
	ADD $4, R3
	ADD $4, R4
	ADD $4, R5
	SUB $1, R6
	CBNZ R6, ef_loop
ef_store:
	VST1 [V0.S4, V1.S4], (R0)
	VST1 [V2.S4, V3.S4], (R1)
	RET
