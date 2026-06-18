package vm

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/egladman/gopherbuzz/ast"
)

// maxFrames caps the call-stack depth to prevent runaway recursion from
// exhausting host memory. 1000 frames is generous for any real Buzz program.
const maxFrames = 1000

// yieldSignal is returned by OpYield to suspend the fiber's exec() call.
// resume() in stdlib.go catches this and saves the fiber's VM state implicitly
// (the VM IS the state — its frames+stack are left intact).
type yieldSignal struct{ value Value }

func (*yieldSignal) Error() string { return "buzz: fiber yield" }

// frame is a single activation record.
// ultra-opt: frames are stored in a pre-allocated []frame (not []*frame) to
// eliminate one heap allocation per call. env is used only for globals and
// closures; local variables use stack slots [base..base+localCount).
// measured: BenchmarkFib allocs/op: 16M → ~0.
// trade-off: frame slice may grow; capacity is pre-set to 16.
// assumes: no concurrent access; one VM per goroutine.
type frame struct {
	chunk *Chunk
	ip    int
	env   *Env    // definition-time env for variable lookup (globals + closures)
	fun   *funObj // current closure (nil for top-level); for OpGetUpvalue/OpSetUpvalue
	base  int     // stack index where this frame's register window begins
	retSP int     // stack index to restore on return
	// this is the bound receiver for a method call (the object whose method this
	// frame is executing), or Null for a plain function / top-level. Read by
	// OpLoadThis. Set from fn.This in OpCall/call, replacing the old per-call
	// newEnv+define("this") — see the ultra-opt note in OpCall.
	this Value
}

// catchEntry records a try/catch handler for structured error propagation.
type catchEntry struct {
	frameIdx int // index into vm.frames of the frame that contains the handler
	stackLen int // stack depth at OpTryBegin (restored on throw)
	catchIP  int // instruction pointer of the catch handler in that frame's chunk
}

// envNameEntry caches a resolved name→slot mapping for the VM's inline name cache.
type envNameEntry struct {
	env  *Env
	slot int32
}

// icacheEntry is one slot in the per-VM OpInvoke inline cache for immutable-map
// method calls (e.g. NBody's math.sqrt(d2)). An immutable map's members never
// change, so for a fixed (chunk, ip, receiver) the resolved callee is permanent.
// The chunk pointer is part of the key because a different chunk re-using the
// same ip would name a different member — within one chunk an ip always names
// the same member, so (chunk, ip) pins the name and recv pins the (immutable)
// map. recv is the receiver Value itself (a heap index); identical bits ⇒ the
// same pinned heap object, so the compare needs no heap dereference.
type icacheEntry struct {
	chunk  *Chunk
	recv   Value
	callee Value
}

// mcacheEntry is one slot in the per-VM member-access inline cache.
// The verification key is (chunk, def): the cache is indexed by instruction ip
// alone, and different chunks reuse the same ip range, so an entry must name the
// chunk it was learned in — two chunks can each have a member access at the same
// ip on the *same* object type but for *different* fields, and a def-only check
// would serve the other chunk's field index. With the chunk pinned, the def
// pointer then guards receiver polymorphism at that exact instruction, and idx
// is the correct field index without any string comparison.
// The zero value (def==nil) is a guaranteed miss since inst.Def is never nil.
type mcacheEntry struct {
	def   *objectDefObj
	chunk *Chunk
	idx   int32
	_     int32 // pad to 24 bytes for aligned access
}

// vm executes compiled Buzz chunks. It is package-internal; embedders run code
// through Session (Exec/ExecChunk/CallValue), never by constructing a vm.
type VM struct {
	ctx         context.Context
	stack       []Value
	frames      []frame // value slice: no pointer per frame
	catchStack  []catchEntry
	cancelN     int            // per-VM back-edge counter; avoids false sharing of a global
	ncache      []envNameEntry // inline name cache; indexed by chunk const-pool index
	ncacheChunk *Chunk         // chunk for which ncache entries are valid
	ncacheEnv   *Env           // resolving env those entries were resolved against
	// mcache is a per-instruction inline cache for OpGetMember/OpSetMember on
	// object receivers. Each entry stores the chunk it was learned in, the
	// objectDef pointer, and the field index for the last object type seen at that
	// IP. The hit check is two pointer compares (entry.chunk == f.chunk &&
	// entry.def == inst.Def); a miss scans and relearns. Indexed by instruction
	// position (f.ip-1). Grow-only and never reset — a stale entry (polymorphic
	// receiver, or a different chunk re-using the same ip after a call) fails the
	// verification and is relearned; it can never yield the wrong field. Per-VM so
	// concurrent VMs sharing a *Chunk never race. See mcacheEntry.
	mcache []mcacheEntry
	// icache is the per-instruction inline cache for OpInvoke on immutable-map
	// receivers (std-module method calls like math.sqrt). Grow-only, never reset:
	// a stale entry (different chunk or receiver at this ip) simply fails the
	// (chunk, recv) verification and is relearned, so it can never call the wrong
	// member. Per-VM, so concurrent VMs sharing a *Chunk never race. See
	// icacheEntry and OpInvoke.
	icache []icacheEntry
	// Debugger step hook. nil in every normal run (set only while a magus.pry()
	// session is stepping), so the per-instruction gate `if vm.stepHook != nil`
	// is a single perfectly-predicted branch off the hot path; see the dispatch
	// loop. Requires the chunk to carry line info (CompileOptions.DebugLines).
	stepHook func(StepEvent, DebugFrame)
	stepMask StepMask
	lastLine int // last source line a StepLine event fired for; 0 = none yet
	// strScratch is a reusable byte buffer for OpBuildStr string interpolation.
	// It is grown to the largest interpolation seen and never shrunk, so a loop
	// that builds strings reuses one backing array instead of allocating a fresh
	// strings.Builder per iteration. See OpBuildStr.
	strScratch []byte
	// isFiber marks this VM as running inside a fiber. When true, OpYield suspends
	// execution and returns a yieldSignal; when false, OpYield dismisses the value
	// and pushes Null, continuing normally (upstream parity: yields are dismissed
	// outside a fiber context without error).
	isFiber bool
}

func NewVM(ctx context.Context) *VM {
	return &VM{
		ctx:    ctx,
		stack:  make([]Value, 0, 64),
		frames: make([]frame, 0, 16),
	}
}

// newFiberVM creates a VM that runs as a fiber (OpYield suspends instead of dismissing).
func newFiberVM(ctx context.Context) *VM {
	vm := NewVM(ctx)
	vm.isFiber = true
	return vm
}

// ncacheEnsure allocates or validates vm.ncache for the given (chunk, env)
// pair. Called lazily on first name-op cache miss to avoid allocating for
// chunks that never execute name ops (e.g. pure slot-mode function bodies).
//
// The cache is keyed by env as well as chunk because a single chunk may run
// under different envs across invocations: a this-bound method builds a fresh
// callEnv per call (see OpCall), and `this` resolves through OpLoadName. Keying
// on chunk alone would let one instance's `this` slot be served to another's
// method call. Plain functions reuse one closure env (fn.Env) for every call,
// and top-level code runs under one env, so the hot paths (Fib recursion,
// LoopSum) still hit the cache — only per-instance method bodies re-resolve.
func (vm *VM) ncacheEnsure(chunk *Chunk, env *Env) {
	if vm.ncacheChunk == chunk && vm.ncacheEnv == env && vm.ncache != nil {
		return
	}
	n := len(chunk.Consts)
	if cap(vm.ncache) >= n {
		vm.ncache = vm.ncache[:n]
		for i := range vm.ncache {
			vm.ncache[i] = envNameEntry{}
		}
	} else {
		vm.ncache = make([]envNameEntry, n)
	}
	vm.ncacheChunk = chunk
	vm.ncacheEnv = env
}

// mcacheLearn records (chunk, def, idx) for the member-access instruction at
// ip, growing the per-VM cache slice as needed. The zero value (def==nil) is a
// safe miss sentinel so no explicit init is needed for new slots.
func (vm *VM) mcacheLearn(ip int, chunk *Chunk, def *objectDefObj, idx int32) {
	if ip >= len(vm.mcache) {
		grown := make([]mcacheEntry, ip+1)
		copy(grown, vm.mcache)
		vm.mcache = grown
	}
	vm.mcache[ip] = mcacheEntry{def: def, chunk: chunk, idx: idx}
}

// icacheLearn records (chunk, recv, callee) for the OpInvoke instruction at ip,
// growing the per-VM cache as needed. Called only for immutable-map receivers,
// whose resolved member is permanently valid (see icacheEntry).
func (vm *VM) icacheLearn(ip int, chunk *Chunk, recv, callee Value) {
	if ip >= len(vm.icache) {
		grown := make([]icacheEntry, ip+1)
		copy(grown, vm.icache)
		vm.icache = grown
	}
	vm.icache[ip] = icacheEntry{chunk: chunk, recv: recv, callee: callee}
}

// run executes chunk inside env and returns the program's result.
func (vm *VM) Run(chunk *Chunk, env *Env) (Value, error) {
	vm.stack = vm.stack[:0]
	vm.frames = vm.frames[:0]
	// Reserve the top-level register window for slot-based locals (chunks
	// compiled without SharedGlobals). base=0, so locals occupy stack[0..n) and
	// the operand stack grows above them — the same layout OpCall builds for a
	// callee frame. localCount is 0 for shared-globals chunks, so this is a
	// no-op there.
	if n := chunk.LocalCount; n > 0 {
		if n > cap(vm.stack) {
			vm.stack = make([]Value, n, n*2)
		} else {
			vm.stack = vm.stack[:n]
		}
		for i := range vm.stack[:n] {
			vm.stack[i] = Null
		}
	}
	// Size the member-access inline cache to the top-level chunk once, reusing the
	// backing array across runs (capacity check, like ncache). This turns the
	// otherwise-incremental mcacheLearn growth into a single up-front sizing; called
	// chunks whose ips exceed this still grow lazily. No clear needed — every read
	// verifies its hint, so reused entries are harmless.
	if n := len(chunk.Code); cap(vm.mcache) >= n {
		vm.mcache = vm.mcache[:n]
	} else {
		vm.mcache = make([]mcacheEntry, n)
	}
	vm.frames = append(vm.frames, frame{chunk: chunk, ip: 0, env: env, base: 0, retSP: 0})
	if v, ok, err := vm.jitRun(); ok {
		return v, err
	}
	return vm.Exec()
}

func (vm *VM) push(v Value) { vm.stack = append(vm.stack, v) }

func (vm *VM) pop() Value {
	n := len(vm.stack) - 1
	v := vm.stack[n]
	vm.stack[n] = Null // clear for GC
	vm.stack = vm.stack[:n]
	return v
}

func (vm *VM) peek() Value { return vm.stack[len(vm.stack)-1] }

// dropWindowThreshold is the callee-window slot count above which frame
// teardown eagerly nil-clears the dropped slots to release their heap refs.
//
// ultra-opt: clearing every dropped window unconditionally measured +13.5% on
//
//	Fib (benchstat n=10) — most frames span only a handful of slots whose
//	contents are overwritten by the next call's pushes, so eager clearing is
//	pure overhead. Wide windows (many locals/temporaries, each a potential heap
//	pointer) are where un-cleared slots pin the most memory, so the OpReturn/
//	OpReturnNull handlers clear only past this slot-count threshold (inline, to
//	stay on the dispatch fast path). A small-frame return pays one length
//	compare — within noise on Fib (benchstat n=10, p=0.09).
//	trade-off: a small window keeps its heap refs reachable until those slots
//	  are reused by a later call at the same depth (bounded by the stack
//	  high-water mark, and reclaimed when the VM is collected) — benign for the
//	  short-lived per-target sessions magus runs.
const dropWindowThreshold = 32

// replaceTop2 collapses the top two stack slots into one: it overwrites the
// second-from-top with v and shrinks the stack by one, clearing the dropped
// top slot so the backing array retains no heap reference.
//
// ultra-opt: binary operators consume two operands and produce one. Doing that
//
//	in place avoids the pop+pop+push trio (two reslices + two nil-clears + one
//	append/grow-check) the naive form pays — a net saving of one append and one
//	nil-clear per arithmetic/comparison op. The int fast paths inline an even
//	leaner variant that skips the clear entirely, since an int operand's obj is
//	already nil (nothing to release). LoopSum's hot loop runs three binary ops
//	per iteration, so this is squarely on the critical path.
//	measured: LoopSum -16%, Fib -4% ns/op (benchstat n=10); see bench file.
//	assumes: caller has already read both operands; len(vm.stack) >= 2.
func (vm *VM) replaceTop2(v Value) {
	sp := len(vm.stack)
	vm.stack[sp-1] = Null
	vm.stack[sp-2] = v
	vm.stack = vm.stack[:sp-1]
}

// checkCancel is called on backward jumps and function calls to honour context
// cancellation.
// ultra-opt: checking every 256 events amortises the channel-select overhead.
// measured: N=256 reduced cancel-check cost to <1% of loop time.
// trade-off: up to 256 extra operations before cancellation is noticed.
// assumes: ctx.Done() closed only when ctx is cancelable; fast path otherwise.
func (vm *VM) checkCancel() error {
	vm.cancelN++
	if vm.cancelN&255 != 0 {
		return nil
	}
	select {
	case <-vm.ctx.Done():
		return vm.ctx.Err()
	default:
		return nil
	}
}

func (vm *VM) Exec() (retVal Value, rerr error) {
	defer func() {
		if r := recover(); r != nil {
			rerr = fmt.Errorf("buzz: internal error: %v", r)
		}
	}()

	if len(vm.frames) == 0 {
		return Null, nil
	}
	// ultra-opt: hoist the active frame pointer and its code slice into locals so
	//   the per-instruction dispatch path avoids re-deriving &vm.frames[len-1] (a
	//   length load + bounds check + address-of) and re-dereferencing f.chunk.Code
	//   (two pointer hops to a slice header) on every opcode. f and code are
	//   refreshed only at the sites that mutate vm.frames — a call into a Buzz fn,
	//   the two returns, throw-unwind, and the implicit end-of-chunk return; direct
	//   calls stay in-frame and need no refresh.
	//   measured: BenchmarkStringInterp -15.6%, LoopSum -8.7%, ForeachMap -7.1%,
	//     Fib -3.9% ns/op; geomean -6.1% across the suite, 0 alloc change
	//     (benchstat, n=10, Go 1.25.10, amd64); see bench/dispatch_hoist.txt.
	//   trade-off: every frame-mutating opcode MUST reload both f and code or it
	//     will read a stale (or realloc'd-away) frame; the refresh sites are
	//     enumerated above and each is commented "refresh f/code".
	//   assumes: one VM per goroutine — vm.frames is never mutated concurrently.
	f := &vm.frames[len(vm.frames)-1]
	code := f.chunk.Code
	// ultra-opt: no per-instruction `f.ip >= len(code)` bound check. The compiler
	//   guarantees every chunk ends in a terminating OpReturn/OpReturnNull (emitted
	//   unconditionally at each chunk-creation site), so control flow can never
	//   fall off the end — ip always lands on the terminator first, which pops the
	//   frame and refreshes code. That trailing OpReturnNull does byte-for-byte the
	//   same cleanup the old implicit-end branch did, so dropping the branch is a
	//   pure win: it ran on every opcode and showed up as ~16% of Fib's profile.
	//   measured: Fib 158->154ms (-2.5%); call-heavy code benefits most, tight
	//     backward-jump loops (LoopSum) are unchanged as that branch was already
	//     perfectly predicted there.
	//   assumes: chunk invariant above; a malformed chunk lacking a terminator
	//     would index out of range (caught by the recover at the top of exec).
	//
	// ultra-opt: hoist stepHook nil-ness into a local bool so the per-instruction
	//   branch reads a register/stack slot rather than dereferencing vm.stepHook
	//   (a function-pointer field inside the VM struct) on every opcode. In normal
	//   (non-debug) runs debug is false and the compiler can predict all six hook
	//   guards in the loop as not-taken without a memory access. The value is
	//   stable for the duration of a single Exec call; SetStepHook/ClearStepHook
	//   are never called concurrently with execution.
	debug := vm.stepHook != nil
	for {
		ins := fetch(code, f.ip) // unchecked fetch; see fetch_unsafe.go (chunk-terminator invariant)
		f.ip++

		// Debugger line hook. In normal runs debug is false and this entire
		// block is a single correctly-predicted not-taken branch. When a pry()
		// session is stepping, it fires once per source-line change before
		// the line's first instruction executes. frameToDebug reads
		// lineAt(f.ip-1) — the instruction just fetched — matching this call site.
		if debug && vm.stepMask&MaskLine != 0 {
			if line := f.chunk.lineAt(f.ip - 1); line != 0 && line != vm.lastLine {
				vm.lastLine = line
				vm.stepHook(StepLine, frameToDebug(f))
			}
		}

		switch ins.Op {
		case OpNop:

		case OpPop:
			vm.pop()

		case OpLoadConst:
			vm.push(vget(f.chunk.Consts, int(ins.A)))

		case OpLoadNull:
			vm.push(Null)

		case OpLoadTrue:
			vm.push(True)

		case OpLoadFalse:
			vm.push(False)

		case OpLoadName:
			constIdx := int(ins.A)
			if vm.ncacheChunk == f.chunk && vm.ncacheEnv == f.env && constIdx < len(vm.ncache) {
				if e := vm.ncache[constIdx]; e.env != nil {
					vm.push(e.env.slots[e.slot])
					continue
				}
			}
			name := vm.asStr(f.chunk.Consts[constIdx]).V
			v, env, slot, ok := f.env.getWithSlot(name)
			if !ok {
				return Null, errUndefinedVar(name)
			}
			vm.ncacheEnsure(f.chunk, f.env)
			vm.ncache[constIdx] = envNameEntry{env: env, slot: slot}
			vm.push(v)

		case OpStoreName:
			// ultra-opt: store *consumes* its operand (pop, not peek). Assignment
			//   is statement-only in buzz (AST has AssignStmt, no AssignExpr) and
			//   compileAssign is OpStoreName's sole emitter, so the stored value is
			//   never read back — the compiler no longer emits the trailing OpPop
			//   it used to pair with the old peek form. This is the OpSetLocal
			//   convention (which already pops), now matched for the Env path.
			//   Removes one full opcode dispatch + one pop per name assignment;
			//   LoopSum does two per iteration.
			//   measured: BenchmarkLoopSumShared -6.3% ns/op (benchstat, n=10).
			//   assumes: OpStoreName has no consumer of its result — guaranteed
			//     while assignment stays statement-only (no AssignExpr in the AST).
			constIdx := int(ins.A)
			if vm.ncacheChunk == f.chunk && vm.ncacheEnv == f.env && constIdx < len(vm.ncache) {
				if e := vm.ncache[constIdx]; e.env != nil {
					e.env.slots[e.slot] = vm.pop()
					continue
				}
			}
			name := vm.asStr(f.chunk.Consts[constIdx]).V
			_, env, slot, ok := f.env.getWithSlot(name)
			if !ok {
				return Null, errAssignUndefined(name)
			}
			env.slots[slot] = vm.pop()
			vm.ncacheEnsure(f.chunk, f.env)
			vm.ncache[constIdx] = envNameEntry{env: env, slot: slot}

		case OpDefName:
			name := vm.asStr(f.chunk.Consts[ins.A]).V
			f.env.define(name, vm.pop())

		case OpPushScope:
			f.env = newEnv(f.env)

		case OpPopScope:
			f.env = f.env.parent

		case OpGetLocal:
			vm.push(vget(vm.stack, f.base+int(ins.A)))

		case OpSetLocal:
			vm.stack[f.base+int(ins.A)] = vm.pop()

		case OpGetUpvalue:
			vm.push(vget(f.fun.Upvals, int(ins.A)))

		case OpSetUpvalue:
			f.fun.Upvals[ins.A] = vm.pop()

		case OpLoadThis:
			vm.push(f.this)

		case OpNeg:
			v := vm.pop()
			switch v.tag() {
			case tagInt:
				vm.push(IntValue(-int64(v.num())))
			case tagFloat:
				vm.push(FloatValue(-v.AsFloat()))
			default:
				return Null, errCannotNegate(v)
			}

		case OpNot:
			vm.push(BoolValue(!vm.pop().Bool()))

		case OpAdd:
			sp := len(vm.stack)
			right, left := vm.stack[sp-1], vm.stack[sp-2]
			// ultra-opt: int+int fast path inlined, in place — no clear needed
			// because the dropped int operand carries no heap reference.
			if left.tag() == tagInt && right.tag() == tagInt {
				vm.stack[sp-2] = IntValue(int64(left.num()) + int64(right.num()))
				vm.stack = vm.stack[:sp-1]
				continue
			}
			// ultra-opt: float+float fast path — see floatBinop. A float carries no
			// heap ref, so the dropped slot needs no clear (mirrors the int path).
			if left.tag() == tagFloat && right.tag() == tagFloat {
				vm.stack[sp-2] = FloatValue(left.AsFloat() + right.AsFloat())
				vm.stack = vm.stack[:sp-1]
				continue
			}
			r, err := vm.slowAdd(left, right)
			if err != nil {
				return Null, err
			}
			vm.replaceTop2(r)

		case OpSub:
			sp := len(vm.stack)
			right, left := vm.stack[sp-1], vm.stack[sp-2]
			// ultra-opt: int-int fast path.
			if left.tag() == tagInt && right.tag() == tagInt {
				vm.stack[sp-2] = IntValue(int64(left.num()) - int64(right.num()))
				vm.stack = vm.stack[:sp-1]
				continue
			}
			// ultra-opt: float+float fast path; see OpAdd.
			if left.tag() == tagFloat && right.tag() == tagFloat {
				vm.stack[sp-2] = FloatValue(left.AsFloat() - right.AsFloat())
				vm.stack = vm.stack[:sp-1]
				continue
			}
			r, err := vm.slowSub(left, right)
			if err != nil {
				return Null, err
			}
			vm.replaceTop2(r)

		case OpMul:
			sp := len(vm.stack)
			right, left := vm.stack[sp-1], vm.stack[sp-2]
			if left.tag() == tagInt && right.tag() == tagInt {
				vm.stack[sp-2] = IntValue(int64(left.num()) * int64(right.num()))
				vm.stack = vm.stack[:sp-1]
				continue
			}
			// ultra-opt: float+float fast path; see OpAdd.
			if left.tag() == tagFloat && right.tag() == tagFloat {
				vm.stack[sp-2] = FloatValue(left.AsFloat() * right.AsFloat())
				vm.stack = vm.stack[:sp-1]
				continue
			}
			r, err := vm.slowMul(left, right)
			if err != nil {
				return Null, err
			}
			vm.replaceTop2(r)

		case OpDiv:
			sp := len(vm.stack)
			right, left := vm.stack[sp-1], vm.stack[sp-2]
			if left.tag() == tagInt && right.tag() == tagInt && right.num() != 0 {
				vm.stack[sp-2] = IntValue(int64(left.num()) / int64(right.num()))
				vm.stack = vm.stack[:sp-1]
				continue
			}
			// ultra-opt: float/float fast path; see OpAdd. A zero divisor falls
			// through so arith() raises buzz's float divide-by-zero error.
			if left.tag() == tagFloat && right.tag() == tagFloat && right.AsFloat() != 0 {
				vm.stack[sp-2] = FloatValue(left.AsFloat() / right.AsFloat())
				vm.stack = vm.stack[:sp-1]
				continue
			}
			r, err := vm.slowDiv(left, right)
			if err != nil {
				return Null, err
			}
			vm.replaceTop2(r)

		case OpMod:
			sp := len(vm.stack)
			right, left := vm.stack[sp-1], vm.stack[sp-2]
			if left.tag() == tagInt && right.tag() == tagInt && right.num() != 0 {
				vm.stack[sp-2] = IntValue(int64(left.num()) % int64(right.num()))
				vm.stack = vm.stack[:sp-1]
				continue
			}
			r, err := vm.slowMod(left, right)
			if err != nil {
				return Null, err
			}
			vm.replaceTop2(r)

		case OpEqual:
			sp := len(vm.stack)
			right, left := vm.stack[sp-1], vm.stack[sp-2]
			// ultra-opt: int==int fast path, in place — mirrors the OpLess/Le/Gt/Ge
			// inline paths. Skips the valuesEqual call + tag switch for the common
			// case, and skips the clear since neither int operand holds a heap ref.
			if left.tag() == tagInt && right.tag() == tagInt {
				vm.stack[sp-2] = BoolValue(left.num() == right.num())
				vm.stack = vm.stack[:sp-1]
				continue
			}
			// ultra-opt: float==float fast path; matches valuesEqual's tagFloat case.
			if left.tag() == tagFloat && right.tag() == tagFloat {
				vm.stack[sp-2] = BoolValue(left.AsFloat() == right.AsFloat())
				vm.stack = vm.stack[:sp-1]
				continue
			}
			vm.replaceTop2(BoolValue(vm.slowEqual(left, right)))

		case OpNotEqual:
			sp := len(vm.stack)
			right, left := vm.stack[sp-1], vm.stack[sp-2]
			// ultra-opt: int!=int fast path; see OpEqual.
			if left.tag() == tagInt && right.tag() == tagInt {
				vm.stack[sp-2] = BoolValue(left.num() != right.num())
				vm.stack = vm.stack[:sp-1]
				continue
			}
			// ultra-opt: float!=float fast path; see OpEqual.
			if left.tag() == tagFloat && right.tag() == tagFloat {
				vm.stack[sp-2] = BoolValue(left.AsFloat() != right.AsFloat())
				vm.stack = vm.stack[:sp-1]
				continue
			}
			vm.replaceTop2(BoolValue(!vm.slowEqual(left, right)))

		case OpLess:
			sp := len(vm.stack)
			right, left := vm.stack[sp-1], vm.stack[sp-2]
			// ultra-opt: int comparison fast path.
			if left.tag() == tagInt && right.tag() == tagInt {
				vm.stack[sp-2] = BoolValue(int64(left.num()) < int64(right.num()))
				vm.stack = vm.stack[:sp-1]
				continue
			}
			// ultra-opt: float<>float fast path; see OpAdd.
			if left.tag() == tagFloat && right.tag() == tagFloat {
				vm.stack[sp-2] = BoolValue(left.AsFloat() < right.AsFloat())
				vm.stack = vm.stack[:sp-1]
				continue
			}
			r, err := vm.slowCompare(OpLess, left, right)
			if err != nil {
				return Null, err
			}
			vm.replaceTop2(r)

		case OpLessEqual:
			sp := len(vm.stack)
			right, left := vm.stack[sp-1], vm.stack[sp-2]
			if left.tag() == tagInt && right.tag() == tagInt {
				vm.stack[sp-2] = BoolValue(int64(left.num()) <= int64(right.num()))
				vm.stack = vm.stack[:sp-1]
				continue
			}
			// ultra-opt: float<>float fast path; see OpAdd.
			if left.tag() == tagFloat && right.tag() == tagFloat {
				vm.stack[sp-2] = BoolValue(left.AsFloat() <= right.AsFloat())
				vm.stack = vm.stack[:sp-1]
				continue
			}
			r, err := vm.slowCompare(OpLessEqual, left, right)
			if err != nil {
				return Null, err
			}
			vm.replaceTop2(r)

		case OpGreater:
			sp := len(vm.stack)
			right, left := vm.stack[sp-1], vm.stack[sp-2]
			if left.tag() == tagInt && right.tag() == tagInt {
				vm.stack[sp-2] = BoolValue(int64(left.num()) > int64(right.num()))
				vm.stack = vm.stack[:sp-1]
				continue
			}
			// ultra-opt: float<>float fast path; see OpAdd.
			if left.tag() == tagFloat && right.tag() == tagFloat {
				vm.stack[sp-2] = BoolValue(left.AsFloat() > right.AsFloat())
				vm.stack = vm.stack[:sp-1]
				continue
			}
			r, err := vm.slowCompare(OpGreater, left, right)
			if err != nil {
				return Null, err
			}
			vm.replaceTop2(r)

		case OpGreaterEqual:
			sp := len(vm.stack)
			right, left := vm.stack[sp-1], vm.stack[sp-2]
			if left.tag() == tagInt && right.tag() == tagInt {
				vm.stack[sp-2] = BoolValue(int64(left.num()) >= int64(right.num()))
				vm.stack = vm.stack[:sp-1]
				continue
			}
			// ultra-opt: float<>float fast path; see OpAdd.
			if left.tag() == tagFloat && right.tag() == tagFloat {
				vm.stack[sp-2] = BoolValue(left.AsFloat() >= right.AsFloat())
				vm.stack = vm.stack[:sp-1]
				continue
			}
			r, err := vm.slowCompare(OpGreaterEqual, left, right)
			if err != nil {
				return Null, err
			}
			vm.replaceTop2(r)

		case OpJump:
			if int(ins.A) <= f.ip-1 {
				if err := vm.checkCancel(); err != nil {
					return Null, err
				}
			}
			f.ip = int(ins.A)

		case OpJumpFalse:
			if !vm.pop().Bool() {
				// A backward conditional jump is a loop back-edge (do..until
				// compiles its repeat edge here); poll cancellation like OpJump
				// so the loop stays killable. Forward exits skip the check.
				if int(ins.A) <= f.ip-1 {
					if err := vm.checkCancel(); err != nil {
						return Null, err
					}
				}
				f.ip = int(ins.A)
			}

		case OpJumpTrue:
			if vm.pop().Bool() {
				if int(ins.A) <= f.ip-1 {
					if err := vm.checkCancel(); err != nil {
						return Null, err
					}
				}
				f.ip = int(ins.A)
			}

		case OpJumpFalsePeek:
			if !vm.peek().Bool() {
				f.ip = int(ins.A)
			}

		case OpJumpTruePeek:
			if vm.peek().Bool() {
				f.ip = int(ins.A)
			}

		case OpJumpIfNull:
			if vm.peek().IsNull() {
				vm.pop()
			} else {
				f.ip = int(ins.A)
			}

		case OpGetMember:
			// Inline-cached member read. For an object receiver, the mcache entry at
			// this IP stores (chunk, objectDefObj*, fieldIdx). A hit is two pointer
			// compares — no string comparison needed. On a miss the field index is
			// found by name scan and learned. Non-object receivers (maps, lists, …)
			// use getMember.
			name := vm.asStr(f.chunk.Consts[ins.A]).V
			obj := vm.pop()
			if obj.tag() == tagObject {
				inst := vm.asObject(obj)
				ip := f.ip - 1
				if ip < len(vm.mcache) {
					if e := vm.mcache[ip]; e.def == inst.Def && e.chunk == f.chunk {
						vm.push(inst.Fields[e.idx])
						continue
					}
				}
				if hit := inst.Def.fieldIndex(name); hit >= 0 {
					vm.mcacheLearn(ip, f.chunk, inst.Def, int32(hit))
					vm.push(inst.Fields[hit])
					continue
				}
			}
			v, err := getMember(vm, obj, name)
			if err != nil {
				return Null, err
			}
			vm.push(v)

		case OpSetMember:
			// Inline-cached member store; mirror of OpGetMember. Hit: pointer compare,
			// direct indexed write. Miss: name scan + learn. Unknown fields → setMember.
			name := vm.asStr(f.chunk.Consts[ins.A]).V
			val := vm.pop()
			obj := vm.pop()
			if obj.tag() == tagObject {
				inst := vm.asObject(obj)
				if !inst.Mut {
					return Null, errImmutable("object")
				}
				ip := f.ip - 1
				if ip < len(vm.mcache) {
					if e := vm.mcache[ip]; e.def == inst.Def && e.chunk == f.chunk {
						inst.Fields[e.idx] = val
						continue
					}
				}
				if hit := inst.Def.fieldIndex(name); hit >= 0 {
					vm.mcacheLearn(ip, f.chunk, inst.Def, int32(hit))
					inst.Fields[hit] = val
					continue
				}
			}
			if err := setMember(vm, obj, name, val); err != nil {
				return Null, err
			}

		case OpGetField:
			// Flat-field read: ins.A is the compile-time field index (declaration order).
			// Direct indexed load — no string compare. Falls back to name-based getMember
			// only when the receiver is not a typed object instance.
			obj := vm.pop()
			if obj.tag() == tagObject {
				inst := vm.asObject(obj)
				if h := int(ins.A); h < len(inst.Fields) {
					vm.push(inst.Fields[h])
					continue
				}
			}
			name := vm.asStr(f.chunk.Consts[ins.B]).V
			v, err := getMember(vm, obj, name)
			if err != nil {
				return Null, err
			}
			vm.push(v)

		case OpSetField:
			// Flat-field store; mirror of OpGetField.
			val := vm.pop()
			obj := vm.pop()
			if obj.tag() == tagObject {
				inst := vm.asObject(obj)
				if !inst.Mut {
					return Null, errImmutable("object")
				}
				if h := int(ins.A); h < len(inst.Fields) {
					inst.Fields[h] = val
					continue
				}
			}
			name := vm.asStr(f.chunk.Consts[ins.B]).V
			if err := setMember(vm, obj, name, val); err != nil {
				return Null, err
			}

		case OpGetIndex:
			idx := vm.pop()
			obj := vm.pop()
			v, err := indexGet(vm, obj, idx, ins.A != 0)
			if err != nil {
				return Null, err
			}
			vm.push(v)

		case OpSetIndex:
			val := vm.pop()
			idx := vm.pop()
			obj := vm.pop()
			if err := setIndex(vm, obj, idx, val); err != nil {
				return Null, err
			}

		case OpNewList:
			n := int(ins.A)
			items := make([]Value, n)
			for i := n - 1; i >= 0; i-- {
				items[i] = vm.pop()
			}
			vm.push(heapValue(tagList, &listObj{Items: items, Mut: ins.B&InstrMutBit != 0}))

		case OpNewMap:
			// ultra-opt: read k/v pairs directly from stack — no intermediate slice.
			n := int(ins.A)
			m := newMapObj()
			m.Mut = ins.B&InstrMutBit != 0
			base := len(vm.stack) - n*2
			for i := 0; i < n; i++ {
				k := vm.stack[base+i*2]
				v := vm.stack[base+i*2+1]
				m.set(k.String(), v)
			}
			vm.stack = vm.stack[:base]
			vm.push(vm.allocMap(m))

		case OpNewClosure:
			fc := f.chunk.Funs[ins.A]
			var upvals []Value
			if len(fc.UpvalInfos) > 0 {
				upvals = make([]Value, len(fc.UpvalInfos))
				for i, info := range fc.UpvalInfos {
					if info.IsLocal {
						upvals[i] = vm.stack[f.base+int(info.Index)]
					} else if f.fun != nil {
						upvals[i] = f.fun.Upvals[info.Index]
					}
				}
			}
			// Propagate the enclosing receiver so a closure created inside a method
			// body still sees `this`. Previously `this` lived in the captured Env;
			// now it rides in frame.this, so we copy it onto the new closure (whose
			// own frame.this will be seeded from here when it is later called). A
			// closure that is itself later bound as a method (getMember) overwrites
			// This with its actual receiver, so seeding the lexical one here is only
			// the default for free-standing closures defined in a method.
			fo := &funObj{Params: fc.Params, Chunk: fc, Env: f.env, Upvals: upvals, This: f.this}
			vm.push(vm.allocFun(fo))

		case OpCall:
			argCount := int(ins.A)
			stackLen := len(vm.stack)
			calleeIdx := stackLen - argCount - 1
			callee := vm.stack[calleeIdx]

			switch callee.tag() {
			case tagDirect:
				directFn := vm.asDirect(callee)
				// ultra-opt: pass the operand-stack window directly instead of
				//   copying args into a fresh slice per call. A direct callable runs
				//   synchronously and never mutates this VM's stack mid-call — it
				//   receives only (ctx, args), and the session-bound direct callables that do
				//   run Buzz code (resume/dispatch) drive a separate newVM, so the
				//   window stays stable for the call's duration. The Callable
				//   no-retain contract (see session.go) forbids holding the slice
				//   past return, so a future push can safely reuse the backing array.
				//   measured: see bench/directargs.txt (BenchmarkDirectCall).
				//   trade-off: a misbehaving Callable that retains args would observe
				//     later stack writes; the contract is documented at the type.
				//   assumes: direct callables don't retain args (audited: stdlib, ffi, and the
				//     magus host bindings all copy out or consume synchronously).
				result, err := directFn.Fn(vm.ctx, vm.stack[calleeIdx+1:stackLen])
				if err != nil {
					if vm.raiseHostError(err) {
						f = &vm.frames[len(vm.frames)-1] // refresh f/code: frames unwound
						code = f.chunk.Code
						continue
					}
					return Null, err
				}
				vm.stack[calleeIdx] = result
				vm.stack = vm.stack[:calleeIdx+1]

			case tagFun:
				fn := vm.asFun(callee)
				if fn.Chunk == nil {
					return Null, errNoChunk()
				}
				if len(vm.frames) >= maxFrames {
					return Null, errStackOverflow()
				}
				if err := vm.checkCancel(); err != nil {
					return Null, err
				}
				// ultra-opt: the ordinary call path builds the frame inline (rather
				//   than via enterFun, which OpInvoke shares) so the hot recursive
				//   call path stays branch-lean — routing it through the out-of-line
				//   enterFun measured ~+3% on BenchmarkFib and ~+12% on BenchmarkCall.
				//   Receiver rides in frame.this (fn.This), not a per-call Env; see
				//   the frame.this note and OpInvoke for the method fast path.
				base := calleeIdx + 1
				need := base + fn.Chunk.LocalCount
				if need > len(vm.stack) {
					vm.growWindow(need)
				} else {
					vm.stack = vm.stack[:need] // truncate extra args if any
				}
				vm.frames = append(vm.frames, frame{
					chunk: fn.Chunk,
					ip:    0,
					env:   fn.Env,
					fun:   fn,
					base:  base,
					retSP: calleeIdx,
					this:  fn.This,
				})
				f = &vm.frames[len(vm.frames)-1] // refresh f/code: frame pushed (slice may realloc)
				code = f.chunk.Code
				if debug {
					vm.lastLine = 0 // new frame: let its first line fire
					if vm.stepMask&MaskCall != 0 {
						vm.stepHook(StepCall, frameToDebug(f))
					}
				}

			default:
				return Null, errNotCallable(callee)
			}

		case OpInvoke:
			// obj.name(args): stack holds receiver arg0…argN. Resolve name on the
			// receiver and call it. For an object method we enter it directly with
			// frame.this = receiver, reusing the receiver's slot as the return slot
			// and the arg run as the register window — no bound *funObj is built.
			// Anything else (a field/map entry holding a callable) falls back to the
			// general dispatch via getMember, which is the rare path.
			argCount := int(ins.B)
			stackLen := len(vm.stack)
			recvIdx := stackLen - argCount - 1
			receiver := vm.stack[recvIdx]
			name := vm.asStr(f.chunk.Consts[ins.A]).V

			if receiver.tag() == tagObject {
				instance := vm.asObject(receiver)
				// A method shadowed by a like-named field is read as the field value
				// (matching getMember's field-first order) and dispatched generally.
				if instance.Def.fieldIndex(name) < 0 {
					if m, ok := instance.Def.method(name); ok {
						// Enter the method with the receiver as this; args already sit
						// at recvIdx+1…stackLen. The receiver slot (recvIdx) becomes the
						// return slot, exactly like OpCall's callee slot.
						if err := vm.enterFun(m, recvIdx+1, recvIdx, receiver); err != nil {
							return Null, err
						}
						f = &vm.frames[len(vm.frames)-1] // refresh f/code: frame pushed
						code = f.chunk.Code
						if debug {
							vm.lastLine = 0
							if vm.stepMask&MaskCall != 0 {
								vm.stepHook(StepCall, frameToDebug(f))
							}
						}
						continue
					}
				}
			}

			// Fallback: resolve the member (field value, map entry, list/str/enum
			// member) and dispatch it like an ordinary call. getMember may bind a
			// method here (e.g. when recv is a map whose entry is a bound method),
			// which is the uncommon path this opcode deliberately doesn't optimize.
			//
			// ultra-opt: an immutable-map receiver (e.g. the `math` module in
			//   NBody's math.sqrt(d2)) resolves the same callee every call, so an
			//   (chunk, ip, receiver) hit skips getMember's mapMethod string-switch
			//   and map hash lookup — the per-call dispatch the NBody profile spent
			//   ~3% in (getMember → mapaccess2_faststr). Only immutable maps are
			//   learned; mutable maps and all other receivers resolve fresh.
			//   measured: BenchmarkComparison/NBody/Warm/Gopherbuzz (benchstat n=6);
			//     Mandelbrot/LoopSum unaffected (slot-mode, no OpInvoke).
			ip := f.ip - 1
			var callee Value
			cached := false
			if ip < len(vm.icache) {
				if e := vm.icache[ip]; e.chunk == f.chunk && e.recv == receiver {
					callee, cached = e.callee, true
				}
			}
			if !cached {
				c, err := getMember(vm, receiver, name)
				if err != nil {
					return Null, err
				}
				callee = c
				if receiver.tag() == tagMap && !vm.asMap(receiver).Mut {
					vm.icacheLearn(ip, f.chunk, receiver, c)
				}
			}
			switch callee.tag() {
			case tagDirect:
				directFn := vm.asDirect(callee)
				result, ferr := directFn.Fn(vm.ctx, vm.stack[recvIdx+1:stackLen])
				if ferr != nil {
					if vm.raiseHostError(ferr) {
						f = &vm.frames[len(vm.frames)-1] // refresh f/code: frames unwound
						code = f.chunk.Code
						continue
					}
					return Null, ferr
				}
				vm.stack[recvIdx] = result
				vm.stack = vm.stack[:recvIdx+1]
			case tagFun:
				fn := vm.asFun(callee)
				if err := vm.enterFun(fn, recvIdx+1, recvIdx, fn.This); err != nil {
					return Null, err
				}
				f = &vm.frames[len(vm.frames)-1] // refresh f/code: frame pushed
				code = f.chunk.Code
				if debug {
					vm.lastLine = 0
					if vm.stepMask&MaskCall != 0 {
						vm.stepHook(StepCall, frameToDebug(f))
					}
				}
			default:
				return Null, errNotCallable(callee)
			}

		case OpReturn:
			rv := vm.pop()
			vm.purgeCatchFrame(len(vm.frames) - 1)
			retSP := f.retSP
			vm.frames = vm.frames[:len(vm.frames)-1]
			if len(vm.stack)-retSP > dropWindowThreshold {
				clear(vm.stack[retSP:]) // release a fat window's heap refs; see dropWindowThreshold
			}
			vm.stack = vm.stack[:retSP]
			if len(vm.frames) == 0 {
				return rv, nil
			}
			f = &vm.frames[len(vm.frames)-1] // refresh f/code: frame popped
			code = f.chunk.Code
			if debug {
				vm.lastLine = 0 // back in caller frame: re-fire its current line
				if vm.stepMask&MaskReturn != 0 {
					vm.stepHook(StepReturn, frameToDebug(f))
				}
			}
			vm.push(rv)

		case OpReturnNull:
			vm.purgeCatchFrame(len(vm.frames) - 1)
			retSP := f.retSP
			vm.frames = vm.frames[:len(vm.frames)-1]
			if len(vm.stack)-retSP > dropWindowThreshold {
				clear(vm.stack[retSP:]) // release a fat window's heap refs; see dropWindowThreshold
			}
			vm.stack = vm.stack[:retSP]
			if len(vm.frames) == 0 {
				return Null, nil
			}
			f = &vm.frames[len(vm.frames)-1] // refresh f/code: frame popped
			code = f.chunk.Code
			if debug {
				vm.lastLine = 0 // back in caller frame: re-fire its current line
				if vm.stepMask&MaskReturn != 0 {
					vm.stepHook(StepReturn, frameToDebug(f))
				}
			}
			vm.push(Null)

		case OpNewObject:
			cv := f.chunk.Consts[ins.A]
			if cv.tag() == tagObjDecl {
				if err := vm.buildObjectDef(vm.asObjDecl(cv), int(ins.B), f.env); err != nil {
					return Null, err
				}
			} else {
				typeName := vm.asStr(cv).V
				if err := vm.buildObjectVal(typeName, int(ins.B&^InstrMutBit), f.env, ins.B&InstrMutBit != 0); err != nil {
					return Null, err
				}
			}

		case OpIterInit:
			iter := vm.pop()
			switch iter.tag() {
			case tagList:
				vm.push(vm.allocIterState(&iterStateObj{list: vm.asList(iter)}))
			case tagMap:
				vm.push(vm.allocIterState(&iterStateObj{mapObj: vm.asMap(iter)}))
			case tagRange:
				r := vm.asRange(iter)
				vm.push(vm.allocIterState(&iterStateObj{rng: r, rangeIdx: r.Lo}))
			case tagFib:
				vm.push(vm.allocIterState(&iterStateObj{fib: iter.asFib()}))
			default:
				return Null, errCannotIterate(iter)
			}

		case OpIterNext:
			state := vm.asIterState(vm.peek())
			wantKey := ins.B == 1
			done := false
			if state.list != nil {
				if state.idx < len(state.list.Items) {
					if wantKey {
						vm.push(IntValue(int64(state.idx)))
					}
					vm.push(state.list.Items[state.idx])
					state.idx++
				} else {
					done = true
				}
			} else if state.mapObj != nil {
				m := state.mapObj
				if state.idx < len(m.Keys) {
					if wantKey {
						vm.push(m.keyVals[state.idx]) // pre-built StrValue, zero alloc
					}
					vm.push(m.Vals[state.idx])
					state.idx++
				} else {
					done = true
				}
			} else if state.rng != nil {
				// Ranges are half-open: a..b yields the integers from a up to (but not
				// including) b, and descends when a > b. The iteration key is the
				// zero-based step count.
				r := state.rng
				if r.Lo <= r.Hi {
					if state.rangeIdx < r.Hi {
						if wantKey {
							vm.push(IntValue(state.rangeIdx - r.Lo))
						}
						vm.push(IntValue(state.rangeIdx))
						state.rangeIdx++
					} else {
						done = true
					}
				} else {
					if state.rangeIdx > r.Hi {
						if wantKey {
							vm.push(IntValue(r.Lo - state.rangeIdx))
						}
						vm.push(IntValue(state.rangeIdx))
						state.rangeIdx--
					} else {
						done = true
					}
				}
			} else if state.fib != nil {
				val, fdone, err := vm.fiberIterNext(state.fib)
				if err != nil {
					return Null, err
				}
				if fdone {
					done = true
				} else {
					if wantKey {
						vm.push(IntValue(int64(state.idx)))
					}
					vm.push(val)
					state.idx++
				}
			} else {
				done = true
			}
			if done {
				vm.pop() // pop iterState
				f.ip = int(ins.A)
			}

		case OpRange:
			hi := vm.pop()
			lo := vm.pop()
			if lo.tag() != tagInt || hi.tag() != tagInt {
				return Null, errRangeOperands(lo, hi)
			}
			vm.push(rangeValue(lo.AsInt(), hi.AsInt()))

		case OpIs:
			name := vm.asStr(f.chunk.Consts[ins.A]).V
			val := vm.pop()
			vm.push(BoolValue(vm.buzzIsType(val, name)))

		case OpAs:
			name := vm.asStr(f.chunk.Consts[ins.A]).V
			val := vm.pop()
			result, err := vm.buzzCast(val, name)
			if err != nil {
				if ins.B == 1 { // `as?`: a type mismatch yields null instead of erroring
					vm.push(Null)
					break
				}
				return Null, err
			}
			vm.push(result)

		case OpTryBegin:
			vm.catchStack = append(vm.catchStack, catchEntry{
				frameIdx: len(vm.frames) - 1,
				stackLen: len(vm.stack),
				catchIP:  int(ins.A),
			})

		case OpTryEnd:
			if len(vm.catchStack) > 0 {
				vm.catchStack = vm.catchStack[:len(vm.catchStack)-1]
			}

		case OpThrow:
			errVal := vm.pop()
			if len(vm.catchStack) == 0 {
				return Null, errUncaught(errVal)
			}
			entry := vm.catchStack[len(vm.catchStack)-1]
			vm.catchStack = vm.catchStack[:len(vm.catchStack)-1]
			// Unwind frames back to the try handler frame.
			vm.frames = vm.frames[:entry.frameIdx+1]
			vm.stack = vm.stack[:entry.stackLen]
			f = &vm.frames[len(vm.frames)-1] // refresh f/code: frames unwound
			code = f.chunk.Code
			f.ip = entry.catchIP
			vm.push(errVal) // catch body receives the error value on top of stack

		case OpYield:
			val := vm.pop()
			// The yield expression evaluates to the yielded value itself, matching
			// upstream Buzz: `final a = yield 7;` binds a == 7 after the fiber is
			// resumed. (Upstream resume passes no separate value back in; the
			// expression result is the value that was yielded.) Push it as the result
			// that the instruction after OpYield reads on the next Exec.
			vm.push(val)
			if vm.isFiber {
				// Fiber context: suspend. Frames+stack (including the value just
				// pushed) are preserved; the next Exec() continues past this instruction.
				return Null, &yieldSignal{value: val}
			}
			// Non-fiber context: yield is dismissed per upstream semantics — the value
			// is already on the stack as the expression result, execution continues.

		case OpFiber:
			// stack: fn arg0…argN → suspended fib
			argc := int(ins.A)
			args := make([]Value, argc)
			for i := argc - 1; i >= 0; i-- {
				args[i] = vm.pop()
			}
			fn := vm.pop()
			if fn.tag() != tagFun {
				return Null, fmt.Errorf("buzz: '&' requires a Buzz function, got %s", fn.buzzKind())
			}
			fibVM := newFiberVM(vm.ctx)
			if err := fibVM.Call(fn, args); err != nil {
				return Null, err
			}
			vm.push(vm.allocFib(&fibObj{vm: fibVM, status: fibSuspended}))

		case OpBuildStr:
			// ultra-opt: build the interpolation into the per-VM strScratch byte
			//   buffer, then string()-copy out once. A single pass over the A stack
			//   values already eliminated the A-1 intermediate strObj allocations a
			//   chained OpAdd would make; reusing strScratch across calls now also
			//   eliminates the per-call strings.Builder backing allocation, so a
			//   loop that interpolates pays one (amortised) buffer growth instead of
			//   a fresh heap alloc each iteration. The final string([]byte) is the
			//   only remaining allocation and is unavoidable (the result must own
			//   its bytes — strScratch is overwritten on the next call).
			//   measured: see bench/buildstr.txt (BenchmarkStringInterp).
			//   trade-off: strScratch is retained for the VM's lifetime, sized to the
			//     largest interpolation seen — bounded and reclaimed with the VM.
			//   assumes: the produced string copies its bytes (string(buf) does), so
			//     overwriting strScratch on the next OpBuildStr is safe.
			n := int(ins.A)
			parts := vm.stack[len(vm.stack)-n:]
			buf := vm.strScratch[:0]
			for _, p := range parts {
				if p.tag() == tagStr {
					buf = append(buf, vm.asStr(p).V...)
				} else {
					buf = append(buf, p.String()...)
				}
			}
			vm.strScratch = buf // retain the (possibly grown) backing array
			vm.stack = vm.stack[:len(vm.stack)-n]
			vm.push(StrValue(string(buf)))

		case OpBinLC:
			// ultra-opt: fused GetLocal;LoadConst;<binop>. Reads the local and the
			//   constant directly — eliminating two pushes, two pops, and two switch
			//   dispatches per occurrence — then runs the SAME polymorphic op as the
			//   unfused form, so it stays correct when an any-typed operand defeats
			//   the static type the checker inferred.
			//   A = slot; B = const index (low 24 bits) | sub-opcode (high 8 bits).
			//   C == 0 (3-instruction fusion): skip two OpNops, push result (or absorb
			//     a following SetLocal at runtime when dst==src1).
			//   C > 0 (4-instruction register form, Pass 1C): skip three OpNops and
			//     write the result directly to stack[frame.base + C - 1]; the SetLocal
			//     was absorbed at compile time so no runtime check is needed.
			//   ultra-opt (P1-M5): bit 31 of B = "both operands are statically proven
			//     int" (set by FusePeephole when OpGetLocal.B==sInt && const is tagInt).
			//     When set, skip the two runtime tag comparisons entirely — sub-opcode
			//     is still in bits 30-24, masked with 0x7F; const index in bits 23-0.
			bothInt := ins.B < 0 // bit 31
			sub := OpCode(uint32(ins.B) >> 24 & 0x7F)
			left := vget(vm.stack, f.base+int(ins.A))
			right := vget(f.chunk.Consts, int(ins.B&0xFFFFFF))
			dst := ins.C
			if dst > 0 {
				f.ip += 3
			} else {
				f.ip += 2
			}
			if bothInt || (left.tag() == tagInt && right.tag() == tagInt) {
				a, b := int64(left.num()), int64(right.num())
				switch sub {
				case OpAdd:
					r := IntValue(a + b)
					if dst > 0 {
						vm.stack[f.base+int(dst-1)] = r
					} else if code[f.ip].Op == OpSetLocal && code[f.ip].A == ins.A {
						vm.stack[f.base+int(ins.A)] = r
						f.ip++
					} else {
						vm.push(r)
					}
					continue
				case OpSub:
					r := IntValue(a - b)
					if dst > 0 {
						vm.stack[f.base+int(dst-1)] = r
					} else if code[f.ip].Op == OpSetLocal && code[f.ip].A == ins.A {
						vm.stack[f.base+int(ins.A)] = r
						f.ip++
					} else {
						vm.push(r)
					}
					continue
				case OpMul:
					if dst > 0 {
						vm.stack[f.base+int(dst-1)] = IntValue(a * b)
					} else {
						vm.push(IntValue(a * b))
					}
					continue
				case OpLess:
					if dst > 0 {
						vm.stack[f.base+int(dst-1)] = BoolValue(a < b)
					} else {
						vm.push(BoolValue(a < b))
					}
					continue
				case OpLessEqual:
					if dst > 0 {
						vm.stack[f.base+int(dst-1)] = BoolValue(a <= b)
					} else {
						vm.push(BoolValue(a <= b))
					}
					continue
				case OpGreater:
					if dst > 0 {
						vm.stack[f.base+int(dst-1)] = BoolValue(a > b)
					} else {
						vm.push(BoolValue(a > b))
					}
					continue
				case OpGreaterEqual:
					if dst > 0 {
						vm.stack[f.base+int(dst-1)] = BoolValue(a >= b)
					} else {
						vm.push(BoolValue(a >= b))
					}
					continue
				case OpEqual:
					if dst > 0 {
						vm.stack[f.base+int(dst-1)] = BoolValue(a == b)
					} else {
						vm.push(BoolValue(a == b))
					}
					continue
				case OpNotEqual:
					if dst > 0 {
						vm.stack[f.base+int(dst-1)] = BoolValue(a != b)
					} else {
						vm.push(BoolValue(a != b))
					}
					continue
				case OpMod, OpDiv:
					r, err := intArith(sub, a, b)
					if err != nil {
						return Null, err
					}
					if dst > 0 {
						vm.stack[f.base+int(dst-1)] = r
					} else {
						vm.push(r)
					}
					continue
				}
			}
			// ultra-opt: float+float fast path for the fused superinstruction — the
			// float kernels' inner-loop locals (Mandelbrot zx/zy, NBody accumulators)
			// land here. floatBinop returns ok==false for OpMod / float divide-by-zero,
			// which fall through to applyBinop so arith() reports the exact error.
			if left.tag() == tagFloat && right.tag() == tagFloat {
				if r, ok := floatBinop(sub, left.AsFloat(), right.AsFloat()); ok {
					if dst > 0 {
						vm.stack[f.base+int(dst-1)] = r
					} else if code[f.ip].Op == OpSetLocal && code[f.ip].A == ins.A {
						vm.stack[f.base+int(ins.A)] = r
						f.ip++
					} else {
						vm.push(r)
					}
					continue
				}
			}
			r, err := applyBinop(vm, sub, left, right)
			if err != nil {
				return Null, err
			}
			if dst > 0 {
				vm.stack[f.base+int(dst-1)] = r
			} else {
				vm.push(r)
			}

		case OpBinLL:
			// ultra-opt: fused GetLocal;GetLocal;<binop>. Reads both locals directly,
			// eliminating two pushes, two pops, and two switch dispatches per use.
			//   A = left slot; B = right slot (low 16 bits) | sub-opcode (high 16 bits).
			//   C == 0 (3-instruction fusion): skip two OpNops, push result (or absorb
			//     a following SetLocal at runtime when dst==src1).
			//   C > 0 (4-instruction register form, Pass 1L): skip three OpNops and
			//     write the result directly to stack[frame.base + C - 1]; the SetLocal
			//     was absorbed at compile time so no runtime check is needed.
			//   ultra-opt (P1-M5): bit 31 of B = "both operands are statically proven
			//     int" (set by FusePeephole when both OpGetLocal.B==sInt). When set,
			//     skip the two runtime tag comparisons; sub-op masked with 0x7FFF.
			bothInt := ins.B < 0 // bit 31
			sub := OpCode(uint32(ins.B) >> 16 & 0x7FFF)
			left := vget(vm.stack, f.base+int(ins.A))
			right := vget(vm.stack, f.base+int(ins.B&0xFFFF))
			dst := ins.C
			if dst > 0 {
				f.ip += 3
			} else {
				f.ip += 2
			}
			if bothInt || (left.tag() == tagInt && right.tag() == tagInt) {
				a, b := int64(left.num()), int64(right.num())
				switch sub {
				case OpAdd:
					r := IntValue(a + b)
					if dst > 0 {
						vm.stack[f.base+int(dst-1)] = r
					} else if code[f.ip].Op == OpSetLocal && code[f.ip].A == ins.A {
						vm.stack[f.base+int(ins.A)] = r
						f.ip++
					} else {
						vm.push(r)
					}
					continue
				case OpSub:
					r := IntValue(a - b)
					if dst > 0 {
						vm.stack[f.base+int(dst-1)] = r
					} else if code[f.ip].Op == OpSetLocal && code[f.ip].A == ins.A {
						vm.stack[f.base+int(ins.A)] = r
						f.ip++
					} else {
						vm.push(r)
					}
					continue
				case OpMul:
					if dst > 0 {
						vm.stack[f.base+int(dst-1)] = IntValue(a * b)
					} else {
						vm.push(IntValue(a * b))
					}
					continue
				case OpLess:
					if dst > 0 {
						vm.stack[f.base+int(dst-1)] = BoolValue(a < b)
					} else {
						vm.push(BoolValue(a < b))
					}
					continue
				case OpLessEqual:
					if dst > 0 {
						vm.stack[f.base+int(dst-1)] = BoolValue(a <= b)
					} else {
						vm.push(BoolValue(a <= b))
					}
					continue
				case OpGreater:
					if dst > 0 {
						vm.stack[f.base+int(dst-1)] = BoolValue(a > b)
					} else {
						vm.push(BoolValue(a > b))
					}
					continue
				case OpGreaterEqual:
					if dst > 0 {
						vm.stack[f.base+int(dst-1)] = BoolValue(a >= b)
					} else {
						vm.push(BoolValue(a >= b))
					}
					continue
				case OpEqual:
					if dst > 0 {
						vm.stack[f.base+int(dst-1)] = BoolValue(a == b)
					} else {
						vm.push(BoolValue(a == b))
					}
					continue
				case OpNotEqual:
					if dst > 0 {
						vm.stack[f.base+int(dst-1)] = BoolValue(a != b)
					} else {
						vm.push(BoolValue(a != b))
					}
					continue
				case OpMod, OpDiv:
					r, err := intArith(sub, a, b)
					if err != nil {
						return Null, err
					}
					if dst > 0 {
						vm.stack[f.base+int(dst-1)] = r
					} else {
						vm.push(r)
					}
					continue
				}
			}
			// ultra-opt: float+float fast path for the fused superinstruction — the
			// float kernels' inner-loop locals (Mandelbrot zx/zy, NBody accumulators)
			// land here. floatBinop returns ok==false for OpMod / float divide-by-zero,
			// which fall through to applyBinop so arith() reports the exact error.
			if left.tag() == tagFloat && right.tag() == tagFloat {
				if r, ok := floatBinop(sub, left.AsFloat(), right.AsFloat()); ok {
					if dst > 0 {
						vm.stack[f.base+int(dst-1)] = r
					} else if code[f.ip].Op == OpSetLocal && code[f.ip].A == ins.A {
						vm.stack[f.base+int(ins.A)] = r
						f.ip++
					} else {
						vm.push(r)
					}
					continue
				}
			}
			r, err := applyBinop(vm, sub, left, right)
			if err != nil {
				return Null, err
			}
			if dst > 0 {
				vm.stack[f.base+int(dst-1)] = r
			} else {
				vm.push(r)
			}

		case OpCmpLC:
			// ultra-opt: fused GetLocal;LoadConst;<cmp>;JumpFalse.
			//   A = local slot; B = const-pool index (low 24)|cmp-op (high 8).
			//   Jump target is in code[f.ip].A (the first absorbed OpNop); skip 3 NOPs.
			sub := OpCode(uint32(ins.B) >> 24)
			left := vget(vm.stack, f.base+int(ins.A))
			right := vget(f.chunk.Consts, int(ins.B&0xFFFFFF))
			target := int(code[f.ip].A)
			f.ip += 3 // skip 3 OpNop slots
			var cond bool
			if left.tag() == tagInt && right.tag() == tagInt {
				a, b := int64(left.num()), int64(right.num())
				switch sub {
				case OpLess:
					cond = a < b
				case OpLessEqual:
					cond = a <= b
				case OpGreater:
					cond = a > b
				case OpGreaterEqual:
					cond = a >= b
				case OpEqual:
					cond = a == b
				case OpNotEqual:
					cond = a != b
				}
			} else if left.tag() == tagFloat && right.tag() == tagFloat {
				// ultra-opt: float loop-condition fast path (e.g. `while (x < 10.0)`),
				// mirroring the int arm — skips applyBinopâcompareâasNumeric.
				a, b := left.AsFloat(), right.AsFloat()
				switch sub {
				case OpLess:
					cond = a < b
				case OpLessEqual:
					cond = a <= b
				case OpGreater:
					cond = a > b
				case OpGreaterEqual:
					cond = a >= b
				case OpEqual:
					cond = a == b
				case OpNotEqual:
					cond = a != b
				}
			} else {
				r, err := applyBinop(vm, sub, left, right)
				if err != nil {
					return Null, err
				}
				cond = r.Bool()
			}
			if !cond {
				f.ip = target
			}

		case OpCheckType:
			// Assert the narrowed value's runtime type (peek, leave it for the store
			// that follows). Inserted by the compiler where an any-typed value enters
			// a typed slot, so a mistyped any surfaces as a clear error instead of
			// silently corrupting a slot a later read trusts.
			v := vm.peek()
			ok := false
			switch ins.A {
			case CheckInt:
				ok = v.tag() == tagInt
			case CheckFloat:
				ok = v.tag() == tagFloat
			case CheckStr:
				ok = v.tag() == tagStr
			case CheckBool:
				ok = v.tag() == tagBool
			case CheckNonNull:
				ok = v.tag() != tagNull
			}
			if !ok {
				return Null, errCheckType(ins.A, v)
			}

		default:
			return Null, errUnknownOpcode(ins.Op)
		}
	}
}

// Dispatch-loop error constructors. Each is //go:noinline so the fmt.Errorf
// call — and the []any boxing + string-conversion temporaries it needs — lives
// in the helper's frame rather than inline in Exec. Exec is one ~37 KB function
// whose stack frame is sized by the union of every case's locals, and it sits at
// a register-pressure cliff (a stray hoisted slice header measurably regressed
// the suite), so keeping these cold formatting paths out of its body shrinks the
// frame the hot dispatch loop runs under. This is the Go-expressible form of the
// hot/cold splitting the Deegen interpreter does via LLVM — the only layout lever
// Go gives you is moving cold code into separate functions.
//
// measured: Exec text 36786→34226 B, frame locals 0x3f0→0x3a8; BenchmarkFib
//
//	-3.70%, BenchmarkMethodCall -7.47% (benchstat n=10, p=0.000, amd64); the
//	call/return-heavy handlers (OpCall/OpReturn/OpInvoke) benefit most as the
//	freed frame relieves register pressure they shared with the cold paths.
//	Arithmetic-loop benches (LoopSum/LoopEq/Call) unchanged, as expected.
//	trade-off: error paths now pay one non-inlined call — they are cold by
//	construction (type errors, undefined names, overflow), never on a hot path.
//
//go:noinline
func errUndefinedVar(name string) error {
	return fmt.Errorf("buzz: undefined variable %q", name)
}

//go:noinline
func errAssignUndefined(name string) error {
	return fmt.Errorf("buzz: cannot assign to undefined variable %q", name)
}

//go:noinline
func errCannotNegate(v Value) error { return fmt.Errorf("buzz: cannot negate %s", v.buzzKind()) }

//go:noinline
func errNoChunk() error { return fmt.Errorf("buzz: function has no compiled chunk") }

//go:noinline
func errStackOverflow() error { return fmt.Errorf("buzz: call stack overflow (limit %d)", maxFrames) }

//go:noinline
func errNotCallable(v Value) error { return fmt.Errorf("buzz: %s is not callable", v.buzzKind()) }

//go:noinline
func errCannotIterate(v Value) error { return fmt.Errorf("buzz: cannot iterate over %s", v.buzzKind()) }

// fiberIterNext drives one step of `foreach (x in &fib())`: it resumes the fiber
// and reports the next yielded value, or done=true once the fiber completes
// without yielding. It mirrors session.builtinResume but stays in-package so
// OpIterNext needs only a small branch plus this out-of-line call (keeping the
// resume machinery out of Exec's hot switch — see README on the I-cache budget).
//
//go:noinline
func (vm *VM) fiberIterNext(f *fibObj) (Value, bool, error) {
	if f.status == fibDone {
		return Null, true, f.err
	}
	f.vm.SetCtx(vm.ctx)
	f.status = fibRunning
	res, err := f.vm.Exec()
	if err != nil {
		if ys, ok := err.(*yieldSignal); ok {
			f.status = fibSuspended
			return ys.value, false, nil
		}
		f.status = fibDone
		f.err = err
		return Null, true, err
	}
	f.status = fibDone
	f.returnVal = res
	return Null, true, nil
}

//go:noinline
func errRangeOperands(lo, hi Value) error {
	return fmt.Errorf("buzz: range operands must be int, got %s..%s", lo.buzzKind(), hi.buzzKind())
}

//go:noinline
func errUncaught(v Value) error { return fmt.Errorf("buzz: uncaught error: %s", v.String()) }

//go:noinline
func errUnknownOpcode(op OpCode) error { return fmt.Errorf("buzz: unknown opcode %d", op) }

//go:noinline
func errCheckType(code int32, v Value) error {
	switch code {
	case CheckNonNull:
		return fmt.Errorf("buzz: force-unwrap of null value")
	}
	want := "value"
	switch code {
	case CheckInt:
		want = "int"
	case CheckFloat:
		want = "double"
	case CheckStr:
		want = "str"
	case CheckBool:
		want = "bool"
	}
	return fmt.Errorf("buzz: expected %s, got %s", want, v.buzzKind())
}

// enterFun pushes a new frame for a Buzz function call. The caller has already
// laid the callee's arguments on the stack at [base, base+argCount); base is the
// frame's register-window origin, retSP is the stack index to restore on return,
// and this is the receiver bound into frame.this (Null for a plain function).
//
// Shared by OpCall and OpInvoke so the method fast path and the ordinary call
// path build identical frames — OpInvoke just supplies an explicit receiver and
// an already-resolved *funObj, avoiding the bound-funObj allocation getMember
// would otherwise make. The caller refreshes its hoisted f/code after this
// returns (the frames slice may have grown).
//
// ultra-opt: the register-window growth (the make+copy+nil-fill that only fires
//
//	when the callee's window doesn't fit in the current stack length) lives in
//	growWindow, marked noinline, so enterFun stays small enough for the inliner
//	to fold it into the OpCall/OpInvoke dispatch arms. Keeping enterFun inlinable
//	is what holds the hot recursive-call path (BenchmarkFib) at parity after the
//	OpCall refactor — an out-of-line call per invocation measured ~+3% on Fib.
//	measured: see bench/methodcall.txt.
func (vm *VM) enterFun(fn *funObj, base, retSP int, this Value) error {
	if fn.Chunk == nil {
		return fmt.Errorf("buzz: function has no compiled chunk")
	}
	if len(vm.frames) >= maxFrames {
		return fmt.Errorf("buzz: call stack overflow (limit %d)", maxFrames)
	}
	if err := vm.checkCancel(); err != nil {
		return err
	}
	need := base + fn.Chunk.LocalCount
	if need > len(vm.stack) {
		vm.growWindow(need)
	} else {
		vm.stack = vm.stack[:need] // truncate extra args if any
	}
	vm.frames = append(vm.frames, frame{
		chunk: fn.Chunk,
		ip:    0,
		env:   fn.Env,
		fun:   fn,
		base:  base,
		retSP: retSP,
		this:  this,
	})
	return nil
}

// growWindow extends the operand stack to length need, reallocating if it
// outgrows cap, and nil-fills the newly exposed slots. Split out of enterFun
// (and marked noinline) so the common in-place case keeps enterFun inlinable;
// this branch fires only when a callee's register window doesn't fit.
//
//go:noinline
func (vm *VM) growWindow(need int) {
	stackLen := len(vm.stack)
	if need > cap(vm.stack) {
		ns := make([]Value, need, need*2)
		copy(ns, vm.stack[:stackLen])
		vm.stack = ns
	} else {
		vm.stack = vm.stack[:need]
	}
	for i := stackLen; i < need; i++ {
		vm.stack[i] = Null
	}
}

// call dispatches a function call. Used by external callers (CallValue, eval).
// Unlike OpCall, this pushes args explicitly since they're not already on the stack.
func (vm *VM) Call(callee Value, args []Value) error {
	switch callee.tag() {
	case tagDirect:
		directFn := vm.asDirect(callee)
		result, err := directFn.Fn(vm.ctx, args)
		if err != nil {
			return err
		}
		vm.push(result)
		return nil

	case tagFun:
		fn := vm.asFun(callee)
		if fn.Chunk == nil {
			return fmt.Errorf("buzz: function has no compiled chunk")
		}
		if len(vm.frames) >= maxFrames {
			return fmt.Errorf("buzz: call stack overflow (limit %d)", maxFrames)
		}
		base := len(vm.stack)
		// Push args into slot positions 0..argCount-1
		for _, arg := range args {
			vm.push(arg)
		}
		// Pre-allocate remaining local slots
		localCount := fn.Chunk.LocalCount
		for i := len(args); i < localCount; i++ {
			vm.push(Null)
		}
		// Receiver rides in frame.this, not a per-call Env — see OpCall.
		vm.frames = append(vm.frames, frame{
			chunk: fn.Chunk,
			ip:    0,
			env:   fn.Env,
			fun:   fn,
			base:  base,
			retSP: base, // truncate back to base on return (no callee on stack)
			this:  fn.This,
		})
		return nil

	default:
		return fmt.Errorf("buzz: %s is not callable", callee.buzzKind())
	}
}

// buildObjectDef pops (name, closure) pairs and creates an objectDefObj.
func (vm *VM) buildObjectDef(decl *ast.ObjectDecl, methodCount int, env *Env) error {
	type method struct {
		name string
		fun  *funObj
	}
	methods := make([]method, methodCount)
	for i := methodCount - 1; i >= 0; i-- {
		fn := vm.pop()
		if fn.tag() != tagFun {
			return fmt.Errorf("buzz: object method is not a function")
		}
		name := vm.pop()
		if name.tag() != tagStr {
			return fmt.Errorf("buzz: object method name is not a string")
		}
		methods[i] = method{vm.asStr(name).V, vm.asFun(fn)}
	}
	def := &objectDefObj{
		Name:    decl.Name,
		Fields:  decl.Fields,
		Methods: make([]methodEntry, methodCount),
		Env:     env,
	}
	for i, m := range methods {
		def.Methods[i] = methodEntry{Name: m.name, Fn: m.fun}
	}
	vm.push(vm.allocObjectDef(def))
	return nil
}

// buildObjectVal pops (name, value) pairs and creates an objectInst with flat field storage.
func (vm *VM) buildObjectVal(typeName string, fieldCount int, env *Env, mut bool) error {
	type fieldPair struct {
		name string
		val  Value
	}
	pairs := make([]fieldPair, fieldCount)
	for i := fieldCount - 1; i >= 0; i-- {
		val := vm.pop()
		name := vm.pop()
		if name.tag() != tagStr {
			return fmt.Errorf("buzz: object field name is not a string")
		}
		pairs[i] = fieldPair{vm.asStr(name).V, val}
	}
	defVal, ok := env.get(typeName)
	if !ok {
		return fmt.Errorf("buzz: unknown object type %q", typeName)
	}
	if defVal.tag() != tagObjectDef {
		return fmt.Errorf("buzz: %q is not an object type", typeName)
	}
	def := vm.asObjectDef(defVal)
	flatFields := make([]Value, len(def.Fields))
	for i := range flatFields {
		flatFields[i] = Null // NaN-box zero value is float 0.0, not Null
	}
	for _, p := range pairs {
		if j := def.fieldIndex(p.name); j >= 0 {
			flatFields[j] = p.val
		}
	}
	vm.push(vm.allocObject(&objectInst{Def: def, Fields: flatFields, Mut: mut}))
	return nil
}

// raiseHostError implements throw semantics for an error returned by a host
// (Go-side) callable: like upstream Buzz's `!>` errors, a host failure is a
// catchable Buzz error, not a VM abort. It unwinds to the innermost active try
// handler — exactly OpThrow's discipline — and delivers the message as a str
// for the catch body. It reports false, leaving all state untouched, when no
// handler is active or the error is a control-flow sentinel (fiber yield,
// context cancellation), which must keep propagating to the embedder.
// Callers must refresh their cached frame/code pointers on true.
func (vm *VM) raiseHostError(err error) bool {
	if len(vm.catchStack) == 0 {
		return false
	}
	if _, ok := err.(*yieldSignal); ok {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	entry := vm.catchStack[len(vm.catchStack)-1]
	vm.catchStack = vm.catchStack[:len(vm.catchStack)-1]
	vm.frames = vm.frames[:entry.frameIdx+1]
	vm.stack = vm.stack[:entry.stackLen]
	vm.frames[len(vm.frames)-1].ip = entry.catchIP
	vm.push(StrValue(err.Error()))
	return true
}

// purgeCatchFrame removes all catch entries that belong to the given frame index.
// Called on frame exit (return / implicit return) to prevent stale catch handlers.
func (vm *VM) purgeCatchFrame(frameIdx int) {
	for len(vm.catchStack) > 0 && vm.catchStack[len(vm.catchStack)-1].frameIdx >= frameIdx {
		vm.catchStack = vm.catchStack[:len(vm.catchStack)-1]
	}
}

// buzzIsType returns whether v's runtime type matches typeName.
func (vm *VM) buzzIsType(v Value, typeName string) bool {
	switch typeName {
	case "null":
		return v.tag() == tagNull
	case "bool":
		return v.tag() == tagBool
	case "int":
		return v.tag() == tagInt
	case "double":
		return v.tag() == tagFloat
	case "str":
		return v.tag() == tagStr
	case "list":
		return v.tag() == tagList
	case "map":
		return v.tag() == tagMap
	case "fun":
		return v.tag() == tagFun || v.tag() == tagDirect
	case "rng":
		return v.tag() == tagRange
	case "pat":
		return v.tag() == tagPat
	}
	if v.tag() == tagObject {
		return vm.asObject(v).Def.Name == typeName
	}
	if v.tag() == tagEnumVal {
		return vm.asEnumVal(v).Enum == typeName
	}
	return false
}

// buzzCast coerces v to the named type.
func (vm *VM) buzzCast(v Value, typeName string) (Value, error) {
	switch typeName {
	case "int":
		switch v.tag() {
		case tagInt:
			return v, nil
		case tagFloat:
			return IntValue(int64(v.AsFloat())), nil
		case tagBool:
			if v.AsBool() {
				return IntValue(1), nil
			}
			return IntValue(0), nil
		case tagStr:
			n, err := strconv.ParseInt(vm.asStr(v).V, 10, 64)
			if err != nil {
				return Null, fmt.Errorf("buzz: cannot cast %q to int", vm.asStr(v).V)
			}
			return IntValue(n), nil
		}
	case "double":
		switch v.tag() {
		case tagFloat:
			return v, nil
		case tagInt:
			return FloatValue(float64(v.AsInt())), nil
		case tagStr:
			f, err := strconv.ParseFloat(vm.asStr(v).V, 64)
			if err != nil {
				return Null, fmt.Errorf("buzz: cannot cast %q to float", vm.asStr(v).V)
			}
			return FloatValue(f), nil
		}
	case "str":
		return StrValue(v.String()), nil
	case "bool":
		return BoolValue(v.Bool()), nil
	}
	// Non-primitive target (an object/list/map/named type): gopherbuzz is
	// dynamically typed, so a cast to such a type is an identity assertion —
	// the value already is whatever it is. `as?` callers still get null only on
	// a failed *primitive* coercion above (e.g. a non-numeric string to int).
	return v, nil
}

// --- Exported debug and control methods ---

// SetCtx updates the VM's context (used when resuming a fiber).
func (vm *VM) SetCtx(ctx context.Context) { vm.ctx = ctx }

// SetStepHook installs cb to fire on events matching mask.
func (vm *VM) SetStepHook(mask StepMask, cb func(StepEvent, DebugFrame)) {
	vm.stepHook = cb
	vm.stepMask = mask
	vm.lastLine = 0
}

// ClearStepHook removes any installed step hook.
func (vm *VM) ClearStepHook() {
	vm.stepHook = nil
	vm.stepMask = 0
}

// DebugFrames returns the active call stack, innermost first.
func (vm *VM) DebugFrames() []DebugFrame {
	out := make([]DebugFrame, len(vm.frames))
	for i := range vm.frames {
		j := len(vm.frames) - 1 - i
		out[i] = frameToDebug(&vm.frames[j])
	}
	return out
}

// CallDepth returns the number of active frames.
func (vm *VM) CallDepth() int { return len(vm.frames) }

// DebugLocals returns named locals at stack level (0=innermost).
func (vm *VM) DebugLocals(level int) map[string]Value {
	frames := vm.frames
	idx := len(frames) - 1 - level
	if idx < 0 || idx >= len(frames) {
		return nil
	}
	f := &frames[idx]
	if f.chunk == nil || len(f.chunk.LocalNames) == 0 {
		return nil
	}
	out := make(map[string]Value, len(f.chunk.LocalNames))
	for slot, name := range f.chunk.LocalNames {
		if name == "" {
			continue
		}
		si := f.base + slot
		if si < 0 || si >= len(vm.stack) {
			continue
		}
		out[name] = vm.stack[si]
	}
	return out
}

// DebugUpvalues returns captured upvalues at stack level (0=innermost).
func (vm *VM) DebugUpvalues(level int) map[string]Value {
	frames := vm.frames
	idx := len(frames) - 1 - level
	if idx < 0 || idx >= len(frames) {
		return nil
	}
	f := &frames[idx]
	if f.fun == nil || len(f.chunk.UpvalNames) == 0 {
		return nil
	}
	out := make(map[string]Value, len(f.fun.Upvals))
	for i, name := range f.chunk.UpvalNames {
		if name == "" || i >= len(f.fun.Upvals) {
			continue
		}
		out[name] = f.fun.Upvals[i]
	}
	return out
}

// YieldSignal is returned by OpYield. Exported so callers can check for it.
// Use errors.As to detect: errors.As(err, new(*YieldSignal)).
type YieldSignal = yieldSignal

// YieldValue extracts the yielded value from a *YieldSignal.
func YieldValue(ys *YieldSignal) Value { return ys.value }

// --- Fiber accessor API ---
//
// FiberStatus mirrors the internal fibStatus values for external inspection.
type FiberStatus uint8

const (
	FiberSuspended FiberStatus = FiberStatus(fibSuspended)
	FiberRunning   FiberStatus = FiberStatus(fibRunning)
	FiberDone      FiberStatus = FiberStatus(fibDone)
)

// IsFiber reports whether v is a fiber value.
func IsFiber(v Value) bool { return v.tag() == tagFib }

// Fiber is a handle to a fiber value's mutable execution state. The fiber driver
// (resume/resolve) lives in the buzz package, outside this one, so it needs to
// read and advance a fiber's status, return value, and terminal error without
// reaching into the unexported fibObj. AsFiber hands out the handle; its methods
// operate on a known-valid fiber, so there are no tag guards or lying sentinels —
// a non-fiber Value never produces a handle.
type Fiber struct{ f *fibObj }

// AsFiber returns a handle to v's fiber state. ok is false if v is not a fiber.
func AsFiber(v Value) (Fiber, bool) {
	if v.tag() != tagFib {
		return Fiber{}, false
	}
	return Fiber{v.asFib()}, true
}

// VM returns the fiber's underlying execution VM.
func (h Fiber) VM() *VM { return h.f.vm }

// Status reports the fiber's current status.
func (h Fiber) Status() FiberStatus { return FiberStatus(h.f.status) }

// SetStatus updates the fiber's status. Only the goroutine that logically owns
// the fiber may call this — fibers never cross goroutines from script code.
func (h Fiber) SetStatus(s FiberStatus) { h.f.status = fibStatus(s) }

// Return reports the value the fiber function returned, cached when it completed.
func (h Fiber) Return() Value { return h.f.returnVal }

// SetReturn caches the fiber's return value for a later resolve.
func (h Fiber) SetReturn(v Value) { h.f.returnVal = v }

// Err reports the terminal error that ended the fiber, or nil if it completed
// cleanly or has not yet run to completion.
func (h Fiber) Err() error { return h.f.err }

// SetErr caches the terminal error that ended the fiber so a later resume/resolve
// re-surfaced it instead of silently returning null.
func (h Fiber) SetErr(err error) { h.f.err = err }
