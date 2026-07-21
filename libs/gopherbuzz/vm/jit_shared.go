//go:build (amd64 || arm64) && !windows && !buzz_safe && !buzz_unsafe

package vm

import "sync"

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
}

var (
	jitCache      sync.Map // *Chunk -> *compiledJIT (jitIneligible == not eligible)
	jitIneligible = &compiledJIT{}
)

// jitRun runs the top frame's chunk as native code. ok=false means "fall back to
// the interpreter". On deopt it resumes the interpreter and returns its result,
// so ok=true always reflects a completed run.
func (vm *VM) jitRun() (Value, bool, error) {
	if !JITEnabled() || len(vm.frames) != 1 {
		return Null, false, nil
	}
	f := &vm.frames[0]
	jc := jitCompileCached(f.chunk)
	if jc == nil {
		return Null, false, nil
	}
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
		switch ctx.status {
		case jitDone:
			return Value(ctx.retVal), true, nil
		case jitCancelCheck:
			// Back-edge poll handed control back: check cancellation (a real Go
			// safepoint), then re-enter at resumeIP.
			select {
			case <-vm.ctx.Done():
				return Null, true, vm.ctx.Err()
			default:
			}
		default: // jitDeopt
			vm.stack = vm.stack[:ctx.sp]
			f.ip = int(ctx.resumeIP)
			v, err := vm.Exec()
			return v, true, err
		}
	}
}

func jitCompileCached(chunk *Chunk) *compiledJIT {
	if v, ok := jitCache.Load(chunk); ok {
		jc := v.(*compiledJIT)
		if jc == jitIneligible {
			return nil
		}
		return jc
	}
	jc := safeCompileJIT(chunk)
	if jc == nil {
		jitCache.Store(chunk, jitIneligible)
		return nil
	}
	jitCache.Store(chunk, jc)
	return jc
}

// safeCompileJIT wraps compileJIT so a code-generator defect degrades to the
// interpreter rather than crashing the host. The underlying assembler
// (golang-asm) panics on an unencodable instruction instead of returning an
// error, so a panic here means "this chunk hit a codegen bug" — mark it
// ineligible (nil) and let the interpreter run it. This preserves the JIT's
// core contract: an unhandled shape is slow, never wrong.
func safeCompileJIT(chunk *Chunk) (jc *compiledJIT) {
	defer func() {
		if recover() != nil {
			jc = nil
		}
	}()
	return compileJIT(chunk)
}

// depths computes the operand-stack depth on entry to each instruction and the
// max depth, or ok=false if the chunk contains an opcode/shape no backend can
// compile. It mirrors the interpreter's stack effects exactly.
func depths(chunk *Chunk) (entry []int, maxDepth int, ok bool) {
	code := chunk.Code
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
		case OpLoadConst, OpGetLocal:
			d++
		case OpSetLocal, OpJumpFalse, OpPop,
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
			if int(ins.A) <= ip {
				return nil, 0, false
			}
		case OpJump:
		case OpCmpLC:
			if !constIsNumeric(chunk, int(uint32(ins.B)&0xFFFFFF)) {
				return nil, 0, false
			}
		case OpBinLC:
			if !constIsNumeric(chunk, int(ins.B&0xFFFFFF)) {
				return nil, 0, false
			}
			sub := OpCode(uint32(ins.B) >> 24 & 0x7F)
			if ins.C == 0 {
				if sub == OpAdd || sub == OpSub {
					return nil, 0, false // runtime SetLocal-absorption shape, unsupported
				}
				d++
			}
		case OpBinLL:
			sub := OpCode(uint32(ins.B) >> 16 & 0x7FFF)
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

func constIsNumeric(chunk *Chunk, idx int) bool {
	if idx < 0 || idx >= len(chunk.Consts) {
		return false
	}
	t := chunk.Consts[idx].tag()
	return t == tagInt || t == tagFloat
}
