//go:build (amd64 || arm64) && !buzz_safe && !buzz_unsafe

package vm

import (
	"runtime"
	"sync"
	"sync/atomic"
	"weak"
)

// Arch-independent half of the baseline JIT: the run/cache/eligibility logic and
// the ABI struct shared by the per-arch code generators (jit_amd64.go,
// jit_arm64.go). Each arch supplies compileJIT, its register/emit helpers, a
// jitEntry trampoline (jit_<arch>.s), and jitArchDefault. See the README
// "Baseline JIT" section for the design.

// jitCtx is the fixed ABI struct passed to generated code (in DI on amd64, R0 on
// arm64). Field offsets are baked into the emitters as off* below — keep in sync.
type jitCtx struct {
	stackData *Value // 0  — &vm.stack[0]; slot i is at base+i
	base      int64  // 8  — frame.base (element index)
	sp        int64  // 16 — out: stack length to restore on deopt
	resumeIP  int64  // 24 — in: entry ip (0 first time); out: resume/re-entry ip
	retVal    uint64 // 32 — out: return value bits when done
	status    int64  // 40 — out: see jitStatus*
	cancelN   int64  // 48 — in/out: back-edge counter (persists across re-entries)
}

const (
	offStackData = 0
	offBase      = 8
	offSP        = 16
	offResumeIP  = 24
	offRetVal    = 32
	offStatus    = 40
	offCancelN   = 48
)

// jitStatus* values written by generated code into jitCtx.status on exit.
const (
	jitDone        = 0 // chunk returned; retVal valid
	jitDeopt       = 1 // hand off to interpreter at resumeIP (sp valid)
	jitCancelCheck = 2 // poll cancellation, then re-enter at resumeIP
)

// nanbox bit patterns the emitters bake in, DERIVED from the canonical nanbox
// layout in value_nanbox.go rather than hand-copied. A change to that layout now
// flows through automatically (or fails to compile) instead of silently
// desyncing the generated code from the interpreter's value representation — a
// mismatch the differential suite catches only when run on a JIT arch.
const (
	jitIntHeader = int64(qnanMask | (nanboxInt << coarseShift))                   // int48 tag header
	jitIntHi16   = int64((qnanMask | (nanboxInt << coarseShift)) >> coarseShift)  // intValue >> 48 == 0x7FFA
	jitBoolHi16  = int64((qnanMask | (nanboxBool << coarseShift)) >> coarseShift) // boolValue >> 48 == 0x7FF9
	jitFalseBits = int64(qnanMask | (nanboxBool << coarseShift))                  // False
	jitNullBits  = int64(qnanMask | (nanboxNull << coarseShift))                  // Null
	jitQNaNMask  = int64(qnanMask)                                                // qnanMask
)

//go:noescape
func jitEntry(code *byte, ctx *jitCtx)

// JITAvailable reports whether a native JIT backend is compiled in.
func JITAvailable() bool { return true }

type compiledJIT struct {
	code     []byte // mmap'd, RX; kept alive for the process (never unmapped)
	entry    *byte
	maxDepth int // max operand-stack slots needed above the locals
	// entryDepth is depths()' per-ip operand-stack depth, retained so an exit can
	// be CHECKED rather than trusted. The backends bake lc+entryDepth[ip] into each
	// deopt stub as the stack height to restore; keeping the same array here lets
	// jitRun assert the two agree, which is an exact equality (not a range check)
	// and costs one load on the cold path. Without it a stub emitting the wrong
	// height silently hands the interpreter a corrupt operand stack.
	entryDepth []int
	// disabled marks a compilation whose native run exited unresumably. The entry
	// is MARKED rather than replaced with jitIneligible, because replacing it would
	// strand the mapping: eviction is the only safe point to unmap (see jitEvict),
	// and here we are not at one — another VM sharing this chunk may be executing
	// these very pages. Atomic for the same reason.
	disabled atomic.Bool
}

var (
	// jitCache maps a chunk to its compilation, keyed WEAKLY. A strong *Chunk key
	// would make the cache itself the reason a chunk can never be collected — and
	// with it the chunk's Code and Consts and the executable pages of its
	// compilation. That is the whole PROCESS's lifetime, not the chunk's, which is
	// what mapExecutable already claimed and did not deliver. Harmless in a
	// short-lived CLI; in the magus daemon, which is long-lived and recompiles a
	// workspace's chunks on every edit, it accumulates. Entries leave via jitEvict.
	jitCache      sync.Map // weak.Pointer[Chunk] -> *compiledJIT (jitIneligible == not eligible)
	jitIneligible = &compiledJIT{}
	// jitCompileMu serializes code generation; see jitCompileCached for why the
	// assembler cannot be entered concurrently. It guards the compile path only —
	// the cache read above it stays lock-free.
	jitCompileMu sync.Mutex
)

// jitEvict drops a dead chunk's cache entry and releases its executable pages.
//
// It runs only once the chunk is unreachable, and THAT is what makes the unmap
// safe: a chunk is reachable from vm.frames for the whole of its native run (the
// KeepAlive in jitRun says so out loud), so this cannot fire underneath executing
// code. Unmapping on any other schedule — an LRU, a size cap, an explicit purge —
// would need a way to prove no goroutine is inside those pages, and there isn't
// one. Reachability IS the proof, which is why eviction is tied to it.
func jitEvict(key weak.Pointer[Chunk]) {
	v, ok := jitCache.LoadAndDelete(key)
	if !ok {
		return
	}
	if c := v.(*compiledJIT); c != jitIneligible {
		unmapExecutable(c.code)
	}
}

// jitTestExitHook, when non-nil, runs after every native exit and may rewrite the
// exit context. Test-only seam: it is the only way to drive the bad-exit recovery
// below, because that path is reached only by a codegen defect and no well-formed
// backend produces one on demand. nil in every normal run, and read once per
// native EXIT (never per iteration), so it is a predicted not-taken branch on the
// cold path. Set only from tests in this package, never concurrently with a run.
var jitTestExitHook func(*jitCtx)

// jitRun runs the top frame's chunk as native code. ok=false means "fall back to
// the interpreter". On deopt it resumes the interpreter and returns its result,
// so ok=true always reflects a completed run.
func (vm *VM) jitRun() (Value, bool, error) {
	if !JITEnabled() || len(vm.frames) != 1 {
		return Null, false, nil
	}
	f := &vm.frames[0]
	jc, codegenFailed := jitCompileCached(f.chunk)
	if codegenFailed {
		jitCompileFails.Add(1)
		if vm.faultHook != nil {
			vm.faultHook(FaultJITCompile)
		}
	}
	if jc == nil {
		return Null, false, nil
	}
	// The chunk must outlive every instruction of the native run: its death is what
	// releases the executable pages we are about to enter (jitEvict). It is already
	// reachable through f, which points into vm.frames and is read after the loop —
	// but that is an inference about liveness the compiler is free to invalidate,
	// and the failure mode if it ever does is unmapping the instruction stream
	// mid-execution. So state it rather than infer it.
	defer runtime.KeepAlive(f.chunk)
	lc := f.chunk.LocalCount
	if f.base+lc == 0 {
		return Null, false, nil // need &stack[0]; nothing to anchor
	}
	need := f.base + lc + jc.maxDepth
	if need > cap(vm.stack) {
		ns := make([]Value, len(vm.stack), need*2)
		copy(ns, vm.stack)
		vm.stack = ns
	}
	// Snapshot the locals window so a bad exit can restart the chunk interpreted
	// from ip 0 rather than trust the native run's account of where it stopped.
	// Generated code mutates locals in place, and the eligibility filter rejects
	// OpCall, so this window is the ENTIRE observable effect of a native run --
	// which is what makes a restart sound rather than a double-application of side
	// effects. Run happens to Null these slots just before entry today, but taking
	// the copy keeps the recovery correct on its own terms instead of by agreement
	// with a caller. O(LocalCount) once per Run, against a loop hot enough to be
	// worth compiling; the array keeps the common case off the heap.
	var snapArr [16]Value
	var snap []Value
	if lc <= len(snapArr) {
		snap = snapArr[:lc]
	} else {
		snap = make([]Value, lc)
	}
	copy(snap, vm.stack[f.base:f.base+lc])
	// Invariant: stackData below aliases &vm.stack[0] for the whole native run.
	// It stays valid only because generated code never reallocates vm.stack — the
	// no-call eligibility filter (depths rejects OpCall) plus this pre-reservation
	// to `need` guarantee no in-flight growth. Lifting the JIT to compile calls
	// would break this and must re-establish the pointer per re-entry.
	ctx := jitCtx{
		stackData: &vm.stack[0],
		base:      int64(f.base),
		sp:        int64(f.base + lc),
		retVal:    uint64(Null),
	}
	for {
		jitRuns.Add(1)
		jitEntry(jc.entry, &ctx)
		if jitTestExitHook != nil {
			jitTestExitHook(&ctx)
		}
		if ctx.status == jitDone {
			return Value(ctx.retVal), true, nil
		}
		// Every other exit names an ip that something is about to JUMP to -- the
		// interpreter on a deopt, the re-entry dispatch on a cancel poll -- so it
		// has to name a real instruction in this chunk before it is used.
		ipOK := ctx.resumeIP >= 0 && ctx.resumeIP < int64(len(jc.entryDepth))
		switch {
		case ctx.status == jitCancelCheck && ipOK:
			// Back-edge poll handed control back: check cancellation (a real Go
			// safepoint), then re-enter at resumeIP.
			select {
			case <-vm.ctx.Done():
				return Null, true, vm.ctx.Err()
			default:
			}
			continue
		case ctx.status == jitDeopt && ipOK && ctx.sp == int64(f.base+lc+jc.entryDepth[ctx.resumeIP]):
			jitDeopts.Add(1)
			vm.stack = vm.stack[:ctx.sp]
			f.ip = int(ctx.resumeIP)
			v, err := vm.Exec()
			return v, true, err
		}
		// Bad exit: an unknown status, an out-of-range resume ip, or a stack height
		// that disagrees with the depth model the same stub was emitted from. None
		// is reachable from Buzz source -- a program can only make the JIT deopt,
		// never make it lie -- so this is a codegen defect, and the honest response
		// is to throw the native run away rather than resume the interpreter on top
		// of state it cannot trust. Restoring the entry locals makes re-running the
		// whole chunk equivalent to never having entered native code; caching the
		// ineligible verdict keeps the defect to one wasted entry per Run rather
		// than one per call, and stops a systemically broken backend (a bad
		// executable mapping, say) from re-entering it forever.
		jitBadExits.Add(1)
		if vm.faultHook != nil {
			vm.faultHook(FaultJITBadExit)
		}
		jc.disabled.Store(true)
		copy(vm.stack[f.base:f.base+lc], snap)
		vm.stack = vm.stack[:f.base+lc]
		f.ip = 0
		v, err := vm.Exec()
		return v, true, err
	}
}

// jitCompileCached returns chunk's cached compilation, or nil when the chunk is
// not eligible. codegenFailed is true only on the call that actually hit a codegen
// panic — later calls read the cached ineligible marker — so a caller reporting it
// reports each defect once per chunk instead of once per run.
func jitCompileCached(chunk *Chunk) (jc *compiledJIT, codegenFailed bool) {
	key := weak.Make(chunk)
	if c, ok := jitCacheLoad(key); ok {
		return c, false
	}
	// Compilation is serialized. golang-asm's NewBuilder lazily initializes
	// package-level assembler tables (obj/arm64's optab and xcmp, and the x86
	// equivalent) with no synchronization of its own, so two goroutines entering
	// the code generator at the same moment race INSIDE it — on any two chunks, not
	// just the same one. magus runs targets concurrently and the daemon serves
	// concurrent requests, so that is a production scenario rather than a test
	// artifact; it went unseen because the differential suite runs one VM at a time.
	// Serializing costs nothing that matters: this happens once per chunk, on the
	// path that then assembles machine code.
	jitCompileMu.Lock()
	defer jitCompileMu.Unlock()
	if c, ok := jitCacheLoad(key); ok {
		return c, false // another goroutine compiled it while we waited
	}
	jc, failed := safeCompileJIT(chunk)
	stored := jc
	if jc == nil {
		stored = jitIneligible
	}
	jitCache.Store(key, stored)
	// The argument is the WEAK key, never the chunk: a cleanup that referenced its
	// own object would keep it alive forever and so never run.
	runtime.AddCleanup(chunk, jitEvict, key)
	return jc, failed
}

// jitCacheLoad reads the cache, collapsing the two "do not run this natively"
// verdicts — a chunk no backend compiles, and one disabled by a bad exit — into the
// same nil. ok reports that the chunk has a cached verdict at all, which is what
// tells the caller not to compile it again.
func jitCacheLoad(key weak.Pointer[Chunk]) (jc *compiledJIT, ok bool) {
	v, ok := jitCache.Load(key)
	if !ok {
		return nil, false
	}
	if c := v.(*compiledJIT); c != jitIneligible && !c.disabled.Load() {
		return c, true
	}
	return nil, true
}

// safeCompileJIT wraps compileJIT so a code-generator defect degrades to the
// interpreter rather than crashing the host. The underlying assembler
// (golang-asm) panics on an unencodable instruction instead of returning an
// error, so a panic here means "this chunk hit a codegen bug" — mark it
// ineligible (nil) and let the interpreter run it. This preserves the JIT's
// core contract: an unhandled shape is slow, never wrong.
//
// failed distinguishes that panic from the ordinary case of a backend DECLINING a
// shape (compileJIT returning nil), which is routine and expected. Both produce
// the same ineligible verdict, so the flag is the only thing that tells a defect
// apart from an unsupported opcode.
func safeCompileJIT(chunk *Chunk) (jc *compiledJIT, failed bool) {
	defer func() {
		if recover() != nil {
			jc, failed = nil, true
		}
	}()
	return jitCompileFn(chunk), false
}

// jitCompileFn is compileJIT, indirected so a test can substitute a generator that
// panics. That is now the ONLY way to reach the recovery above: since depths()
// validates every operand the backends index, no malformed chunk gets that far, so
// the path has no trigger a test could otherwise construct. Keeping it testable
// matters more than the indirection costs — this is one indirect call on a path
// that goes on to assemble machine code.
var jitCompileFn = compileJIT

// depths computes the operand-stack depth on entry to each instruction and the
// max depth, or ok=false if the chunk contains an opcode/shape no backend can
// compile. It mirrors the interpreter's stack effects exactly.
//
// It is also the JIT's ONLY validation of the chunk, so every operand a backend
// indexes or turns into an address is range-checked HERE. The interpreter can
// afford to assume well-formedness — its indexing is bounds-checked in the default
// build, and a compiler-emitted chunk is well-formed by construction (the premise
// spelled out in fetch_unsafe.go) — but generated code cannot, for two reasons.
// Its indexing is not checked at all: a local slot becomes a raw offset off
// &stack[base], so an out-of-range one reads past the reservation with nothing to
// catch it (see the README on why a fault in generated code is unrecoverable). And
// the premise itself does not hold here, because marshal.Unmarshal builds chunks
// from .bo bytes the compiler never wrote.
//
// A rejection is the routine, silent "ineligible" verdict an unsupported opcode
// gets, which is the point: the chunk runs interpreted and correct, instead of
// panicking the code generator or miscompiling into an out-of-bounds access. Each
// check names what it is protecting.
func depths(chunk *Chunk) (entry []int, maxDepth int, ok bool) {
	code := chunk.Code
	if len(code) == 0 {
		return nil, 0, false
	}
	// Protects `cont := label[ip+1]`, which every numeric op emits on the premise
	// that a numeric op is never last. That holds exactly when the chunk ends in a
	// return — the same terminator invariant fetch_unsafe.go relies on, asserted
	// here rather than assumed, since a truncated chunk would otherwise index the
	// label table one past its end.
	if last := code[len(code)-1].Op; last != OpReturn && last != OpReturnNull {
		return nil, 0, false
	}
	lc := chunk.LocalCount
	slotOK := func(s int32) bool { return s >= 0 && int(s) < lc }          // protects slotOff(s)
	targetOK := func(t int32) bool { return t >= 0 && int(t) < len(code) } // protects label[t]
	// absorbedNops protects the fused forms. The interpreter SKIPS the slots a
	// fusion swallowed (f.ip += n) while the backends emit nothing for them and
	// walk on, so the two agree only if those slots really are OpNop; anything else
	// is native code the interpreter never runs. chunk.go writes 2 for the
	// 3-instruction form and 3 for the 4-instruction register form.
	absorbedNops := func(ip, n int) bool {
		if ip+n >= len(code) {
			return false
		}
		for k := 1; k <= n; k++ {
			if code[ip+k].Op != OpNop {
				return false
			}
		}
		return true
	}
	// fusedDest validates a fused op's destination register and reports how many
	// nop slots that form absorbs. C is dst_slot+1, so C==0 means "push to the
	// operand stack"; a negative C is malformed either way.
	fusedDest := func(c int32) (nops int, ok bool) {
		switch {
		case c < 0:
			return 0, false
		case c == 0:
			return 2, true
		default:
			return 3, slotOK(c - 1)
		}
	}
	entry = make([]int, len(code))
	d := 0
	for ip := 0; ip < len(code); ip++ {
		entry[ip] = d
		if d > maxDepth {
			maxDepth = d
		}
		ins := code[ip]
		switch ins.Op {
		case OpNop:
		case OpLoadConst:
			if ins.A < 0 || int(ins.A) >= len(chunk.Consts) {
				return nil, 0, false // protects chunk.Consts[ins.A]
			}
			d++
		case OpGetLocal:
			if !slotOK(ins.A) {
				return nil, 0, false
			}
			d++
		case OpSetLocal:
			if !slotOK(ins.A) {
				return nil, 0, false
			}
			d--
		case OpJumpFalse:
			if !targetOK(ins.A) {
				return nil, 0, false
			}
			d--
		case OpPop,
			OpAdd, OpSub, OpMul, OpDiv, OpMod,
			OpEqual, OpNotEqual, OpLess, OpLessEqual, OpGreater, OpGreaterEqual:
			// OpPop drops the operand-stack top; in the backends' static-depth
			// model that slot is simply abandoned (overwritten by the next push),
			// so OpPop emits no code — only the depth decrements here.
			d--
		case OpJumpFalsePeek, OpJumpTruePeek:
			// Short-circuit peek-jumps (the `and`/`or` operators). They test the
			// top WITHOUT popping (depth unchanged) and the compiler only ever
			// targets them forward (the end of the &&/|| expression). A backward
			// peek-jump would be a control-flow shape the backends don't emit, so
			// bail rather than miscompile it.
			if int(ins.A) <= ip || !targetOK(ins.A) {
				return nil, 0, false
			}
		case OpJump:
			if !targetOK(ins.A) {
				return nil, 0, false
			}
		case OpCmpLC:
			// 4-instruction fusion (chunk.go Pass 1): the compare, then three
			// absorbed nops whose FIRST carries the branch target in its A field.
			// Both the interpreter and the backends read code[ip+1].A for it.
			if !slotOK(ins.A) ||
				!constIsNumeric(chunk, int(uint32(ins.B)&0xFFFFFF)) ||
				!jitSubOK(OpCode(uint32(ins.B)>>24)) ||
				!absorbedNops(ip, 3) ||
				!targetOK(code[ip+1].A) {
				return nil, 0, false
			}
		case OpBinLC:
			sub := OpCode(uint32(ins.B) >> 24 & 0x7F)
			nops, destOK := fusedDest(ins.C)
			if !destOK || !slotOK(ins.A) || !jitSubOK(sub) ||
				!constIsNumeric(chunk, int(ins.B&0xFFFFFF)) ||
				!absorbedNops(ip, nops) {
				return nil, 0, false
			}
			if ins.C == 0 {
				if sub == OpAdd || sub == OpSub {
					return nil, 0, false // runtime SetLocal-absorption shape, unsupported
				}
				d++
			}
		case OpBinLL:
			sub := OpCode(uint32(ins.B) >> 16 & 0x7FFF)
			nops, destOK := fusedDest(ins.C)
			if !destOK || !slotOK(ins.A) || !slotOK(ins.B&0xFFFF) || !jitSubOK(sub) ||
				!absorbedNops(ip, nops) {
				return nil, 0, false
			}
			if ins.C == 0 {
				if sub == OpAdd || sub == OpSub {
					return nil, 0, false
				}
				d++
			}
		case OpReturnNull:
		case OpReturn:
			d--
		default:
			return nil, 0, false
		}
		if d < 0 {
			return nil, 0, false
		}
		if d > maxDepth {
			maxDepth = d
		}
	}
	return entry, maxDepth, true
}

// jitSubOK reports whether a fused op's sub-opcode is one emitNumeric compiles.
// Needed because emitNumeric does NOT reject an unknown sub: its int path ends in
// `default: // comparison`, so an unrecognized value falls into the compare branch
// and asks intSetCC for a SETcc that does not exist, which reaches the assembler
// as a zero instruction rather than as an error.
func jitSubOK(sub OpCode) bool {
	switch sub {
	case OpAdd, OpSub, OpMul, OpDiv, OpMod,
		OpEqual, OpNotEqual, OpLess, OpLessEqual, OpGreater, OpGreaterEqual:
		return true
	}
	return false
}

func constIsNumeric(chunk *Chunk, idx int) bool {
	if idx < 0 || idx >= len(chunk.Consts) {
		return false
	}
	t := chunk.Consts[idx].tag()
	return t == tagInt || t == tagFloat
}
