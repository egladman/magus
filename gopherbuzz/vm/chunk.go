package vm

// Instr is a single bytecode instruction.
//
// C is the compile-time destination register for 3-address ops:
//   - C == 0 (zero value): result goes to the operand stack (stack form)
//   - C > 0:  result goes to stack[frame.base + C - 1] (register form; C-1 is the slot)
//
// The +1 bias lets the zero value keep the old push-to-stack semantics so every
// Instr literal that omits C defaults to stack form with no migration work.
// Only OpBinLL and OpBinLC currently use C; all other opcodes leave
// it zero.
type Instr struct {
	Op OpCode
	A  int32 // primary operand
	B  int32 // secondary operand
	C  int32 // destination register (0=stack, C>0 → slot C-1); see note above
}

// UpvalInfo describes one upvalue captured by a closure.
type UpvalInfo struct {
	IsLocal bool  // true: capture from immediately enclosing fn's local slot
	Index   int32 // slot index (IsLocal=true) or upvalue index (IsLocal=false)
}

// Chunk is a compiled unit: a sequence of instructions with a constant pool
// and a list of nested function chunks.
//
// Treat a Chunk as an opaque handle: obtain one only from Compile/CompileWith
// or Session.Compile and pass it to Session.ExecChunk. The VM trusts compiler
// invariants (notably that the instruction stream ends in a terminating return),
// so a hand-populated Chunk can fault at execution time. The exported fields
// exist for the compiler and VM in this package, not for external mutation.
type Chunk struct {
	Name string
	// Doc is the documentation comment block of the source `fun` declaration this
	// chunk compiles, or "" when undocumented. In-memory only: it is not part of the
	// serialized .bo format (see marshal.go), so a chunk recovered from bytecode
	// carries no Doc. FunDoc reads it; host code uses it to recover a spell target
	// handler's comment from a freshly-compiled (workspace-local) spell.
	Doc    string
	Params []string // parameter names (empty for top-level)
	Code   []Instr
	// Lines maps each instruction in Code to its 1-based source line (parallel
	// slice). Populated by emit from CurLine; consumed by the debugger to report
	// the current line of a paused frame and to drive line-level step hooks. Only
	// populated when debug info is requested (see CompileOptions.DebugLines); nil
	// otherwise, so the hot compile/run path pays nothing.
	Lines []int32
	// CurLine is the source line the compiler is currently emitting for; the
	// compiler updates it per statement/expression and emit copies it into Lines.
	CurLine int32
	// Consts holds literal values and string names referenced by instructions.
	Consts []Value
	// Funs holds compiled nested function bodies (referenced by OpNewClosure).
	Funs []*Chunk
	// LocalCount is the number of local variable slots this chunk needs.
	LocalCount int
	// UpvalInfos describes the upvalue capture descriptors for this chunk.
	UpvalInfos []UpvalInfo
	// LocalNames maps a local's stack slot to its source name, and UpvalNames maps
	// an upvalue index to its name. Both are populated only under DebugLines so the
	// debugger can label a frame's locals/upvalues; nil otherwise.
	LocalNames []string
	UpvalNames []string
	// Exports holds the names of top-level declarations marked export. Populated
	// only in SharedGlobals mode (session-compiled chunks); nil otherwise.
	Exports []string
	// Private holds the names of non-exported top-level declarations that were
	// bound into the Env (not slot-promoted) — i.e. non-exported captured vars and
	// non-exported functions. A flat-importing session reads it to enforce
	// exports-only import visibility: these names stay live in the runtime Env (the
	// module's own functions read them) but are hidden from the importer's checker.
	// In-memory only, like Doc/Exports' compile-time role; not serialized.
	Private []string
}

func (c *Chunk) Emit(op OpCode, a, b int32) int {
	c.Code = append(c.Code, Instr{Op: op, A: a, B: b})
	if c.Lines != nil {
		c.Lines = append(c.Lines, c.CurLine)
	}
	return len(c.Code) - 1
}

// LineAt returns the 1-based source line for the instruction at ip, or 0 when
// no debug-line info was compiled (DebugLines off) or ip is out of range.
func (c *Chunk) LineAt(ip int) int { return c.lineAt(ip) }

// lineAt is the unexported alias used internally.
func (c *Chunk) lineAt(ip int) int {
	if ip < 0 || ip >= len(c.Lines) {
		return 0
	}
	return int(c.Lines[ip])
}

func (c *Chunk) EmitJump(op OpCode) int { return c.Emit(op, 0, 0) }

func (c *Chunk) PatchJump(idx int) { c.Code[idx].A = int32(len(c.Code)) }

func (c *Chunk) Current() int { return len(c.Code) }

// AddConst appends v to the constant pool. String constants are deduplicated.
func (c *Chunk) AddConst(v Value) int32 {
	if v.tag() == tagStr {
		sv := v.asStr().V
		for i, e := range c.Consts {
			if e.tag() == tagStr && e.asStr().V == sv {
				return int32(i)
			}
		}
	}
	c.Consts = append(c.Consts, v)
	return int32(len(c.Consts) - 1)
}

// AddFun appends a nested function chunk and returns its index.
func (c *Chunk) AddFun(child *Chunk) int32 {
	c.Funs = append(c.Funs, child)
	return int32(len(c.Funs) - 1)
}

// FoldConsts performs a single-pass constant-folding optimization on chunk's
// instruction stream. It replaces sequences like:
//
//	OpLoadConst <int>  OpLoadConst <int>  OpAdd   →  OpLoadConst <folded>
//
// Only integer arithmetic is folded; floating-point is left to the VM to
// preserve NaN/Inf semantics correctly. The pass is O(n) and allocation-free.
func FoldConsts(c *Chunk) {
	code := c.Code
	n := len(code)
	for i := 0; i+2 < n; i++ {
		a, b, op := code[i], code[i+1], code[i+2]
		if a.Op != OpLoadConst || b.Op != OpLoadConst {
			continue
		}
		lv := c.Consts[a.A]
		rv := c.Consts[b.A]
		if lv.tag() != tagInt || rv.tag() != tagInt {
			continue
		}
		li, ri := int64(lv.num()), int64(rv.num())
		var result int64
		switch op.Op {
		case OpAdd:
			result = li + ri
		case OpSub:
			result = li - ri
		case OpMul:
			result = li * ri
		case OpDiv:
			if ri == 0 {
				continue
			}
			result = li / ri
		case OpMod:
			if ri == 0 {
				continue
			}
			result = li % ri
		default:
			continue
		}
		idx := c.AddConst(IntValue(result))
		code[i] = Instr{Op: OpLoadConst, A: idx}
		code[i+1] = Instr{Op: OpNop}
		code[i+2] = Instr{Op: OpNop}
	}
	for _, fc := range c.Funs {
		FoldConsts(fc)
	}
}

// fusableBinop reports whether op is a binary arithmetic/comparison opcode that
// can be the tail of a GetLocal;LoadConst;<op> fusion.
func fusableBinop(op OpCode) bool {
	switch op {
	case OpAdd, OpSub, OpMul, OpDiv, OpMod,
		OpLess, OpLessEqual, OpGreater, OpGreaterEqual, OpEqual, OpNotEqual:
		return true
	}
	return false
}

// fusableCondOp reports whether op is a comparison opcode suitable for
// GetLocal;LoadConst;<op>;JumpFalse → OpCmpLC fusion.
func fusableCondOp(op OpCode) bool {
	switch op {
	case OpLess, OpLessEqual, OpGreater, OpGreaterEqual, OpEqual, OpNotEqual:
		return true
	}
	return false
}

// FusePeephole fuses common instruction sequences into superinstructions,
// reducing dispatch count. Two passes in order — longer patterns first.
//
// Jump operands are absolute indices, so fusion rewrites in place (super + OpNop
// fill) rather than collapsing the stream. A branch may target the start of a
// window (e.g. a loop back-edge), but a target inside the window would skip
// operand evaluation — fusion is suppressed when any jump targets a slot inside.
//
// Pass 1  — 4-instruction: GetLocal;LoadConst;<cmp>;JumpFalse → OpCmpLC.
// Pass 1L — 4-instruction: GetLocal;GetLocal;<binop>;SetLocal  → OpBinLL (C=dst+1).
// Pass 1C — 4-instruction: GetLocal;LoadConst;<binop>;SetLocal → OpBinLC (C=dst+1).
// Pass 2  — 3-instruction: GetLocal;GetLocal;<binop>  → OpBinLL (C=0, push);
//
//	GetLocal;LoadConst;<binop> → OpBinLC (C=0, push).
//
// Pass 1L/1C emit the destination register in Instr.C (with a +1 bias so C=0
// keeps the "push to operand stack" meaning of the zero value). These run before
// Pass 2 so the SetLocal is absorbed at compile time rather than checked at
// runtime — saving a dispatch and enabling the dst≠src1 case that runtime
// SetLocal absorption cannot handle.
func FusePeephole(c *Chunk) {
	code := c.Code
	n := len(code)
	// Collect every in-chunk branch destination once; used by both passes.
	var targets map[int]bool
	for _, ins := range code {
		switch ins.Op {
		case OpJump, OpJumpFalse, OpJumpTrue, OpJumpFalsePeek, OpJumpTruePeek,
			OpJumpIfNull, OpIterNext, OpTryBegin:
			if targets == nil {
				targets = make(map[int]bool)
			}
			targets[int(ins.A)] = true
		}
	}

	// Pass 1: GetLocal;LoadConst;<cmp>;JumpFalse → OpCmpLC.
	// Covers the canonical while/for loop condition (i < N, i >= lo, etc.).
	// The jump target is stored in the first absorbed OpNop's A field so the
	// handler can branch without an extra instruction; three NOPs follow.
	for i := 0; i+3 < n; i++ {
		if code[i].Op != OpGetLocal ||
			code[i+1].Op != OpLoadConst ||
			!fusableCondOp(code[i+2].Op) ||
			code[i+3].Op != OpJumpFalse ||
			targets[i+1] || targets[i+2] || targets[i+3] {
			continue
		}
		cidx := code[i+1].A
		if cidx < 0 || cidx >= 1<<24 {
			continue
		}
		target := code[i+3].A
		code[i] = Instr{Op: OpCmpLC, A: code[i].A, B: cidx | int32(code[i+2].Op)<<24}
		code[i+1] = Instr{Op: OpNop, A: target} // target preserved for handler
		code[i+2] = Instr{Op: OpNop}
		code[i+3] = Instr{Op: OpNop}
		i += 3
	}

	// Pass 1L/1C: 4-instruction patterns that encode a compile-time destination
	// register in Instr.C (C = dst_slot + 1). These absorb the trailing SetLocal
	// at compile time, saving a runtime dispatch and allowing dst≠src1.
	// Guard: no branch targets inside the window (i+1..i+3).
	const slotTypeInt = 1
	const provenIntFlag = ^int32(0x7FFFFFFF)
	for i := 0; i+3 < n; i++ {
		if code[i].Op != OpGetLocal || targets[i+1] || targets[i+2] || targets[i+3] {
			continue
		}
		if code[i+3].Op != OpSetLocal || !fusableBinop(code[i+2].Op) {
			continue
		}
		dstSlot := code[i+3].A
		switch {
		case code[i+1].Op == OpGetLocal:
			slotL, slotR := code[i].A, code[i+1].A
			op := code[i+2].Op
			if slotR < 0 || slotR >= 1<<16 || int(op) >= 1<<16 {
				continue
			}
			var flag int32
			if code[i].B == slotTypeInt && code[i+1].B == slotTypeInt {
				flag = provenIntFlag
			}
			code[i] = Instr{Op: OpBinLL, A: slotL, B: slotR | int32(op)<<16 | flag, C: dstSlot + 1}
			code[i+1] = Instr{Op: OpNop}
			code[i+2] = Instr{Op: OpNop}
			code[i+3] = Instr{Op: OpNop}
			i += 3
		case code[i+1].Op == OpLoadConst:
			cidx := code[i+1].A
			op := code[i+2].Op
			if cidx < 0 || cidx >= 1<<24 {
				continue
			}
			var flag int32
			if code[i].B == slotTypeInt && len(c.Consts) > int(cidx) && c.Consts[cidx].tag() == tagInt {
				flag = provenIntFlag
			}
			code[i] = Instr{Op: OpBinLC, A: code[i].A, B: cidx | int32(op)<<24 | flag, C: dstSlot + 1}
			code[i+1] = Instr{Op: OpNop}
			code[i+2] = Instr{Op: OpNop}
			code[i+3] = Instr{Op: OpNop}
			i += 3
		}
	}

	// Pass 2: 3-instruction patterns.
	//
	// "Both-int proven" flag: bit 31 of the fused instruction's B field is set
	// when the compiler has statically proven both operands are int (via the
	// slotType annotation in OpGetLocal.B and the constant's tag). The VM handler
	// reads it to skip the runtime tag check, replacing two tag loads+compares
	// with a single sign-bit test per dispatch. Sound because OpCheckType is
	// emitted wherever any→int flows, so a proven-int slot is always int at
	// runtime. The flag shares the B field without stealing encoding bits:
	//   OpBinLL B = slotR | op<<16 | flag<<31  (sub-op is at most 8-bit value << 16, flag in bit 31)
	//   OpBinLC B = cidx | op<<24 | flag<<31   (sub-op is at most 8-bit value << 24, flag in bit 31)
	// Limitation: params, upvalues, globals, and call/member/index results always
	// emit B=0 on OpGetLocal (slotType=sUnknown), so only stack-slot locals with a
	// tracked primitive type trigger the fast path.
	for i := 0; i+2 < n; i++ {
		if code[i].Op != OpGetLocal || targets[i+1] || targets[i+2] {
			continue
		}
		switch {
		case code[i+1].Op == OpGetLocal && fusableBinop(code[i+2].Op):
			slotR := code[i+1].A
			op := code[i+2].Op
			if slotR < 0 || slotR >= 1<<16 || int(op) >= 1<<16 {
				continue
			}
			var flag int32
			if code[i].B == slotTypeInt && code[i+1].B == slotTypeInt {
				flag = provenIntFlag
			}
			code[i] = Instr{Op: OpBinLL, A: code[i].A, B: slotR | int32(op)<<16 | flag}
			code[i+1] = Instr{Op: OpNop}
			code[i+2] = Instr{Op: OpNop}
			i += 2
		case code[i+1].Op == OpLoadConst && fusableBinop(code[i+2].Op):
			cidx := code[i+1].A
			op := code[i+2].Op
			if cidx < 0 || cidx >= 1<<24 {
				continue
			}
			var flag int32
			if code[i].B == slotTypeInt && len(c.Consts) > int(cidx) && c.Consts[cidx].tag() == tagInt {
				flag = provenIntFlag
			}
			code[i] = Instr{Op: OpBinLC, A: code[i].A, B: cidx | int32(op)<<24 | flag}
			code[i+1] = Instr{Op: OpNop}
			code[i+2] = Instr{Op: OpNop}
			i += 2
		}
	}

	for _, fc := range c.Funs {
		FusePeephole(fc)
	}
}
