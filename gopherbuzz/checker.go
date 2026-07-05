package buzz

import (
	"fmt"
	"strings"

	"github.com/egladman/gopherbuzz/ast"
	"github.com/egladman/gopherbuzz/types"
)

// typeError is a type-checking diagnostic.
type typeError struct {
	Line, Col int
	Msg       string
}

func (e typeError) Error() string {
	return fmt.Sprintf("buzz: line %d:%d: %s", e.Line, e.Col, e.Msg)
}

type scopeEntry struct {
	typ     types.Type
	isConst bool
}

type checker struct {
	errors   []typeError
	scopes   []map[string]scopeEntry
	retTyp   types.Type
	yieldTyp types.Type            // non-nil when inside a function with a *> yield annotation
	types    map[string]types.Type // named type definitions (objects, enums)
	// moduleFuncs maps each imported module's bound name to its exported function
	// declarations. collectTopLevel uses this to build a typed namespace ObjectType
	// for the import so qualified access (e.g. state\wm()) resolves precisely.
	moduleFuncs map[string][]*ast.FunDecl
	// private names are visible in a flat-imported module's runtime Env but hidden
	// from this file by exports-only import visibility; referencing one yields an
	// "export it" hint rather than a bare "undefined". See session.importPrivate.
	private map[string]bool
}

// Check type-checks prog after pre-registering extraGlobals as types.Any.
// This allows callers to inject dynamically-defined names (e.g. from SetVal) so the
// checker doesn't flag them as undefined. private names are hidden by exports-only
// import visibility: referencing one is undefined here, but the checker points at
// the missing `export` instead of a bare "undefined".
func checkWithGlobals(prog *ast.Program, extraGlobals []string, imported []ast.Node, moduleFuncs map[string][]*ast.FunDecl, private map[string]bool) []typeError {
	c := &checker{
		types:       map[string]types.Type{},
		moduleFuncs: moduleFuncs,
		private:     private,
	}
	c.pushScope()
	c.registerBuiltins()
	for _, name := range extraGlobals {
		if _, ok := c.scopes[len(c.scopes)-1][name]; !ok {
			c.define(name, types.Unknown, false)
		}
	}
	// Register object/enum types pulled in from flat imports before collecting
	// the current file's top-level names, so the importer can use them in
	// annotations and literals. Same registration as collectTopLevel's
	// Object/Enum cases; field cross-references resolve lazily via resolveType.
	c.registerTypeDecls(imported)
	c.collectTopLevel(prog)
	for _, s := range prog.Stmts {
		c.checkStmt(s)
	}
	return c.errors
}

// registerBuiltins pre-defines the stdlib functions so the checker doesn't
// report them as undefined.
func (c *checker) registerBuiltins() {
	anyRet := &types.FuncType{Params: []types.Type{types.Any}, Ret: types.Any, Variadic: true}
	c.define("print", anyRet, true)
	c.define("str", &types.FuncType{Params: []types.Type{types.Any}, Ret: types.Str}, true)
	c.define("int", &types.FuncType{Params: []types.Type{types.Any}, Ret: types.Int}, true)
	c.define("double", &types.FuncType{Params: []types.Type{types.Any}, Ret: types.Double}, true)
	c.define("bool", &types.FuncType{Params: []types.Type{types.Any}, Ret: types.Bool}, true)
	c.define("len", &types.FuncType{Params: []types.Type{types.Any}, Ret: types.Int}, true)
	c.define("keys", &types.FuncType{Params: []types.Type{types.Any}, Ret: &types.ListType{Elem: types.Str}}, true)
	c.define("values", &types.FuncType{Params: []types.Type{types.Any}, Ret: &types.ListType{Elem: types.Any}}, true)
	c.define("append", anyRet, true)
	c.define("range", anyRet, true)
	c.define("error", anyRet, true)
	c.define("assert", anyRet, true)
	c.define("type", &types.FuncType{Params: []types.Type{types.Any}, Ret: types.Str}, true)
	// resume/resolve are keyword-expressions; they are not callable identifiers.
	// zdef(libname str, cdecl str) → map of direct callables (FFI). Return type
	// is Unknown (not Any) so member access on the returned map doesn't fire E28.
	c.define("zdef", &types.FuncType{Params: []types.Type{types.Str, types.Str}, Ret: types.Unknown}, true)
}

func (c *checker) pushScope() { c.scopes = append(c.scopes, map[string]scopeEntry{}) }
func (c *checker) popScope()  { c.scopes = c.scopes[:len(c.scopes)-1] }

func (c *checker) define(name string, typ types.Type, isConst bool) {
	c.scopes[len(c.scopes)-1][name] = scopeEntry{typ: typ, isConst: isConst}
}

func (c *checker) lookup(name string) (scopeEntry, bool) {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if e, ok := c.scopes[i][name]; ok {
			return e, true
		}
	}
	return scopeEntry{}, false
}

func (c *checker) errorf(p ast.Pos, format string, args ...any) {
	c.errors = append(c.errors, typeError{
		Line: p.Line, Col: p.Col,
		Msg: fmt.Sprintf(format, args...),
	})
}

// collectTopLevel does a first pass to register top-level names so functions
// can reference each other regardless of declaration order.
func (c *checker) collectTopLevel(prog *ast.Program) {
	for _, s := range prog.Stmts {
		switch v := s.(type) {
		case *ast.ImportStmt:
			if v.Alias == "_" {
				break // flat import: nothing bound under a name
			}
			parts := strings.Split(v.Path, "/")
			name := parts[len(parts)-1]
			if v.Alias != "" {
				name = v.Alias
			}
			// If we have exported function signatures for this module, build a
			// typed namespace object so qualified access (e.g. state\wm()) resolves
			// to the declared return type instead of any. This lets the checker
			// propagate types through cross-module calls and enforce E28 correctly.
			if fds, ok := c.moduleFuncs[name]; ok && len(fds) > 0 {
				nt := &types.ObjectType{Name: name, Fields: map[string]types.Type{}, Methods: map[string]*types.FuncType{}, IsNamespace: true}
				for _, fd := range fds {
					nt.Fields[fd.Name] = c.funDeclType(fd)
				}
				c.define(name, nt, false)
			} else {
				// No tracked function signatures (synthetic module or no exported funs):
				// use Unknown so member access on the namespace doesn't fire E28.
				c.define(name, types.Unknown, false)
			}
		case *ast.FunDecl:
			c.define(v.Name, c.funDeclType(v), true)
		case *ast.ObjectDecl:
			c.registerTypeDecls([]ast.Node{v})
		case *ast.EnumDecl:
			c.registerTypeDecls([]ast.Node{v})
		case *ast.ExprStmt:
			// A top-level `zdef("lib", "<decls>")` declares its symbols as free
			// functions (upstream Buzz semantics; the compiler binds them as
			// globals). Pre-declare each as a lenient variadic callable so bare
			// calls type-check regardless of arity or argument labels.
			if _, names, ok := zdefDeclNames(v.Expr); ok {
				// zdef symbols are FFI callables whose return types can't be tracked
				// statically; return Unknown so field access on their results doesn't
				// fire E28 (Unknown is the tracking-failure sentinel, not user `any`).
				ffiFn := &types.FuncType{Params: []types.Type{types.Unknown}, Ret: types.Unknown, Variadic: true}
				for _, name := range names {
					c.define(name, ffiFn, true)
				}
			}
		}
	}
}

// registerTypeDecls records each object/enum declaration as a named type
// (resolvable in annotations) and binds its name in the current scope (so it
// can be referenced as an object/enum-def value). Shared by collectTopLevel
// (current-file decls) and checkWithGlobals (flat-imported decls).
func (c *checker) registerTypeDecls(decls []ast.Node) {
	for _, d := range decls {
		switch v := d.(type) {
		case *ast.ObjectDecl:
			ot := c.buildObjectType(v)
			c.types[v.Name] = ot
			c.define(v.Name, ot, true)
		case *ast.EnumDecl:
			et := &types.EnumType{Name: v.Name, Cases: v.Cases}
			c.types[v.Name] = et
			c.define(v.Name, et, true)
		}
	}
}

func (c *checker) buildObjectType(v *ast.ObjectDecl) *types.ObjectType {
	ot := &types.ObjectType{Name: v.Name, Fields: map[string]types.Type{}, Methods: map[string]*types.FuncType{}}
	for _, f := range v.Fields {
		ot.Fields[f.Name] = types.ParseAnnot(f.TypeAnnot)
	}
	for _, m := range v.Methods {
		ot.Methods[m.Name] = c.funDeclType(m)
	}
	return ot
}

func (c *checker) funDeclType(fd *ast.FunDecl) *types.FuncType {
	params := make([]types.Type, len(fd.Params))
	for i := range fd.Params {
		pt := types.Unknown // unannotated: tracking failure, not explicit any
		if i < len(fd.ParamAnnots) && fd.ParamAnnots[i] != "" {
			pt = c.resolveAnnot(fd.ParamAnnots[i])
		}
		params[i] = pt
	}
	ret := types.Unknown // unannotated: accept any return
	if fd.RetAnnot != "" {
		ret = c.resolveAnnot(fd.RetAnnot)
	}
	var yield types.Type
	if fd.YieldAnnot != "" {
		yield = c.resolveAnnot(fd.YieldAnnot)
	}
	return &types.FuncType{Params: params, Ret: ret, Yield: yield, ParamNames: fd.Params}
}

// resolveAnnot parses a type annotation string and resolves NamedType references.
func (c *checker) resolveAnnot(s string) types.Type {
	t := types.ParseAnnot(s)
	return c.resolveType(t)
}

func (c *checker) resolveType(t types.Type) types.Type {
	switch v := t.(type) {
	case *types.NamedType:
		if resolved, ok := c.types[v.Name]; ok {
			return resolved
		}
		return v
	case *types.ListType:
		return &types.ListType{Elem: c.resolveType(v.Elem)}
	case *types.MapType:
		return &types.MapType{Key: c.resolveType(v.Key), Val: c.resolveType(v.Val)}
	case *types.FuncType:
		params := make([]types.Type, len(v.Params))
		for i, p := range v.Params {
			params[i] = c.resolveType(p)
		}
		return &types.FuncType{Params: params, Ret: c.resolveType(v.Ret)}
	}
	return t
}

func (c *checker) checkStmt(n ast.Node) {
	switch v := n.(type) {
	case *ast.ImportStmt, *ast.NamespaceStmt:
		// already handled in collectTopLevel (or purely syntactic)
	case *ast.DeclStmt:
		c.checkDecl(v)
	case *ast.AssignStmt:
		c.checkAssign(v)
	case *ast.ExprStmt:
		c.infer(v.Expr)
	case *ast.ReturnStmt:
		c.checkReturn(v)
	case *ast.BlockStmt:
		c.pushScope()
		for _, s := range v.Stmts {
			c.checkStmt(s)
		}
		c.popScope()
	case *ast.IfStmt:
		c.checkIf(v)
	case *ast.WhileStmt:
		cond := c.infer(v.Cond)
		if cond != types.Any && cond != types.Unknown && cond != types.Bool {
			c.errorf(ast.NodePos(v.Cond), "while condition must be bool, got %s", cond.TypeName())
		}
		c.checkBlock(v.Body)
	case *ast.DoStmt:
		c.checkBlock(v.Body)
		cond := c.infer(v.Cond)
		if cond != types.Any && cond != types.Unknown && cond != types.Bool {
			c.errorf(ast.NodePos(v.Cond), "do-until condition must be bool, got %s", cond.TypeName())
		}
	case *ast.ForStmt:
		c.pushScope()
		if v.Init != nil {
			c.checkStmt(v.Init)
		}
		if v.Cond != nil {
			cond := c.infer(v.Cond)
			if cond != types.Any && cond != types.Unknown && cond != types.Bool {
				c.errorf(ast.NodePos(v.Cond), "for condition must be bool, got %s", cond.TypeName())
			}
		}
		if v.Post != nil {
			c.checkStmt(v.Post)
		}
		for _, s := range v.Body.Stmts {
			c.checkStmt(s)
		}
		c.popScope()
	case *ast.ForEachStmt:
		c.checkForEach(v)
	case *ast.FunDecl:
		c.checkFunDecl(v)
	case *ast.TestDecl:
		// A test block body is checked in its own scope, like a void function body.
		c.checkBlock(v.Body)
	case *ast.ObjectDecl:
		c.checkObjectDecl(v)
	case *ast.EnumDecl:
		// already collected in first pass; nothing else to check
	case *ast.BreakStmt, *ast.ContinueStmt:
		// nothing
	case *ast.TryStmt:
		c.checkBlock(v.Body)
		c.pushScope()
		c.define(v.ErrName, types.Any, false)
		for _, s := range v.Catch.Stmts {
			c.checkStmt(s)
		}
		c.popScope()
	case *ast.ThrowStmt:
		c.infer(v.Value)
	}
}

func (c *checker) checkDecl(v *ast.DeclStmt) {
	inferred := c.infer(v.Value)
	declTyp := inferred
	if v.TypeAnnot != "" {
		annotTyp := c.resolveAnnot(v.TypeAnnot)
		if !types.Compat(inferred, annotTyp) {
			c.errorf(v.Pos, "cannot assign %s to %s variable %q",
				inferred.TypeName(), annotTyp.TypeName(), v.Name)
		}
		declTyp = annotTyp
	}
	c.define(v.Name, declTyp, v.IsConst)
}

func (c *checker) checkAssign(v *ast.AssignStmt) {
	if id, ok := v.Target.(*ast.IdentExpr); ok {
		// `_` is the discard target: accept any value and bind nothing.
		if id.Name == "_" {
			c.infer(v.Value)
			return
		}
		if e, found := c.lookup(id.Name); found && e.isConst {
			c.errorf(id.Pos, "cannot assign to final %q", id.Name)
		} else if found {
			rhs := c.infer(v.Value)
			if !types.Compat(rhs, e.typ) {
				c.errorf(v.Pos, "cannot assign %s to %s", rhs.TypeName(), e.typ.TypeName())
			}
			return
		}
	}
	c.infer(v.Target)
	c.infer(v.Value)
}

func (c *checker) checkReturn(v *ast.ReturnStmt) {
	if v.Value == nil {
		return
	}
	if c.retTyp == types.Void {
		c.errorf(v.Pos, "void function cannot return a value")
		return
	}
	ret := c.infer(v.Value)
	// Skip return type check for fiber functions (fib<V,R> annotations or *> syntax):
	// the declared return type in these cases represents the fiber value type, not
	// the checked function return type.
	_, retIsFibType := c.retTyp.(*types.FibType)
	if c.retTyp != nil && c.retTyp != types.Any && c.retTyp != types.Fib && !retIsFibType && c.yieldTyp == nil && !types.Compat(ret, c.retTyp) {
		c.errorf(v.Pos, "return type mismatch: got %s, want %s",
			ret.TypeName(), c.retTyp.TypeName())
	}
}

func (c *checker) checkIf(v *ast.IfStmt) {
	cond := c.infer(v.Cond)
	if v.BindName != "" {
		// Optional-call narrowing: `if (opt -> name)` binds name to opt's non-null
		// value inside Then. Optionals are erased to their base type in this
		// checker, so the inferred cond type is name's type; no bool check applies.
		c.pushScope()
		c.define(v.BindName, cond, false)
		for _, s := range v.Then.Stmts {
			c.checkStmt(s)
		}
		c.popScope()
		if v.Else != nil {
			c.checkStmt(v.Else)
		}
		return
	}
	if cond != types.Any && cond != types.Unknown && cond != types.Bool {
		c.errorf(ast.NodePos(v.Cond), "if condition must be bool, got %s", cond.TypeName())
	}
	c.checkBlock(v.Then)
	if v.Else != nil {
		c.checkStmt(v.Else)
	}
}

func (c *checker) checkBlock(b *ast.BlockStmt) {
	c.pushScope()
	for _, s := range b.Stmts {
		c.checkStmt(s)
	}
	c.popScope()
}

func (c *checker) checkForEach(v *ast.ForEachStmt) {
	iterTyp := c.infer(v.Iter)
	// Default to Unknown (tracking failure) so that iterating an unresolved
	// iterable (e.g. a string method whose return type we don't track) doesn't
	// assign Any to the loop variable and trigger spurious E28 errors.
	valTyp, keyTyp := types.Unknown, types.Unknown
	switch it := iterTyp.(type) {
	case *types.ListType:
		valTyp = it.Elem
		keyTyp = types.Int
	case *types.MapType:
		keyTyp = it.Key
		valTyp = it.Val
	case *types.PrimitiveType:
		if it.Name == "rng" {
			valTyp = types.Int
			keyTyp = types.Int
		}
	case *types.FibType:
		// foreach over a fiber binds each yielded value.
		valTyp = it.Yield
		keyTyp = types.Int
	}
	c.pushScope()
	c.define(v.ValName, valTyp, false)
	if v.KeyName != "" {
		c.define(v.KeyName, keyTyp, false)
	}
	for _, s := range v.Body.Stmts {
		c.checkStmt(s)
	}
	c.popScope()
}

func (c *checker) checkFunDecl(fd *ast.FunDecl) {
	ft := c.funDeclType(fd)
	// Re-register in current scope (may be a nested function not seen in first pass).
	c.define(fd.Name, ft, true)

	savedRet := c.retTyp
	savedYield := c.yieldTyp
	c.retTyp = ft.Ret
	if fd.YieldAnnot != "" {
		c.yieldTyp = c.resolveAnnot(fd.YieldAnnot)
	} else {
		c.yieldTyp = nil
	}
	c.pushScope()
	c.define("this", types.Unknown, false)
	for i, name := range fd.Params {
		pt := types.Unknown
		if i < len(ft.Params) {
			pt = ft.Params[i]
		}
		c.define(name, pt, false)
	}
	for _, s := range fd.Body.Stmts {
		c.checkStmt(s)
	}
	c.popScope()
	c.retTyp = savedRet
	c.yieldTyp = savedYield
}

func (c *checker) checkObjectDecl(v *ast.ObjectDecl) {
	ot, _ := c.types[v.Name].(*types.ObjectType)
	if ot == nil {
		ot = c.buildObjectType(v)
	}
	for _, m := range v.Methods {
		ft := c.funDeclType(m)
		savedRet := c.retTyp
		c.retTyp = ft.Ret
		c.pushScope()
		c.define("this", ot, false)
		for i, name := range m.Params {
			pt := types.Unknown
			if i < len(ft.Params) {
				pt = ft.Params[i]
			}
			c.define(name, pt, false)
		}
		for _, s := range m.Body.Stmts {
			c.checkStmt(s)
		}
		c.popScope()
		c.retTyp = savedRet
	}
}

// infer returns the inferred types.Type of expression n.
func (c *checker) infer(n ast.Node) types.Type {
	if n == nil {
		return types.Any
	}
	switch v := n.(type) {
	case *ast.IntLit:
		return types.Int
	case *ast.FloatLit:
		return types.Double
	case *ast.StringLit:
		return types.Str
	case *ast.BoolLit:
		return types.Bool
	case *ast.NullLit:
		return types.Null
	case *ast.InterpExpr:
		for _, part := range v.Parts {
			if part.Expr != nil {
				c.infer(part.Expr)
			}
		}
		return types.Str
	case *ast.IdentExpr:
		return c.inferIdent(v)
	case *ast.BinaryExpr:
		return c.inferBinary(v)
	case *ast.UnaryExpr:
		return c.inferUnary(v)
	case *ast.CallExpr:
		return c.inferCall(v)
	case *ast.MemberExpr:
		return c.inferMember(v)
	case *ast.IndexExpr:
		return c.inferIndex(v)
	case *ast.ForceExpr:
		// Optionals are erased to their base type in this checker, so force-unwrap
		// reports the operand's type unchanged.
		return c.infer(v.Operand)
	case *ast.PatLit:
		return types.Pat
	case *ast.FunExpr:
		return c.inferFunExpr(v)
	case *ast.MapExpr:
		return c.inferMapExpr(v)
	case *ast.ListExpr:
		return c.inferListExpr(v)
	case *ast.ObjectLit:
		return c.inferObjectLit(v)
	case *ast.RangeExpr:
		c.infer(v.Lo)
		c.infer(v.Hi)
		return types.Rng
	case *ast.IsExpr:
		c.infer(v.Expr)
		return types.Bool
	case *ast.AsExpr:
		c.infer(v.Expr)
		return c.resolveAnnot(v.TypeName)
	case *ast.CatchExpr:
		// `expr catch default` evaluates to expr's success type; infer the default
		// too so type errors inside it still surface.
		t := c.infer(v.Expr)
		c.infer(v.Default)
		return t
	case *ast.YieldExpr:
		vt := c.infer(v.Value)
		if c.yieldTyp != nil && !types.Compat(vt, c.yieldTyp) {
			c.errorf(v.Pos, "yield type mismatch: got %s, want %s", vt.TypeName(), c.yieldTyp.TypeName())
		}
		return types.Null // yield expression evaluates to null (the resumed value)
	case *ast.FiberExpr:
		calleeTyp := c.infer(v.Call.Callee)
		if ft, ok := calleeTyp.(*types.FuncType); ok {
			c.resolveNamedArgs(v.Call, ft)
		} else {
			v.Call.ArgNames = nil
		}
		for _, a := range v.Call.Args {
			c.infer(a)
		}
		ft, ok := calleeTyp.(*types.FuncType)
		if !ok {
			return types.Fib // callee type unknown (any) — leave the fiber untyped
		}
		if !ft.Variadic && len(v.Call.Args) != len(ft.Params) {
			c.errorf(v.Pos, "wrong argument count: got %d, want %d", len(v.Call.Args), len(ft.Params))
		}
		// Recover the fiber's yield/return types from the wrapped function so
		// `resume`/`resolve` on this inline fiber are typed (not just `any`).
		yield := ft.Yield
		if yield == nil {
			yield = types.Any
		}
		return &types.FibType{Yield: yield, Return: ft.Ret}
	case *ast.ResumeExpr:
		fibTyp := c.infer(v.Fiber)
		if ft, ok := fibTyp.(*types.FibType); ok {
			return ft.Yield
		}
		return types.Any
	case *ast.ResolveExpr:
		fibTyp := c.infer(v.Fiber)
		if ft, ok := fibTyp.(*types.FibType); ok {
			return ft.Return
		}
		return types.Any
	default:
		return types.Any
	}
}

func (c *checker) inferIdent(v *ast.IdentExpr) types.Type {
	if e, ok := c.lookup(v.Name); ok {
		return e.typ
	}
	if c.private[v.Name] {
		c.errorf(v.Pos, "undefined: %s (an imported module declares %q but does not export it — add `export` to it)", v.Name, v.Name)
	} else {
		c.errorf(v.Pos, "undefined: %s", v.Name)
	}
	return types.Any
}

func (c *checker) inferBinary(v *ast.BinaryExpr) types.Type {
	left := c.infer(v.Left)
	right := c.infer(v.Right)
	switch v.Op {
	case "+":
		if left == types.Str || right == types.Str {
			return types.Str
		}
		if _, ok := left.(*types.ListType); ok {
			return left // list concatenation: [T] + ... → [T]
		}
		if _, ok := right.(*types.ListType); ok {
			return right
		}
		return c.numericResult(v.Pos, left, right)
	case "-", "*", "%":
		return c.numericResult(v.Pos, left, right)
	case "/":
		if left == types.Double || right == types.Double {
			return types.Double
		}
		if left == types.Unknown || right == types.Unknown {
			return types.Unknown
		}
		return types.Int
	case "<", ">", "<=", ">=", "==", "!=":
		return types.Bool
	case "and", "or":
		return types.Bool
	case "??":
		if left != types.Null && left != types.Any && left != types.Unknown {
			return left
		}
		return right
	}
	return types.Any
}

func (c *checker) numericResult(p ast.Pos, left, right types.Type) types.Type {
	if left == types.Any || left == types.Unknown || right == types.Any || right == types.Unknown {
		return types.Unknown
	}
	if left == types.Double || right == types.Double {
		return types.Double
	}
	if left == types.Int && right == types.Int {
		return types.Int
	}
	if left != types.Int && left != types.Double {
		c.errorf(p, "invalid type %s in arithmetic expression", left.TypeName())
	}
	return types.Any
}

func (c *checker) inferUnary(v *ast.UnaryExpr) types.Type {
	t := c.infer(v.Operand)
	switch v.Op {
	case "-":
		if t == types.Any || t == types.Unknown || t == types.Int || t == types.Double {
			return t
		}
		c.errorf(v.Pos, "unary - requires numeric operand, got %s", t.TypeName())
		return types.Any
	case "!":
		return types.Bool
	}
	return types.Any
}

func (c *checker) inferCall(v *ast.CallExpr) types.Type {
	calleeTyp := c.infer(v.Callee)
	ft, ok := calleeTyp.(*types.FuncType)
	if ok {
		c.resolveNamedArgs(v, ft)
	} else {
		// Dynamic callee (any-typed value, host function): labels cannot be
		// resolved, so arguments pass in written order. Upstream-style call
		// sites write them in declaration order, which makes this correct.
		v.ArgNames = nil
	}
	for _, a := range v.Args {
		c.infer(a)
	}
	// An explicit generic type argument (`buf.readZAt::<double>(...)`) names the
	// call's result type. gopherbuzz doesn't model generic signatures, so without
	// this the result would be `any`; honoring the hint matches upstream Buzz.
	if v.TypeArg != "" {
		return c.resolveAnnot(v.TypeArg)
	}
	if !ok {
		return types.Unknown
	}
	if !ft.Variadic && len(v.Args) != len(ft.Params) {
		c.errorf(v.Pos, "wrong argument count: got %d, want %d", len(v.Args), len(ft.Params))
	}
	if ft.Ret == nil || ft.Ret == types.Void {
		return types.Void
	}
	return ft.Ret
}

// resolveNamedArgs reorders a call's labeled arguments (upstream Buzz's
// `f(a: 1, b: 2)` syntax) into the callee's declared parameter order, so the
// compiler and VM only ever see positional calls. Positional arguments fill
// parameter slots left to right and must precede named ones; every problem —
// an unknown or duplicate label, a label colliding with a positional slot, a
// missing parameter — is a checker error at the call site.
func (c *checker) resolveNamedArgs(v *ast.CallExpr, ft *types.FuncType) {
	if v.ArgNames == nil {
		return
	}
	defer func() { v.ArgNames = nil }()
	if len(ft.ParamNames) == 0 || ft.Variadic {
		// No declared names to resolve against (builtins, variadics): written
		// order stands, mirroring the dynamic-callee rule.
		return
	}
	n := len(ft.ParamNames)
	slots := make([]ast.Node, n)
	filled := make([]bool, n)
	sawNamed := false
	pos := 0
	for i, arg := range v.Args {
		name := v.ArgNames[i]
		if name == "" {
			if sawNamed {
				c.errorf(v.Pos, "positional argument after named argument")
				return
			}
			if pos >= n {
				c.errorf(v.Pos, "wrong argument count: got %d, want %d", len(v.Args), n)
				return
			}
			slots[pos] = arg
			filled[pos] = true
			pos++
			continue
		}
		sawNamed = true
		idx := -1
		for j, pn := range ft.ParamNames {
			if pn == name {
				idx = j
				break
			}
		}
		if idx < 0 {
			c.errorf(v.Pos, "unknown argument name %q (parameters are %s)", name, strings.Join(ft.ParamNames, ", "))
			return
		}
		if filled[idx] {
			c.errorf(v.Pos, "argument %q given more than once", name)
			return
		}
		slots[idx] = arg
		filled[idx] = true
	}
	for j, ok := range filled {
		if !ok {
			c.errorf(v.Pos, "missing argument %q", ft.ParamNames[j])
			return
		}
	}
	v.Args = slots
}

func (c *checker) inferMember(v *ast.MemberExpr) types.Type {
	ot := c.infer(v.Object)
	// Resolve NamedType before the Any check: a field typed as Foo (unresolved
	// at buildObjectType time) may be resolvable here. An unresolvable NamedType
	// (e.g. Boxed from a synthetic Go module) returns Unknown rather than Any so
	// chained member access on synthetic-module values doesn't fire E28.
	if nt, ok := ot.(*types.NamedType); ok {
		if resolved, ok2 := c.types[nt.Name]; ok2 {
			ot = resolved
		} else {
			return types.Unknown
		}
	}
	// E28: upstream buzz rejects field access on explicitly `any`-typed values.
	// Unknown (tracking failure) passes through so zdef/host results stay quiet.
	if ot == types.Any {
		c.errorf(v.Pos, "`any` is not field accessible")
		return types.Any
	}
	switch t := ot.(type) {
	case *types.ObjectType:
		if ft, ok := t.Fields[v.Name]; ok {
			return ft
		}
		if mt, ok := t.Methods[v.Name]; ok {
			return mt
		}
		// Namespace objects (built from imported module exports) may have
		// untracked exported finals/vars; treat missing fields as Unknown.
		if t.IsNamespace {
			return types.Unknown
		}
		c.errorf(v.Pos, "object %s has no field or method %q", t.Name, v.Name)
		return types.Unknown
	case *types.ListType:
		if v.Name == "len" {
			return types.Int
		}
		return types.Unknown
	case *types.MapType:
		if v.Name == "len" {
			return types.Int
		}
		return types.Unknown
	case *types.EnumType:
		for _, cas := range t.Cases {
			if cas == v.Name {
				return t
			}
		}
		c.errorf(v.Pos, "enum %s has no case %q", t.Name, v.Name)
		return types.Unknown
	}
	return types.Unknown
}

func (c *checker) inferIndex(v *ast.IndexExpr) types.Type {
	ot := c.infer(v.Object)
	c.infer(v.Index)
	switch t := ot.(type) {
	case *types.ListType:
		return t.Elem
	case *types.MapType:
		return t.Val
	}
	return types.Unknown
}

func (c *checker) inferFunExpr(v *ast.FunExpr) types.Type {
	params := make([]types.Type, len(v.Params))
	for i := range v.Params {
		pt := types.Unknown
		if i < len(v.ParamAnnots) && v.ParamAnnots[i] != "" {
			pt = c.resolveAnnot(v.ParamAnnots[i])
		}
		params[i] = pt
	}
	ret := types.Unknown // unannotated: accept any return
	if v.RetAnnot != "" {
		ret = c.resolveAnnot(v.RetAnnot)
	}
	var yield types.Type
	if v.YieldAnnot != "" {
		yield = c.resolveAnnot(v.YieldAnnot)
	}

	savedRet := c.retTyp
	savedYield := c.yieldTyp
	c.retTyp = ret
	c.yieldTyp = yield
	c.pushScope()
	for i, name := range v.Params {
		c.define(name, params[i], false)
	}
	for _, s := range v.Body.Stmts {
		c.checkStmt(s)
	}
	c.popScope()
	c.retTyp = savedRet
	c.yieldTyp = savedYield

	return &types.FuncType{Params: params, Ret: ret, Yield: yield, ParamNames: v.Params}
}

func (c *checker) inferMapExpr(v *ast.MapExpr) types.Type {
	if len(v.Keys) == 0 {
		return &types.MapType{Key: types.Str, Val: types.Any}
	}
	keyTyp := c.infer(v.Keys[0])
	valTyp := c.infer(v.Values[0])
	for i := 1; i < len(v.Keys); i++ {
		c.infer(v.Keys[i])
		c.infer(v.Values[i])
	}
	return &types.MapType{Key: keyTyp, Val: valTyp}
}

func (c *checker) inferListExpr(v *ast.ListExpr) types.Type {
	if len(v.Items) == 0 {
		if v.ElemType != "" {
			return &types.ListType{Elem: c.resolveAnnot(v.ElemType)}
		}
		return &types.ListType{Elem: types.Any}
	}
	elemTyp := c.infer(v.Items[0])
	for _, item := range v.Items[1:] {
		c.infer(item)
	}
	return &types.ListType{Elem: elemTyp}
}

func (c *checker) inferObjectLit(v *ast.ObjectLit) types.Type {
	resolved, ok := c.types[v.TypeName]
	if !ok {
		c.errorf(v.Pos, "undefined type %q", v.TypeName)
		return types.Any
	}
	ot, ok := resolved.(*types.ObjectType)
	if !ok {
		c.errorf(v.Pos, "%s is not an object type", v.TypeName)
		return types.Any
	}
	for i, key := range v.Keys {
		if _, exists := ot.Fields[key]; !exists {
			c.errorf(v.Pos, "object %s has no field %q", v.TypeName, key)
		}
		if i < len(v.Values) {
			c.infer(v.Values[i])
		}
	}
	return ot
}
