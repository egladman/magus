package vm

// OpCode is a single bytecode instruction code.
type OpCode byte

const (
	OpNop OpCode = iota

	// Stack
	OpPop
	OpLoadConst // A = constant index
	OpLoadNull  // push Null
	OpLoadTrue  // push True
	OpLoadFalse // push False

	// Global variable access (env-based: globals + legacy top-level)
	OpLoadName  // push env.get(consts[A].string)
	OpStoreName // env[consts[A].string] = pop (assigns existing binding)
	OpDefName   // env.define(consts[A].string) = pop

	// Scope management — used only for top-level code blocks.
	// Function bodies use slot-based locals (B2) instead.
	OpPushScope
	OpPopScope

	// Slot-based local variable access (B2).
	// A = slot index relative to frame.base.
	//
	// ultra-opt: direct slice index replaces hash-map lookup through the
	// parent-chain env. Eliminates newEnv() per block/loop entirely.
	// measured: LoopSum allocs/op 2000004 → ~0; Fib allocs/op 13M → ~0.
	// trade-off: compiler must pre-resolve all locals to slot indices.
	// assumes: frame.base set correctly; stack pre-extended by localCount.
	OpGetLocal // push stack[frame.base + A]
	OpSetLocal // stack[frame.base + A] = pop

	// Upvalue access (B2): variables captured from outer function scopes.
	// A = index into current closure's Upvals slice.
	// ultra-opt: O(1) array access vs env chain walk.
	OpGetUpvalue // push frame.fun.Upvals[A]
	OpSetUpvalue // frame.fun.Upvals[A] = pop

	// Receiver access: pushes the current frame's bound receiver (`this`).
	// ultra-opt: `this` lives in frame.this (set by OpCall from fn.This), not in
	// a per-call heap Env, so a method body reads it with a single struct-field
	// load instead of walking an Env chain — and the call no longer allocates an
	// Env to hold it. See OpCall and frame.this in vm.go.
	OpLoadThis // push frame.this

	// Unary
	OpNeg
	OpNot

	// Binary arithmetic
	OpAdd
	OpSub
	OpMul
	OpDiv
	OpMod

	// Comparison
	OpEqual
	OpNotEqual
	OpLess
	OpLessEqual
	OpGreater
	OpGreaterEqual

	// Control flow
	OpJump          // ip = A (unconditional)
	OpJumpFalse     // pop; if false → ip = A
	OpJumpTrue      // pop; if true → ip = A
	OpJumpFalsePeek // peek (no pop); if false → ip = A
	OpJumpTruePeek  // peek (no pop); if true → ip = A
	OpJumpIfNull    // peek; if null pop+continue; else → ip = A

	// Member / index
	OpGetMember // pop obj → push obj.consts[A]
	OpSetMember // pop val; pop obj → obj.consts[A] = val
	OpGetIndex  // pop idx; pop obj → push obj[idx]
	OpSetIndex  // pop val; pop idx; pop obj → obj[idx] = val

	// Collection constructors
	OpNewList // pop A items → push list
	OpNewMap  // pop A*2 items (k, v pairs) → push map

	// Functions
	OpNewClosure // push closure from chunk.funs[A], capturing upvalues
	OpCall       // A = arg count; stack: callee arg0…argN → result
	OpReturn     // return pop
	OpReturnNull // return Null

	// Method invocation: obj.name(args) without materializing a bound method.
	// A = method-name const index, B = arg count. Stack on entry:
	// receiver arg0…argN. Resolves name on the receiver and, for an object
	// method, enters it with frame.this = receiver directly — skipping the bound
	// *funObj copy that OpGetMember+OpCall would allocate. Falls back to the
	// general call path when the resolved member is an ordinary function/direct callable
	// value (e.g. a callable stored in a map field).
	// ultra-opt: removes the last per-method-call allocation (see getMember's
	// bound-funObj copy). measured: bench/methodcall.txt.
	OpInvoke

	// Objects
	OpNewObject // A = type-name const index; B = override count

	// Iteration (foreach)
	OpIterInit // pop iterable → push iterState
	OpIterNext // peek iterState; if done pop+jump A; else push (key,) val

	// Range, type tests, type casts
	OpRange // pop Hi, pop Lo → push Lo..Hi range
	OpIs    // A = type-name const idx; pop value → push bool
	OpAs    // A = type-name const idx; pop value → push coerced value

	// Error handling
	OpTryBegin // A = catch-handler IP; push catch context onto catchStack
	OpTryEnd   // pop catch context from catchStack
	OpThrow    // pop value; unwind to nearest catch or return error

	// Fibers
	// OpYield: pop the yield value; push Null (the expression result for the resumer).
	// In a fiber VM: suspend and return the yield value to the resumer via yieldSignal.
	// Outside a fiber VM: the yield is dismissed — Null is already pushed, execution continues.
	OpYield
	// OpFiber: A = arg count; stack before: fn arg0…argN; stack after: suspended fib.
	OpFiber

	// String interpolation: pop A values, stringify each, concatenate, push result.
	// Replaces A-1 intermediate OpAdd calls for string interpolation.
	OpBuildStr

	// Superinstruction (peephole fusion, see chunk.go FusePeephole).
	//
	// OpLocalConstOp fuses GetLocal;LoadConst;<binop> into one dispatch.
	//   A = local slot (relative to frame.base)
	//   B = const-pool index (low 24 bits) | sub-opcode (high 8 bits)
	// The sub-opcode is the original binary op (OpAdd…OpGreaterEqual). The handler
	// reads the local and constant directly — no two pushes + two pops + two
	// dispatches — then runs the *same* polymorphic op, so it is sound even when the
	// operands aren't the static type the checker inferred. The two absorbed slots
	// stay as OpNop for jump-target index stability and are skipped at runtime.
	OpLocalConstOp

	// OpLocalLocalOp fuses GetLocal;GetLocal;<binop> into one dispatch.
	//   A = left slot (relative to frame.base)
	//   B = right slot (low 16 bits) | sub-opcode (high 16 bits)
	// Same discipline as OpLocalConstOp. Targets the two-local arithmetic pattern
	// (e.g. sum + i) that OpLocalConstOp does not cover.
	OpLocalLocalOp

	// OpForCondLC fuses GetLocal;LoadConst;<cmp>;JumpFalse into one dispatch.
	//   A = local slot; B = const-pool index (low 24 bits) | cmp-opcode (high 8 bits)
	//   The jump target is stored in code[ip].A (the first absorbed OpNop);
	//   three OpNop slots follow the super for jump-target stability.
	// Covers the canonical while/for loop condition (i < N).
	OpForCondLC

	// OpCheckType asserts that the value on top of the stack has the runtime
	// primitive type named by operand A (one of CheckInt/CheckFloat/CheckStr/
	// CheckBool); it errors otherwise and leaves the value in place (peek, not pop).
	// The compiler inserts it where a gradually-typed `any` value is bound into a
	// typed slot, making typed values runtime-sound so later code (and future
	// type-specialized opcodes) may trust the static type without a per-use guard.
	OpCheckType

	// OpGetField reads an object field by its compile-time decl index instead of a
	// name lookup. A = field-index hint, B = field-name const index. It is an inline
	// cache: object field sets are stored in declaration order, so the hint hits for
	// the common case (one Keys[hint]==name compare, then Vals[hint]); on a miss
	// (non-canonical or non-object receiver) it falls back to the name-based path,
	// so it is sound for any receiver. The compiler emits it for `this.field` inside
	// a method body, where `this` is statically — and, by dispatch, dynamically —
	// the object type.
	OpGetField
	// OpSetField stores into an object field by decl index; same encoding and
	// inline-cache/fallback discipline as OpGetField. Stores stay checked.
	OpSetField
)

// OpCheckType operand A: the primitive type to assert. Exported so the compiler
// (package buzz) can emit the right code without reaching into value tags.
const (
	CheckInt   = 1
	CheckFloat = 2
	CheckStr   = 3
	CheckBool  = 4
)
