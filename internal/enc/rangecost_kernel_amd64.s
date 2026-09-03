//go:build !noasm

#include "textflag.h"

// func subMinReduceAVX2(a, b []int32) int32
// Returns min over i in [0,n) of a[i]-b[i], where n = len(a). Requires
// len(a) == len(b) == n >= 8. Fused: subtract into a running 8-lane vector min,
// one horizontal reduce, scalar tail. No scratch buffer. The horizontal-reduce
// shuffle sequence mirrors the simd i32 MaxAbs kernel.
TEXT ·subMinReduceAVX2(SB), NOSPLIT, $0-52
    MOVQ a_base+0(FP), R8       // a ptr
    MOVQ a_len+8(FP), CX        // n (>=8)
    MOVQ b_base+24(FP), DI      // b ptr
    MOVQ CX, R9
    SHRQ $3, R9                 // full 8-element blocks (>=1)
    VMOVDQU (R8), Y0
    VMOVDQU (DI), Y2
    VPSUBD  Y2, Y0, Y0          // acc = a[0:8] - b[0:8]
    MOVQ R9, AX                 // block counter
    DECQ AX                     // blocks after block 0
    JZ   smr_reduce
    LEAQ 32(R8), SI             // a working ptr at block 1
    LEAQ 32(DI), BX             // b working ptr at block 1
smr_loop:
    VMOVDQU (SI), Y1
    VMOVDQU (BX), Y3
    VPSUBD  Y3, Y1, Y1          // Y1 = a - b
    VPMINSD Y1, Y0, Y0          // acc = lanewise min(acc, diff)
    ADDQ $32, SI
    ADDQ $32, BX
    DECQ AX
    JNZ  smr_loop
smr_reduce:
    VEXTRACTI128 $1, Y0, X3
    VPMINSD X3, X0, X0          // fold lanes 4..7 into 0..3
    VPSHUFD $0x4E, X0, X3       // swap 64-bit halves
    VPMINSD X3, X0, X0
    VPSHUFD $0xB1, X0, X3       // swap 32-bit within pairs
    VPMINSD X3, X0, X0          // lane0 = min over all 8 lanes
    MOVQ X0, AX                 // EAX = running min
    // scalar tail: (n mod 8) residuals at &a[fullBlocks*8], &b[...]
    MOVQ CX, DX
    ANDQ $7, DX
    JZ   smr_done
    MOVQ R9, R10
    SHLQ $5, R10               // fullBlocks * 32 bytes
    LEAQ (R8)(R10*1), R11      // a tail ptr
    LEAQ (DI)(R10*1), R12      // b tail ptr
smr_tail:
    MOVL (R11), R13
    SUBL (R12), R13            // r = a[i] - b[i]
    CMPL R13, AX
    JGE  smr_tail_next
    MOVL R13, AX               // r < min -> new min
smr_tail_next:
    ADDQ $4, R11
    ADDQ $4, R12
    DECQ DX
    JNZ  smr_tail
smr_done:
    MOVL AX, ret+48(FP)
    VZEROUPPER
    RET
