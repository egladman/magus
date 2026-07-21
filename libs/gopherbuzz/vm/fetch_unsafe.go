//go:build buzz_unsafe

package vm

import "unsafe"

// fetch returns code[ip] without a bounds check. It is the per-opcode
// instruction fetch in the dispatch loop (exec), run once for every instruction
// executed, so the bounds check it elides is the single most frequently executed
// branch in the interpreter.
//
// ── Why unsafe here ──────────────────────────────────────────────────────────
//
// code[ip] carries an IsInBounds check the compiler cannot prove away, because
// from its local view ip is just an int that gets reassigned by jumps. But the
// VM maintains an invariant that makes the check provably redundant: every chunk
// the compiler emits ends in a terminating OpReturn/OpReturnNull (emitted
// unconditionally at each chunk-creation site — see CompileWith and
// compileFunChunk), and that terminator pops the frame before ip can advance
// past it. Control flow therefore can never fall off the end of code, and every
// jump target the compiler emits is an index into the same code slice. So ip is
// always in [0, len(code)) at the point of fetch — the bounds check never fails
// on a well-formed chunk, and well-formedness is guaranteed by the compiler, not
// by anything reachable from Buzz source.
//
// ── Why this is safe ─────────────────────────────────────────────────────────
//
//   - The pointer stays within the backing array: ip < len(code) by the
//     terminator invariant above, so &code[0] + ip*size addresses a real element
//     that the code slice keeps alive (we hold code for the whole call). No
//     pointer escapes, none is laundered through uintptr beyond the single
//     add+deref the runtime itself would do, and Instr contains no pointers so
//     there is no GC tracing concern for the element.
//   - The safe build (fetch_safe.go, -tags buzz_safe) does the plain checked
//     code[ip]. CI builds and tests both, so if a malformed chunk ever violated
//     the invariant the safe build would panic with a clear index-out-of-range
//     at exactly this line, instead of the unsafe build reading one element of
//     garbage. (The unsafe build's own recover() in exec still backstops a truly
//     corrupt chunk, but the safe twin is the precise check.)
//
// This mirrors the existing "no per-instruction f.ip >= len(code) bound check"
// decision already documented in exec(): same invariant, now also applied to the
// indexing itself rather than only the end-of-stream guard.
//
// measured: see bench/fetch_unsafe.txt.
// assumes: every chunk ends in a terminating return (compiler invariant); ip is
//
//	only ever set to a compiler-emitted offset within this code slice.
func fetch(code []Instr, ip int) Instr {
	//nolint:gosec // G103: audited unchecked fetch — ip is in [0,len(code)) by the
	// chunk-terminator invariant documented above; Instr holds no pointers. The
	// buzz_safe twin (fetch_safe.go) does the checked index.
	return *(*Instr)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(code)), uintptr(ip)*unsafe.Sizeof(Instr{})))
}

// vget returns s[i] without a bounds check. It is the value-load counterpart to
// fetch, used by the hot load opcodes (OpLoadConst, OpGetLocal, OpGetUpvalue)
// where i is provably in range:
//
//   - OpLoadConst: i is a const-pool index the compiler emitted via AddConst, so
//     i < len(chunk.Consts) by construction.
//   - OpGetLocal: i is f.base+slot with slot < chunk.LocalCount; OpCall/enterFun
//     pre-size the register window to base+LocalCount (growWindow), and the
//     operand stack only ever grows above it, so i < len(vm.stack) for the whole
//     frame.
//   - OpGetUpvalue: i is an upvalue index < len(UpvalInfos), and OpNewClosure
//     sizes Upvals to exactly len(UpvalInfos).
//
// ── Why this is load-only (and OpSetLocal/OpSetUpvalue are NOT routed here) ──
// A Value carries a heap pointer (obj). The compiler emits a GC write barrier
// for a *store* of a pointer-bearing value into a slice; an unsafe pointer write
// would elide that barrier and let the GC miss the store — a use-after-free. So
// vget covers loads only (no barrier needed); stores keep the checked index form
// precisely to preserve their write barrier.
//
// The buzz_safe twin (fetch_safe.go) does the checked s[i], so a violated
// invariant panics there under -tags buzz_safe instead of reading a stray slot.
//
// measured: BenchmarkLoopSum -4.13% (p=0.005), BenchmarkLoopEq -2.16% (p=0.002)
//
//	(benchstat n=10, amd64, Go 1.25); call-bound benches (Fib/Call/MethodCall)
//	unchanged, as their local-load density is low relative to call overhead.
//
// assumes: 0 <= i < len(s), guaranteed by the per-opcode invariants above.
func vget(s []Value, i int) Value {
	//nolint:gosec // G103: audited unchecked load — i in [0,len(s)) by the per-opcode
	// invariants above; load-only, so no write-barrier concern. buzz_safe checks it.
	return *(*Value)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(s)), uintptr(i)*unsafe.Sizeof(Value{})))
}
