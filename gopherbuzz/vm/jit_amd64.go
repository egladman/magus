//go:build amd64 && !windows && !buzz_safe && !buzz_unsafe

package vm

import (
	"sort"

	asm "github.com/twitchyliquid64/golang-asm"
	"github.com/twitchyliquid64/golang-asm/obj"
	"github.com/twitchyliquid64/golang-asm/obj/x86"
	"golang.org/x/sys/unix"
)

// amd64 backend for the baseline JIT. The arch-independent run/cache/eligibility
// logic is in jit_shared.go; this file is the code generator.
//
// Each numeric op has an int fast path (inline) and a double (SSE) fast path (out
// of line) that also promotes a mixed int operand to double (CVTSI2SD, matching
// the interpreter's asNumeric); anything else — a non-number via `any`, a NaN
// result, float ÷0/% — deopts to the interpreter. Codegen is golang-asm; only the
// trampoline (jit_amd64.s) is hand asm. See the README "Baseline JIT" section.

// jitArchDefault is the default-on state for this arch (amd64 is runtime-verified).
const jitArchDefault = true

// register aliases used by the emitter.
const (
	rAX = x86.REG_AX
	rCX = x86.REG_CX
	rDX = x86.REG_DX
	rBX = x86.REG_BX
	rR8 = x86.REG_R8 // &stack[base]
	rR9 = x86.REG_R9 // qnanMask
	rX0 = x86.REG_X0
	rX1 = x86.REG_X1
)

// compileJIT emits native code for the whole chunk, or returns nil if it is not
// JIT-eligible.
func compileJIT(chunk *Chunk) *compiledJIT {
	entry, maxDepth, ok := depths(chunk)
	if !ok {
		return nil
	}
	code := chunk.Code
	lc := int64(chunk.LocalCount)

	b, err := asm.NewBuilder("amd64", len(code)*16+64)
	if err != nil {
		return nil
	}

	// Low-level emit helpers (operand order is Plan9: From, To).
	add := b.AddInstruction
	np := b.NewProg
	rr := func(as obj.As, from, to int16) {
		p := np()
		p.As = as
		p.From.Type = obj.TYPE_REG
		p.From.Reg = from
		p.To.Type = obj.TYPE_REG
		p.To.Reg = to
		add(p)
	}
	ri := func(as obj.As, imm int64, to int16) {
		p := np()
		p.As = as
		p.From.Type = obj.TYPE_CONST
		p.From.Offset = imm
		p.To.Type = obj.TYPE_REG
		p.To.Reg = to
		add(p)
	}
	// cmpImm emits CMPQ reg, $imm. CMP is the one op whose immediate goes in the
	// To slot (Go asm rejects "CMPQ $imm, reg"); flag sense stays reg ? imm.
	cmpImm := func(reg int16, imm int64) {
		p := np()
		p.As = x86.ACMPQ
		p.From.Type = obj.TYPE_REG
		p.From.Reg = reg
		p.To.Type = obj.TYPE_CONST
		p.To.Offset = imm
		add(p)
	}
	ld := func(to int16, base int16, off int64) {
		p := np()
		p.As = x86.AMOVQ
		p.From.Type = obj.TYPE_MEM
		p.From.Reg = base
		p.From.Offset = off
		p.To.Type = obj.TYPE_REG
		p.To.Reg = to
		add(p)
	}
	st := func(base int16, off int64, from int16) {
		p := np()
		p.As = x86.AMOVQ
		p.From.Type = obj.TYPE_REG
		p.From.Reg = from
		p.To.Type = obj.TYPE_MEM
		p.To.Reg = base
		p.To.Offset = off
		add(p)
	}
	dst1 := func(as obj.As, to int16) { p := np(); p.As = as; p.To.Type = obj.TYPE_REG; p.To.Reg = to; add(p) } // SETcc
	src1 := func(as obj.As, from int16) {
		p := np()
		p.As = as
		p.From.Type = obj.TYPE_REG
		p.From.Reg = from
		add(p)
	} // IDIVQ
	nul := func(as obj.As) { p := np(); p.As = as; add(p) } // CQO, RET
	incMem := func(base int16, off int64) {
		p := np()
		p.As = x86.AINCQ
		p.To.Type = obj.TYPE_MEM
		p.To.Reg = base
		p.To.Offset = off
		add(p)
	}
	br := func(as obj.As, target *obj.Prog) {
		p := np()
		p.As = as
		p.To.Type = obj.TYPE_BRANCH
		p.To.Val = target
		add(p)
	}

	// Pre-create a label anchor (ANOP, zero bytes) per ip so branches can target
	// forward ips directly by pointer. Branch targets are always real instruction
	// starts (loop headers, returns), never the interior of a fused op.
	label := make([]*obj.Prog, len(code))
	for ip := range label {
		label[ip] = np()
		label[ip].As = obj.ANOP
	}

	// Deopt / cancel stubs: lazily created anchors, bodies emitted after the body.
	type stubInfo struct {
		p     *obj.Prog
		ip    int
		depth int
	}
	deoptAnchor := map[int]*obj.Prog{}
	cancelAnchor := map[int]*obj.Prog{}
	var deopts, cancels []stubInfo
	deoptFor := func(ip int) *obj.Prog {
		if p, ok := deoptAnchor[ip]; ok {
			return p
		}
		p := np()
		p.As = obj.ANOP
		deoptAnchor[ip] = p
		deopts = append(deopts, stubInfo{p, ip, entry[ip]})
		return p
	}
	cancelFor := func(ip int) *obj.Prog {
		if p, ok := cancelAnchor[ip]; ok {
			return p
		}
		p := np()
		p.As = obj.ANOP
		cancelAnchor[ip] = p
		cancels = append(cancels, stubInfo{p, ip, entry[ip]})
		return p
	}

	opOff := func(d int) int64 { return 8 * (lc + int64(d)) }
	slotOff := func(slot int32) int64 { return 8 * int64(slot) }

	// numeric building blocks --------------------------------------------------
	guardIntElse := func(reg int16, floatP *obj.Prog) {
		rr(x86.AMOVQ, reg, rDX)
		ri(x86.ASHRQ, 48, rDX)
		cmpImm(rDX, jitIntHi16)
		br(x86.AJNE, floatP)
	}
	decodeInt := func(reg int16) { ri(x86.ASHLQ, 16, reg); ri(x86.ASARQ, 16, reg) }
	boxInt := func(reg int16) {
		ri(x86.ASHLQ, 16, reg)
		ri(x86.ASHRQ, 16, reg)
		ri(x86.AMOVQ, jitIntHeader, rDX)
		rr(x86.AORQ, rDX, reg)
	}
	// toDoubleXmm loads reg into the SSE register xmm as a float64, promoting a
	// tagged int operand to double (CVTSI2SD) — the interpreter's asNumeric
	// promotion (e.g. `px * 0.0125` with px:int). A normal double is reinterpreted
	// as-is; anything else (a non-number via `any`) deopts. reg is clobbered when
	// it holds an int (decoded in place); callers no longer need it afterward.
	toDoubleXmm := func(reg, xmm int16, ip int) {
		isDouble := np()
		isDouble.As = obj.ANOP
		done := np()
		done.As = obj.ANOP
		rr(x86.AMOVQ, reg, rDX)
		rr(x86.AANDQ, rR9, rDX)
		rr(x86.ACMPQ, rR9, rDX)
		br(x86.AJNE, isDouble) // (v & qnan)!=qnan ⇒ normal double
		// tagged: must be int (else a non-number → deopt).
		rr(x86.AMOVQ, reg, rDX)
		ri(x86.ASHRQ, 48, rDX)
		cmpImm(rDX, jitIntHi16)
		br(x86.AJNE, deoptFor(ip))
		decodeInt(reg)              // sign-extend the 48-bit int payload
		rr(x86.ACVTSQ2SD, reg, xmm) // int64 → float64
		br(obj.AJMP, done)
		add(isDouble)
		rr(x86.AMOVQ, reg, xmm) // reinterpret the double's raw bits
		add(done)
	}
	boxBool := func(setcc obj.As) {
		dst1(setcc, rDX)
		rr(x86.AMOVBQZX, rDX, rAX)
		ri(x86.AMOVQ, jitFalseBits, rCX)
		rr(x86.AORQ, rCX, rAX)
	}

	intSetCC := func(sub OpCode) obj.As {
		switch sub {
		case OpLess:
			return x86.ASETLT
		case OpLessEqual:
			return x86.ASETLE
		case OpGreater:
			return x86.ASETGT
		case OpGreaterEqual:
			return x86.ASETGE
		case OpEqual:
			return x86.ASETEQ
		default:
			return x86.ASETNE
		}
	}
	floatSetCC := func(sub OpCode) obj.As {
		switch sub {
		case OpLess:
			return x86.ASETCS // below
		case OpLessEqual:
			return x86.ASETLS // below-or-equal
		case OpGreater:
			return x86.ASETHI // above
		case OpGreaterEqual:
			return x86.ASETCC // above-or-equal
		case OpEqual:
			return x86.ASETEQ
		default:
			return x86.ASETNE
		}
	}
	floatArithAs := func(sub OpCode) obj.As {
		switch sub {
		case OpAdd:
			return x86.AADDSD
		case OpSub:
			return x86.ASUBSD
		case OpMul:
			return x86.AMULSD
		default:
			return x86.ADIVSD
		}
	}
	isCmp := func(sub OpCode) bool {
		switch sub {
		case OpLess, OpLessEqual, OpGreater, OpGreaterEqual, OpEqual, OpNotEqual:
			return true
		}
		return false
	}

	// emitNumeric takes raw left in AX, raw right in CX. The int path is inline
	// and falls through to the next instruction; the float path is out-of-line
	// (cold) so it doesn't bloat the loop — on an int-guard miss it jumps to the
	// cold stub, which handles doubles or deopts, then jumps back to the next op.
	var floatStubs []func()
	emitNumeric := func(sub OpCode, ip int, store func(reg int16)) {
		fstub := np()
		fstub.As = obj.ANOP
		cont := label[ip+1] // numeric ops are never the terminator, so ip+1 exists

		guardIntElse(rAX, fstub)
		guardIntElse(rCX, fstub)
		// int fast path (AX, CX decoded in place); falls through to cont.
		decodeInt(rAX)
		decodeInt(rCX)
		switch {
		case sub == OpAdd:
			rr(x86.AADDQ, rCX, rAX)
			boxInt(rAX)
		case sub == OpSub:
			rr(x86.ASUBQ, rCX, rAX)
			boxInt(rAX)
		case sub == OpMul:
			rr(x86.AIMULQ, rCX, rAX)
			boxInt(rAX)
		case sub == OpDiv:
			cmpImm(rCX, 0)
			br(x86.AJEQ, deoptFor(ip))
			nul(x86.ACQO)
			src1(x86.AIDIVQ, rCX)
			boxInt(rAX)
		case sub == OpMod:
			cmpImm(rCX, 0)
			br(x86.AJEQ, deoptFor(ip))
			nul(x86.ACQO)
			src1(x86.AIDIVQ, rCX)
			rr(x86.AMOVQ, rDX, rAX)
			boxInt(rAX)
		default: // comparison
			rr(x86.ACMPQ, rAX, rCX) // From=left To=right ⇒ left < right via SETLT
			boxBool(intSetCC(sub))
		}
		store(rAX)

		// Cold float path (emitted after the body). AX, CX still hold raw bits.
		floatStubs = append(floatStubs, func() {
			add(fstub)
			if sub == OpMod {
				br(obj.AJMP, deoptFor(ip)) // float % is a runtime error in the interpreter
				return
			}
			// Promote each operand to a double in X0/X1 (int → CVTSI2SD), or deopt
			// on a non-number. This handles mixed int/float operands (e.g.
			// px*0.0125 with px:int) the interpreter's asNumeric also promotes.
			toDoubleXmm(rAX, rX0, ip)
			toDoubleXmm(rCX, rX1, ip)
			if isCmp(sub) {
				rr(x86.AUCOMISD, rX1, rX0) // below ⇒ X0 < X1 ⇒ left < right
				boxBool(floatSetCC(sub))
			} else {
				if sub == OpDiv { // ±0.0 divisor ⇒ float division-by-zero error → deopt
					rr(x86.AMOVQ, rX1, rDX) // promoted divisor bits
					rr(x86.AADDQ, rDX, rDX) // raw<<1; ±0.0 → 0
					cmpImm(rDX, 0)
					br(x86.AJEQ, deoptFor(ip))
				}
				rr(floatArithAs(sub), rX1, rX0)
				rr(x86.AUCOMISD, rX0, rX0) // NaN result → deopt (interp canonicalizes NaN)
				br(x86.AJPS, deoptFor(ip))
				rr(x86.AMOVQ, rX0, rAX)
			}
			store(rAX)
			br(obj.AJMP, cont)
		})
	}

	emitPoll := func(target int) {
		incMem(rBX, offCancelN)
		ld(rAX, rBX, offCancelN)
		ri(x86.AANDQ, 255, rAX)
		br(x86.AJEQ, cancelFor(target)) // ZF ⇒ (cancelN & 255)==0
	}
	storeFalseBranch := func(target int) func(int16) {
		return func(reg int16) {
			ri(x86.AMOVQ, jitFalseBits, rDX)
			rr(x86.ACMPQ, rDX, reg)
			br(x86.AJEQ, label[target]) // reg == False ⇒ condition false ⇒ take exit jump
		}
	}

	// Prologue: BX=ctx, R8=&stack[base], R9=qnanMask.
	rr(x86.AMOVQ, x86.REG_DI, rBX)
	ld(rR8, rBX, offStackData)
	ld(rAX, rBX, offBase)
	ri(x86.ASHLQ, 3, rAX)
	rr(x86.AADDQ, rAX, rR8)
	ri(x86.AMOVQ, jitQNaNMask, rR9)

	// Re-entry dispatch: jump to the back-edge target named by ctx.resumeIP. On
	// the first entry resumeIP is 0 and falls through to ip 0.
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
		ld(rAX, rBX, offResumeIP)
		for _, t := range targets {
			cmpImm(rAX, int64(t))
			br(x86.AJEQ, label[t])
		}
	}

	for ip := 0; ip < len(code); ip++ {
		add(label[ip])
		ins := code[ip]
		d := entry[ip]
		switch ins.Op {
		case OpNop:

		case OpLoadConst:
			ri(x86.AMOVQ, int64(uint64(chunk.Consts[ins.A])), rAX)
			st(rR8, opOff(d), rAX)

		case OpGetLocal:
			ld(rAX, rR8, slotOff(ins.A))
			st(rR8, opOff(d), rAX)

		case OpSetLocal:
			ld(rAX, rR8, opOff(d-1))
			st(rR8, slotOff(ins.A), rAX)

		case OpAdd, OpSub, OpMul, OpDiv, OpMod,
			OpEqual, OpNotEqual, OpLess, OpLessEqual, OpGreater, OpGreaterEqual:
			ld(rAX, rR8, opOff(d-2))
			ld(rCX, rR8, opOff(d-1))
			emitNumeric(ins.Op, ip, func(r int16) { st(rR8, opOff(d-2), r) })

		case OpBinLC:
			sub := OpCode(uint32(ins.B) >> 24 & 0x7F)
			cbits := int64(uint64(chunk.Consts[int(ins.B&0xFFFFFF)]))
			ld(rAX, rR8, slotOff(ins.A))
			ri(x86.AMOVQ, cbits, rCX)
			dstC := ins.C
			emitNumeric(sub, ip, func(r int16) {
				if dstC > 0 {
					st(rR8, slotOff(dstC-1), r)
				} else {
					st(rR8, opOff(d), r)
				}
			})

		case OpBinLL:
			sub := OpCode(uint32(ins.B) >> 16 & 0x7FFF)
			rslot := int32(ins.B & 0xFFFF)
			ld(rAX, rR8, slotOff(ins.A))
			ld(rCX, rR8, slotOff(rslot))
			dstC := ins.C
			emitNumeric(sub, ip, func(r int16) {
				if dstC > 0 {
					st(rR8, slotOff(dstC-1), r)
				} else {
					st(rR8, opOff(d), r)
				}
			})

		case OpCmpLC:
			sub := OpCode(uint32(ins.B) >> 24)
			cbits := int64(uint64(chunk.Consts[int(uint32(ins.B)&0xFFFFFF)]))
			target := int(code[ip+1].A)
			ld(rAX, rR8, slotOff(ins.A))
			ri(x86.AMOVQ, cbits, rCX)
			emitNumeric(sub, ip, storeFalseBranch(target))

		case OpPop:
			// No code: the static-depth model abandons the dropped slot (the next
			// push overwrites it); nanbox Values hold no Go pointer, so nothing to
			// clear. depths() already decremented the depth for this ip.

		case OpJumpFalsePeek:
			// `and` short-circuit: peek the top (depth unchanged) and, if false,
			// jump forward leaving it as the expression result. Mirrors the
			// OpJumpFalse guard+compare but without the pop and forward-only (no
			// back-edge poll — depths() rejects backward peek-jumps).
			ld(rAX, rR8, opOff(d-1))
			rr(x86.AMOVQ, rAX, rDX)
			ri(x86.ASHRQ, 48, rDX)
			cmpImm(rDX, jitBoolHi16)
			br(x86.AJNE, deoptFor(ip)) // non-bool operand → deopt (interp does .Bool())
			ri(x86.AMOVQ, jitFalseBits, rDX)
			rr(x86.ACMPQ, rDX, rAX)
			br(x86.AJEQ, label[ins.A]) // false ⇒ short-circuit forward jump

		case OpJumpTruePeek:
			// `or` short-circuit: peek; if true, jump forward leaving it. Mirror of
			// OpJumpFalsePeek with the inverted branch sense.
			ld(rAX, rR8, opOff(d-1))
			rr(x86.AMOVQ, rAX, rDX)
			ri(x86.ASHRQ, 48, rDX)
			cmpImm(rDX, jitBoolHi16)
			br(x86.AJNE, deoptFor(ip))
			ri(x86.AMOVQ, jitFalseBits, rDX)
			rr(x86.ACMPQ, rDX, rAX)
			br(x86.AJNE, label[ins.A]) // != false ⇒ true ⇒ forward jump

		case OpJump:
			if int(ins.A) <= ip {
				emitPoll(int(ins.A))
			}
			br(obj.AJMP, label[ins.A])

		case OpJumpFalse:
			ld(rAX, rR8, opOff(d-1))
			rr(x86.AMOVQ, rAX, rDX)
			ri(x86.ASHRQ, 48, rDX)
			cmpImm(rDX, jitBoolHi16)
			br(x86.AJNE, deoptFor(ip))
			ri(x86.AMOVQ, jitFalseBits, rDX)
			rr(x86.ACMPQ, rDX, rAX)
			if int(ins.A) <= ip { // taken edge backward → poll on the false path only
				br(x86.AJNE, label[ip+1]) // true ⇒ continue
				emitPoll(int(ins.A))
				br(obj.AJMP, label[ins.A])
			} else {
				br(x86.AJEQ, label[ins.A]) // false ⇒ forward jump
			}

		case OpReturnNull:
			ri(x86.AMOVQ, jitNullBits, rAX)
			st(rBX, offRetVal, rAX)
			ri(x86.AMOVQ, jitDone, rAX)
			st(rBX, offStatus, rAX)
			nul(obj.ARET)

		case OpReturn:
			ld(rAX, rR8, opOff(d-1))
			st(rBX, offRetVal, rAX)
			ri(x86.AMOVQ, jitDone, rAX)
			st(rBX, offStatus, rAX)
			nul(obj.ARET)
		}
	}

	// Cold float fast paths (out of line so the int loop body stays tight).
	for _, fn := range floatStubs {
		fn()
	}

	// Deopt stubs: status=deopt, resume ip, live stack length, then return.
	for _, s := range deopts {
		add(s.p)
		ri(x86.AMOVQ, int64(s.ip), rAX)
		st(rBX, offResumeIP, rAX)
		ri(x86.AMOVQ, jitDeopt, rAX)
		st(rBX, offStatus, rAX)
		ld(rAX, rBX, offBase)
		ri(x86.AMOVQ, lc+int64(s.depth), rDX)
		rr(x86.AADDQ, rDX, rAX)
		st(rBX, offSP, rAX)
		nul(obj.ARET)
	}
	// Cancel stubs: status=cancelcheck, resume ip (loop header), then return.
	for _, s := range cancels {
		add(s.p)
		ri(x86.AMOVQ, int64(s.ip), rAX)
		st(rBX, offResumeIP, rAX)
		ri(x86.AMOVQ, jitCancelCheck, rAX)
		st(rBX, offStatus, rAX)
		nul(obj.ARET)
	}

	buf := b.Assemble()
	if len(buf) == 0 {
		return nil
	}
	// W^X: map writable, copy, then flip to read+execute so the page is never
	// simultaneously writable and executable (required under strict W^X kernels).
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
	return &compiledJIT{code: mem, entry: &mem[0], maxDepth: maxDepth}
}
