//go:build arm64 && !windows && !buzz_safe && !buzz_unsafe

package vm

import (
	"sort"

	asm "github.com/twitchyliquid64/golang-asm"
	"github.com/twitchyliquid64/golang-asm/obj"
	"github.com/twitchyliquid64/golang-asm/obj/arm64"
	"golang.org/x/sys/unix"
)

// arm64 backend for the baseline JIT (arch-independent half in jit_shared.go).
//
// Scope vs amd64: this backend handles the INTEGER fast path only (add/sub/mul
// and the six comparisons). Float operands, division, and modulo deopt to the
// interpreter — the same sound fallback the amd64 backend uses for cases it
// declines, just wider here. That keeps the unrunnable arm64 codegen small.
//
// This backend is enabled by default. Correctness is gated by the shared
// differential suite (TestJITMatchesInterpreter runs every JIT program with the
// JIT on and off and requires identical results), and any shape this backend
// declines — floats, division, modulo, or an unhandled opcode — deopts to the
// interpreter, so an un-handled case is slow, never wrong. amd64 remains the
// primary, most-exercised backend.
const jitArchDefault = true

// registers (avoid R16-R18 veneer/platform, R27 REGTMP, R28 g, R29 FP, R30 LR).
const (
	aR0 = arm64.REG_R0 // ctx (incoming)
	aR1 = arm64.REG_R1 // &stack[base]
	aR3 = arm64.REG_R3 // left / scratch
	aR4 = arm64.REG_R4 // right / scratch
	aR5 = arm64.REG_R5 // temp
	aR6 = arm64.REG_R6 // temp
)

// jitEntry is declared in jit_shared.go; jit_arm64.s provides the arm64 impl.

// flushICache performs AArch64 instruction-cache maintenance over [p, p+n) so
// freshly written, then RX-mapped, bytes are coherently fetched as code.
// Implemented in jit_arm64.s; amd64 needs no equivalent (x86 is I-cache coherent).
func flushICache(p *byte, n int)

func compileJIT(chunk *Chunk) *compiledJIT {
	entry, maxDepth, ok := depths(chunk)
	if !ok {
		return nil
	}
	code := chunk.Code
	lc := int64(chunk.LocalCount)

	b, err := asm.NewBuilder("arm64", len(code)*16+64)
	if err != nil {
		return nil
	}
	add := b.AddInstruction
	np := b.NewProg

	// emit helpers (Plan9 arm64: 3-operand is From=Rm, Reg=Rn, To=Rd).
	movImm := func(dst int16, imm int64) {
		p := np()
		p.As = arm64.AMOVD
		p.From.Type = obj.TYPE_CONST
		p.From.Offset = imm
		p.To.Type = obj.TYPE_REG
		p.To.Reg = dst
		add(p)
	}
	ld := func(dst, base int16, off int64) {
		p := np()
		p.As = arm64.AMOVD
		p.From.Type = obj.TYPE_MEM
		p.From.Reg = base
		p.From.Offset = off
		p.To.Type = obj.TYPE_REG
		p.To.Reg = dst
		add(p)
	}
	st := func(base int16, off int64, src int16) {
		p := np()
		p.As = arm64.AMOVD
		p.From.Type = obj.TYPE_REG
		p.From.Reg = src
		p.To.Type = obj.TYPE_MEM
		p.To.Reg = base
		p.To.Offset = off
		add(p)
	}
	alu := func(as obj.As, rm, rn, rd int16) { // rd = rn OP rm
		p := np()
		p.As = as
		p.From.Type = obj.TYPE_REG
		p.From.Reg = rm
		p.Reg = rn
		p.To.Type = obj.TYPE_REG
		p.To.Reg = rd
		add(p)
	}
	shiftImm := func(as obj.As, n int64, rn, rd int16) { // rd = rn SHIFT n
		p := np()
		p.As = as
		p.From.Type = obj.TYPE_CONST
		p.From.Offset = n
		p.Reg = rn
		p.To.Type = obj.TYPE_REG
		p.To.Reg = rd
		add(p)
	}
	addImm := func(imm int64, rn, rd int16) {
		p := np()
		p.As = arm64.AADD
		p.From.Type = obj.TYPE_CONST
		p.From.Offset = imm
		p.Reg = rn
		p.To.Type = obj.TYPE_REG
		p.To.Reg = rd
		add(p)
	}
	andImm := func(imm int64, rn, rd int16) {
		p := np()
		p.As = arm64.AAND
		p.From.Type = obj.TYPE_CONST
		p.From.Offset = imm
		p.Reg = rn
		p.To.Type = obj.TYPE_REG
		p.To.Reg = rd
		add(p)
	}
	cmpRR := func(rm, rn int16) { // flags = rn - rm
		p := np()
		p.As = arm64.ACMP
		p.From.Type = obj.TYPE_REG
		p.From.Reg = rm
		p.Reg = rn
		add(p)
	}
	cmpImm := func(imm int64, rn int16) { // flags = rn - imm
		if imm == 0 {
			// CMP $0 miscompiles: the assembler's cmp() lets the register-compare
			// optab match a zero constant, and the unset From.Reg encodes as x0 —
			// so `CMP $0, Rn` becomes `CMP R0, Rn`. Compare the zero register instead.
			cmpRR(arm64.REGZERO, rn)
			return
		}
		p := np()
		p.As = arm64.ACMP
		p.From.Type = obj.TYPE_CONST
		p.From.Offset = imm
		p.Reg = rn
		add(p)
	}
	cset := func(cond int16, rd int16) {
		p := np()
		p.As = arm64.ACSET
		p.From.Type = obj.TYPE_REG
		p.From.Reg = cond
		p.To.Type = obj.TYPE_REG
		p.To.Reg = rd
		add(p)
	}
	br := func(as obj.As, target *obj.Prog) {
		p := np()
		p.As = as
		p.To.Type = obj.TYPE_BRANCH
		p.To.Val = target
		add(p)
	}
	ret := func() {
		p := np()
		p.As = obj.ARET
		p.To.Type = obj.TYPE_REG
		p.To.Reg = arm64.REG_R30 // arm64 RET = BR R30; oplook requires explicit Rn
		add(p)
	}

	// label anchors per ip (ANOP, zero bytes); deopt/cancel anchors lazily.
	label := make([]*obj.Prog, len(code))
	for ip := range label {
		label[ip] = np()
		label[ip].As = obj.ANOP
	}
	deoptAnchor := map[int]*obj.Prog{}
	cancelAnchor := map[int]*obj.Prog{}
	type stub struct {
		p     *obj.Prog
		ip    int
		depth int
	}
	var deopts, cancels []stub
	deoptFor := func(ip int) *obj.Prog {
		if p, ok := deoptAnchor[ip]; ok {
			return p
		}
		p := np()
		p.As = obj.ANOP
		deoptAnchor[ip] = p
		deopts = append(deopts, stub{p, ip, entry[ip]})
		return p
	}
	cancelFor := func(ip int) *obj.Prog {
		if p, ok := cancelAnchor[ip]; ok {
			return p
		}
		p := np()
		p.As = obj.ANOP
		cancelAnchor[ip] = p
		cancels = append(cancels, stub{p, ip, entry[ip]})
		return p
	}

	opOff := func(d int) int64 { return 8 * (lc + int64(d)) }
	slotOff := func(slot int32) int64 { return 8 * int64(slot) }

	guardIntDeopt := func(reg int16, ip int) {
		shiftImm(arm64.ALSR, 48, reg, aR5)
		movImm(aR6, jitIntHi16)
		cmpRR(aR6, aR5) // aR5 - aR6
		br(arm64.ABNE, deoptFor(ip))
	}
	decodeInt := func(reg int16) {
		shiftImm(arm64.ALSL, 16, reg, reg)
		shiftImm(arm64.AASR, 16, reg, reg)
	}
	boxInt := func(reg int16) {
		shiftImm(arm64.ALSL, 16, reg, reg)
		shiftImm(arm64.ALSR, 16, reg, reg)
		movImm(aR5, jitIntHeader)
		alu(arm64.AORR, aR5, reg, reg)
	}
	setCC := func(sub OpCode) int16 {
		switch sub {
		case OpLess:
			return arm64.COND_LT
		case OpLessEqual:
			return arm64.COND_LE
		case OpGreater:
			return arm64.COND_GT
		case OpGreaterEqual:
			return arm64.COND_GE
		case OpEqual:
			return arm64.COND_EQ
		default:
			return arm64.COND_NE
		}
	}
	isCmp := func(sub OpCode) bool {
		switch sub {
		case OpLess, OpLessEqual, OpGreater, OpGreaterEqual, OpEqual, OpNotEqual:
			return true
		}
		return false
	}

	// emitNumeric: raw left in R3, raw right in R4. Integer fast path; float, div,
	// and mod deopt (this backend is int-only). Result (boxed) left in R3.
	emitNumeric := func(sub OpCode, ip int, store func(reg int16)) {
		if sub == OpDiv || sub == OpMod {
			br(obj.AJMP, deoptFor(ip)) // unsupported on arm64 → interpreter
			return
		}
		guardIntDeopt(aR3, ip)
		guardIntDeopt(aR4, ip)
		decodeInt(aR3)
		decodeInt(aR4)
		switch {
		case sub == OpAdd:
			alu(arm64.AADD, aR4, aR3, aR3)
			boxInt(aR3)
		case sub == OpSub:
			alu(arm64.ASUB, aR4, aR3, aR3)
			boxInt(aR3)
		case sub == OpMul:
			alu(arm64.AMUL, aR4, aR3, aR3)
			boxInt(aR3)
		default: // comparison
			cmpRR(aR4, aR3) // aR3 - aR4 (left - right)
			cset(setCC(sub), aR3)
			movImm(aR5, jitFalseBits)
			alu(arm64.AORR, aR5, aR3, aR3)
		}
		store(aR3)
	}

	emitPoll := func(target int) {
		ld(aR6, aR0, offCancelN)
		addImm(1, aR6, aR6)
		st(aR0, offCancelN, aR6)
		andImm(255, aR6, aR6)
		cmpImm(0, aR6)
		br(arm64.ABEQ, cancelFor(target))
	}
	storeFalseBranch := func(target int) func(int16) {
		return func(reg int16) {
			movImm(aR5, jitFalseBits)
			cmpRR(aR5, reg) // reg - false
			br(arm64.ABEQ, label[target])
		}
	}

	// arm64's span7 starts at cursym.Func.Text.Link (the second instruction),
	// skipping the first. Prepend a sentinel NOP so the real prologue starts
	// at the second position.
	{
		p := np()
		p.As = obj.ANOP
		add(p)
	}

	// Prologue: R1 = &stack[base].
	ld(aR1, aR0, offStackData)
	ld(aR5, aR0, offBase)
	shiftImm(arm64.ALSL, 3, aR5, aR5)
	alu(arm64.AADD, aR5, aR1, aR1)

	// Re-entry dispatch on ctx.resumeIP.
	backEdge := map[int]bool{}
	for ip := 0; ip < len(code); ip++ {
		if op := code[ip].Op; op == OpJump || op == OpJumpFalse {
			if int(code[ip].A) <= ip {
				backEdge[int(code[ip].A)] = true
			}
		}
	}
	if len(backEdge) > 0 {
		targets := make([]int, 0, len(backEdge))
		for t := range backEdge {
			targets = append(targets, t)
		}
		sort.Ints(targets)
		ld(aR5, aR0, offResumeIP)
		for _, t := range targets {
			cmpImm(int64(t), aR5)
			br(arm64.ABEQ, label[t])
		}
	}

	for ip := 0; ip < len(code); ip++ {
		add(label[ip])
		ins := code[ip]
		d := entry[ip]
		switch ins.Op {
		case OpNop:

		case OpLoadConst:
			movImm(aR3, int64(uint64(chunk.Consts[ins.A])))
			st(aR1, opOff(d), aR3)

		case OpGetLocal:
			ld(aR3, aR1, slotOff(ins.A))
			st(aR1, opOff(d), aR3)

		case OpSetLocal:
			ld(aR3, aR1, opOff(d-1))
			st(aR1, slotOff(ins.A), aR3)

		case OpAdd, OpSub, OpMul, OpDiv, OpMod,
			OpEqual, OpNotEqual, OpLess, OpLessEqual, OpGreater, OpGreaterEqual:
			ld(aR3, aR1, opOff(d-2))
			ld(aR4, aR1, opOff(d-1))
			emitNumeric(ins.Op, ip, func(r int16) { st(aR1, opOff(d-2), r) })

		case OpBinLC:
			sub := OpCode(uint32(ins.B) >> 24 & 0x7F)
			cbits := int64(uint64(chunk.Consts[int(ins.B&0xFFFFFF)]))
			ld(aR3, aR1, slotOff(ins.A))
			movImm(aR4, cbits)
			dstC := ins.C
			emitNumeric(sub, ip, func(r int16) {
				if dstC > 0 {
					st(aR1, slotOff(dstC-1), r)
				} else {
					st(aR1, opOff(d), r)
				}
			})

		case OpBinLL:
			sub := OpCode(uint32(ins.B) >> 16 & 0x7FFF)
			rslot := int32(ins.B & 0xFFFF)
			ld(aR3, aR1, slotOff(ins.A))
			ld(aR4, aR1, slotOff(rslot))
			dstC := ins.C
			emitNumeric(sub, ip, func(r int16) {
				if dstC > 0 {
					st(aR1, slotOff(dstC-1), r)
				} else {
					st(aR1, opOff(d), r)
				}
			})

		case OpCmpLC:
			sub := OpCode(uint32(ins.B) >> 24)
			cbits := int64(uint64(chunk.Consts[int(uint32(ins.B)&0xFFFFFF)]))
			target := int(code[ip+1].A)
			ld(aR3, aR1, slotOff(ins.A))
			movImm(aR4, cbits)
			if isCmp(sub) {
				emitNumeric(sub, ip, storeFalseBranch(target))
			} else {
				br(obj.AJMP, deoptFor(ip))
			}

		case OpPop:
			// No code: the static-depth model abandons the dropped slot (the next
			// push overwrites it); nanbox Values hold no Go pointer. See depths().

		case OpJumpFalsePeek:
			// `and` short-circuit: peek top (depth unchanged), jump forward if
			// false. Mirrors OpJumpFalse's guard+compare without the pop;
			// forward-only (depths() rejects backward peek-jumps).
			ld(aR3, aR1, opOff(d-1))
			shiftImm(arm64.ALSR, 48, aR3, aR5)
			movImm(aR6, jitBoolHi16)
			cmpRR(aR6, aR5)
			br(arm64.ABNE, deoptFor(ip)) // non-bool → deopt
			movImm(aR5, jitFalseBits)
			cmpRR(aR5, aR3)              // aR3 - false
			br(arm64.ABEQ, label[ins.A]) // false ⇒ forward jump

		case OpJumpTruePeek:
			// `or` short-circuit: peek; jump forward if true. Inverted branch.
			ld(aR3, aR1, opOff(d-1))
			shiftImm(arm64.ALSR, 48, aR3, aR5)
			movImm(aR6, jitBoolHi16)
			cmpRR(aR6, aR5)
			br(arm64.ABNE, deoptFor(ip))
			movImm(aR5, jitFalseBits)
			cmpRR(aR5, aR3)
			br(arm64.ABNE, label[ins.A]) // != false ⇒ true ⇒ forward jump

		case OpJump:
			if int(ins.A) <= ip {
				emitPoll(int(ins.A))
			}
			br(obj.AJMP, label[ins.A])

		case OpJumpFalse:
			ld(aR3, aR1, opOff(d-1))
			shiftImm(arm64.ALSR, 48, aR3, aR5)
			movImm(aR6, jitBoolHi16)
			cmpRR(aR6, aR5)
			br(arm64.ABNE, deoptFor(ip))
			movImm(aR5, jitFalseBits)
			cmpRR(aR5, aR3) // aR3 - false
			if int(ins.A) <= ip {
				br(arm64.ABNE, label[ip+1]) // true → continue
				emitPoll(int(ins.A))
				br(obj.AJMP, label[ins.A])
			} else {
				br(arm64.ABEQ, label[ins.A]) // false → jump
			}

		case OpReturnNull:
			movImm(aR3, jitNullBits)
			st(aR0, offRetVal, aR3)
			movImm(aR3, jitDone)
			st(aR0, offStatus, aR3)
			ret()

		case OpReturn:
			ld(aR3, aR1, opOff(d-1))
			st(aR0, offRetVal, aR3)
			movImm(aR3, jitDone)
			st(aR0, offStatus, aR3)
			ret()
		}
	}

	for _, s := range deopts {
		add(s.p)
		movImm(aR3, int64(s.ip))
		st(aR0, offResumeIP, aR3)
		movImm(aR3, jitDeopt)
		st(aR0, offStatus, aR3)
		ld(aR3, aR0, offBase)
		movImm(aR5, lc+int64(s.depth))
		alu(arm64.AADD, aR5, aR3, aR3)
		st(aR0, offSP, aR3)
		ret()
	}
	for _, s := range cancels {
		add(s.p)
		movImm(aR3, int64(s.ip))
		st(aR0, offResumeIP, aR3)
		movImm(aR3, jitCancelCheck)
		st(aR0, offStatus, aR3)
		ret()
	}

	buf := b.Assemble()
	if len(buf) == 0 {
		return nil
	}
	mem, err := unix.Mmap(-1, 0, len(buf),
		unix.PROT_READ|unix.PROT_WRITE,
		unix.MAP_PRIVATE|unix.MAP_ANONYMOUS)
	if err != nil {
		return nil
	}
	copy(mem, buf)
	if err := unix.Mprotect(mem, unix.PROT_READ|unix.PROT_EXEC); err != nil {
		_ = unix.Munmap(mem)
		return nil
	}
	// AArch64 needs explicit cache maintenance before the freshly written bytes
	// are fetched as instructions; Mprotect alone does not invalidate the I-cache.
	flushICache(&mem[0], len(mem))
	return &compiledJIT{code: mem, entry: &mem[0], maxDepth: maxDepth}
}
