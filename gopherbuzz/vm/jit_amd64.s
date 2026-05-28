// Baseline JIT entry trampoline (amd64). jitEntry calls generated code that
// takes *jitCtx in DI, writes results through it, and returns via RET. The code
// never allocates, grows the stack, or calls back into Go, so entering it is a C
// leaf call: NOSPLIT, no stack map. We save the callee-saved regs it may use.

//go:build amd64 && !buzz_safe && !buzz_unsafe

#include "textflag.h"

// func jitEntry(code *byte, ctx *jitCtx)
TEXT ·jitEntry(SB), NOSPLIT, $64-16
	MOVQ code+0(FP), AX
	MOVQ ctx+8(FP), DI
	// Preserve the registers the generated code is allowed to use that a Go
	// caller may expect intact across this ABI0 call.
	MOVQ BX, 0(SP)
	MOVQ R12, 8(SP)
	MOVQ R13, 16(SP)
	MOVQ R14, 24(SP)
	MOVQ R15, 32(SP)
	CALL AX
	MOVQ 0(SP), BX
	MOVQ 8(SP), R12
	MOVQ 16(SP), R13
	MOVQ 24(SP), R14
	MOVQ 32(SP), R15
	RET
