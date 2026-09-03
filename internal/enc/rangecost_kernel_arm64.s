//go:build !noasm

#include "textflag.h"

// func subMinReduceNEON(a, b []int32) int32
// Returns min over i in [0,n) of a[i]-b[i], n = len(a). Requires
// len(a) == len(b) == n >= 4. Fused: subtract into a running 4-lane vector min,
// SMINV horizontal reduce, scalar tail. SMIN/SMINV and the vector SUB have no Go
// arm64 mnemonics, so they are the verbatim instruction encodings (fields
// cross-checked against the simd i32 MaxAbs kernel's SMIN/SMINV WORDs).
TEXT ·subMinReduceNEON(SB), NOSPLIT, $0-52
    MOVD a_base+0(FP), R2
    MOVD a_len+8(FP), R3
    MOVD b_base+24(FP), R8
    LSR  $2, R3, R4                  // R4 = full 4-element blocks (>=1)
    VLD1.P 16(R2), [V0.S4]           // a[0:4], advance
    VLD1.P 16(R8), [V2.S4]           // b[0:4], advance
    WORD $0x6EA28400                 // SUB V0.4S, V0.4S, V2.4S  (acc = a-b; Rd0 Rn0 Rm2)
    SUB  $1, R4                      // blocks after block 0
    CBZ  R4, smr_neon_reduce
smr_neon_loop:
    VLD1.P 16(R2), [V1.S4]           // a block, advance
    VLD1.P 16(R8), [V2.S4]           // b block, advance
    WORD $0x6EA28421                 // SUB  V1.4S, V1.4S, V2.4S  (Rd1 Rn1 Rm2)
    WORD $0x4EA16C00                 // SMIN V0.4S, V0.4S, V1.4S  (acc=min(acc,diff); Rd0 Rn0 Rm1)
    SUB  $1, R4
    CBNZ R4, smr_neon_loop
smr_neon_reduce:
    WORD $0x4EB1A803                 // SMINV S3, V0.4S
    FMOVS F3, R5                     // R5 = running min (low 32 = int32)
    // scalar tail: (n mod 4) residuals; R2, R8 already at &a[full*4], &b[full*4]
    AND  $3, R3, R4
    CBZ  R4, smr_neon_done
smr_neon_tail:
    MOVW.P 4(R2), R6                 // a[i] (sign-extended)
    MOVW.P 4(R8), R7                 // b[i]
    SUBW R7, R6, R6                  // r = a - b (32-bit)
    CMPW R5, R6                      // (R6 - R5), signed 32-bit
    CSEL LT, R6, R5, R5             // R5 = min(r, R5)
    SUB  $1, R4
    CBNZ R4, smr_neon_tail
smr_neon_done:
    MOVW R5, ret+48(FP)
    RET
