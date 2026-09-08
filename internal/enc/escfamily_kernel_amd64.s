//go:build !noasm

#include "textflag.h"

// func escFamilyAccumAVX2(acc16, acc24 *[8]int32, ax, ay, base16, base24 []int32)
//
// Accumulates the escape-family big-values cost of pairs [0,n), n=len(ax), into
// the two per-family 8-lane accumulators acc16 (tables 16..23) and acc24
// (24..31). Bit-exact mirror of escFamilyAccumGo: per pair, per table j,
//   impossibleCost                     if ax>maxv[j] || ay>maxv[j]
//   base + escCount*linbits[j]         otherwise
// where escCount = (ax>=15) + (ay>=15). The common non-escaping pair (both
// magnitudes < 15) takes a fast path: escCount is 0 and no maxv can bind, so it
// is just acc += broadcast(base), skipping the compare/blend. Both accumulators
// stay resident in Y0/Y1 across the whole run and are written back once.
//
// Requires len(ay)==len(base16)==len(base24)==n. n>=0 is safe (n==0 stores the
// accumulators unchanged); the Go dispatcher only routes here for n>=4.
TEXT ·escFamilyAccumAVX2(SB), NOSPLIT, $0-112
	MOVQ acc16+0(FP), R13
	MOVQ acc24+8(FP), R14
	VMOVDQU (R13), Y0                    // acc16 (in/out)
	VMOVDQU (R14), Y1                    // acc24 (in/out)
	VMOVDQU ·escFam16MaxVal(SB), Y2      // per-table maxVal, family 16
	VMOVDQU ·escFam24MaxVal(SB), Y3      // per-table maxVal, family 24
	VMOVDQU ·escFam16LinbitsI32(SB), Y4  // per-table linbits, family 16
	VMOVDQU ·escFam24LinbitsI32(SB), Y5  // per-table linbits, family 24
	VPBROADCASTD ·escFamImpossible(SB), Y6

	MOVQ ax_base+16(FP), R8
	MOVQ ay_base+40(FP), R9
	MOVQ base16_base+64(FP), R10
	MOVQ base24_base+88(FP), R11
	MOVQ ax_len+24(FP), R12
	TESTQ R12, R12
	JZ ef_store
ef_loop:
	MOVL (R8), AX                        // ax[p]
	MOVL (R9), CX                        // ay[p]
	CMPL AX, $15
	JGE ef_slow
	CMPL CX, $15
	JGE ef_slow
	// fast path: both < escMaxDirect -> addend is just base for every table
	VPBROADCASTD (R10), Y7               // base16[p]
	VPADDD Y7, Y0, Y0
	VPBROADCASTD (R11), Y8               // base24[p]
	VPADDD Y8, Y1, Y1
	JMP ef_next
ef_slow:
	VPBROADCASTD (R10), Y7               // addend16 = base16
	VPBROADCASTD (R11), Y8               // addend24 = base24
	CMPL AX, $15
	JL ef_noEscX
	VPADDD Y4, Y7, Y7                    // ax escapes: += linbits
	VPADDD Y5, Y8, Y8
ef_noEscX:
	CMPL CX, $15
	JL ef_noEscY
	VPADDD Y4, Y7, Y7                    // ay escapes: += linbits
	VPADDD Y5, Y8, Y8
ef_noEscY:
	VPBROADCASTD (R8), Y9                // bax
	VPBROADCASTD (R9), Y10               // bay
	// family 16: mask where ax>maxv16 || ay>maxv16, then select impossibleCost
	VPCMPGTD Y2, Y9, Y11                 // bax > maxv16
	VPCMPGTD Y2, Y10, Y12                // bay > maxv16
	VPOR Y12, Y11, Y11
	VPBLENDVB Y11, Y6, Y7, Y7            // mask ? impossible : addend16
	VPADDD Y7, Y0, Y0
	// family 24
	VPCMPGTD Y3, Y9, Y11                 // bax > maxv24
	VPCMPGTD Y3, Y10, Y12                // bay > maxv24
	VPOR Y12, Y11, Y11
	VPBLENDVB Y11, Y6, Y8, Y8            // mask ? impossible : addend24
	VPADDD Y8, Y1, Y1
ef_next:
	ADDQ $4, R8
	ADDQ $4, R9
	ADDQ $4, R10
	ADDQ $4, R11
	DECQ R12
	JNZ ef_loop
ef_store:
	VMOVDQU Y0, (R13)
	VMOVDQU Y1, (R14)
	VZEROUPPER
	RET
