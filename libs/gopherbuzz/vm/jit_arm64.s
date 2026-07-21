// Baseline JIT entry trampoline (arm64). jitEntry calls generated code that
// takes *jitCtx in R0, writes results through it, and returns via RET. The code
// uses only caller-saved registers (R0-R6), never allocates, grows the stack, or
// calls back into Go — a C leaf call. The $16 frame lets Go save/restore LR
// across the indirect CALL.

//go:build arm64 && !windows && !buzz_safe && !buzz_unsafe

#include "textflag.h"

// func jitEntry(code *byte, ctx *jitCtx)
TEXT ·jitEntry(SB), NOSPLIT, $16-16
	MOVD code+0(FP), R1
	MOVD ctx+8(FP), R0
	CALL (R1)
	RET

// func flushICache(p *byte, n int)
//
// AArch64 is not I-cache coherent: bytes written through the data cache and then
// made executable (Mprotect RX) are NOT guaranteed visible to the instruction
// fetch path. Calling generated code without this maintenance can execute stale
// bytes — intermittent, CPU-dependent wrong results or crashes. This emits the
// architectural clean-DC / invalidate-IC to Point of Unification sequence over
// [p, p+n). A fixed 8-byte stride is below the minimum cache-line size (16 B), so
// every line in the range is covered; the redundant ops on the same line are
// harmless and let us skip reading CTR_EL0. The bracketing barriers order the
// maintenance against the prior writes and the subsequent fetch.
TEXT ·flushICache(SB), NOSPLIT, $0-16
	MOVD p+0(FP), R0
	MOVD n+8(FP), R1
	ADD  R0, R1, R2 // R2 = end
	MOVD R0, R3     // R3 = cursor
dcloop:
	DC   CVAU, R3   // clean data cache line to PoU
	ADD  $8, R3, R3
	CMP  R2, R3
	BLO  dcloop
	DSB  $11        // DSB ISH: order the cleans before the invalidates
	MOVD R0, R3     // reset cursor
icloop:
	// IC IVAU, R3 — invalidate instruction cache line to PoU. Go's arm64
	// assembler lacks the IVAU mnemonic, so emit it as the equivalent SYS:
	// IC IVAU, Xt == SYS #3, C7, C5, #1, Xt == SYS $0x37520, Rt.
	SYS  $0x37520, R3
	ADD  $8, R3, R3
	CMP  R2, R3
	BLO  icloop
	DSB  $11        // DSB ISH: order the invalidates before the fetch
	ISB  $15        // ISB SY: flush the pipeline so refetch sees fresh code
	RET
