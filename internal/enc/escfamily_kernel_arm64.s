//go:build !noasm

#include "textflag.h"

// func escFamilyPrefixNEON(prefixCost *[40][32]int32, pb *[40]int, nBands int, ax, ay, base16, base24 *[288]int32)
//
// arm64 NEON counterpart of escFamilyPrefixAVX2 (issue #68). Each family's 8
// table lanes live in two 4-wide int32 vectors (lo tables 0..3, hi tables 4..7):
// acc16 in V0/V1, acc24 in V2/V3, resident across the whole run and zeroed once.
// Walks coding bands 0..nBands-1; for each band it accumulates that band's pairs
// then writes the CUMULATIVE accumulators into prefixCost[k+1][16..23] and
// [24..31] as two contiguous two-vector stores at the 128-byte row stride. Band
// k covers pairs [pb[k], pb[k+1]); pb[0] is 0, so the pair pointers advance
// continuously and only pb[k+1] is read. A zero-width band stores the unchanged
// accumulators. Columns 0..15 and row 0 are never touched.
//
// Per-pair cost is the bit-exact mirror of escFamilyAccumGo, per table j:
//   impossibleCost                 if ax>maxv[j] || ay>maxv[j]
//   base + escCount*linbits[j]     otherwise
// with escCount = (ax>=15) + (ay>=15). The common non-escaping pair takes a
// scalar-gated fast path of pure broadcast-adds. The per-pair body is identical
// to the former per-band kernel (ax/ay scalars in R11/R12 here, freeing R6/R7/R8
// for the pair counter, band end and band index); only the band loop and
// in-place stores wrap it.
TEXT ·escFamilyPrefixNEON(SB), NOSPLIT, $0-56
	MOVD nBands+16(FP), R1
	CMP $0, R1
	BLE ret                              // nBands <= 0: nothing to do
	VEOR V0.B16, V0.B16, V0.B16          // acc16 lo = 0
	VEOR V1.B16, V1.B16, V1.B16          // acc16 hi = 0
	VEOR V2.B16, V2.B16, V2.B16          // acc24 lo = 0
	VEOR V3.B16, V3.B16, V3.B16          // acc24 hi = 0
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

	MOVD pb+8(FP), R0
	ADD $8, R0                           // &pb[1] (only pb[k+1] is read)
	MOVD ax+24(FP), R2
	MOVD ay+32(FP), R3
	MOVD base16+40(FP), R4
	MOVD base24+48(FP), R5
	MOVD prefixCost+0(FP), R10
	ADD $128, R10                        // &prefixCost[1] (row stride 32*4)
	MOVD $0, R6                          // p = 0
	MOVD $0, R8                          // k = 0

band_loop:
	MOVD (R0), R7                        // end = pb[k+1]
pair_loop:
	CMP R7, R6
	BGE band_store                       // p >= end: band done
	MOVW (R2), R11                       // ax[p]
	MOVW (R3), R12                       // ay[p]
	CMP $15, R11
	BGE slow
	CMP $15, R12
	BGE slow
	// fast path: both < escMaxDirect -> addend is base for every table
	VLD1R (R4), [V13.S4]
	VADD V13.S4, V0.S4, V0.S4
	VADD V13.S4, V1.S4, V1.S4
	VLD1R (R5), [V14.S4]
	VADD V14.S4, V2.S4, V2.S4
	VADD V14.S4, V3.S4, V3.S4
	B pair_next
slow:
	VLD1R (R4), [V13.S4]                 // addend16 lo = base16
	VLD1R (R4), [V14.S4]                 // addend16 hi = base16
	VLD1R (R5), [V15.S4]                 // addend24 lo = base24
	VLD1R (R5), [V16.S4]                 // addend24 hi = base24
	CMP $15, R11
	BLT noEscX
	VADD V8.S4, V13.S4, V13.S4           // ax escapes: += linbits
	VADD V9.S4, V14.S4, V14.S4
	VADD V10.S4, V15.S4, V15.S4
	VADD V11.S4, V16.S4, V16.S4
noEscX:
	CMP $15, R12
	BLT noEscY
	VADD V8.S4, V13.S4, V13.S4           // ay escapes: += linbits
	VADD V9.S4, V14.S4, V14.S4
	VADD V10.S4, V15.S4, V15.S4
	VADD V11.S4, V16.S4, V16.S4
noEscY:
	VDUP R11, V17.S4                     // bax
	VDUP R12, V18.S4                     // bay
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
pair_next:
	ADD $4, R2
	ADD $4, R3
	ADD $4, R4
	ADD $4, R5
	ADD $1, R6
	B pair_loop
band_store:
	ADD $64, R10, R9                     // col16 base = row + 64
	VST1 [V0.S4, V1.S4], (R9)            // prefixCost[k+1][16..23]
	ADD $96, R10, R9                     // col24 base = row + 96
	VST1 [V2.S4, V3.S4], (R9)            // prefixCost[k+1][24..31]
	ADD $8, R0                           // &pb[k+2]
	ADD $128, R10                        // next prefixCost row
	ADD $1, R8                           // k++
	CMP R1, R8
	BLT band_loop
ret:
	RET
