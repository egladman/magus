package buzz

import (
	"fmt"

	"github.com/egladman/gopherbuzz/ast"
	vmpackage "github.com/egladman/gopherbuzz/vm"
)

// CompileOptions controls how a program's top-level scope is compiled.
type CompileOptions struct {
	// SharedGlobals compiles top-level declarations as runtime Env bindings
	// (OpDefName/OpLoadName) rather than stack slots. Set it when several chunks
	// execute against one shared Env and must observe each other's top-level
	// definitions — the magus multi-magusfile model and the REPL both rely on
	// this. When false (the default), top-level variables are slot-based locals:
	// faster (no per-access map hashing), but private to a single Run. Function
	// bodies always use slots regardless of this flag.
	SharedGlobals bool

	// DebugLines records a source line for every emitted instruction (Chunk.lines)
	// so the debugger can report a paused frame's current line and drive
	// line-level step hooks. Off by default — the one-shot fast path pays nothing;
	// the session path (Session.Compile) turns it on so magus.pry() works.
	DebugLines bool

	// PromoteTopLevel, only meaningful together with SharedGlobals, slot-promotes
	// a top-level var/const that is provably chunk-private: not exported and never
	// referenced from inside any function/fiber body (where it would either need to
	// outlive the top-level frame or change from a live-Env read to a by-value
	// upvalue snapshot). Promoted vars become stack slots — the same fast path
	// block-locals and function bodies already use — while exported and
	// closure-captured top-level names stay Env bindings, so cross-chunk visibility
	// is unchanged for them. Leave it false for the REPL/incremental path, where a
	// later chunk may reference any earlier top-level name by name.
	PromoteTopLevel bool

	// ImportedTypes are the exported object/enum declarations of flat-imported
	// modules (the same set handed to the checker). They are seeded into the
	// compiler's typeDecls so an object literal of an imported type
	// (`config\Config{...}`) applies that type's field defaults, exactly as a
	// local-type literal does. Without this, an imported-type literal only carries
	// the fields it sets and leaves the rest null — upstream Buzz applies the
	// defaults, so this is a parity fix, not an extension.
	ImportedTypes []ast.Node
}

// CompileWith compiles prog under opts. See CompileOptions. Pass the zero
// CompileOptions{} for a self-contained program whose top-level variables are
// slot-based locals (the one-shot fast path); set SharedGlobals for the
// session model. (This is the standalone counterpart to Session.Compile, which
// compiles source against a session's shared scope.)
func CompileWith(prog *ast.Program, opts CompileOptions) (*Chunk, error) {
	c := newCompiler(nil, "<main>", nil)
	c.useSlots = !opts.SharedGlobals
	c.debugLines = opts.DebugLines
	if opts.SharedGlobals {
		c.initModuleScope(prog) // per-module Env keys for private globals
	}
	if opts.SharedGlobals && opts.PromoteTopLevel {
		c.promoteTopLevel = true
		c.keepEnv = topLevelKeepEnv(prog)
	}
	if opts.DebugLines {
		c.chunk.Lines = []int32{} // non-nil: emit now records a line per instruction
	}
	// Imported object types first, keyed by bare name (the last segment, which is
	// how a `ns\Name{...}` literal resolves -- see the parser). Local declarations
	// below overwrite on a name clash, so a local type always shadows an import.
	for _, n := range opts.ImportedTypes {
		if od, ok := n.(*ast.ObjectDecl); ok {
			c.typeDecls[od.Name] = od
		}
	}
	for _, s := range prog.Stmts {
		if od, ok := s.(*ast.ObjectDecl); ok {
			c.typeDecls[od.Name] = od
		}
	}
	for _, s := range prog.Stmts {
		if err := c.compileStmt(s); err != nil {
			return nil, err
		}
	}
	c.chunk.Emit(vmpackage.OpReturnNull, 0, 0)
	// localCount is the register-window size for VM.Run pre-allocation.
	// Even in SharedGlobals mode, block-local variables (depth > 0) use slots,
	// so nextSlot may be > 0 and the window must be pre-allocated.
	c.chunk.LocalCount = int(c.nextSlot)
	FoldConsts(c.chunk)
	FusePeephole(c.chunk)
	return c.chunk, nil
}

// promotable reports whether a top-level declaration may be slot-promoted instead
// of bound into the shared Env. Only applies in PromoteTopLevel mode at the chunk
// top level; the name must be neither exported nor in keepEnv (the set of names
// referenced from inside a function/fiber body, computed by topLevelKeepEnv). When
// it returns false the caller falls through to the OpDefName path, so the var stays
// an Env binding visible to later chunks exactly as before.
func (c *compiler) promotable(v *ast.DeclStmt) bool {
	return c.promoteTopLevel && c.depth == 0 && !v.IsExported && !c.keepEnv[v.Name]
}

// topLevelKeepEnv returns the set of top-level names that must remain Env bindings
// even under PromoteTopLevel: every identifier referenced from inside any function
// or fiber body. A top-level var captured by a closure cannot become a slot — the
// closure outlives the top-level frame, and slot capture is a by-value upvalue
// snapshot rather than the live-Env read the Env path gives. The scan is a sound
// over-approximation: it collects every name used inside a function body, including
// that function's own params/locals, so a top-level var is only promoted when its
// name appears nowhere inside any nested function. Exported names are handled at the
// declaration site (promotable checks IsExported) and need not be listed here.
func topLevelKeepEnv(prog *ast.Program) map[string]bool {
	keep := map[string]bool{}
	for _, s := range prog.Stmts {
		collectFuncRefs(s, false, keep)
	}
	return keep
}

// collectFuncRefs walks n and records, into keep, the Name of every IdentExpr that
// appears while inFunc is true — i.e. lexically inside a function/fiber body.
// Descending into a FunDecl/FunExpr (or object method) body sets inFunc; all other
// recursion preserves it, so top-level code outside any function contributes no
// names. The switch is exhaustive over ast node types; unrecognized or nil nodes
// contribute nothing.
func collectFuncRefs(n ast.Node, inFunc bool, keep map[string]bool) {
	switch v := n.(type) {
	case nil:
		return
	case *ast.IdentExpr:
		if inFunc {
			keep[v.Name] = true
		}
	case *ast.DeclStmt:
		collectFuncRefs(v.Value, inFunc, keep)
	case *ast.AssignStmt:
		collectFuncRefs(v.Target, inFunc, keep)
		collectFuncRefs(v.Value, inFunc, keep)
	case *ast.ReturnStmt:
		collectFuncRefs(v.Value, inFunc, keep)
	case *ast.ExprStmt:
		collectFuncRefs(v.Expr, inFunc, keep)
	case *ast.BlockStmt:
		for _, s := range v.Stmts {
			collectFuncRefs(s, inFunc, keep)
		}
	case *ast.IfStmt:
		collectFuncRefs(v.Cond, inFunc, keep)
		collectFuncRefs(v.Then, inFunc, keep)
		collectFuncRefs(v.Else, inFunc, keep)
	case *ast.WhileStmt:
		collectFuncRefs(v.Cond, inFunc, keep)
		collectFuncRefs(v.Body, inFunc, keep)
	case *ast.ForStmt:
		collectFuncRefs(v.Init, inFunc, keep)
		collectFuncRefs(v.Cond, inFunc, keep)
		collectFuncRefs(v.Post, inFunc, keep)
		collectFuncRefs(v.Body, inFunc, keep)
	case *ast.ForEachStmt:
		collectFuncRefs(v.Iter, inFunc, keep)
		collectFuncRefs(v.Body, inFunc, keep)
	case *ast.DoStmt:
		collectFuncRefs(v.Body, inFunc, keep)
		collectFuncRefs(v.Cond, inFunc, keep)
	case *ast.TryStmt:
		collectFuncRefs(v.Body, inFunc, keep)
		collectFuncRefs(v.Catch, inFunc, keep)
	case *ast.ThrowStmt:
		collectFuncRefs(v.Value, inFunc, keep)
	case *ast.FunDecl:
		// Entering a function body: everything inside it captures by reference today.
		collectFuncRefs(v.Body, true, keep)
	case *ast.FunExpr:
		collectFuncRefs(v.Body, true, keep)
	case *ast.ObjectDecl:
		for i := range v.Fields {
			collectFuncRefs(v.Fields[i].Default, inFunc, keep)
		}
		for _, m := range v.Methods {
			collectFuncRefs(m, inFunc, keep)
		}
	case *ast.BinaryExpr:
		collectFuncRefs(v.Left, inFunc, keep)
		collectFuncRefs(v.Right, inFunc, keep)
	case *ast.UnaryExpr:
		collectFuncRefs(v.Operand, inFunc, keep)
	case *ast.CallExpr:
		collectFuncRefs(v.Callee, inFunc, keep)
		for _, a := range v.Args {
			collectFuncRefs(a, inFunc, keep)
		}
	case *ast.MemberExpr:
		collectFuncRefs(v.Object, inFunc, keep)
	case *ast.IndexExpr:
		collectFuncRefs(v.Object, inFunc, keep)
		collectFuncRefs(v.Index, inFunc, keep)
	case *ast.MapExpr:
		for _, k := range v.Keys {
			collectFuncRefs(k, inFunc, keep)
		}
		for _, val := range v.Values {
			collectFuncRefs(val, inFunc, keep)
		}
	case *ast.ListExpr:
		for _, it := range v.Items {
			collectFuncRefs(it, inFunc, keep)
		}
	case *ast.ObjectLit:
		for _, val := range v.Values {
			collectFuncRefs(val, inFunc, keep)
		}
	case *ast.InterpExpr:
		for i := range v.Parts {
			collectFuncRefs(v.Parts[i].Expr, inFunc, keep)
		}
	case *ast.RangeExpr:
		collectFuncRefs(v.Lo, inFunc, keep)
		collectFuncRefs(v.Hi, inFunc, keep)
	case *ast.IsExpr:
		collectFuncRefs(v.Expr, inFunc, keep)
	case *ast.AsExpr:
		collectFuncRefs(v.Expr, inFunc, keep)
	case *ast.CatchExpr:
		collectFuncRefs(v.Expr, inFunc, keep)
		collectFuncRefs(v.Default, inFunc, keep)
	case *ast.YieldExpr:
		collectFuncRefs(v.Value, inFunc, keep)
	case *ast.FiberExpr:
		collectFuncRefs(v.Call, inFunc, keep)
	case *ast.ResumeExpr:
		collectFuncRefs(v.Fiber, inFunc, keep)
	case *ast.ResolveExpr:
		collectFuncRefs(v.Fiber, inFunc, keep)
	}
	// Leaf nodes (literals, Import/Namespace/Break/Continue/Enum) and any node not
	// listed have no identifier children that matter here.
}

type loopInfo struct {
	breakPatch     []int
	continuePatch  []int
	continueTarget int
	isForeach      bool
}

type localVar struct {
	name  string
	depth int
	slot  int32
}

// styp is the compiler's conservative static type for a local slot or an
// expression: a small lattice over the primitive types the VM can cheaply
// type-check. sUnknown means "not statically known to be any single primitive"
// (the gradually-typed `any`, a function result, a member/index read, a
// param/upvalue/global, …). It is deliberately coarse — it only needs to be
// SOUND (never claim a concrete type the runtime value might not have), so the
// fast paths it gates and the OpCheckType narrowings it inserts are correct even
// though it tracks less than the full checker.
type styp uint8

const (
	sUnknown styp = iota
	sInt
	sFloat
	sStr
	sBool
)

// annotStyp maps a primitive type annotation to its styp. Compound/object
// annotations ([int], {str:int}, Foo) are sUnknown — OpCheckType only guards the
// four primitives the VM checks in one tag compare.
func annotStyp(annot string) styp {
	switch annot {
	case "int":
		return sInt
	case "double":
		return sFloat
	case "str":
		return sStr
	case "bool":
		return sBool
	default:
		return sUnknown
	}
}

// checkCode maps an styp to the OpCheckType operand, or 0 if the type is not a
// checkable primitive (caller skips the check).
func checkCode(t styp) int32 {
	switch t {
	case sInt:
		return vmpackage.CheckInt
	case sFloat:
		return vmpackage.CheckFloat
	case sStr:
		return vmpackage.CheckStr
	case sBool:
		return vmpackage.CheckBool
	default:
		return 0
	}
}

// setSlotType records slot's tracked primitive type (grows slotTypes as slots are
// never reused, only appended). A slot left at sUnknown is treated as dynamic.
func (c *compiler) setSlotType(slot int32, t styp) {
	for int32(len(c.slotTypes)) <= slot {
		c.slotTypes = append(c.slotTypes, sUnknown)
	}
	c.slotTypes[slot] = t
}

// slotType returns the tracked type of a local slot, or sUnknown.
func (c *compiler) slotType(slot int32) styp {
	if slot < 0 || int(slot) >= len(c.slotTypes) {
		return sUnknown
	}
	return c.slotTypes[slot]
}

func (c *compiler) setSlotObjFields(slot int32, fields map[string]int32) {
	for int32(len(c.slotObjFields)) <= slot {
		c.slotObjFields = append(c.slotObjFields, nil)
	}
	c.slotObjFields[slot] = fields
}

func (c *compiler) clearSlotObjFields(slot int32) {
	if slot >= 0 && int(slot) < len(c.slotObjFields) {
		c.slotObjFields[slot] = nil
	}
}

// localObjFieldIndex returns the compile-time field index for obj.name when obj
// is a local variable whose slot was initialized from a statically-known object
// type (and not subsequently reassigned), or ok=false otherwise.
func (c *compiler) localObjFieldIndex(obj ast.Node, name string) (int32, bool) {
	id, ok := obj.(*ast.IdentExpr)
	if !ok {
		return 0, false
	}
	slot := c.resolveLocal(id.Name)
	if slot < 0 || int(slot) >= len(c.slotObjFields) || c.slotObjFields[slot] == nil {
		return 0, false
	}
	idx, ok := c.slotObjFields[slot][name]
	return idx, ok
}

// staticType infers an expression's conservative primitive type. Anything it is
// not certain about (calls, members, indices, params, upvalues, globals, the and/
// or/?? operators) is sUnknown. Only local slots it has tracked, literals, and
// arithmetic/comparison over statically-typed operands yield a concrete type.
func (c *compiler) staticType(n ast.Node) styp {
	switch v := n.(type) {
	case *ast.IntLit:
		return sInt
	case *ast.FloatLit:
		return sFloat
	case *ast.StringLit, *ast.InterpExpr:
		return sStr
	case *ast.IdentExpr:
		return c.slotType(c.resolveLocal(v.Name))
	case *ast.UnaryExpr:
		switch v.Op {
		case "-":
			if t := c.staticType(v.Operand); t == sInt || t == sFloat {
				return t
			}
		case "not":
			return sBool
		}
		return sUnknown
	case *ast.BinaryExpr:
		switch v.Op {
		case "==", "!=", "<", "<=", ">", ">=":
			return sBool
		case "+", "-", "*", "/", "%":
			lt, rt := c.staticType(v.Left), c.staticType(v.Right)
			if lt == sInt && rt == sInt {
				return sInt
			}
			if (lt == sInt || lt == sFloat) && (rt == sInt || rt == sFloat) {
				return sFloat // mixed/float numeric promotes to float
			}
			if v.Op == "+" && lt == sStr {
				return sStr
			}
		}
		return sUnknown
	}
	return sUnknown
}

type upvalRef struct {
	isLocal bool
	index   int32
}

type compiler struct {
	chunk     *Chunk
	parent    *compiler
	typeDecls map[string]*ast.ObjectDecl
	loops     []loopInfo
	// useSlots selects slot-based locals (a stack register window) over runtime
	// Env bindings for this scope's declarations and lookups. Always true for
	// function bodies; true for the top-level chunk only when compiled as a
	// self-contained unit (see CompileOptions.SharedGlobals).
	useSlots bool
	// promoteTopLevel enables slot promotion of chunk-private top-level vars in
	// SharedGlobals mode (see CompileOptions.PromoteTopLevel). Only set on the
	// top-level compiler; keepEnv lists the names that must stay Env bindings.
	promoteTopLevel bool
	keepEnv         map[string]bool
	// debugLines propagates CompileOptions.DebugLines into nested function
	// compilers so every chunk in the program carries a parallel line table.
	debugLines bool
	locals     []localVar
	upvals     []upvalRef
	depth      int
	nextSlot   int32
	// slotTypes records the conservative static primitive type of each local slot
	// (indexed by slot; slots are never reused). A slot is tracked concrete only
	// when every store to it is statically that type or guarded by an inserted
	// OpCheckType, so reads may trust it. Params, upvalues, globals, and
	// dynamically-initialized locals stay sUnknown.
	slotTypes []styp
	// slotObjFields records the compile-time field map for a local slot that was
	// directly initialized from a named ObjectLit (e.g. `var c = Card{...}`).
	// Enables OpGetField/OpSetField emission for typed local vars, not just `this`.
	// Cleared on any reassignment to that slot; nil entries mean "unknown".
	slotObjFields []map[string]int32
	// thisFields maps a field name to its declaration index when compiling a method
	// body, so `this.field` can be emitted as an inline-cached OpGetField/OpSetField
	// instead of a name lookup. nil outside a method body (and in nested closures,
	// which fall back to the name path). `this` is bound by dispatch, so it is
	// always the object type — these accesses need no shape guard.
	thisFields map[string]int32
	// zdefSeq names the per-statement temporary that holds a lowered top-level
	// zdef handle while its symbols are bound as globals (see zdef_decl.go).
	zdefSeq int32
	// nsPrefix and privTop give a namespaced module its own Env keys for PRIVATE
	// top-level vars and funcs. In SharedGlobals mode every module's top-level
	// declarations land in one shared Env keyed by bare name, so two modules that
	// each declare a private `var panel` would collide on the same slot. When a
	// module has a `namespace X;`, privTop holds its private top-level names and
	// nsPrefix ("\0X\0") qualifies them at every def/load/store (see globalName).
	// Exports stay bare — they're unique across modules and reached via the
	// namespace object. Inherited by nested function compilers so a reference to a
	// private global inside a function mangles to the same key as its definition.
	nsPrefix string
	privTop  map[string]bool
}

func newCompiler(parent *compiler, name string, params []string) *compiler {
	c := &compiler{
		chunk:     &Chunk{Name: name, Params: params},
		parent:    parent,
		typeDecls: map[string]*ast.ObjectDecl{},
	}
	if parent != nil {
		for k, v := range parent.typeDecls {
			c.typeDecls[k] = v
		}
	}
	return c
}

func (c *compiler) defineLocal(name string) int32 {
	slot := c.nextSlot
	c.nextSlot++
	c.locals = append(c.locals, localVar{name: name, depth: c.depth, slot: slot})
	if c.debugLines {
		// Slots are never reused (nextSlot only grows), so localNames[slot] is a
		// stable name→slot record for the lifetime of the chunk.
		for int32(len(c.chunk.LocalNames)) <= slot {
			c.chunk.LocalNames = append(c.chunk.LocalNames, "")
		}
		c.chunk.LocalNames[slot] = name
	}
	return slot
}

func (c *compiler) resolveLocal(name string) int32 {
	for i := len(c.locals) - 1; i >= 0; i-- {
		if c.locals[i].name == name {
			return c.locals[i].slot
		}
	}
	return -1
}

func (c *compiler) addUpvalue(isLocal bool, index int32) int32 {
	for i, u := range c.upvals {
		if u.isLocal == isLocal && u.index == index {
			return int32(i)
		}
	}
	c.upvals = append(c.upvals, upvalRef{isLocal: isLocal, index: index})
	return int32(len(c.upvals) - 1)
}

func (c *compiler) resolveUpvalue(name string) int32 {
	if c.parent == nil || !c.parent.useSlots {
		return -1
	}
	if slot := c.parent.resolveLocal(name); slot >= 0 {
		return c.recordUpvalName(c.addUpvalue(true, slot), name)
	}
	if uv := c.parent.resolveUpvalue(name); uv >= 0 {
		return c.recordUpvalName(c.addUpvalue(false, uv), name)
	}
	return -1
}

// recordUpvalName records idx→name in the chunk's upvalue name table (DebugLines
// only) so the debugger can label captured upvalues, then returns idx unchanged.
func (c *compiler) recordUpvalName(idx int32, name string) int32 {
	if c.debugLines && idx >= 0 {
		for int32(len(c.chunk.UpvalNames)) <= idx {
			c.chunk.UpvalNames = append(c.chunk.UpvalNames, "")
		}
		c.chunk.UpvalNames[idx] = name
	}
	return idx
}

func (c *compiler) enterBlock() { c.depth++ }

func (c *compiler) exitBlock() {
	c.depth--
	for len(c.locals) > 0 && c.locals[len(c.locals)-1].depth > c.depth {
		c.locals = c.locals[:len(c.locals)-1]
	}
}

// blockHasDecls reports whether any direct child of stmts is a *ast.DeclStmt.
// Not recursive — inner blocks handle their own scopes.
func blockHasDecls(stmts []ast.Node) bool {
	for _, s := range stmts {
		if _, ok := s.(*ast.DeclStmt); ok {
			return true
		}
	}
	return false
}

func (c *compiler) nameConst(s string) int32 {
	return c.chunk.AddConst(StrValue(s))
}

// globalName maps a top-level identifier to its shared-Env key. A private var or
// func of a namespaced module is qualified with nsPrefix so it can't collide with
// a same-named private in another module; exports, imports, builtins, and any name
// not declared private here keep their bare spelling. Called at every OpDefName /
// OpLoadName / OpStoreName site that touches a user-level top-level name.
func (c *compiler) globalName(name string) string {
	if c.privTop != nil && c.privTop[name] {
		return c.nsPrefix + name
	}
	return name
}

// initModuleScope records the module's namespace and the set of its private
// top-level vars/funcs, so globalName can give them per-module Env keys. Only
// meaningful in SharedGlobals mode (where modules share one Env); a module with no
// `namespace` — the entry program — keeps bare keys, which is unambiguous because
// it is the only namespace-less module in a program.
func (c *compiler) initModuleScope(prog *ast.Program) {
	var ns string
	for _, s := range prog.Stmts {
		if n, ok := s.(*ast.NamespaceStmt); ok {
			ns = n.Name
			break
		}
	}
	if ns == "" {
		return
	}
	priv := map[string]bool{}
	for _, s := range prog.Stmts {
		switch d := s.(type) {
		case *ast.DeclStmt:
			if !d.IsExported {
				priv[d.Name] = true
			}
		case *ast.FunDecl:
			if !d.IsExported {
				priv[d.Name] = true
			}
		}
	}
	if len(priv) == 0 {
		return
	}
	c.nsPrefix = "\x00" + ns + "\x00"
	c.privTop = priv
}

// compileZdefDecl lowers a top-level `zdef("lib", "<decls>")` statement into
// module-global bindings for the symbols it declares (upstream Buzz zdef
// semantics; see zdef_decl.go). It evaluates the zdef call once into a private
// temp holding the handle map, then binds each declared name to handle[name].
// The symbols are exported so importing modules can call them by bare name.
func (c *compiler) compileZdefDecl(call *ast.CallExpr, names []string) error {
	if err := c.compileExpr(call); err != nil { // → handle map on the stack
		return err
	}
	tmp := fmt.Sprintf("$zdef%d", c.zdefSeq)
	c.zdefSeq++
	c.chunk.Emit(vmpackage.OpDefName, c.nameConst(tmp), 0) // bind handle, pops it
	c.chunk.Private = append(c.chunk.Private, tmp)
	for _, name := range names {
		c.chunk.Emit(vmpackage.OpLoadName, c.nameConst(tmp), 0)    // push handle
		c.chunk.Emit(vmpackage.OpLoadConst, c.nameConst(name), 0)  // push key
		c.chunk.Emit(vmpackage.OpGetIndex, 0, 0)                   // → handle[name]
		c.chunk.Emit(vmpackage.OpDefName, c.nameConst(name), 0)    // bind global
		c.chunk.Exports = append(c.chunk.Exports, name)
	}
	return nil
}

func (c *compiler) pushLoop(continueTarget int, isForeach bool) {
	c.loops = append(c.loops, loopInfo{continueTarget: continueTarget, isForeach: isForeach})
}

func (c *compiler) popLoop() loopInfo {
	n := len(c.loops) - 1
	li := c.loops[n]
	c.loops = c.loops[:n]
	return li
}

func (c *compiler) currentLoop() *loopInfo {
	if len(c.loops) == 0 {
		return nil
	}
	return &c.loops[len(c.loops)-1]
}

func (c *compiler) patchBreaks(li loopInfo) {
	end := int32(c.chunk.Current())
	for _, idx := range li.breakPatch {
		c.chunk.Code[idx].A = end
	}
}

func (c *compiler) compileStmt(n ast.Node) error {
	if c.debugLines {
		c.chunk.CurLine = int32(ast.NodePos(n).Line)
	}
	switch v := n.(type) {
	case *ast.ImportStmt, *ast.NamespaceStmt:
		return nil

	case *ast.DeclStmt:
		if err := c.compileExpr(v.Value); err != nil {
			return err
		}
		// Determine the slot's tracked type. An explicit primitive annotation is
		// authoritative: if the initializer is not statically that type, the value
		// may be an any-laundered mismatch, so assert it at runtime (OpCheckType)
		// before binding — that assertion is what lets later reads trust the type.
		// An unannotated decl simply inherits the initializer's static type.
		slotType := c.staticType(v.Value)
		if v.TypeAnnot != "" {
			if at := annotStyp(v.TypeAnnot); at != sUnknown {
				if slotType != at {
					c.chunk.Emit(vmpackage.OpCheckType, checkCode(at), 0)
				}
				slotType = at
			} else {
				slotType = sUnknown // compound/object annotation: not slot-tracked
			}
		}
		if c.useSlots || c.depth > 0 || c.promotable(v) {
			slot := c.defineLocal(v.Name)
			c.setSlotType(slot, slotType)
			if lit, ok := v.Value.(*ast.ObjectLit); ok {
				if decl, found := c.typeDecls[lit.TypeName]; found {
					fields := make(map[string]int32, len(decl.Fields))
					for i, f := range decl.Fields {
						fields[f.Name] = int32(i)
					}
					c.setSlotObjFields(slot, fields)
				}
			}
			c.chunk.Emit(vmpackage.OpSetLocal, slot, 0)
		} else {
			c.chunk.Emit(vmpackage.OpDefName, c.nameConst(c.globalName(v.Name)), 0)
			if v.IsExported {
				c.chunk.Exports = append(c.chunk.Exports, v.Name)
			} else {
				// Non-exported Env binding (a captured var or a non-exported
				// function): stays live for this module's own code, but a flat
				// importer hides it (exports-only visibility). See Chunk.Private.
				c.chunk.Private = append(c.chunk.Private, v.Name)
			}
		}
		return nil

	case *ast.AssignStmt:
		return c.compileAssign(v)

	case *ast.ReturnStmt:
		if v.Value == nil {
			c.chunk.Emit(vmpackage.OpReturnNull, 0, 0)
			return nil
		}
		if err := c.compileExpr(v.Value); err != nil {
			return err
		}
		c.chunk.Emit(vmpackage.OpReturn, 0, 0)
		return nil

	case *ast.ExprStmt:
		// A top-level `zdef("lib", "<decls>")` declares its symbols as module
		// globals (upstream Buzz semantics), rather than evaluating to a discarded
		// handle. Only at module top level — inside a function it stays an ordinary
		// expression returning the handle. The symbols bind as Env globals
		// (OpDefName), reachable by bare name in both compile modes.
		if c.depth == 0 {
			if call, names, ok := zdefDeclNames(v.Expr); ok {
				return c.compileZdefDecl(call, names)
			}
		}
		if err := c.compileExpr(v.Expr); err != nil {
			return err
		}
		c.chunk.Emit(vmpackage.OpPop, 0, 0)
		return nil

	case *ast.BlockStmt:
		c.enterBlock()
		for _, s := range v.Stmts {
			if err := c.compileStmt(s); err != nil {
				return err
			}
		}
		c.exitBlock()
		return nil

	case *ast.IfStmt:
		return c.compileIf(v)

	case *ast.WhileStmt:
		return c.compileWhile(v)

	case *ast.DoStmt:
		return c.compileDoUntil(v)

	case *ast.ForStmt:
		return c.compileForLoop(v)

	case *ast.ForEachStmt:
		return c.compileForEach(v)

	case *ast.BreakStmt:
		li := c.currentLoop()
		if li == nil {
			return fmt.Errorf("buzz: break outside loop")
		}
		if li.isForeach {
			c.chunk.Emit(vmpackage.OpPop, 0, 0)
		}
		idx := c.chunk.EmitJump(vmpackage.OpJump)
		li.breakPatch = append(li.breakPatch, idx)
		return nil

	case *ast.ContinueStmt:
		li := c.currentLoop()
		if li == nil {
			return fmt.Errorf("buzz: continue outside loop")
		}
		if li.continueTarget >= 0 {
			c.chunk.Emit(vmpackage.OpJump, int32(li.continueTarget), 0)
		} else {
			idx := c.chunk.EmitJump(vmpackage.OpJump)
			li.continuePatch = append(li.continuePatch, idx)
		}
		return nil

	case *ast.FunDecl:
		idx, err := c.compileFunChunk(v.Name, v.Doc, v.Params, v.Body.Stmts)
		if err != nil {
			return err
		}
		c.chunk.Emit(vmpackage.OpNewClosure, idx, 0)
		if c.useSlots || c.depth > 0 {
			slot := c.defineLocal(v.Name)
			c.chunk.Emit(vmpackage.OpSetLocal, slot, 0)
		} else {
			c.chunk.Emit(vmpackage.OpDefName, c.nameConst(c.globalName(v.Name)), 0)
			if v.IsExported {
				c.chunk.Exports = append(c.chunk.Exports, v.Name)
			} else {
				// Non-exported Env binding (a captured var or a non-exported
				// function): stays live for this module's own code, but a flat
				// importer hides it (exports-only visibility). See Chunk.Private.
				c.chunk.Private = append(c.chunk.Private, v.Name)
			}
		}
		return nil

	case *ast.TestDecl:
		return c.compileTestDecl(v)

	case *ast.ObjectDecl:
		return c.compileObjectDecl(v)

	case *ast.EnumDecl:
		return c.compileEnumDecl(v)

	case *ast.TryStmt:
		return c.compileTryCatch(v)

	case *ast.ThrowStmt:
		if err := c.compileExpr(v.Value); err != nil {
			return err
		}
		c.chunk.Emit(vmpackage.OpThrow, 0, 0)
		return nil

	// yield is an expression; if reached here as a statement it was wrapped in ExprStmt.
	// Fall through to default error to catch unexpected direct usage.

	default:
		return fmt.Errorf("buzz: compile: unknown statement %T", n)
	}
}

func (c *compiler) compileAssign(v *ast.AssignStmt) error {
	switch t := v.Target.(type) {
	case *ast.IdentExpr:
		if err := c.compileExpr(v.Value); err != nil {
			return err
		}
		// `_` is the discard target: evaluate the value (for its side effects, e.g.
		// `_ = yield x`) and drop it. It binds no name, so this is valid anywhere,
		// not only where `_` happens to be a loop variable.
		if t.Name == "_" {
			c.chunk.Emit(vmpackage.OpPop, 0, 0)
			return nil
		}
		if slot := c.resolveLocal(t.Name); slot >= 0 {
			// Preserve the slot's tracked type across reassignment: if the slot is a
			// tracked primitive and the new value is not statically that type, assert
			// it (OpCheckType) so the invariant "this slot always holds T" still holds
			// and downstream reads stay sound.
			if st := c.slotType(slot); st != sUnknown && c.staticType(v.Value) != st {
				c.chunk.Emit(vmpackage.OpCheckType, checkCode(st), 0)
			}
			c.clearSlotObjFields(slot)
			c.chunk.Emit(vmpackage.OpSetLocal, slot, 0)
			return nil
		}
		if c.useSlots {
			if uv := c.resolveUpvalue(t.Name); uv >= 0 {
				c.chunk.Emit(vmpackage.OpSetUpvalue, uv, 0)
				return nil
			}
		}
		// OpStoreName consumes its operand (see the handler in vm.go); no
		// trailing OpPop, mirroring OpSetLocal.
		c.chunk.Emit(vmpackage.OpStoreName, c.nameConst(c.globalName(t.Name)), 0)
		return nil
	case *ast.MemberExpr:
		if idx, ok := c.thisFieldIndex(t.Object, t.Name); ok {
			c.chunk.Emit(vmpackage.OpLoadThis, 0, 0)
			if err := c.compileExpr(v.Value); err != nil {
				return err
			}
			c.chunk.Emit(vmpackage.OpSetField, idx, c.nameConst(t.Name))
			return nil
		}
		if idx, ok := c.localObjFieldIndex(t.Object, t.Name); ok {
			if err := c.compileExpr(t.Object); err != nil {
				return err
			}
			if err := c.compileExpr(v.Value); err != nil {
				return err
			}
			c.chunk.Emit(vmpackage.OpSetField, idx, c.nameConst(t.Name))
			return nil
		}
		if err := c.compileExpr(t.Object); err != nil {
			return err
		}
		if err := c.compileExpr(v.Value); err != nil {
			return err
		}
		c.chunk.Emit(vmpackage.OpSetMember, c.nameConst(t.Name), 0)
		return nil
	case *ast.IndexExpr:
		if err := c.compileExpr(t.Object); err != nil {
			return err
		}
		if err := c.compileExpr(t.Index); err != nil {
			return err
		}
		if err := c.compileExpr(v.Value); err != nil {
			return err
		}
		c.chunk.Emit(vmpackage.OpSetIndex, 0, 0)
		return nil
	default:
		return fmt.Errorf("buzz: invalid assignment target %T", v.Target)
	}
}

func (c *compiler) compileIf(v *ast.IfStmt) error {
	if err := c.compileExpr(v.Cond); err != nil {
		return err
	}
	jf := c.chunk.EmitJump(vmpackage.OpJumpFalse)
	c.enterBlock()
	for _, s := range v.Then.Stmts {
		if err := c.compileStmt(s); err != nil {
			return err
		}
	}
	c.exitBlock()
	if v.Else == nil {
		c.chunk.PatchJump(jf)
		return nil
	}
	jmp := c.chunk.EmitJump(vmpackage.OpJump)
	c.chunk.PatchJump(jf)
	if err := c.compileStmt(v.Else); err != nil {
		return err
	}
	c.chunk.PatchJump(jmp)
	return nil
}

func (c *compiler) compileWhile(v *ast.WhileStmt) error {
	top := c.chunk.Current()
	c.pushLoop(top, false)
	if err := c.compileExpr(v.Cond); err != nil {
		return err
	}
	jf := c.chunk.EmitJump(vmpackage.OpJumpFalse)
	c.enterBlock()
	for _, s := range v.Body.Stmts {
		if err := c.compileStmt(s); err != nil {
			return err
		}
	}
	c.exitBlock()
	c.chunk.Emit(vmpackage.OpJump, int32(top), 0)
	c.chunk.PatchJump(jf)
	li := c.popLoop()
	c.patchBreaks(li)
	return nil
}

func (c *compiler) compileDoUntil(v *ast.DoStmt) error {
	top := c.chunk.Current()
	c.pushLoop(top, false)
	c.enterBlock()
	for _, s := range v.Body.Stmts {
		if err := c.compileStmt(s); err != nil {
			return err
		}
	}
	c.exitBlock()
	// Patch any pending continue jumps to the condition check.
	li := c.currentLoop()
	li.continueTarget = c.chunk.Current()
	target := int32(li.continueTarget)
	for _, idx := range li.continuePatch {
		c.chunk.Code[idx].A = target
	}
	if err := c.compileExpr(v.Cond); err != nil {
		return err
	}
	// Jump back to top if condition is false (until = repeat while NOT true).
	c.chunk.Emit(vmpackage.OpJumpFalse, int32(top), 0)
	popped := c.popLoop()
	c.patchBreaks(popped)
	return nil
}

func (c *compiler) compileTryCatch(v *ast.TryStmt) error {
	// Emit TryBegin with a placeholder for the catch IP.
	tryBeginIdx := c.chunk.EmitJump(vmpackage.OpTryBegin)

	// Compile the try body.
	c.enterBlock()
	for _, s := range v.Body.Stmts {
		if err := c.compileStmt(s); err != nil {
			return err
		}
	}
	c.exitBlock()

	// End of try body: pop catch context and jump over catch handler.
	c.chunk.Emit(vmpackage.OpTryEnd, 0, 0)
	skipCatchIdx := c.chunk.EmitJump(vmpackage.OpJump)

	// Patch TryBegin to point here (the catch handler).
	c.chunk.PatchJump(tryBeginIdx)

	// Compile the catch body: bind error to ErrName in a slot, then run handler.
	c.enterBlock()
	slot := c.defineLocal(v.ErrName)
	c.chunk.Emit(vmpackage.OpSetLocal, slot, 0)
	for _, s := range v.Catch.Stmts {
		if err := c.compileStmt(s); err != nil {
			return err
		}
	}
	c.exitBlock()

	// Patch the jump-over-catch to here.
	c.chunk.PatchJump(skipCatchIdx)
	return nil
}

// compileCatchExpr compiles `expr catch default` (inline catch): expr runs under
// a try handler; if it throws, the handler discards the error and the expression
// evaluates to default. Mirrors compileTryCatch's jump discipline, but leaves
// exactly one value on the stack. The throw machinery truncates the stack to the
// depth recorded at OpTryBegin and pushes the error, so the handler pops that
// error before evaluating the default — both paths net +1 on the stack.
func (c *compiler) compileCatchExpr(v *ast.CatchExpr) error {
	tryBeginIdx := c.chunk.EmitJump(vmpackage.OpTryBegin)
	if err := c.compileExpr(v.Expr); err != nil {
		return err
	}
	c.chunk.Emit(vmpackage.OpTryEnd, 0, 0)
	skipIdx := c.chunk.EmitJump(vmpackage.OpJump)

	c.chunk.PatchJump(tryBeginIdx)
	c.chunk.Emit(vmpackage.OpPop, 0, 0) // discard the thrown error value
	if err := c.compileExpr(v.Default); err != nil {
		return err
	}
	c.chunk.PatchJump(skipIdx)
	return nil
}

func (c *compiler) compileForLoop(v *ast.ForStmt) error {
	// outer block scopes the init variable
	c.enterBlock()
	if v.Init != nil {
		if err := c.compileStmt(v.Init); err != nil {
			return err
		}
	}
	top := c.chunk.Current()
	var jf int
	hasCond := v.Cond != nil
	if hasCond {
		if err := c.compileExpr(v.Cond); err != nil {
			return err
		}
		jf = c.chunk.EmitJump(vmpackage.OpJumpFalse)
	}
	c.pushLoop(-1, false)
	c.enterBlock()
	for _, s := range v.Body.Stmts {
		if err := c.compileStmt(s); err != nil {
			return err
		}
	}
	c.exitBlock()
	li := c.currentLoop()
	li.continueTarget = c.chunk.Current()
	target := int32(li.continueTarget)
	for _, idx := range li.continuePatch {
		c.chunk.Code[idx].A = target
	}
	if v.Post != nil {
		if err := c.compileStmt(v.Post); err != nil {
			return err
		}
	}
	c.chunk.Emit(vmpackage.OpJump, int32(top), 0)
	popped := c.popLoop()
	if hasCond {
		c.chunk.PatchJump(jf)
	}
	c.patchBreaks(popped)
	c.exitBlock()
	return nil
}

func (c *compiler) compileForEach(v *ast.ForEachStmt) error {
	if err := c.compileExpr(v.Iter); err != nil {
		return err
	}
	c.chunk.Emit(vmpackage.OpIterInit, 0, 0)

	var keyB int32
	if v.KeyName != "" {
		keyB = 1
	}

	// Always slot-based: iteration variables are always block-local and never
	// cross-chunk visible, so slots are correct in both slot and SharedGlobals mode.
	top := c.chunk.Current()
	jdone := c.chunk.Emit(vmpackage.OpIterNext, 0, keyB)
	c.pushLoop(top, true)
	c.enterBlock()
	// OpIterNext (not done) pushes: [key?,] val (val on top)
	valSlot := c.defineLocal(v.ValName)
	c.chunk.Emit(vmpackage.OpSetLocal, valSlot, 0)
	if v.KeyName != "" {
		keySlot := c.defineLocal(v.KeyName)
		c.chunk.Emit(vmpackage.OpSetLocal, keySlot, 0)
	}
	for _, s := range v.Body.Stmts {
		if err := c.compileStmt(s); err != nil {
			return err
		}
	}
	c.exitBlock()
	c.chunk.Emit(vmpackage.OpJump, int32(top), 0)
	c.chunk.Code[jdone].A = int32(c.chunk.Current())
	li := c.popLoop()
	c.patchBreaks(li)
	return nil
}

func (c *compiler) compileFunChunk(name, doc string, params []string, stmts []ast.Node) (int32, error) {
	return c.compileFunChunkThis(name, doc, params, stmts, nil)
}

// compileTestDecl lowers `test "name" { body }` to a call that registers the body
// (compiled as a zero-arg closure) with the session's test runner:
//
//	testRegistrarName("name", fun() { body });
//
// Registration is cheap and side-effect-free; the body runs only when the runner
// (buzz --test → Session.Tests) invokes the closure, so a normal run never
// executes a test block. The registrar is bound in the session env under a name
// no user identifier can spell, so it never collides and the checker — which runs
// on the AST before this lowering — never sees the synthetic reference.
func (c *compiler) compileTestDecl(v *ast.TestDecl) error {
	c.chunk.Emit(vmpackage.OpLoadName, c.nameConst(testRegistrarName), 0)
	c.chunk.Emit(vmpackage.OpLoadConst, c.chunk.AddConst(StrValue(v.Name)), 0)
	idx, err := c.compileFunChunk("test "+v.Name, "", nil, v.Body.Stmts)
	if err != nil {
		return err
	}
	c.chunk.Emit(vmpackage.OpNewClosure, idx, 0)
	c.chunk.Emit(vmpackage.OpCall, 2, 0)
	c.chunk.Emit(vmpackage.OpPop, 0, 0)
	return nil
}

// compileFunChunkThis compiles a function/method body. thisFields is non-nil only
// for an object method's direct body, enabling this.field slot access there.
func (c *compiler) compileFunChunkThis(name, doc string, params []string, stmts []ast.Node, thisFields map[string]int32) (int32, error) {
	fc := &compiler{
		chunk:      &Chunk{Name: name, Doc: doc, Params: params},
		parent:     c,
		typeDecls:  make(map[string]*ast.ObjectDecl),
		useSlots:   true,
		debugLines: c.debugLines,
		thisFields: thisFields,
		// Inherit the module's private-global mangling so a reference to a private
		// top-level name inside this function resolves to the same Env key as its
		// definition (see compiler.nsPrefix).
		nsPrefix: c.nsPrefix,
		privTop:  c.privTop,
	}
	if c.debugLines {
		fc.chunk.Lines = []int32{} // non-nil: record a line per instruction
	}
	for k, v := range c.typeDecls {
		fc.typeDecls[k] = v
	}
	// pre-assign param slots 0..numParams-1
	for _, p := range params {
		fc.defineLocal(p)
	}
	for _, s := range stmts {
		if err := fc.compileStmt(s); err != nil {
			return 0, err
		}
	}
	fc.chunk.Emit(vmpackage.OpReturnNull, 0, 0)
	fc.chunk.LocalCount = int(fc.nextSlot)
	fc.chunk.UpvalInfos = make([]UpvalInfo, len(fc.upvals))
	for i, u := range fc.upvals {
		fc.chunk.UpvalInfos[i] = UpvalInfo{IsLocal: u.isLocal, Index: u.index}
	}
	return c.chunk.AddFun(fc.chunk), nil
}

// thisFieldIndex returns the decl-order field index for `obj.name` when obj is
// the `this` identifier inside a method body whose object fields are tracked,
// and ok=false otherwise (the caller then emits the name-based member path).
func (c *compiler) thisFieldIndex(obj ast.Node, name string) (int32, bool) {
	if c.thisFields == nil {
		return 0, false
	}
	if id, ok := obj.(*ast.IdentExpr); !ok || id.Name != "this" {
		return 0, false
	}
	idx, ok := c.thisFields[name]
	return idx, ok
}

func (c *compiler) compileObjectDecl(v *ast.ObjectDecl) error {
	// Field name → declaration index, so a method body can resolve this.field to a
	// fixed slot (instances store fields in this order; see buildObjectVal).
	thisFields := make(map[string]int32, len(v.Fields))
	for i, f := range v.Fields {
		thisFields[f.Name] = int32(i)
	}
	for _, m := range v.Methods {
		idx, err := c.compileFunChunkThis(v.Name+"."+m.Name, m.Doc, m.Params, m.Body.Stmts, thisFields)
		if err != nil {
			return err
		}
		c.chunk.Emit(vmpackage.OpLoadConst, c.nameConst(m.Name), 0)
		c.chunk.Emit(vmpackage.OpNewClosure, idx, 0)
	}
	c.typeDecls[v.Name] = v
	nameIdx := c.nameConst(v.Name)
	// Store the ObjectDecl as a const so the VM can access field info.
	declIdx := c.chunk.AddConst(ObjDeclValue(v))
	c.chunk.Emit(vmpackage.OpNewObject, declIdx, int32(len(v.Methods)))
	c.chunk.Emit(vmpackage.OpDefName, nameIdx, 0)
	if c.depth == 0 {
		if v.IsExported {
			c.chunk.Exports = append(c.chunk.Exports, v.Name)
		} else {
			c.chunk.Private = append(c.chunk.Private, v.Name)
		}
	}
	return nil
}

func (c *compiler) compileEnumDecl(v *ast.EnumDecl) error {
	idx := c.chunk.AddConst(EnumDefValue(v.Name, v.Cases))
	c.chunk.Emit(vmpackage.OpLoadConst, idx, 0)
	c.chunk.Emit(vmpackage.OpDefName, c.nameConst(v.Name), 0)
	if c.depth == 0 {
		if v.IsExported {
			c.chunk.Exports = append(c.chunk.Exports, v.Name)
		} else {
			c.chunk.Private = append(c.chunk.Private, v.Name)
		}
	}
	return nil
}

func (c *compiler) compileExpr(n ast.Node) error {
	if c.debugLines {
		c.chunk.CurLine = int32(ast.NodePos(n).Line)
	}
	switch v := n.(type) {
	case *ast.NullLit:
		c.chunk.Emit(vmpackage.OpLoadNull, 0, 0)
	case *ast.BoolLit:
		if v.Val {
			c.chunk.Emit(vmpackage.OpLoadTrue, 0, 0)
		} else {
			c.chunk.Emit(vmpackage.OpLoadFalse, 0, 0)
		}
	case *ast.IntLit:
		c.chunk.Emit(vmpackage.OpLoadConst, c.chunk.AddConst(IntValue(v.Val)), 0)
	case *ast.FloatLit:
		c.chunk.Emit(vmpackage.OpLoadConst, c.chunk.AddConst(FloatValue(v.Val)), 0)
	case *ast.StringLit:
		c.chunk.Emit(vmpackage.OpLoadConst, c.chunk.AddConst(StrValue(v.Val)), 0)
	case *ast.PatLit:
		// Compile the regex once, at compile time, so a malformed pattern is a
		// compile error and the value lives in the const pool (no per-eval recompile,
		// no new Exec opcode).
		pv, err := PatValue(v.Pattern)
		if err != nil {
			return fmt.Errorf("buzz: line %d:%d: %w", v.Line, v.Col, err)
		}
		c.chunk.Emit(vmpackage.OpLoadConst, c.chunk.AddConst(pv), 0)
	case *ast.InterpExpr:
		return c.compileInterp(v)
	case *ast.IdentExpr:
		// `this` is the bound receiver: it lives in frame.this (set per call from
		// the method's funObj.This), never in a local slot, upvalue, or Env. Emit
		// the dedicated OpLoadThis so a method body reads it with one field load
		// and the call site allocates no Env for it. The checker governs where
		// `this` is legal; outside a method frame.this is Null.
		if v.Name == "this" {
			c.chunk.Emit(vmpackage.OpLoadThis, 0, 0)
			return nil
		}
		if slot := c.resolveLocal(v.Name); slot >= 0 {
			// B carries the slot's static type so FusePeephole can emit a
			// "both-int proven" flag in fused superinstructions, skipping the
			// per-dispatch tag check. Only the four primitive styps are encoded;
			// sUnknown (=0) means "no guarantee" and the flag won't be set.
			// Limitation: valid only for slot-based locals — upvalues, globals,
			// params, call/member/index results stay sUnknown (B=0). Sound because
			// OpCheckType is emitted wherever an any-typed value enters a typed slot.
			c.chunk.Emit(vmpackage.OpGetLocal, slot, int32(c.slotType(slot)))
			return nil
		}
		if c.useSlots {
			if uv := c.resolveUpvalue(v.Name); uv >= 0 {
				c.chunk.Emit(vmpackage.OpGetUpvalue, uv, 0)
				return nil
			}
		}
		c.chunk.Emit(vmpackage.OpLoadName, c.nameConst(c.globalName(v.Name)), 0)
	case *ast.BinaryExpr:
		return c.compileBinary(v)
	case *ast.UnaryExpr:
		return c.compileUnary(v)
	case *ast.CallExpr:
		return c.compileCall(v)
	case *ast.MemberExpr:
		if idx, ok := c.thisFieldIndex(v.Object, v.Name); ok {
			c.chunk.Emit(vmpackage.OpLoadThis, 0, 0)
			c.chunk.Emit(vmpackage.OpGetField, idx, c.nameConst(v.Name))
		} else if idx, ok := c.localObjFieldIndex(v.Object, v.Name); ok {
			if err := c.compileExpr(v.Object); err != nil {
				return err
			}
			c.chunk.Emit(vmpackage.OpGetField, idx, c.nameConst(v.Name))
		} else {
			if err := c.compileExpr(v.Object); err != nil {
				return err
			}
			c.chunk.Emit(vmpackage.OpGetMember, c.nameConst(v.Name), 0)
		}
	case *ast.IndexExpr:
		if err := c.compileExpr(v.Object); err != nil {
			return err
		}
		if err := c.compileExpr(v.Index); err != nil {
			return err
		}
		// A=1 selects the checked subscript (out-of-bounds → null); A=0 errors.
		var opt int32
		if v.Optional {
			opt = 1
		}
		c.chunk.Emit(vmpackage.OpGetIndex, opt, 0)
	case *ast.ForceExpr:
		if err := c.compileExpr(v.Operand); err != nil {
			return err
		}
		// Force-unwrap asserts non-null at runtime, leaving the value in place.
		c.chunk.Emit(vmpackage.OpCheckType, vmpackage.CheckNonNull, 0)
	case *ast.FunExpr:
		idx, err := c.compileFunChunk("<fun>", "", v.Params, v.Body.Stmts)
		if err != nil {
			return err
		}
		c.chunk.Emit(vmpackage.OpNewClosure, idx, 0)
	case *ast.ListExpr:
		for _, item := range v.Items {
			if err := c.compileExpr(item); err != nil {
				return err
			}
		}
		c.chunk.Emit(vmpackage.OpNewList, int32(len(v.Items)), mutFlag(v.Mut))
	case *ast.MapExpr:
		for i, k := range v.Keys {
			if err := c.compileExpr(k); err != nil {
				return err
			}
			if err := c.compileExpr(v.Values[i]); err != nil {
				return err
			}
		}
		c.chunk.Emit(vmpackage.OpNewMap, int32(len(v.Keys)), mutFlag(v.Mut))
	case *ast.ObjectLit:
		return c.compileObjectLit(v)
	case *ast.RangeExpr:
		if err := c.compileExpr(v.Lo); err != nil {
			return err
		}
		if err := c.compileExpr(v.Hi); err != nil {
			return err
		}
		c.chunk.Emit(vmpackage.OpRange, 0, 0)
	case *ast.IsExpr:
		if err := c.compileExpr(v.Expr); err != nil {
			return err
		}
		c.chunk.Emit(vmpackage.OpIs, c.nameConst(v.TypeName), 0)
	case *ast.AsExpr:
		if err := c.compileExpr(v.Expr); err != nil {
			return err
		}
		var opt int32
		if v.Optional {
			opt = 1
		}
		c.chunk.Emit(vmpackage.OpAs, c.nameConst(v.TypeName), opt)
	case *ast.CatchExpr:
		return c.compileCatchExpr(v)
	case *ast.YieldExpr:
		if err := c.compileExpr(v.Value); err != nil {
			return err
		}
		c.chunk.Emit(vmpackage.OpYield, 0, 0)
	case *ast.FiberExpr:
		// Compile callee then args, emit OpFiber(argc).
		if err := c.compileExpr(v.Call.Callee); err != nil {
			return err
		}
		for _, arg := range v.Call.Args {
			if err := c.compileExpr(arg); err != nil {
				return err
			}
		}
		c.chunk.Emit(vmpackage.OpFiber, int32(len(v.Call.Args)), 0)
	case *ast.ResumeExpr:
		// Compile as a call to the session-bound "resume" callable.
		c.chunk.Emit(vmpackage.OpLoadName, c.nameConst("resume"), 0)
		if err := c.compileExpr(v.Fiber); err != nil {
			return err
		}
		c.chunk.Emit(vmpackage.OpCall, 1, 0)
	case *ast.ResolveExpr:
		// Compile as a call to the session-bound "resolve" callable.
		c.chunk.Emit(vmpackage.OpLoadName, c.nameConst("resolve"), 0)
		if err := c.compileExpr(v.Fiber); err != nil {
			return err
		}
		c.chunk.Emit(vmpackage.OpCall, 1, 0)
	default:
		return fmt.Errorf("buzz: compile: unknown expression %T", n)
	}
	return nil
}

func (c *compiler) compileInterp(v *ast.InterpExpr) error {
	if len(v.Parts) == 0 {
		c.chunk.Emit(vmpackage.OpLoadConst, c.chunk.AddConst(StrValue("")), 0)
		return nil
	}
	// Push each part's value; OpBuildStr will convert and join them all at once.
	for _, part := range v.Parts {
		if part.Expr == nil {
			c.chunk.Emit(vmpackage.OpLoadConst, c.chunk.AddConst(StrValue(part.Lit)), 0)
		} else {
			if err := c.compileExpr(part.Expr); err != nil {
				return err
			}
		}
	}
	c.chunk.Emit(vmpackage.OpBuildStr, int32(len(v.Parts)), 0)
	return nil
}

func (c *compiler) compileBinary(v *ast.BinaryExpr) error {
	switch v.Op {
	case "and":
		if err := c.compileExpr(v.Left); err != nil {
			return err
		}
		j := c.chunk.EmitJump(vmpackage.OpJumpFalsePeek)
		c.chunk.Emit(vmpackage.OpPop, 0, 0)
		if err := c.compileExpr(v.Right); err != nil {
			return err
		}
		c.chunk.PatchJump(j)
		return nil
	case "or":
		if err := c.compileExpr(v.Left); err != nil {
			return err
		}
		j := c.chunk.EmitJump(vmpackage.OpJumpTruePeek)
		c.chunk.Emit(vmpackage.OpPop, 0, 0)
		if err := c.compileExpr(v.Right); err != nil {
			return err
		}
		c.chunk.PatchJump(j)
		return nil
	case "??":
		if err := c.compileExpr(v.Left); err != nil {
			return err
		}
		j := c.chunk.EmitJump(vmpackage.OpJumpIfNull)
		if err := c.compileExpr(v.Right); err != nil {
			return err
		}
		c.chunk.PatchJump(j)
		return nil
	}

	if err := c.compileExpr(v.Left); err != nil {
		return err
	}
	if err := c.compileExpr(v.Right); err != nil {
		return err
	}
	switch v.Op {
	case "+":
		c.chunk.Emit(vmpackage.OpAdd, 0, 0)
	case "-":
		c.chunk.Emit(vmpackage.OpSub, 0, 0)
	case "*":
		c.chunk.Emit(vmpackage.OpMul, 0, 0)
	case "/":
		c.chunk.Emit(vmpackage.OpDiv, 0, 0)
	case "%":
		c.chunk.Emit(vmpackage.OpMod, 0, 0)
	case "==":
		c.chunk.Emit(vmpackage.OpEqual, 0, 0)
	case "!=":
		c.chunk.Emit(vmpackage.OpNotEqual, 0, 0)
	case "<":
		c.chunk.Emit(vmpackage.OpLess, 0, 0)
	case "<=":
		c.chunk.Emit(vmpackage.OpLessEqual, 0, 0)
	case ">":
		c.chunk.Emit(vmpackage.OpGreater, 0, 0)
	case ">=":
		c.chunk.Emit(vmpackage.OpGreaterEqual, 0, 0)
	default:
		return fmt.Errorf("buzz: compile: unknown binary op %q", v.Op)
	}
	return nil
}

func (c *compiler) compileUnary(v *ast.UnaryExpr) error {
	if err := c.compileExpr(v.Operand); err != nil {
		return err
	}
	switch v.Op {
	case "-":
		c.chunk.Emit(vmpackage.OpNeg, 0, 0)
	case "!":
		c.chunk.Emit(vmpackage.OpNot, 0, 0)
	default:
		return fmt.Errorf("buzz: compile: unknown unary op %q", v.Op)
	}
	return nil
}

func (c *compiler) compileCall(v *ast.CallExpr) error {
	// Method-call fast path: obj.name(args) compiles to OpInvoke, which resolves
	// the method on the receiver and enters it with the receiver bound as `this`
	// without allocating a bound method value (the bound-funObj copy getMember
	// would make). The receiver is pushed once and reused as the call's base; the
	// VM handler reads the method name from the const pool (A) and arg count (B).
	if m, ok := v.Callee.(*ast.MemberExpr); ok {
		if err := c.compileExpr(m.Object); err != nil {
			return err
		}
		for _, arg := range v.Args {
			if err := c.compileExpr(arg); err != nil {
				return err
			}
		}
		c.chunk.Emit(vmpackage.OpInvoke, c.nameConst(m.Name), int32(len(v.Args)))
		return nil
	}
	if err := c.compileExpr(v.Callee); err != nil {
		return err
	}
	for _, arg := range v.Args {
		if err := c.compileExpr(arg); err != nil {
			return err
		}
	}
	c.chunk.Emit(vmpackage.OpCall, int32(len(v.Args)), 0)
	return nil
}

func (c *compiler) compileObjectLit(v *ast.ObjectLit) error {
	decl, ok := c.typeDecls[v.TypeName]
	if !ok {
		for i, key := range v.Keys {
			c.chunk.Emit(vmpackage.OpLoadConst, c.chunk.AddConst(StrValue(key)), 0)
			if err := c.compileExpr(v.Values[i]); err != nil {
				return err
			}
		}
		c.chunk.Emit(vmpackage.OpNewObject, c.nameConst(v.TypeName), int32(len(v.Keys))|mutFlag(v.Mut))
		return nil
	}

	overrides := make(map[string]ast.Node, len(v.Keys))
	for i, k := range v.Keys {
		overrides[k] = v.Values[i]
	}
	for _, f := range decl.Fields {
		c.chunk.Emit(vmpackage.OpLoadConst, c.chunk.AddConst(StrValue(f.Name)), 0)
		if expr, hasOverride := overrides[f.Name]; hasOverride {
			if err := c.compileExpr(expr); err != nil {
				return err
			}
		} else if f.Default != nil {
			if err := c.compileExpr(f.Default); err != nil {
				return err
			}
		} else {
			c.chunk.Emit(vmpackage.OpLoadNull, 0, 0)
		}
	}
	c.chunk.Emit(vmpackage.OpNewObject, c.nameConst(v.TypeName), int32(len(decl.Fields))|mutFlag(v.Mut))
	return nil
}

// mutFlag returns InstrMutBit when mut is set, for packing into a constructor's
// B operand (OpNewList/OpNewMap/OpNewObject).
func mutFlag(mut bool) int32 {
	if mut {
		return vmpackage.InstrMutBit
	}
	return 0
}
