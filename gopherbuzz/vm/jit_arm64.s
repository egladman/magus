// Baseline JIT entry trampoline (arm64). jitEntry calls generated code that
// takes *jitCtx in R0, writes results through it, and returns via RET. The code
// uses only caller-saved registers (R0-R6), never allocates, grows the stack, or
// calls back into Go — a C leaf call. The $16 frame lets Go save/restore LR
// across the indirect CALL.

//go:build arm64 && !buzz_safe && !buzz_unsafe

#include "textflag.h"

// func jitEntry(code *byte, ctx *jitCtx)
TEXT ·jitEntry(SB), NOSPLIT, $16-16
	MOVD code+0(FP), R1
	MOVD ctx+8(FP), R0
	CALL (R1)
	RET
