package buzz

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/diagnostics"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/egladman/magus/libs/gopherbuzz/types"
)

// typeError is a type-checking diagnostic. Code is its BZZ diagnostic code, or empty for an error kind that
// has not been given a specific code (which then renders as a plain message, no code, no docs link).
// Severity's zero value is SeverityError, so every existing construction of typeError (the checker never
// sets it) keeps meaning "error" unchanged; only the parser's unused-import warning sets SeverityWarning.
type typeError struct {
	Line, Col int
	Code      diagnostics.Code
	Msg       string
	Severity  Severity
}

func (e typeError) Error() string {
	msg := fmt.Sprintf("buzz: line %d:%d: %s", e.Line, e.Col, e.Msg)
	if e.Severity == SeverityWarning {
		msg = fmt.Sprintf("buzz: line %d:%d: warning: %s", e.Line, e.Col, e.Msg)
	}
	if e.Code == "" {
		return msg
	}
	return fmt.Sprintf("[%s] %s\n  see: %s", e.Code, msg, bzz.URL(e.Code))
}

type scopeEntry struct {
	typ     types.Type
	isConst bool
}

type checker struct {
	errors   []typeError
	scopes   []map[string]scopeEntry
	retTyp   types.Type
	yieldTyp types.Type // non-nil when inside a function with a *> yield annotation
	// raiseDeclared is true while checking the body of a function that declared
	// !> - a call to a raising function is legal there without a surrounding
	// try/catch, because the caller's own caller must handle it (or itself
	// propagate). Set per-FunDecl in checkFunDecl, alongside retTyp/yieldTyp.
	raiseDeclared bool
	// catchDepth counts enclosing try/catch bodies and catch-expression
	// operands (`expr catch default`). A call to a raising function is legal
	// inside either without the enclosing function declaring !>, since the
	// error is handled right there rather than propagated.
	catchDepth int
	types      map[string]types.Type // named type definitions (objects, enums)
	// expected is the stack of types expected at the position being inferred; see
	// inferExpected. Empty outside any annotated context.
	expected []types.Type
	// moduleFuncs maps each imported module's bound name to its exported function
	// declarations. collectTopLevel uses this to build a typed namespace ObjectType
	// for the import so qualified access (e.g. state\wm()) resolves precisely.
	moduleFuncs map[string][]*ast.FunDecl
	// moduleTypes holds each imported module's exported object/enum declarations,
	// so a namespace object can carry them as fields (`io\File`, `io\FileMode`).
	moduleTypes map[string][]ast.Node
	// enumNS maps an enum's bare name to the namespace it is reachable through, for
	// a module imported without flattening. See ast.EnumCaseExpr.EnumNS.
	enumNS map[string]string
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
func checkWithGlobals(prog *ast.Program, extraGlobals []string, imported []ast.Node, moduleFuncs map[string][]*ast.FunDecl, moduleTypes map[string][]ast.Node, private map[string]bool) []typeError {
	c := &checker{
		types:       map[string]types.Type{},
		moduleFuncs: moduleFuncs,
		moduleTypes: moduleTypes,
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

// errorf records a type error with NO code (an unclassified kind). Sites with a specific documented code
// call errorfc instead.
func (c *checker) errorf(p ast.Pos, format string, args ...any) {
	c.errorfc(p, "", format, args...)
}

// errorfc records a type error under a specific BZZ code.
func (c *checker) errorfc(p ast.Pos, code diagnostics.Code, format string, args ...any) {
	c.errors = append(c.errors, typeError{
		Line: p.Line, Col: p.Col, Code: code,
		Msg: fmt.Sprintf(format, args...),
	})
}

// collectTopLevel does a first pass to register top-level names so functions
// can reference each other regardless of declaration order.
func (c *checker) collectTopLevel(prog *ast.Program) {
	for _, s := range prog.Stmts {
		switch v := s.(type) {
		case *ast.ImportStmt:
			// The module's lookup name is the path's last segment with the `buzz:` stdlib
			// scheme stripped, which is the same bare name resolveImport binds under.
			parts := strings.Split(strings.TrimPrefix(v.Path, "buzz:"), "/")
			name := parts[len(parts)-1]
			if v.Alias == "_" || len(v.Only) > 0 {
				// A flat or selective import binds members UNPREFIXED, so their signatures
				// have to be defined under their bare names. The session already splats the
				// values into the env, so without this the names resolve but carry no
				// parameter types - and an inferred enum case in an argument (`hash(.Md5,
				// ...)`) has nothing to resolve against.
				wanted := map[string]bool{}
				for _, n := range v.Only {
					wanted[n] = true
				}
				for _, fd := range c.moduleFuncs[name] {
					if len(wanted) > 0 && !wanted[fd.Name] {
						continue
					}
					c.define(fd.Name, c.funDeclType(fd), true)
				}
				break
			}
			if v.Alias != "" {
				name = v.Alias
			}
			// If we have exported function signatures for this module, build a
			// typed namespace object so qualified access (e.g. state\wm()) resolves
			// to the declared return type instead of any. This lets the checker
			// propagate types through cross-module calls and enforce E28 correctly.
			fds := c.moduleFuncs[name]
			decls := c.moduleTypes[name]
			if len(fds) > 0 || len(decls) > 0 {
				nt := &types.ObjectType{Name: name, Fields: map[string]types.Type{}, Methods: map[string]*types.FuncType{}, IsNamespace: true}
				for _, fd := range fds {
					nt.Fields[fd.Name] = c.funDeclType(fd)
				}
				// An exported TYPE is reachable through the namespace too, as the type value
				// itself, so `io\File.open(...)` resolves the same static method a bare
				// `File.open(...)` does - and `io\FileMode.read` the same case.
				for _, d := range decls {
					switch v := d.(type) {
					case *ast.ObjectDecl:
						nt.Fields[v.Name] = c.buildObjectType(v)
					case *ast.EnumDecl:
						nt.Fields[v.Name] = &types.EnumType{Name: v.Name, Cases: v.Cases}
						// The enum's VALUE lives behind the namespace, so a resolved case has to
						// compile to `ns\Enum.case` rather than the bare name the checker uses.
						if c.enumNS == nil {
							c.enumNS = map[string]string{}
						}
						c.enumNS[v.Name] = name
					}
				}
				c.define(name, nt, false)
			} else {
				// No tracked function signatures (native module or no exported funs):
				// use Unknown so member access on the namespace doesn't fire E28.
				c.define(name, types.Unknown, false)
			}
		case *ast.FunDecl:
			c.define(v.Name, c.funDeclType(v), true)
		case *ast.DeclStmt:
			// Hoist top-level variables so a body may reference one declared LATER in
			// the file -- what upstream calls a placeholder. `test` blocks and function
			// bodies only run after the whole file has executed, so a forward reference
			// from inside one is resolved by the time it matters; refusing it statically
			// is what blocked upstream source that puts its `final` after the tests.
			//
			// Only the annotation is trusted here. Inferring the initializer would mean
			// evaluating expressions whose own dependencies are not collected yet, so an
			// unannotated placeholder stays Unknown (the tracking-failure sentinel, which
			// suppresses member-access errors rather than asserting a wrong type). Either
			// way checkDecl re-defines the name with its precise type when the
			// declaration itself is reached, so only earlier references see this entry.
			typ := types.Type(types.Unknown)
			if v.TypeAnnot != "" {
				typ = c.resolveAnnot(v.TypeAnnot)
			}
			c.define(v.Name, typ, v.IsConst)
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
	ot := &types.ObjectType{
		Name: v.Name, Fields: map[string]types.Type{}, Methods: map[string]*types.FuncType{},
		IsProtocol: v.IsProtocol, Conforms: v.Conforms,
	}
	for _, f := range v.Fields {
		ot.Fields[f.Name] = types.ParseAnnot(f.TypeAnnot)
	}
	// Static fields share the instance field map. The object type is the type of
	// both the type value (Foo.next) and an instance, so one map answers both, at
	// the cost of letting an instance name a static too - the same latitude the
	// enum member path already takes.
	for _, f := range v.StaticFields {
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
	return &types.FuncType{Params: params, Ret: ret, Yield: yield, Raises: fd.ErrAnnot != "", ParamNames: fd.Params, ParamDefaults: fd.ParamDefaults}
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
			c.errorfc(ast.NodePos(v.Cond), NonBoolCondition, "while condition must be bool, got %s", cond.TypeName())
		}
		c.checkBlock(v.Body)
	case *ast.DoStmt:
		c.checkBlock(v.Body)
		cond := c.infer(v.Cond)
		if cond != types.Any && cond != types.Unknown && cond != types.Bool {
			c.errorfc(ast.NodePos(v.Cond), NonBoolCondition, "do-until condition must be bool, got %s", cond.TypeName())
		}
	case *ast.ForStmt:
		c.pushScope()
		for _, init := range v.Init {
			c.checkStmt(init)
		}
		if v.Cond != nil {
			cond := c.infer(v.Cond)
			if cond != types.Any && cond != types.Unknown && cond != types.Bool {
				c.errorfc(ast.NodePos(v.Cond), NonBoolCondition, "for condition must be bool, got %s", cond.TypeName())
			}
		}
		for _, post := range v.Post {
			c.checkStmt(post)
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
		// It also runs like an implicit catch: the runner (runTests in
		// cmd/buzz/main.go) invokes the compiled closure and reports any raised
		// error as a FAIL rather than propagating it, so a raising call inside a
		// test needs no !> declaration or explicit try/catch of its own.
		c.catchDepth++
		c.checkBlock(v.Body)
		c.catchDepth--
	case *ast.ObjectDecl:
		c.checkObjectDecl(v)
	case *ast.EnumDecl:
		// already collected in first pass; nothing else to check
	case *ast.BreakStmt, *ast.ContinueStmt:
		// nothing
	case *ast.TryStmt:
		c.catchDepth++
		c.checkBlock(v.Body)
		c.catchDepth--
		for _, cl := range v.Catches {
			c.pushScope()
			// The binding takes the clause's declared error type when it has one;
			// an untyped clause catches anything, so its binding is Any.
			errTyp := types.Any
			if cl.TypeName != "" {
				errTyp = c.resolveAnnot(cl.TypeName)
			}
			c.define(cl.ErrName, errTyp, false)
			for _, s := range cl.Body.Stmts {
				c.checkStmt(s)
			}
			c.popScope()
		}
	case *ast.ThrowStmt:
		c.infer(v.Value)
	case *ast.OutStmt:
		c.infer(v.Value)
	}
}

func (c *checker) checkDecl(v *ast.DeclStmt) {
	// Resolve the annotation FIRST so it can be the expected type for the value:
	// `final c: Suit = .one` has no other way to know which enum `.one` names.
	var annot types.Type
	if v.TypeAnnot != "" {
		annot = c.resolveAnnot(v.TypeAnnot)
	}
	inferred := c.inferExpected(v.Value, annot)
	declTyp := inferred
	if v.TypeAnnot != "" {
		annotTyp := annot
		if !types.Compat(inferred, annotTyp) {
			c.errorfc(v.Pos, TypeMismatch, "cannot assign %s to %s variable %q",
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
			// The variable's type is the expected type for the value, so `assigned = .en`
			// resolves against the declaration.
			rhs := c.inferExpected(v.Value, e.typ)
			if !types.Compat(rhs, e.typ) {
				c.errorfc(v.Pos, TypeMismatch, "cannot assign %s to %s", rhs.TypeName(), e.typ.TypeName())
			}
			return
		}
	}
	// A member or index target: its own type is likewise the expected type for the
	// value (`mutableList[0] = .it` resolves through the list's element type).
	c.inferExpected(v.Value, c.infer(v.Target))
}

func (c *checker) checkReturn(v *ast.ReturnStmt) {
	if v.Value == nil {
		return
	}
	if c.retTyp == types.Void {
		c.errorf(v.Pos, "void function cannot return a value")
		return
	}
	// The declared return type is the expected type for the returned expression, so
	// a value that needs a hint can resolve against it -- `fun f() > Locale { return
	// .it; }` has nothing else to say which enum `.it` names.
	ret := c.inferExpected(v.Value, c.retTyp)
	// Skip return type check for fiber functions (fib<V,R> annotations or *> syntax):
	// the declared return type in these cases represents the fiber value type, not
	// the checked function return type.
	_, retIsFibType := c.retTyp.(*types.FibType)
	if c.retTyp != nil && c.retTyp != types.Any && c.retTyp != types.Fib && !retIsFibType && c.yieldTyp == nil && !types.Compat(ret, c.retTyp) {
		c.errorfc(v.Pos, TypeMismatch, "return type mismatch: got %s, want %s",
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
		c.errorfc(ast.NodePos(v.Cond), NonBoolCondition, "if condition must be bool, got %s", cond.TypeName())
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

	// An extern declaration IS the signature and nothing else - there is no body to
	// descend into. Defining the type above is the whole point of it: every call
	// site now checks against a real signature instead of Unknown.
	if fd.IsExtern {
		return
	}

	savedRet := c.retTyp
	savedYield := c.yieldTyp
	savedRaise := c.raiseDeclared
	c.retTyp = ft.Ret
	if fd.YieldAnnot != "" {
		c.yieldTyp = c.resolveAnnot(fd.YieldAnnot)
	} else {
		c.yieldTyp = nil
	}
	c.raiseDeclared = fd.ErrAnnot != ""
	c.pushScope()
	c.define("this", types.Unknown, false)
	for i, name := range fd.Params {
		pt := types.Unknown
		if i < len(ft.Params) {
			pt = ft.Params[i]
		}
		// A default is checked against its own parameter's type, which is how an
		// inferred enum case (`case: Suit = .hearts`) learns which enum it names.
		// It is resolved here, once at the declaration, not at every call site.
		if i < len(fd.ParamDefaults) && fd.ParamDefaults[i] != nil {
			c.inferExpected(fd.ParamDefaults[i], pt)
		}
		c.define(name, pt, false)
	}
	for _, s := range fd.Body.Stmts {
		c.checkStmt(s)
	}
	c.popScope()
	c.retTyp = savedRet
	c.yieldTyp = savedYield
	c.raiseDeclared = savedRaise
}

func (c *checker) checkObjectDecl(v *ast.ObjectDecl) {
	if v.IsProtocol {
		// Nothing to check inside a protocol: its members are signatures, already
		// recorded as the type's method set by registerTypeDecls. Walking them as
		// method declarations would look for bodies that by definition do not exist.
		return
	}
	ot, _ := c.types[v.Name].(*types.ObjectType)
	if ot == nil {
		ot = c.buildObjectType(v)
	}
	// Field defaults were previously never inferred at all. They have to be, because
	// `mouse: MouseKind = .drag` is the enum shorthand's most common home and its
	// field annotation is the only thing that says which enum `.drag` names. Inferred,
	// not compared: this deliberately resolves the default without also introducing a
	// compatibility error that has never fired before.
	for _, f := range slices.Concat(v.Fields, v.StaticFields) {
		if f.Default == nil {
			continue
		}
		// resolveAnnot, not ot.Fields: buildObjectType fills those with
		// types.ParseAnnot, which knows the primitives but not this checker's named
		// types, so an enum field would arrive here as something that is not an
		// EnumType and the case could never resolve.
		c.inferExpected(f.Default, c.resolveAnnot(f.TypeAnnot))
	}
	for _, m := range v.Methods {
		// Rebuilt, then written back: buildObjectType runs while the top level is
		// still being registered, so a method whose annotation names a type
		// declared LATER in the file resolved to nothing there. Every named type
		// exists by now, and call sites read ot.Methods, not this local.
		ft := c.funDeclType(m)
		ot.Methods[m.Name] = ft
		savedRet := c.retTyp
		savedRaise := c.raiseDeclared
		c.retTyp = ft.Ret
		c.raiseDeclared = m.ErrAnnot != ""
		c.pushScope()
		c.define("this", ot, false)
		for i, name := range m.Params {
			pt := types.Unknown
			if i < len(ft.Params) {
				pt = ft.Params[i]
			}
			// Same as a free function's: a method parameter's default is resolved
			// against that parameter's type, which is what tells `.two` its enum.
			if i < len(m.ParamDefaults) && m.ParamDefaults[i] != nil {
				c.inferExpected(m.ParamDefaults[i], pt)
			}
			c.define(name, pt, false)
		}
		for _, s := range m.Body.Stmts {
			c.checkStmt(s)
		}
		c.popScope()
		c.retTyp = savedRet
		c.raiseDeclared = savedRaise
	}
}

// infer returns the inferred types.Type of expression n.
// inferExpected infers n with want as the type expected at this position, so a
// receiver-less construct that cannot name its own type (currently only the
// inferred enum case, `.one`) can resolve against it.
//
// A stack rather than a field because these nest: a list literal annotated
// [Locale] pushes Locale for each element, and an element may itself be a call
// whose arguments push their own parameter types.
func (c *checker) inferExpected(n ast.Node, want types.Type) types.Type {
	c.expected = append(c.expected, want)
	defer func() { c.expected = c.expected[:len(c.expected)-1] }()
	return c.infer(n)
}

// wantType is the type expected at the current position, or nil outside any
// annotated context.
func (c *checker) wantType() types.Type {
	if len(c.expected) == 0 {
		return nil
	}
	return c.expected[len(c.expected)-1]
}

// objectOf unwraps want to the object type it ultimately expects, so an
// anonymous object literal resolves the same whether the annotation is Foo,
// Foo?, or [Foo]. A namespace object is not one: its fields are module
// exports, not a shape a literal can fill.
func objectOf(want types.Type) *types.ObjectType {
	switch t := want.(type) {
	case *types.ObjectType:
		if t.IsNamespace {
			return nil
		}
		return t
	case *types.ListType:
		return objectOf(t.Elem)
	}
	return nil
}

// enumOf unwraps want to the enum it ultimately expects, so `.one` resolves the
// same whether the annotation is Suit, Suit?, or [Suit].
func enumOf(want types.Type) *types.EnumType {
	switch t := want.(type) {
	case *types.EnumType:
		return t
	case *types.ListType:
		return enumOf(t.Elem)
	}
	return nil
}

func (c *checker) infer(n ast.Node) types.Type {
	if n == nil {
		return types.Any
	}
	switch v := n.(type) {
	case *ast.TypeExpr:
		v.Resolved = canonicalTypeName(c.resolveAnnot(v.Annot))
		return types.TypeVal
	case *ast.TypeOfExpr:
		// typeof is STATIC: resolve the operand's inferred type here, once, and
		// record its canonical spelling for the compiler to emit as a constant. The
		// operand is never evaluated at runtime, which is what lets `typeof` tell
		// `final list = []` ([any]) from `final slist: [str] = []` ([str]) - the same
		// empty list, distinguishable only before it runs.
		v.Resolved = canonicalTypeName(c.infer(v.Operand))
		return types.TypeVal
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
	case *ast.IfExpr:
		cond := c.infer(v.Cond)
		if cond != types.Any && cond != types.Unknown && cond != types.Bool {
			c.errorfc(ast.NodePos(v.Cond), NonBoolCondition, "inline if condition must be bool, got %s", cond.TypeName())
		}
		then := c.infer(v.Then)
		els := c.infer(v.Else)
		// The branches decide the type only when they agree; a null branch makes
		// the result optional, which this checker erases, so it reports the other
		// branch. Anything else is genuinely two types and stays Unknown.
		switch {
		case types.Compat(els, then):
			return then
		case types.Compat(then, els):
			return els
		default:
			return types.Unknown
		}
	case *ast.BlockExpr:
		// The body is checked so names inside it are still resolved, but the
		// block's own type is Unknown: it comes from whichever `out` runs, and
		// picking one statically would be a guess. Unknown is compatible with
		// every target, so the annotation at the use site decides.
		c.pushScope()
		for _, s := range v.Body.Stmts {
			c.checkStmt(s)
		}
		c.popScope()
		return types.Unknown
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
	case *ast.MatchExpr:
		// The subject's type is the expected type for every condition, which is what
		// lets a bare enum case appear as one (`match (locale) { .fr -> ... }`), and
		// the match's own expected type flows into each body, so a branch value can
		// resolve the same way (`final l: Locale? = match (n) { 1 -> .fr, ... }`).
		subjTyp := c.infer(v.Subject)
		want := c.wantType()
		var result types.Type
		for _, br := range v.Branches {
			for _, cond := range br.Conds {
				c.inferExpected(cond, subjTyp)
			}
			// A `-> { ... }` body is a block of STATEMENTS, not an expression: check it in
			// its own scope (sibling arms must not see each other's locals) and treat it
			// as yielding null, which is what the compiler pushes for it.
			bodyTyp := types.Type(types.Null)
			if blk, isBlock := br.Body.(*ast.BlockStmt); isBlock {
				c.checkBlock(blk)
			} else {
				bodyTyp = c.inferExpected(br.Body, want)
			}
			// The first branch that yields something other than null names the match's
			// type. Optionals are erased in this checker, so a `-> null` arm only makes
			// the result nullable, which is not tracked separately.
			if result == nil && bodyTyp != types.Null {
				result = bodyTyp
			}
		}
		if result == nil {
			return types.Unknown
		}
		return result
	case *ast.EnumCaseExpr:
		et := enumOf(c.wantType())
		if et == nil {
			c.errorf(v.Pos, "cannot infer which enum .%s belongs to here; name it explicitly (Enum.%s)", v.Name, v.Name)
			return types.Unknown
		}
		if !slices.Contains(et.Cases, v.Name) {
			c.errorf(v.Pos, "enum %s has no case %q", et.Name, v.Name)
			return types.Unknown
		}
		// Record the resolution for the compiler: it emits the same access as an
		// explicit Enum.case, and this is the only point at which the enum is known.
		v.Enum = et.Name
		v.EnumNS = c.enumNS[et.Name]
		return et
	case *ast.IsExpr:
		c.infer(v.Expr)
		return types.Bool
	case *ast.AsExpr:
		c.infer(v.Expr)
		return c.resolveAnnot(v.TypeName)
	case *ast.CatchExpr:
		// `expr catch default` evaluates to expr's success type, which is therefore also
		// the expected type for the default -- the two are alternatives for one value,
		// so a default that needs a hint resolves against it (`failLocale() catch .en`).
		// The raise, if any, is handled right here (the default IS the catch), so
		// Expr is inferred under catchDepth like a try body.
		c.catchDepth++
		t := c.infer(v.Expr)
		c.catchDepth--
		c.inferExpected(v.Default, t)
		return t
	case *ast.YieldExpr:
		// The declared yield type is the expected type for the yielded value, the same
		// way a return propagates its own -- `*> Locale?` is what resolves `yield .en`.
		vt := c.inferExpected(v.Value, c.yieldTyp)
		if c.yieldTyp != nil && !types.Compat(vt, c.yieldTyp) {
			c.errorfc(v.Pos, TypeMismatch, "yield type mismatch: got %s, want %s", vt.TypeName(), c.yieldTyp.TypeName())
		}
		return types.Null // yield expression evaluates to null (the resumed value)
	case *ast.FiberExpr:
		calleeTyp := c.infer(v.Call.Callee)
		if ft, ok := calleeTyp.(*types.FuncType); ok {
			c.resolveNamedArgs(v.Call, ft)
		} else {
			v.Call.ArgNames = nil
		}
		// A fiber wraps an ordinary call, so its arguments get their parameter types the
		// same way a direct call's do (inferCall). Inferring them bare left an anonymous
		// `.{ ... }` argument as a map, so `&f(.{ points = 8 })` produced something
		// whose methods did not exist.
		for i, a := range v.Call.Args {
			if ft, ok := calleeTyp.(*types.FuncType); ok && i < len(ft.Params) {
				c.inferExpected(a, c.resolveType(ft.Params[i]))
				continue
			}
			c.infer(a)
		}
		ft, ok := calleeTyp.(*types.FuncType)
		if !ok {
			return types.Fib // callee type unknown (any) — leave the fiber untyped
		}
		if !ft.Variadic && len(v.Call.Args) != len(ft.Params) {
			c.errorfc(v.Pos, ArgumentError, "wrong argument count: got %d, want %d", len(v.Call.Args), len(ft.Params))
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
		c.errorfc(v.Pos, UndefinedName, "undefined: %s (an imported module declares %q but does not export it; add `export` to it)", v.Name, v.Name)
	} else {
		c.errorfc(v.Pos, UndefinedName, "undefined: %s", v.Name)
	}
	return types.Any
}

func (c *checker) inferBinary(v *ast.BinaryExpr) types.Type {
	// Each side is the expected type for the other, which is what lets `suit == .one`
	// resolve. Inferring one side first costs nothing: an operand that cannot use a
	// hint ignores it. Only the enum-case shorthand reads it today, and comparing
	// against a bare case is the shape upstream uses most.
	var left, right types.Type
	if _, leftIsCase := v.Left.(*ast.EnumCaseExpr); leftIsCase {
		// A bare case on the LEFT has to wait for the right side to name its enum.
		// Inferring left-first here would REPORT "cannot infer which enum" before any
		// hint existed, and the re-infer below cannot retract an emitted error -- which
		// is what made the reversed comparison `.fr == Locale.fr` fail.
		right = c.infer(v.Right)
		left = c.inferExpected(v.Left, right)
	} else {
		left = c.infer(v.Left)
		right = c.inferExpected(v.Right, left)
		if left == types.Unknown {
			// The left side may itself have been the shorthand, with the right naming the
			// enum; re-infer it now that there is something to resolve against.
			left = c.inferExpected(v.Left, right)
		}
	}
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
		// Map merge: {K: V} + {K: V} → {K: V}, the map counterpart of list
		// concatenation (upstream tests/behavior/composite-assign.buzz). The right
		// operand wins on a duplicate key, which is why the result takes the LEFT
		// type only when it is a map - a merge cannot widen the key or value type.
		if _, ok := left.(*types.MapType); ok {
			return left
		}
		if _, ok := right.(*types.MapType); ok {
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
	case "&", "|", "^", "<<", ">>":
		return c.integerResult(v.Pos, v.Op, left, right)
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

// integerResult types a bitwise operator. Unlike numericResult there is no
// float arm: upstream Buzz declares the bitwise operators over int alone.
func (c *checker) integerResult(p ast.Pos, op string, left, right types.Type) types.Type {
	for _, t := range [...]types.Type{left, right} {
		if t == types.Any || t == types.Unknown {
			return types.Unknown
		}
		if t != types.Int {
			c.errorfc(p, TypeMismatch, "%s requires int operands, got %s", op, t.TypeName())
			return types.Int
		}
	}
	return types.Int
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
		c.errorfc(p, TypeMismatch, "invalid type %s in arithmetic expression", left.TypeName())
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
		c.errorfc(v.Pos, TypeMismatch, "unary - requires numeric operand, got %s", t.TypeName())
		return types.Any
	case "!":
		return types.Bool
	case "~":
		if t == types.Any || t == types.Unknown || t == types.Int {
			return types.Int
		}
		c.errorfc(v.Pos, TypeMismatch, "unary ~ requires int operand, got %s", t.TypeName())
		return types.Int
	}
	return types.Any
}

func (c *checker) inferCall(v *ast.CallExpr) types.Type {
	calleeTyp := c.infer(v.Callee)
	ft, ok := calleeTyp.(*types.FuncType)
	if ok {
		// Propagate-or-catch: a call to a function that declared !> (or, for a
		// host extern, is authored as raising in std.Method) is only legal when
		// the enclosing function also declared !> - so the error keeps
		// propagating outward - or the call sits inside a try/catch or `catch`
		// expression that handles it right here. Neither means the error can
		// reach a caller with no way to know it might.
		if ft.Raises && !c.raiseDeclared && c.catchDepth == 0 {
			c.errorfc(v.Pos, UnhandledRaise, "call may raise but is neither declared with !> nor caught")
		}
		c.resolveNamedArgs(v, ft)
	} else {
		// Dynamic callee (any-typed value, host function): labels cannot be
		// resolved, so arguments pass in written order. Upstream-style call
		// sites write them in declaration order, which makes this correct.
		v.ArgNames = nil
	}
	for i, a := range v.Args {
		// Each argument is inferred against its parameter's type, so a construct
		// that cannot name its own type (`.one`, and anything else that reads the
		// expected type) resolves from the signature.
		if ok && i < len(ft.Params) {
			// resolveType, not the param as-is: a signature's annotations are parsed by
			// types.ParseAnnot, which knows the primitives but not this checker's named
			// types, so `task: FiberTask` arrives as a NamedType. An anonymous `.{ ... }`
			// argument looks for an OBJECT in its expected type and would find none, and
			// so stay a plain map whose methods then do not exist.
			c.inferExpected(a, c.resolveType(ft.Params[i]))
			continue
		}
		c.infer(a)
	}
	// An explicit generic type argument (`buf.readZAt::<double>(...)`) names the
	// call's result type. gopherbuzz doesn't model generic signatures, so without
	// this the result would be `any`; honoring the hint matches upstream Buzz.
	if v.TypeArg != "" {
		// The hint only helps when it names a type this checker knows. A call inside a
		// generic function passes its own type PARAMETERS through (`lambda::<A, B>()`),
		// and those are erased - resolving them yields an opaque named type that then
		// fails against the declared return. Fall through to the callee's signature in
		// that case, which is the honest answer.
		t := c.resolveAnnot(v.TypeArg)
		if _, erased := t.(*types.NamedType); erased || t == types.Unknown {
			// Unknown, not the callee's signature: a type parameter carries no
			// information here, and Unknown is compatible with everything, so the
			// caller's own declared return decides. Falling through instead would
			// report the erased lambda's return as void.
			return types.Unknown
		}
		return t
	}
	if !ok {
		return types.Unknown
	}
	if !ft.Variadic && len(v.Args) != len(ft.Params) {
		c.errorfc(v.Pos, ArgumentError, "wrong argument count: got %d, want %d", len(v.Args), len(ft.Params))
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
	// A call with no labels still needs this pass when the callee declares
	// defaults: `hey("John")` fills three slots the caller never wrote.
	if v.ArgNames == nil && !hasDefault(ft) {
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
		name := ""
		if i < len(v.ArgNames) {
			name = v.ArgNames[i]
		}
		if name == "" {
			if sawNamed {
				c.errorfc(v.Pos, ArgumentError, "positional argument after named argument")
				return
			}
			if pos >= n {
				c.errorfc(v.Pos, ArgumentError, "wrong argument count: got %d, want %d", len(v.Args), n)
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
			c.errorfc(v.Pos, ArgumentError, "unknown argument name %q (parameters are %s)", name, strings.Join(ft.ParamNames, ", "))
			return
		}
		if filled[idx] {
			c.errorfc(v.Pos, ArgumentError, "argument %q given more than once", name)
			return
		}
		slots[idx] = arg
		filled[idx] = true
	}
	for j, ok := range filled {
		if ok {
			continue
		}
		// An unwritten slot takes the parameter's declared default, evaluated here
		// at the call site. Upstream restricts defaults to constants, so there is
		// nothing in one that could see the callee's scope rather than this one.
		if j < len(ft.ParamDefaults) && ft.ParamDefaults[j] != nil {
			slots[j] = ft.ParamDefaults[j]
			continue
		}
		c.errorfc(v.Pos, ArgumentError, "missing argument %q", ft.ParamNames[j])
		return
	}
	v.Args = slots
}

// hasDefault reports whether any of ft's parameters declares a default value.
func hasDefault(ft *types.FuncType) bool {
	return slices.ContainsFunc(ft.ParamDefaults, func(n ast.Node) bool { return n != nil })
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
		// A case access yields the EnumType too, so by this point `Hey` and `Hey.one`
		// are indistinguishable here and both admit the value accessors the VM
		// provides. The cost is that `Hey.name` type-checks; the VM answers null for
		// it, which is the same latitude the enum-def member path already takes.
		switch v.Name {
		case "name":
			return types.Str
		case "value":
			return types.Int
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
	savedRaise := c.raiseDeclared
	c.retTyp = ret
	c.yieldTyp = yield
	c.raiseDeclared = v.ErrAnnot != ""
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
	c.raiseDeclared = savedRaise

	return &types.FuncType{Params: params, Ret: ret, Yield: yield, Raises: v.ErrAnnot != "", ParamNames: v.Params, ParamDefaults: v.ParamDefaults}
}

func (c *checker) inferMapExpr(v *ast.MapExpr) types.Type {
	// An anonymous object literal `.{ field = expr }` parses to a map keyed by
	// the field names, so when the expected type is an object, each value is
	// inferred against that field's type. Without this a `.{ kind = .two }`
	// assigned to an annotated field has nothing to tell `.two` its enum.
	if ot := objectOf(c.wantType()); v.Anon && ot != nil {
		for i, k := range v.Keys {
			name, isField := k.(*ast.StringLit)
			ft, known := types.Unknown, false
			if isField {
				ft, known = ot.Fields[name.Val]
			}
			if known {
				// resolveType, not ft as-is: buildObjectType fills Fields with
				// types.ParseAnnot, which knows the primitives but not this
				// checker's named types, so an enum field arrives as a NamedType
				// and the case could never resolve against it.
				c.inferExpected(v.Values[i], c.resolveType(ft))
				continue
			}
			c.infer(v.Values[i])
		}
		// Record the resolution for the compiler, the same way an inferred enum
		// case records its enum: this is the only point at which the object a
		// bare `.{ ... }` fills is known.
		v.ObjectName = ot.Name
		return ot
	}
	// An explicit `{<K: V>, ...}` annotation on the literal itself decides its type
	// and is the expected type for the entries, the map counterpart of a list's
	// ElemType above. It takes precedence over any expected type from the use site,
	// because the literal said what it is.
	if v.ValType != "" || v.KeyType != "" {
		keyTyp, valTyp := c.resolveAnnot(v.KeyType), c.resolveAnnot(v.ValType)
		for i := range v.Keys {
			c.inferExpected(v.Keys[i], keyTyp)
			c.inferExpected(v.Values[i], valTyp)
		}
		return &types.MapType{Key: keyTyp, Val: valTyp}
	}
	// An annotated map literal passes its declared element types down, so a
	// nested `.{ ... }` value knows which object it is filling in.
	if want, ok := c.wantType().(*types.MapType); ok {
		for i := range v.Keys {
			c.inferExpected(v.Keys[i], c.resolveType(want.Key))
			c.inferExpected(v.Values[i], c.resolveType(want.Val))
		}
		return want
	}
	if len(v.Keys) == 0 {
		// `{any: any}`, matching upstream and the empty-list case below: an
		// unannotated `{}` constrains neither half. Defaulting the key to str was
		// invisible until `typeof {}` had to render it, and it would have rejected
		// an int-keyed map assigned from an empty literal.
		return &types.MapType{Key: types.Any, Val: types.Any}
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
	// An explicit `[<T>, ...]` element type is an annotation, so it both decides the
	// list's type and is the expected type for every element. Propagating it is what
	// lets an element that needs a hint resolve -- an inferred enum case has no other
	// way to learn which enum `.it` names.
	if v.ElemType != "" {
		elemTyp := c.resolveAnnot(v.ElemType)
		for _, item := range v.Items {
			c.inferExpected(item, elemTyp)
		}
		return &types.ListType{Elem: elemTyp}
	}
	if len(v.Items) == 0 {
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
		c.errorfc(v.Pos, UndefinedType, "undefined type %q", v.TypeName)
		return types.Any
	}
	ot, ok := resolved.(*types.ObjectType)
	if !ok {
		c.errorf(v.Pos, "%s is not an object type", v.TypeName)
		return types.Any
	}
	for i, key := range v.Keys {
		ft, exists := ot.Fields[key]
		if !exists {
			c.errorf(v.Pos, "object %s has no field %q", v.TypeName, key)
		}
		if i >= len(v.Values) {
			continue
		}
		if !exists {
			c.infer(v.Values[i])
			continue
		}
		// The field's declared type is the expected type for its value, so a value that
		// needs a hint resolves (`HasLocalField{ locale = .en }`). resolveType, not ft
		// as-is: buildObjectType fills Fields via types.ParseAnnot, which knows the
		// primitives but not this checker's named types, so an enum field arrives as a
		// NamedType the case could never resolve against -- the same reason the
		// anonymous `.{ ... }` path in inferMapExpr resolves it.
		c.inferExpected(v.Values[i], c.resolveType(ft))
	}
	return ot
}

// canonicalTypeName is the SINGLE source of the spelling a type value carries.
// Both sides of `typeof x == <T>` route through it - the checker for the typeof
// operand, the compiler for the literal's annotation - so the two can never
// disagree over spacing or an alias. It follows upstream's rendering: a map is
// `{key: val}` with the space, which types.MapType.TypeName omits.
func canonicalTypeName(t types.Type) string {
	switch v := t.(type) {
	case *types.ListType:
		return "[" + canonicalTypeName(v.Elem) + "]"
	case *types.MapType:
		return "{" + canonicalTypeName(v.Key) + ": " + canonicalTypeName(v.Val) + "}"
	case nil:
		return "any"
	}
	// Unknown is the checker's tracking-failure sentinel, not a type a user can
	// write. Reporting it as `any` keeps a type value printable and comparable
	// instead of leaking `<unknown>` into a program's output.
	if t == types.Unknown || t == nil {
		return "any"
	}
	return t.TypeName()
}
