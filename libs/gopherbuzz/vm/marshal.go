package vm

import (
	"context"
	"fmt"
	"math"

	"github.com/egladman/magus/libs/gopherbuzz/ast"
)

// BytecodeVersion is the current bytecode format version; UnmarshalChunk rejects
// blobs with a different version. Increment it whenever any of the following
// change: opcode numbering (opcode.go), Chunk/Instr/UpvalInfo layout (chunk.go),
// the serializable Value constant subset, or AST node types (ast.go) — the decoder
// rejects mismatched blobs rather than mis-executing stale bytecode.
//
// v2 adds the per-chunk Exports list, so a session-compiled (SharedGlobals)
// module recovered from bytecode re-declares its exported names — without it a
// bytecode-loaded spell module would expose none of its mgs_ contract.
//
// v3 adds the OpBinLC superinstruction (FusePeephole). A v2 binary would
// hit it as an unknown opcode, so the version guard must reject v3 blobs.
//
// v4 adds OpCheckType (compiler-inserted any→typed-slot assertions). Same
// reasoning: an older binary lacks the opcode, so reject v4 blobs.
//
// v5 adds OpGetField/OpSetField (inline-cached this.field access). Same reasoning.
//
// v6 encodes the compiler's static int-type proof in OpGetLocal.B and in bit 31
// of OpBinLC/OpBinLL.B. An older VM reads a bit-31-set B as
// sub-opcode 0x80|op, which no case matches — it falls to applyBinop (correct
// but slow). Version guard prevents a stale VM from taking the implicit speed hit.
//
// v7 adds Instr.C (4 bytes, compile-time destination register). An older VM would
// read C's bytes as the next instruction's Op/A fields, silently mis-executing.
const BytecodeVersion uint16 = 9

var (
	// bcMagic prefixes the bytecode (.bo) blob; bdbMagic the debug-info (.bdb)
	// blob. Both come from Marshal (the .bdb via DebugOnly) but are persisted
	// and loaded independently, mirroring upstream Buzz's bzzc split: the .bo
	// executes standalone, and a matching .bdb folds source positions back on.
	bcMagic  = [4]byte{'B', 'Z', 'B', 'C'}
	bdbMagic = [4]byte{'B', 'Z', 'D', 'B'}
)

// MarshalOption configures what Chunk.Marshal emits.
type MarshalOption func(*marshalConfig)

type marshalConfig struct {
	debugOnly bool
}

// DebugOnly makes Marshal emit the debug-info (.bdb) blob — per-chunk source
// lines and the local / upvalue name tables — instead of the executable
// bytecode (.bo). Pair the result with AttachDebug to refold source positions
// onto a chunk recovered by UnmarshalChunk.
func DebugOnly() MarshalOption {
	return func(c *marshalConfig) { c.debugOnly = true }
}

// Marshal serializes c into a portable binary blob. By default it emits the
// executable .bo bytecode (instructions, constants, nested functions), which
// UnmarshalChunk recreates into a runnable Chunk without re-parsing or
// re-compiling. With DebugOnly it instead emits the separate .bdb debug blob;
// the two are produced together but persisted and loaded independently.
func (c *Chunk) Marshal(opts ...MarshalOption) ([]byte, error) {
	var cfg marshalConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	e := &enc{}
	if cfg.debugOnly {
		e.header(bdbMagic)
		e.debugChunk(c)
		return e.buf, nil
	}
	e.header(bcMagic)
	if err := e.chunk(c); err != nil {
		return nil, err
	}
	return e.buf, nil
}

// UnmarshalChunk deserializes a Chunk produced by Chunk.Marshal. It returns
// an error if the magic header or version do not match the current build. The
// recovered Chunk carries no debug info; use AttachDebug to fold in a .bdb.
func UnmarshalChunk(data []byte) (*Chunk, error) {
	d := &dec{buf: data}
	if err := d.header(bcMagic); err != nil {
		return nil, fmt.Errorf("buzz: unmarshal: %w", err)
	}
	c, err := d.chunk()
	if err != nil {
		return nil, fmt.Errorf("buzz: unmarshal: %w", err)
	}
	return c, nil
}

// AttachDebug folds a .bdb blob (Marshal with DebugOnly) back onto c, restoring
// source lines and name tables across the whole function tree. The blob must
// come from the same chunk's Marshal pair: AttachDebug walks c's funs in the
// identical order the debug blob was written and errors if the shapes diverge
// (or leave trailing bytes), rather than silently mismatching line numbers.
func (c *Chunk) AttachDebug(data []byte) error {
	d := &dec{buf: data}
	if err := d.header(bdbMagic); err != nil {
		return fmt.Errorf("buzz: attach debug: %w", err)
	}
	if err := d.attachDebug(c); err != nil {
		return fmt.Errorf("buzz: attach debug: %w", err)
	}
	if d.off != len(d.buf) {
		return fmt.Errorf("buzz: attach debug: %d trailing bytes (chunk/.bdb shape mismatch)", len(d.buf)-d.off)
	}
	return nil
}

// ExecBytecode deserializes a Chunk from data and runs it in env using a fresh VM.
// This is the low-level counterpart; Session.ExecBytecode in the buzz package is
// the normal host-facing entry point.
func ExecBytecode(ctx context.Context, data []byte, env *Env) error {
	c, err := UnmarshalChunk(data)
	if err != nil {
		return err
	}
	vm := NewVM(ctx)
	_, err = vm.Run(c, env)
	return err
}

// --- encoder ---

type enc struct{ buf []byte }

func (e *enc) header(magic [4]byte) {
	e.raw(magic[:])
	e.u16(BytecodeVersion)
}

func (e *enc) raw(b []byte) { e.buf = append(e.buf, b...) }

func (e *enc) u8(b uint8) { e.buf = append(e.buf, b) }

func (e *enc) u16(n uint16) {
	e.buf = append(e.buf, byte(n), byte(n>>8))
}

func (e *enc) u32(n uint32) {
	e.buf = append(e.buf, byte(n), byte(n>>8), byte(n>>16), byte(n>>24))
}

func (e *enc) u64(n uint64) {
	e.buf = append(e.buf,
		byte(n), byte(n>>8), byte(n>>16), byte(n>>24),
		byte(n>>32), byte(n>>40), byte(n>>48), byte(n>>56))
}

func (e *enc) i32(n int32) { e.u32(uint32(n)) }

func (e *enc) boolean(b bool) {
	if b {
		e.u8(1)
	} else {
		e.u8(0)
	}
}

func (e *enc) str(s string) {
	e.u32(uint32(len(s)))
	e.raw([]byte(s))
}

func (e *enc) strs(ss []string) {
	e.u32(uint32(len(ss)))
	for _, s := range ss {
		e.str(s)
	}
}

func (e *enc) pos(p ast.Pos) {
	e.i32(int32(p.Line))
	e.i32(int32(p.Col))
}

func (e *enc) chunk(c *Chunk) error {
	e.str(c.Name)
	e.strs(c.Params)
	e.i32(int32(c.LocalCount))
	e.u32(uint32(len(c.UpvalInfos)))
	for _, u := range c.UpvalInfos {
		e.boolean(u.IsLocal)
		e.i32(u.Index)
	}
	e.u32(uint32(len(c.Code)))
	for _, ins := range c.Code {
		e.u8(uint8(ins.Op))
		e.i32(ins.A)
		e.i32(ins.B)
		e.i32(ins.C)
	}
	e.u32(uint32(len(c.Consts)))
	for _, v := range c.Consts {
		if err := e.constVal(v); err != nil {
			return err
		}
	}
	e.u32(uint32(len(c.Funs)))
	for _, f := range c.Funs {
		if err := e.chunk(f); err != nil {
			return err
		}
	}
	// Exports: the names a SharedGlobals module declared `export`. ExecChunk
	// reads these to repopulate the session's exported set; empty for nested and
	// non-shared chunks.
	e.strs(c.Exports)
	return nil
}

// debugChunk writes c's debug record (a presence flag, then source lines and
// name tables when set) followed by its funs' records in pre-order — the same
// traversal dec.attachDebug replays to reattach them.
func (e *enc) debugChunk(c *Chunk) {
	e.boolean(c.Lines != nil)
	if c.Lines != nil {
		e.u32(uint32(len(c.Lines)))
		for _, l := range c.Lines {
			e.i32(l)
		}
		e.strs(c.LocalNames)
		e.strs(c.UpvalNames)
	}
	for _, f := range c.Funs {
		e.debugChunk(f)
	}
}

// const-value discriminant tags (separate namespace from AST node tags below).
const (
	constTagNull    = 0
	constTagBool    = 1
	constTagInt     = 2
	constTagFloat   = 3
	constTagStr     = 4
	constTagEnumDef = 5
	constTagObjDecl = 6
	constTagPat     = 7
)

func (e *enc) constVal(v Value) error {
	switch v.tag() {
	case tagNull:
		e.u8(constTagNull)
	case tagBool:
		e.u8(constTagBool)
		e.boolean(v.AsBool())
	case tagInt:
		e.u8(constTagInt)
		e.u64(v.num())
	case tagFloat:
		e.u8(constTagFloat)
		e.u64(v.num())
	case tagStr:
		e.u8(constTagStr)
		e.str(v.asStr().V)
	case tagEnumDef:
		ed := v.asEnumDef()
		e.u8(constTagEnumDef)
		e.str(ed.Name)
		e.strs(ed.Cases)
	case tagObjDecl:
		e.u8(constTagObjDecl)
		return e.node(v.asObjDecl())
	case tagPat:
		e.u8(constTagPat)
		e.str(v.asPat().src)
	default:
		return fmt.Errorf("buzz: marshal: cannot serialize constant of kind %s", v.buzzKind())
	}
	return nil
}

// AST node discriminant tags.
const (
	nodeNil          = 0
	nodeIntLit       = 1
	nodeFloatLit     = 2
	nodeBoolLit      = 3
	nodeNullLit      = 4
	nodeStringLit    = 5
	nodeIdentExpr    = 6
	nodeBinaryExpr   = 7
	nodeUnaryExpr    = 8
	nodeCallExpr     = 9
	nodeMemberExpr   = 10
	nodeIndexExpr    = 11
	nodeFunExpr      = 12
	nodeMapExpr      = 13
	nodeListExpr     = 14
	nodeObjectLit    = 15
	nodeInterpExpr   = 16
	nodeRangeExpr    = 17
	nodeIsExpr       = 18
	nodeAsExpr       = 19
	nodeBlockStmt    = 20
	nodeImportStmt   = 21
	nodeDeclStmt     = 22
	nodeAssignStmt   = 23
	nodeReturnStmt   = 24
	nodeExprStmt     = 25
	nodeIfStmt       = 26
	nodeWhileStmt    = 27
	nodeForStmt      = 28
	nodeForEachStmt  = 29
	nodeBreakStmt    = 30
	nodeContinueStmt = 31
	nodeFunDecl      = 32
	nodeObjectDecl   = 33
	nodeEnumDecl     = 34
	nodeDoStmt       = 35
	nodeTryStmt      = 36
	nodeThrowStmt    = 37
	nodeYieldExpr    = 38
	nodeFiberExpr    = 39
	nodeResumeExpr   = 40
	nodeResolveExpr  = 41
	nodeCatchExpr    = 42
)

func (e *enc) node(n ast.Node) error {
	if n == nil {
		e.u8(nodeNil)
		return nil
	}
	p := ast.NodePos(n)
	switch v := n.(type) {
	case *ast.IntLit:
		e.u8(nodeIntLit)
		e.pos(p)
		e.u64(uint64(v.Val))
	case *ast.FloatLit:
		e.u8(nodeFloatLit)
		e.pos(p)
		e.u64(math.Float64bits(v.Val))
	case *ast.BoolLit:
		e.u8(nodeBoolLit)
		e.pos(p)
		e.boolean(v.Val)
	case *ast.NullLit:
		e.u8(nodeNullLit)
		e.pos(p)
	case *ast.StringLit:
		e.u8(nodeStringLit)
		e.pos(p)
		e.str(v.Val)
	case *ast.IdentExpr:
		e.u8(nodeIdentExpr)
		e.pos(p)
		e.str(v.Name)
	case *ast.BinaryExpr:
		e.u8(nodeBinaryExpr)
		e.pos(p)
		e.str(v.Op)
		if err := e.node(v.Left); err != nil {
			return err
		}
		return e.node(v.Right)
	case *ast.UnaryExpr:
		e.u8(nodeUnaryExpr)
		e.pos(p)
		e.str(v.Op)
		return e.node(v.Operand)
	case *ast.CallExpr:
		e.u8(nodeCallExpr)
		e.pos(p)
		if err := e.node(v.Callee); err != nil {
			return err
		}
		e.u32(uint32(len(v.Args)))
		for _, a := range v.Args {
			if err := e.node(a); err != nil {
				return err
			}
		}
	case *ast.MemberExpr:
		e.u8(nodeMemberExpr)
		e.pos(p)
		e.str(v.Name)
		return e.node(v.Object)
	case *ast.IndexExpr:
		e.u8(nodeIndexExpr)
		e.pos(p)
		if err := e.node(v.Object); err != nil {
			return err
		}
		return e.node(v.Index)
	case *ast.FunExpr:
		e.u8(nodeFunExpr)
		e.pos(p)
		e.strs(v.Params)
		e.strs(v.ParamAnnots)
		e.str(v.RetAnnot)
		e.str(v.YieldAnnot)
		return e.node(v.Body)
	case *ast.MapExpr:
		e.u8(nodeMapExpr)
		e.pos(p)
		e.u32(uint32(len(v.Keys)))
		for i := range v.Keys {
			if err := e.node(v.Keys[i]); err != nil {
				return err
			}
			if err := e.node(v.Values[i]); err != nil {
				return err
			}
		}
	case *ast.ListExpr:
		e.u8(nodeListExpr)
		e.pos(p)
		e.u32(uint32(len(v.Items)))
		for _, item := range v.Items {
			if err := e.node(item); err != nil {
				return err
			}
		}
	case *ast.ObjectLit:
		e.u8(nodeObjectLit)
		e.pos(p)
		e.str(v.TypeName)
		e.strs(v.Keys)
		e.u32(uint32(len(v.Values)))
		for _, val := range v.Values {
			if err := e.node(val); err != nil {
				return err
			}
		}
	case *ast.InterpExpr:
		e.u8(nodeInterpExpr)
		e.pos(p)
		e.u32(uint32(len(v.Parts)))
		for _, part := range v.Parts {
			e.str(part.Lit)
			if err := e.node(part.Expr); err != nil {
				return err
			}
		}
	case *ast.RangeExpr:
		e.u8(nodeRangeExpr)
		e.pos(p)
		if err := e.node(v.Lo); err != nil {
			return err
		}
		return e.node(v.Hi)
	case *ast.IsExpr:
		e.u8(nodeIsExpr)
		e.pos(p)
		e.str(v.TypeName)
		return e.node(v.Expr)
	case *ast.AsExpr:
		e.u8(nodeAsExpr)
		e.pos(p)
		e.str(v.TypeName)
		return e.node(v.Expr)
	case *ast.BlockStmt:
		e.u8(nodeBlockStmt)
		e.pos(p)
		e.u32(uint32(len(v.Stmts)))
		for _, s := range v.Stmts {
			if err := e.node(s); err != nil {
				return err
			}
		}
	case *ast.ImportStmt:
		e.u8(nodeImportStmt)
		e.pos(p)
		e.str(v.Path)
	case *ast.DeclStmt:
		e.u8(nodeDeclStmt)
		e.pos(p)
		e.boolean(v.IsConst)
		e.str(v.Name)
		e.str(v.TypeAnnot)
		return e.node(v.Value)
	case *ast.AssignStmt:
		e.u8(nodeAssignStmt)
		e.pos(p)
		if err := e.node(v.Target); err != nil {
			return err
		}
		return e.node(v.Value)
	case *ast.ReturnStmt:
		e.u8(nodeReturnStmt)
		e.pos(p)
		return e.node(v.Value)
	case *ast.ExprStmt:
		e.u8(nodeExprStmt)
		e.pos(p)
		return e.node(v.Expr)
	case *ast.IfStmt:
		e.u8(nodeIfStmt)
		e.pos(p)
		if err := e.node(v.Cond); err != nil {
			return err
		}
		if err := e.node(v.Then); err != nil {
			return err
		}
		return e.node(v.Else)
	case *ast.WhileStmt:
		e.u8(nodeWhileStmt)
		e.pos(p)
		if err := e.node(v.Cond); err != nil {
			return err
		}
		return e.node(v.Body)
	case *ast.ForStmt:
		e.u8(nodeForStmt)
		e.pos(p)
		if err := e.node(v.Init); err != nil {
			return err
		}
		if err := e.node(v.Cond); err != nil {
			return err
		}
		if err := e.node(v.Post); err != nil {
			return err
		}
		return e.node(v.Body)
	case *ast.ForEachStmt:
		e.u8(nodeForEachStmt)
		e.pos(p)
		e.str(v.KeyName)
		e.str(v.ValName)
		if err := e.node(v.Iter); err != nil {
			return err
		}
		return e.node(v.Body)
	case *ast.BreakStmt:
		e.u8(nodeBreakStmt)
		e.pos(p)
	case *ast.ContinueStmt:
		e.u8(nodeContinueStmt)
		e.pos(p)
	case *ast.FunDecl:
		e.u8(nodeFunDecl)
		e.pos(p)
		e.str(v.Name)
		e.strs(v.Params)
		e.strs(v.ParamAnnots)
		e.str(v.RetAnnot)
		e.str(v.YieldAnnot)
		e.boolean(v.IsStatic)
		return e.node(v.Body)
	case *ast.ObjectDecl:
		e.u8(nodeObjectDecl)
		e.pos(p)
		e.str(v.Name)
		e.u32(uint32(len(v.Fields)))
		for _, f := range v.Fields {
			e.str(f.Name)
			e.str(f.TypeAnnot)
			if err := e.node(f.Default); err != nil {
				return err
			}
		}
		e.u32(uint32(len(v.Methods)))
		for _, m := range v.Methods {
			if err := e.node(m); err != nil {
				return err
			}
		}
	case *ast.EnumDecl:
		e.u8(nodeEnumDecl)
		e.pos(p)
		e.str(v.Name)
		e.strs(v.Cases)
	case *ast.DoStmt:
		e.u8(nodeDoStmt)
		e.pos(p)
		if err := e.node(v.Body); err != nil {
			return err
		}
		return e.node(v.Cond)
	case *ast.TryStmt:
		e.u8(nodeTryStmt)
		e.pos(p)
		if err := e.node(v.Body); err != nil {
			return err
		}
		e.str(v.ErrName)
		return e.node(v.Catch)
	case *ast.ThrowStmt:
		e.u8(nodeThrowStmt)
		e.pos(p)
		return e.node(v.Value)
	case *ast.CatchExpr:
		e.u8(nodeCatchExpr)
		e.pos(p)
		if err := e.node(v.Expr); err != nil {
			return err
		}
		return e.node(v.Default)
	case *ast.YieldExpr:
		e.u8(nodeYieldExpr)
		e.pos(p)
		return e.node(v.Value)
	case *ast.FiberExpr:
		e.u8(nodeFiberExpr)
		e.pos(p)
		return e.node(v.Call)
	case *ast.ResumeExpr:
		e.u8(nodeResumeExpr)
		e.pos(p)
		return e.node(v.Fiber)
	case *ast.ResolveExpr:
		e.u8(nodeResolveExpr)
		e.pos(p)
		return e.node(v.Fiber)
	default:
		return fmt.Errorf("buzz: marshal: unknown AST node type %T", n)
	}
	return nil
}

// --- decoder ---

type dec struct {
	buf []byte
	off int
}

func (d *dec) raw(n int) ([]byte, error) {
	if n < 0 || n > len(d.buf)-d.off {
		return nil, fmt.Errorf("unexpected end of data at offset %d", d.off)
	}
	b := d.buf[d.off : d.off+n]
	d.off += n
	return b, nil
}

// header reads and validates the 4-byte magic and version prefix shared by the
// .bo and .bdb blobs; callers wrap the error with their own context.
func (d *dec) header(magic [4]byte) error {
	m, err := d.raw(4)
	if err != nil {
		return err
	}
	if [4]byte(m) != magic {
		return fmt.Errorf("invalid magic")
	}
	ver, err := d.u16()
	if err != nil {
		return err
	}
	if ver != BytecodeVersion {
		return fmt.Errorf("version mismatch: got %d, want %d", ver, BytecodeVersion)
	}
	return nil
}

// checkCount guards make([]T, n) against corrupt or hostile input: every
// encodable element costs at least 1 byte, so n > remaining bytes is
// provably impossible for valid data and indicates a corrupt blob.
func (d *dec) checkCount(n uint32) error {
	if int64(n) > int64(len(d.buf)-d.off) {
		return fmt.Errorf("count %d exceeds remaining %d bytes", n, len(d.buf)-d.off)
	}
	return nil
}

func (d *dec) u8() (uint8, error) {
	b, err := d.raw(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (d *dec) u16() (uint16, error) {
	b, err := d.raw(2)
	if err != nil {
		return 0, err
	}
	return uint16(b[0]) | uint16(b[1])<<8, nil
}

func (d *dec) u32() (uint32, error) {
	b, err := d.raw(4)
	if err != nil {
		return 0, err
	}
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24, nil
}

func (d *dec) u64() (uint64, error) {
	b, err := d.raw(8)
	if err != nil {
		return 0, err
	}
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56, nil
}

func (d *dec) i32() (int32, error) {
	n, err := d.u32()
	return int32(n), err
}

func (d *dec) boolean() (bool, error) {
	b, err := d.u8()
	return b != 0, err
}

func (d *dec) str() (string, error) {
	n, err := d.u32()
	if err != nil {
		return "", err
	}
	b, err := d.raw(int(n))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (d *dec) strs() ([]string, error) {
	n, err := d.u32()
	if err != nil {
		return nil, err
	}
	if err := d.checkCount(n); err != nil {
		return nil, err
	}
	ss := make([]string, int(n))
	for i := range ss {
		ss[i], err = d.str()
		if err != nil {
			return nil, err
		}
	}
	return ss, nil
}

func (d *dec) chunk() (*Chunk, error) {
	c := &Chunk{}
	var err error
	if c.Name, err = d.str(); err != nil {
		return nil, err
	}
	if c.Params, err = d.strs(); err != nil {
		return nil, err
	}
	lc, err := d.i32()
	if err != nil {
		return nil, err
	}
	c.LocalCount = int(lc)

	uvCount, err := d.u32()
	if err != nil {
		return nil, err
	}
	if err := d.checkCount(uvCount); err != nil {
		return nil, err
	}
	if uvCount > 0 {
		c.UpvalInfos = make([]UpvalInfo, int(uvCount))
		for i := range c.UpvalInfos {
			if c.UpvalInfos[i].IsLocal, err = d.boolean(); err != nil {
				return nil, err
			}
			if c.UpvalInfos[i].Index, err = d.i32(); err != nil {
				return nil, err
			}
		}
	}

	codeLen, err := d.u32()
	if err != nil {
		return nil, err
	}
	if err := d.checkCount(codeLen); err != nil {
		return nil, err
	}
	if codeLen > 0 {
		c.Code = make([]Instr, int(codeLen))
		for i := range c.Code {
			op, err := d.u8()
			if err != nil {
				return nil, err
			}
			a, err := d.i32()
			if err != nil {
				return nil, err
			}
			b, err := d.i32()
			if err != nil {
				return nil, err
			}
			cv, err := d.i32()
			if err != nil {
				return nil, err
			}
			c.Code[i] = Instr{Op: OpCode(op), A: a, B: b, C: cv}
		}
	}

	constsLen, err := d.u32()
	if err != nil {
		return nil, err
	}
	if err := d.checkCount(constsLen); err != nil {
		return nil, err
	}
	if constsLen > 0 {
		c.Consts = make([]Value, int(constsLen))
		for i := range c.Consts {
			if c.Consts[i], err = d.constVal(); err != nil {
				return nil, err
			}
		}
	}

	funsLen, err := d.u32()
	if err != nil {
		return nil, err
	}
	if err := d.checkCount(funsLen); err != nil {
		return nil, err
	}
	if funsLen > 0 {
		c.Funs = make([]*Chunk, int(funsLen))
		for i := range c.Funs {
			if c.Funs[i], err = d.chunk(); err != nil {
				return nil, err
			}
		}
	}
	if c.Exports, err = d.strs(); err != nil {
		return nil, err
	}
	return c, nil
}

// attachDebug replays the pre-order walk enc.debugChunk wrote, reattaching each
// chunk's lines and name tables onto the already-decoded tree rooted at c.
func (d *dec) attachDebug(c *Chunk) error {
	hasDebug, err := d.boolean()
	if err != nil {
		return err
	}
	if hasDebug {
		linesLen, err := d.u32()
		if err != nil {
			return err
		}
		if err := d.checkCount(linesLen); err != nil {
			return err
		}
		c.Lines = make([]int32, int(linesLen))
		for i := range c.Lines {
			if c.Lines[i], err = d.i32(); err != nil {
				return err
			}
		}
		if c.LocalNames, err = d.strs(); err != nil {
			return err
		}
		if c.UpvalNames, err = d.strs(); err != nil {
			return err
		}
	}
	for _, f := range c.Funs {
		if err := d.attachDebug(f); err != nil {
			return err
		}
	}
	return nil
}

func (d *dec) constVal() (Value, error) {
	tag, err := d.u8()
	if err != nil {
		return Null, err
	}
	switch tag {
	case constTagNull:
		return Null, nil
	case constTagBool:
		b, err := d.boolean()
		if err != nil {
			return Null, err
		}
		return BoolValue(b), nil
	case constTagInt:
		n, err := d.u64()
		if err != nil {
			return Null, err
		}
		return IntValue(int64(n)), nil
	case constTagFloat:
		n, err := d.u64()
		if err != nil {
			return Null, err
		}
		return FloatValue(math.Float64frombits(n)), nil
	case constTagStr:
		s, err := d.str()
		if err != nil {
			return Null, err
		}
		return StrValue(s), nil
	case constTagEnumDef:
		name, err := d.str()
		if err != nil {
			return Null, err
		}
		cases, err := d.strs()
		if err != nil {
			return Null, err
		}
		return heapValue(tagEnumDef, &enumDefObj{Name: name, Cases: cases}), nil
	case constTagObjDecl:
		n, err := d.node()
		if err != nil {
			return Null, err
		}
		decl, ok := n.(*ast.ObjectDecl)
		if !ok {
			return Null, fmt.Errorf("constVal: expected *ast.ObjectDecl, got %T", n)
		}
		return heapValue(tagObjDecl, &objDeclPayload{decl}), nil
	case constTagPat:
		s, err := d.str()
		if err != nil {
			return Null, err
		}
		return PatValue(s)
	default:
		return Null, fmt.Errorf("unknown const tag %d", tag)
	}
}

func (d *dec) pos() (ast.Pos, error) {
	line, err := d.i32()
	if err != nil {
		return ast.Pos{}, err
	}
	col, err := d.i32()
	if err != nil {
		return ast.Pos{}, err
	}
	return ast.Pos{Line: int(line), Col: int(col)}, nil
}

func (d *dec) node() (ast.Node, error) {
	tag, err := d.u8()
	if err != nil {
		return nil, err
	}
	if tag == nodeNil {
		return nil, nil
	}
	p, err := d.pos()
	if err != nil {
		return nil, err
	}
	switch tag {
	case nodeIntLit:
		n, err := d.u64()
		if err != nil {
			return nil, err
		}
		return &ast.IntLit{Pos: p, Val: int64(n)}, nil
	case nodeFloatLit:
		n, err := d.u64()
		if err != nil {
			return nil, err
		}
		return &ast.FloatLit{Pos: p, Val: math.Float64frombits(n)}, nil
	case nodeBoolLit:
		b, err := d.boolean()
		if err != nil {
			return nil, err
		}
		return &ast.BoolLit{Pos: p, Val: b}, nil
	case nodeNullLit:
		return &ast.NullLit{Pos: p}, nil
	case nodeStringLit:
		s, err := d.str()
		if err != nil {
			return nil, err
		}
		return &ast.StringLit{Pos: p, Val: s}, nil
	case nodeIdentExpr:
		name, err := d.str()
		if err != nil {
			return nil, err
		}
		return &ast.IdentExpr{Pos: p, Name: name}, nil
	case nodeBinaryExpr:
		op, err := d.str()
		if err != nil {
			return nil, err
		}
		left, err := d.node()
		if err != nil {
			return nil, err
		}
		right, err := d.node()
		if err != nil {
			return nil, err
		}
		return &ast.BinaryExpr{Pos: p, Op: op, Left: left, Right: right}, nil
	case nodeUnaryExpr:
		op, err := d.str()
		if err != nil {
			return nil, err
		}
		operand, err := d.node()
		if err != nil {
			return nil, err
		}
		return &ast.UnaryExpr{Pos: p, Op: op, Operand: operand}, nil
	case nodeCallExpr:
		callee, err := d.node()
		if err != nil {
			return nil, err
		}
		argc, err := d.u32()
		if err != nil {
			return nil, err
		}
		if err := d.checkCount(argc); err != nil {
			return nil, err
		}
		args := make([]ast.Node, int(argc))
		for i := range args {
			if args[i], err = d.node(); err != nil {
				return nil, err
			}
		}
		return &ast.CallExpr{Pos: p, Callee: callee, Args: args}, nil
	case nodeMemberExpr:
		name, err := d.str()
		if err != nil {
			return nil, err
		}
		obj, err := d.node()
		if err != nil {
			return nil, err
		}
		return &ast.MemberExpr{Pos: p, Object: obj, Name: name}, nil
	case nodeIndexExpr:
		obj, err := d.node()
		if err != nil {
			return nil, err
		}
		idx, err := d.node()
		if err != nil {
			return nil, err
		}
		return &ast.IndexExpr{Pos: p, Object: obj, Index: idx}, nil
	case nodeFunExpr:
		params, err := d.strs()
		if err != nil {
			return nil, err
		}
		paramAnnots, err := d.strs()
		if err != nil {
			return nil, err
		}
		retAnnot, err := d.str()
		if err != nil {
			return nil, err
		}
		yieldAnnot, err := d.str()
		if err != nil {
			return nil, err
		}
		body, err := d.node()
		if err != nil {
			return nil, err
		}
		blockBody, ok := body.(*ast.BlockStmt)
		if !ok {
			return nil, fmt.Errorf("FunExpr body: expected *ast.BlockStmt, got %T", body)
		}
		return &ast.FunExpr{Pos: p, Params: params, ParamAnnots: paramAnnots, RetAnnot: retAnnot, YieldAnnot: yieldAnnot, Body: blockBody}, nil
	case nodeMapExpr:
		n, err := d.u32()
		if err != nil {
			return nil, err
		}
		if err := d.checkCount(n); err != nil {
			return nil, err
		}
		keys := make([]ast.Node, int(n))
		vals := make([]ast.Node, int(n))
		for i := range keys {
			if keys[i], err = d.node(); err != nil {
				return nil, err
			}
			if vals[i], err = d.node(); err != nil {
				return nil, err
			}
		}
		return &ast.MapExpr{Pos: p, Keys: keys, Values: vals}, nil
	case nodeListExpr:
		n, err := d.u32()
		if err != nil {
			return nil, err
		}
		if err := d.checkCount(n); err != nil {
			return nil, err
		}
		items := make([]ast.Node, int(n))
		for i := range items {
			if items[i], err = d.node(); err != nil {
				return nil, err
			}
		}
		return &ast.ListExpr{Pos: p, Items: items}, nil
	case nodeObjectLit:
		typeName, err := d.str()
		if err != nil {
			return nil, err
		}
		keys, err := d.strs()
		if err != nil {
			return nil, err
		}
		vcount, err := d.u32()
		if err != nil {
			return nil, err
		}
		if err := d.checkCount(vcount); err != nil {
			return nil, err
		}
		vals := make([]ast.Node, int(vcount))
		for i := range vals {
			if vals[i], err = d.node(); err != nil {
				return nil, err
			}
		}
		return &ast.ObjectLit{Pos: p, TypeName: typeName, Keys: keys, Values: vals}, nil
	case nodeInterpExpr:
		n, err := d.u32()
		if err != nil {
			return nil, err
		}
		if err := d.checkCount(n); err != nil {
			return nil, err
		}
		parts := make([]ast.InterpPart, int(n))
		for i := range parts {
			if parts[i].Lit, err = d.str(); err != nil {
				return nil, err
			}
			if parts[i].Expr, err = d.node(); err != nil {
				return nil, err
			}
		}
		return &ast.InterpExpr{Pos: p, Parts: parts}, nil
	case nodeRangeExpr:
		lo, err := d.node()
		if err != nil {
			return nil, err
		}
		hi, err := d.node()
		if err != nil {
			return nil, err
		}
		return &ast.RangeExpr{Pos: p, Lo: lo, Hi: hi}, nil
	case nodeIsExpr:
		typeName, err := d.str()
		if err != nil {
			return nil, err
		}
		expr, err := d.node()
		if err != nil {
			return nil, err
		}
		return &ast.IsExpr{Pos: p, TypeName: typeName, Expr: expr}, nil
	case nodeAsExpr:
		typeName, err := d.str()
		if err != nil {
			return nil, err
		}
		expr, err := d.node()
		if err != nil {
			return nil, err
		}
		return &ast.AsExpr{Pos: p, TypeName: typeName, Expr: expr}, nil
	case nodeBlockStmt:
		n, err := d.u32()
		if err != nil {
			return nil, err
		}
		if err := d.checkCount(n); err != nil {
			return nil, err
		}
		stmts := make([]ast.Node, int(n))
		for i := range stmts {
			if stmts[i], err = d.node(); err != nil {
				return nil, err
			}
		}
		return &ast.BlockStmt{Pos: p, Stmts: stmts}, nil
	case nodeImportStmt:
		path, err := d.str()
		if err != nil {
			return nil, err
		}
		return &ast.ImportStmt{Pos: p, Path: path}, nil
	case nodeDeclStmt:
		isConst, err := d.boolean()
		if err != nil {
			return nil, err
		}
		name, err := d.str()
		if err != nil {
			return nil, err
		}
		typeAnnot, err := d.str()
		if err != nil {
			return nil, err
		}
		val, err := d.node()
		if err != nil {
			return nil, err
		}
		return &ast.DeclStmt{Pos: p, IsConst: isConst, Name: name, TypeAnnot: typeAnnot, Value: val}, nil
	case nodeAssignStmt:
		target, err := d.node()
		if err != nil {
			return nil, err
		}
		val, err := d.node()
		if err != nil {
			return nil, err
		}
		return &ast.AssignStmt{Pos: p, Target: target, Value: val}, nil
	case nodeReturnStmt:
		val, err := d.node()
		if err != nil {
			return nil, err
		}
		return &ast.ReturnStmt{Pos: p, Value: val}, nil
	case nodeExprStmt:
		expr, err := d.node()
		if err != nil {
			return nil, err
		}
		return &ast.ExprStmt{Pos: p, Expr: expr}, nil
	case nodeIfStmt:
		cond, err := d.node()
		if err != nil {
			return nil, err
		}
		then, err := d.node()
		if err != nil {
			return nil, err
		}
		blockThen, ok := then.(*ast.BlockStmt)
		if !ok {
			return nil, fmt.Errorf("IfStmt then: expected *ast.BlockStmt, got %T", then)
		}
		els, err := d.node()
		if err != nil {
			return nil, err
		}
		return &ast.IfStmt{Pos: p, Cond: cond, Then: blockThen, Else: els}, nil
	case nodeWhileStmt:
		cond, err := d.node()
		if err != nil {
			return nil, err
		}
		body, err := d.node()
		if err != nil {
			return nil, err
		}
		blockBody, ok := body.(*ast.BlockStmt)
		if !ok {
			return nil, fmt.Errorf("WhileStmt body: expected *ast.BlockStmt, got %T", body)
		}
		return &ast.WhileStmt{Pos: p, Cond: cond, Body: blockBody}, nil
	case nodeForStmt:
		init, err := d.node()
		if err != nil {
			return nil, err
		}
		cond, err := d.node()
		if err != nil {
			return nil, err
		}
		post, err := d.node()
		if err != nil {
			return nil, err
		}
		body, err := d.node()
		if err != nil {
			return nil, err
		}
		blockBody, ok := body.(*ast.BlockStmt)
		if !ok {
			return nil, fmt.Errorf("ForStmt body: expected *ast.BlockStmt, got %T", body)
		}
		return &ast.ForStmt{Pos: p, Init: init, Cond: cond, Post: post, Body: blockBody}, nil
	case nodeForEachStmt:
		keyName, err := d.str()
		if err != nil {
			return nil, err
		}
		valName, err := d.str()
		if err != nil {
			return nil, err
		}
		iter, err := d.node()
		if err != nil {
			return nil, err
		}
		body, err := d.node()
		if err != nil {
			return nil, err
		}
		blockBody, ok := body.(*ast.BlockStmt)
		if !ok {
			return nil, fmt.Errorf("ForEachStmt body: expected *ast.BlockStmt, got %T", body)
		}
		return &ast.ForEachStmt{Pos: p, KeyName: keyName, ValName: valName, Iter: iter, Body: blockBody}, nil
	case nodeBreakStmt:
		return &ast.BreakStmt{Pos: p}, nil
	case nodeContinueStmt:
		return &ast.ContinueStmt{Pos: p}, nil
	case nodeFunDecl:
		name, err := d.str()
		if err != nil {
			return nil, err
		}
		params, err := d.strs()
		if err != nil {
			return nil, err
		}
		paramAnnots, err := d.strs()
		if err != nil {
			return nil, err
		}
		retAnnot, err := d.str()
		if err != nil {
			return nil, err
		}
		yieldAnnot, err := d.str()
		if err != nil {
			return nil, err
		}
		isStatic, err := d.boolean()
		if err != nil {
			return nil, err
		}
		body, err := d.node()
		if err != nil {
			return nil, err
		}
		blockBody, ok := body.(*ast.BlockStmt)
		if !ok {
			return nil, fmt.Errorf("FunDecl body: expected *ast.BlockStmt, got %T", body)
		}
		return &ast.FunDecl{Pos: p, Name: name, Params: params, ParamAnnots: paramAnnots, RetAnnot: retAnnot, YieldAnnot: yieldAnnot, IsStatic: isStatic, Body: blockBody}, nil
	case nodeObjectDecl:
		name, err := d.str()
		if err != nil {
			return nil, err
		}
		fcount, err := d.u32()
		if err != nil {
			return nil, err
		}
		if err := d.checkCount(fcount); err != nil {
			return nil, err
		}
		fields := make([]ast.ObjField, int(fcount))
		for i := range fields {
			if fields[i].Name, err = d.str(); err != nil {
				return nil, err
			}
			if fields[i].TypeAnnot, err = d.str(); err != nil {
				return nil, err
			}
			if fields[i].Default, err = d.node(); err != nil {
				return nil, err
			}
		}
		mcount, err := d.u32()
		if err != nil {
			return nil, err
		}
		if err := d.checkCount(mcount); err != nil {
			return nil, err
		}
		methods := make([]*ast.FunDecl, int(mcount))
		for i := range methods {
			mn, err := d.node()
			if err != nil {
				return nil, err
			}
			fd, ok := mn.(*ast.FunDecl)
			if !ok {
				return nil, fmt.Errorf("ObjectDecl method: expected *ast.FunDecl, got %T", mn)
			}
			methods[i] = fd
		}
		return &ast.ObjectDecl{Pos: p, Name: name, Fields: fields, Methods: methods}, nil
	case nodeEnumDecl:
		name, err := d.str()
		if err != nil {
			return nil, err
		}
		cases, err := d.strs()
		if err != nil {
			return nil, err
		}
		return &ast.EnumDecl{Pos: p, Name: name, Cases: cases}, nil
	case nodeDoStmt:
		body, err := d.node()
		if err != nil {
			return nil, err
		}
		blockBody, ok := body.(*ast.BlockStmt)
		if !ok {
			return nil, fmt.Errorf("DoStmt body: expected *ast.BlockStmt, got %T", body)
		}
		cond, err := d.node()
		if err != nil {
			return nil, err
		}
		return &ast.DoStmt{Pos: p, Body: blockBody, Cond: cond}, nil
	case nodeTryStmt:
		body, err := d.node()
		if err != nil {
			return nil, err
		}
		blockBody, ok := body.(*ast.BlockStmt)
		if !ok {
			return nil, fmt.Errorf("TryStmt body: expected *ast.BlockStmt, got %T", body)
		}
		errName, err := d.str()
		if err != nil {
			return nil, err
		}
		catch, err := d.node()
		if err != nil {
			return nil, err
		}
		blockCatch, ok := catch.(*ast.BlockStmt)
		if !ok {
			return nil, fmt.Errorf("TryStmt catch: expected *ast.BlockStmt, got %T", catch)
		}
		return &ast.TryStmt{Pos: p, Body: blockBody, ErrName: errName, Catch: blockCatch}, nil
	case nodeThrowStmt:
		val, err := d.node()
		if err != nil {
			return nil, err
		}
		return &ast.ThrowStmt{Pos: p, Value: val}, nil
	case nodeCatchExpr:
		expr, err := d.node()
		if err != nil {
			return nil, err
		}
		def, err := d.node()
		if err != nil {
			return nil, err
		}
		return &ast.CatchExpr{Pos: p, Expr: expr, Default: def}, nil
	case nodeYieldExpr:
		val, err := d.node()
		if err != nil {
			return nil, err
		}
		return &ast.YieldExpr{Pos: p, Value: val}, nil
	case nodeFiberExpr:
		call, err := d.node()
		if err != nil {
			return nil, err
		}
		callExpr, ok := call.(*ast.CallExpr)
		if !ok {
			return nil, fmt.Errorf("FiberExpr: expected *ast.CallExpr, got %T", call)
		}
		return &ast.FiberExpr{Pos: p, Call: callExpr}, nil
	case nodeResumeExpr:
		fiber, err := d.node()
		if err != nil {
			return nil, err
		}
		return &ast.ResumeExpr{Pos: p, Fiber: fiber}, nil
	case nodeResolveExpr:
		fiber, err := d.node()
		if err != nil {
			return nil, err
		}
		return &ast.ResolveExpr{Pos: p, Fiber: fiber}, nil
	default:
		return nil, fmt.Errorf("unknown AST node tag %d", tag)
	}
}
