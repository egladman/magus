package buzz

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
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

// As hands errors.As a *diagnostics.Error carrying this error's code, so a caller can branch
// on BZZ1002 without naming this unexported type.
//
// The embedder needs that and cannot have it any other way: magus sits ABOVE this module and
// its lowest layers sit below it, so the code was only reachable by substring-matching a
// sentence written for humans. Its stale-binary explainer reached for errors.As instead, found
// nothing to match, and the explanation it exists to print never appeared for the direct
// undefined-name case.
//
// An uncoded error declines: there is no diagnostic to hand back, and inventing one with an
// empty code would make every plain type error look like a coded one.
func (e typeError) As(target any) bool {
	p, ok := target.(**diagnostics.Error)
	if !ok || e.Code == "" {
		return false
	}
	*p = bzz.Errorf(e.Code, "%s", e.Msg)
	return true
}

type scopeEntry struct {
	typ     types.Type
	isConst bool
	// varDecl marks a `var` local, and assigned records whether anything ever wrote
	// to it. A var that is never written should have been a `final`, which upstream
	// reports. Both stay false for a parameter, a global, or a final.
	varDecl  bool
	assigned bool
	// declaredLocal marks a local (or parameter) whose USE should be tracked, and
	// read records whether anything ever evaluated it.
	declaredLocal bool
	read          bool
	// optional records that the DECLARATION spelled a trailing `?`. The type system
	// erases optionality (ParseAnnot consumes the `?`), so this is the only place it
	// survives - deliberately narrow, and only consulted where a null would be a
	// hard error rather than a value.
	optional bool
	pos      ast.Pos
	name     string
}

type checker struct {
	errors []typeError
	// warnings are non-fatal diagnostics; they never fail Exec/Compile. Kept apart from
	// errors so a caller cannot mistake one for the other by length alone.
	warnings []typeError
	// loopDepth counts the enclosing loop bodies being checked, so a statement can ask
	// whether it runs repeatedly. Only the BODY counts: a for-clause is not a loop body.
	loopDepth int
	scopes    []map[string]scopeEntry
	retTyp    types.Type
	// retOptional records that the enclosing function's return annotation ended in
	// `?`. Like scopeEntry.optional it exists because ParseAnnot erases the marker.
	retOptional bool
	yieldTyp    types.Type // non-nil when inside a function with a *> yield annotation
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
	// fnScopeBase is where the current function's own scope starts. Upstream scopes
	// shadowing to the current function, so a nested closure may reuse an outer name.
	fnScopeBase int
	types       map[string]types.Type // named type definitions (objects, enums)
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
	// moduleVars holds each imported module's exported `final`/`var` names, so the
	// namespace object is COMPLETE and a missing member can be reported.
	moduleVars map[string][]*ast.DeclStmt
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
func checkWithGlobals(prog *ast.Program, extraGlobals []string, imported []ast.Node, moduleFuncs map[string][]*ast.FunDecl, moduleTypes map[string][]ast.Node, moduleVars map[string][]*ast.DeclStmt, private map[string]bool) (errs []typeError, warnings []typeError) {
	c := &checker{
		types:       map[string]types.Type{},
		moduleFuncs: moduleFuncs,
		moduleTypes: moduleTypes,
		moduleVars:  moduleVars,
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
	c.inferUnannotatedReturns(prog)
	for _, s := range prog.Stmts {
		c.checkStmt(s)
	}
	return c.errors, c.warnings
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
func (c *checker) popScope() {
	c.checkUnassignedVars()
	c.checkUnusedLocals()
	c.scopes = c.scopes[:len(c.scopes)-1]
}

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

// warnfc records a non-fatal diagnostic under a specific BZZ code. Unlike errorfc this
// never fails a compile; callers read it back through Session.Warnings.
func (c *checker) warnfc(p ast.Pos, code diagnostics.Code, format string, args ...any) {
	c.warnings = append(c.warnings, typeError{
		Line: p.Line, Col: p.Col, Code: code, Severity: SeverityWarning,
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
			vars := c.moduleVars[name]
			if len(fds) > 0 || len(decls) > 0 || len(vars) > 0 {
				nt := &types.ObjectType{Name: name, Fields: map[string]types.Type{}, Methods: map[string]*types.FuncType{}, IsNamespace: true}
				for _, fd := range fds {
					nt.Fields[fd.Name] = c.funDeclType(fd)
				}
				// An exported final/var is a member too. The TYPE is best-effort - an
				// unannotated one stays Unknown rather than being inferred, since its
				// initializer may name things private to the defining module. Recording
				// the NAME is the point.
				for _, vd := range vars {
					var vt types.Type = types.Unknown
					if vd.TypeAnnot != "" {
						vt = c.resolveAnnot(vd.TypeAnnot)
					}
					nt.Fields[vd.Name] = vt
				}
				// An exported TYPE is reachable through the namespace too, as the type value
				// itself, so `io\File.open(...)` resolves the same static method a bare
				// `File.open(...)` does - and `io\FileMode.read` the same case.
				for _, d := range decls {
					switch v := d.(type) {
					case *ast.ObjectDecl:
						nt.Fields[v.Name] = c.buildObjectType(v)
					case *ast.EnumDecl:
						nt.Fields[v.Name] = &types.EnumType{Name: v.Name, Cases: v.Cases, Backing: v.Backing}
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
			// A zdef struct is a TYPE, registered the same way a written `object`
			// is, so the literal and `typeof` resolve through the usual paths.
			isStruct := map[string]bool{}
			if structs := zdefStructDecls(v.Expr); len(structs) > 0 {
				nodes := make([]ast.Node, len(structs))
				for i, od := range structs {
					nodes[i] = od
					isStruct[od.Name] = true
				}
				c.registerTypeDecls(nodes)
			}
			if _, names, ok := zdefDeclNames(v.Expr); ok {
				// zdef symbols are FFI callables whose return types can't be tracked
				// statically; return Unknown so field access on their results doesn't
				// fire E28 (Unknown is the tracking-failure sentinel, not user `any`).
				ffiFn := &types.FuncType{Params: []types.Type{types.Unknown}, Ret: types.Unknown, Variadic: true}
				for _, name := range names {
					// A struct name is a TYPE, already bound by registerTypeDecls above.
					// Rebinding it as a callable made `typeof Data` answer the callable's
					// type instead of `<type>`, and would let `Data(...)` type-check.
					if isStruct[name] {
						continue
					}
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
			et := &types.EnumType{Name: v.Name, Cases: v.Cases, Backing: v.Backing}
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
		return &types.ListType{Elem: c.resolveType(v.Elem), Mut: v.Mut}
	case *types.MapType:
		return &types.MapType{Key: c.resolveType(v.Key), Val: c.resolveType(v.Val), Mut: v.Mut}
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
		c.loopDepth++
		c.checkBlock(v.Body)
		c.loopDepth--
	case *ast.DoStmt:
		c.loopDepth++
		c.checkBlock(v.Body)
		c.loopDepth--
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
		c.loopDepth++
		for _, s := range v.Body.Stmts {
			c.checkStmt(s)
		}
		c.loopDepth--
		c.popScope()
	case *ast.ForEachStmt:
		c.loopDepth++
		c.checkForEach(v)
		c.loopDepth--
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
		// A direct throw is NOT held to propagate-or-catch, though a CALL to a raising
		// function is. That asymmetry is deliberate and measured (2026-08-11): applying
		// the rule closes upstream's fiber-error-location.buzz and nothing else, while
		// breaking seven of magus's own suites - its spells, tour files and scripts
		// throw from functions that declare no !>. Closing it is a corpus-wide
		// annotation migration for one file, so it is a decision rather than an
		// oversight.
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
	c.checkLocalShadowing(v)
	c.define(v.Name, declTyp, v.IsConst)
	// Only a LOCAL var is tracked: scopes[0] is the global scope, where a var may be
	// written by any later top-level statement or by a function body, so "never
	// assigned here" says nothing.
	// `_` is the discard name, never assigned by construction, so it is exempt -
	// upstream's own anonymous-objects.buzz writes `var _ = ...`.
	c.declareTracked(v.Name, v.Pos)
	if strings.HasSuffix(v.TypeAnnot, "?") && len(c.scopes) > 1 {
		e := c.scopes[len(c.scopes)-1][v.Name]
		e.optional = true
		c.scopes[len(c.scopes)-1][v.Name] = e
	}
	if !v.IsConst && v.Name != "_" && len(c.scopes) > 1 {
		e := c.scopes[len(c.scopes)-1][v.Name]
		e.varDecl, e.pos, e.name = true, v.Pos, v.Name
		c.scopes[len(c.scopes)-1][v.Name] = e
	}
}

func (c *checker) checkAssign(v *ast.AssignStmt) {
	c.flagStringAccumulation(v)
	if id, ok := v.Target.(*ast.IdentExpr); ok {
		// `_` is the discard target: accept any value and bind nothing.
		if id.Name == "_" {
			c.infer(v.Value)
			return
		}
		c.markAssigned(id.Name)
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
	//
	// Writing through the name justifies its `var`, same as noteMutatingUse does
	// for `.append`. Upstream accepts both, and only warns where it comments.
	if root := rootIdentName(v.Target); root != "" {
		c.markAssigned(root)
	}
	c.inferExpected(v.Value, c.infer(v.Target))
}

// rootIdentName returns the identifier a target writes through (`a.b[0].c` is a
// write to `a`), or "" when it is rooted in something that names no local.
func rootIdentName(target ast.Node) string {
	for {
		switch t := target.(type) {
		case *ast.IdentExpr:
			return t.Name
		case *ast.MemberExpr:
			target = t.Object
		case *ast.IndexExpr:
			target = t.Object
		default:
			return ""
		}
	}
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
	// A non-optional return cannot yield a possibly-null value. Optionality is
	// erased in the type system, so this reads the declared `?` recorded on the
	// scope entry - see possiblyNullName.
	if v.Value != nil && !c.retOptional && c.retTyp != nil && c.retTyp != types.Void && c.retTyp != types.Any && c.retTyp != types.Unknown {
		if name, isNull := c.possiblyNullName(v.Value); isNull {
			c.errorfc(ast.NodePos(v.Value), TypeMismatch, "return value may be null: %q is optional but %s declares a non-optional return", name, "this function")
		}
	}
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
	c.checkUnreachable(b)
	c.popScope()
}

// flagStringAccumulation warns on `s = s + part` inside a loop. Every iteration copies the
// whole string built so far, and on the default nanbox build each intermediate is pinned in
// the global object heap for the life of the process, so none of those copies is ever
// reclaimed - the cost is quadratic in time and unbounded in memory. Collecting the parts in
// a list and joining once is the fix.
//
// Deliberately narrow: only a bare `+` spine with the target as one of its operands counts,
// so `s = trim(s)` or `s = s.sub(1)` stay quiet. Those rebuild s too, but they do not GROW
// it, and warning on every mention of s on the right would make this noise.
func (c *checker) flagStringAccumulation(v *ast.AssignStmt) {
	if c.loopDepth == 0 {
		return
	}
	id, ok := v.Target.(*ast.IdentExpr)
	if !ok {
		return
	}
	if e, found := c.lookup(id.Name); !found || e.typ != types.Str {
		return
	}
	if !concatSpineRefers(v.Value, id.Name) {
		return
	}
	c.warnfc(v.Pos, StringAccumulation,
		"%q is rebuilt from itself inside a loop, copying the whole string every iteration; collect the parts in a list and join() it once",
		id.Name)
}

// concatSpineRefers reports whether n is a `+` chain carrying name as a direct operand.
func concatSpineRefers(n ast.Node, name string) bool {
	bin, ok := n.(*ast.BinaryExpr)
	if !ok || bin.Op != "+" {
		return false
	}
	for _, side := range []ast.Node{bin.Left, bin.Right} {
		if id, ok := side.(*ast.IdentExpr); ok && id.Name == name {
			return true
		}
		if concatSpineRefers(side, name) {
			return true
		}
	}
	return false
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
	c.checkMainSig(fd, ft)

	// An extern declaration IS the signature and nothing else - there is no body to
	// descend into. Defining the type above is the whole point of it: every call
	// site now checks against a real signature instead of Unknown.
	if fd.IsExtern {
		return
	}

	savedRet := c.retTyp
	savedRetOpt := c.retOptional
	savedYield := c.yieldTyp
	savedRaise := c.raiseDeclared
	c.retTyp = ft.Ret
	c.retOptional = strings.HasSuffix(fd.RetAnnot, "?")
	if fd.YieldAnnot != "" {
		c.yieldTyp = c.resolveAnnot(fd.YieldAnnot)
	} else {
		c.yieldTyp = nil
	}
	c.raiseDeclared = fd.ErrAnnot != ""
	c.pushScope()
	savedFnBase := c.fnScopeBase
	c.fnScopeBase = len(c.scopes) - 1
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
			if i < len(fd.ParamAnnots) {
				c.checkMutableDefault(fd.Pos, "parameter", name, fd.ParamAnnots[i], fd.ParamDefaults[i])
			}
		}
		c.define(name, pt, false)
		if i < len(fd.ParamAnnots) && strings.HasSuffix(fd.ParamAnnots[i], "?") {
			e := c.scopes[len(c.scopes)-1][name]
			e.optional = true
			c.scopes[len(c.scopes)-1][name] = e
		}
	}
	for _, s := range fd.Body.Stmts {
		c.checkStmt(s)
	}
	// Not checkBlock: this function opened its own scope above so the parameters are
	// visible, so the unreachable pass has to be invoked directly. Without this a
	// dead statement was only ever reported inside a nested block - a `return`
	// followed by more code at the TOP level of a function went unnoticed.
	c.checkUnreachable(fd.Body)
	c.checkFunReturns(fd)
	c.popScope()
	c.fnScopeBase = savedFnBase
	c.retTyp = savedRet
	c.retOptional = savedRetOpt
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
	c.checkProtocolConformance(v, ot)
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
		c.checkMutableDefault(v.Pos, "field", f.Name, f.TypeAnnot, f.Default)
	}
	for _, m := range v.Methods {
		// Rebuilt, then written back: buildObjectType runs while the top level is
		// still being registered, so a method whose annotation names a type
		// declared LATER in the file resolved to nothing there. Every named type
		// exists by now, and call sites read ot.Methods, not this local.
		ft := c.funDeclType(m)
		ot.Methods[m.Name] = ft
		c.checkReservedMethodSig(m, ft)
		// An extern method IS the signature and nothing else, exactly as a top-level
		// extern is: recording ft above was the whole point, and there is no body to
		// descend into.
		if m.IsExtern {
			continue
		}
		savedRet := c.retTyp
		savedRetOpt := c.retOptional
		savedRaise := c.raiseDeclared
		savedYield := c.yieldTyp
		c.retTyp = ft.Ret
		c.retOptional = strings.HasSuffix(m.RetAnnot, "?")
		c.raiseDeclared = m.ErrAnnot != ""
		// A method's *> annotation was never recorded here, unlike a free function's
		// in checkFunDecl. Nothing surfaced it because every yield check is guarded on
		// yieldTyp being non-nil, so `yield` inside a method silently went unchecked -
		// and a method calling another yielding method looked like an undeclared yield.
		if m.YieldAnnot != "" {
			c.yieldTyp = c.resolveAnnot(m.YieldAnnot)
		} else {
			c.yieldTyp = nil
		}
		c.pushScope()
		savedFnBase := c.fnScopeBase
		c.fnScopeBase = len(c.scopes) - 1
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
				if i < len(m.ParamAnnots) {
					c.checkMutableDefault(m.Pos, "parameter", name, m.ParamAnnots[i], m.ParamDefaults[i])
				}
			}
			c.define(name, pt, false)
		}
		for _, s := range m.Body.Stmts {
			c.checkStmt(s)
		}
		// Same reason checkFunDecl invokes these directly: the method opened its own
		// scope for `this` and the parameters, so it never went through checkBlock.
		c.checkUnreachable(m.Body)
		c.popScope()
		c.fnScopeBase = savedFnBase
		c.retTyp = savedRet
		c.retOptional = savedRetOpt
		c.raiseDeclared = savedRaise
		c.yieldTyp = savedYield
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
		// Naming a TYPE yields `<type>`, not that type. `typeof A{}` is `<A>` - the
		// type of an instance - but `typeof A` asks after the type VALUE itself, whose
		// type is `type`. Upstream's types-as-value.buzz asserts both, and the two read
		// identically here otherwise: a type name is bound in scope carrying its own
		// type, so inferring the operand answered `<A>` for either spelling.
		if id, isName := v.Operand.(*ast.IdentExpr); isName {
			if declared, isType := c.types[id.Name]; isType {
				// Compared by identity so a local that SHADOWS the name with an
				// instance still reports the instance's type.
				if e, bound := c.lookup(id.Name); !bound || e.typ == declared {
					v.Resolved = canonicalTypeName(types.TypeVal)
					return types.TypeVal
				}
			}
		}
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
		c.checkUnreachable(v.Body)
		// Every path has to produce a value. `terminates` answers that here because
		// `out` counts as terminal: a body that does not terminate has a path falling
		// off the end, which would make the block silently null.
		if !terminates(v.Body) {
			c.errorf(v.Pos, "all block expression paths must end with `out`")
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
		// Arm analysis runs AFTER the loop above: a bare enum case (`.one`) only
		// knows which enum it belongs to once inferExpected has resolved it against
		// the subject, and coverage cannot be computed before that.
		c.checkMatchArms(v, subjTyp)
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
		// A yield with NO enclosing *> annotation is deliberately allowed, which is a
		// known divergence: upstream rejects it twice over (compile_errors/
		// yield-location.buzz "Can't yield here", and yield-without-annotation.buzz,
		// where the absent annotation means void). Requiring it here was tried on
		// 2026-08-11 and reverted - the dismissal is documented on ast.YieldExpr and
		// pinned by TestYieldOutsideFiberDismissed, ~18 of this package's own fiber
		// fixtures omit the annotation, and so does magus's s3-cache spell
		// (`fun listing() > any !> any` yields without one). Closing it is a dialect
		// decision with a migration, not an oversight to patch.
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
		c.markRead(v.Name)
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
		// Deliberately UNTYPED. Checking it was tried on 2026-08-11 and reverted: the
		// rule is only as good as the inference under it, and two of its false
		// positives are not fixable here. `extractList::<int>(...) == list` compares a
		// generic call's result, and type arguments are ERASED by design, so the
		// left side is whatever the un-substituted body suggested. Closing
		// import-syntax-error.buzz this way costs correct programs.
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
		// Yield propagates the same way a raise does, and for the same reason: a
		// function that yields suspends its whole call chain, so every frame between
		// the yield and the fiber has to say so. An intermediate that stays silent
		// would let a caller resume into a suspension it never declared. Upstream
		// pins all three shapes (deep-yield, deep-yield-missing-intermediate,
		// deep-yield-wrong-intermediate-type).
		//
		// This does NOT fire for `&f()`: FiberExpr infers its own callee and
		// arguments rather than routing through here, which is exactly right - wrapping
		// the call in a fiber is what CONSUMES the yield instead of propagating it.
		if ft.Yield != nil {
			switch {
			case c.yieldTyp == nil:
				c.errorfc(v.Pos, TypeMismatch, "call to a yielding function must be declared with *> %s or wrapped in a fiber (&call)", ft.Yield.TypeName())
			case !types.Compat(ft.Yield, c.yieldTyp):
				c.errorfc(v.Pos, TypeMismatch, "yield type mismatch: callee yields %s, enclosing function declares %s", ft.Yield.TypeName(), c.yieldTyp.TypeName())
			}
		}
		c.resolveNamedArgs(v, ft)
	} else {
		// Dynamic callee (any-typed value, host function): labels cannot be
		// resolved, so arguments pass in written order. Upstream-style call
		// sites write them in declaration order, which makes this correct.
		v.ArgNames = nil
		// A plain NAME whose type is known and is not a function can never be called.
		//
		// Restricted to an identifier callee on purpose. A built-in collection or
		// string method is not modelled as a FuncType at all - inferMember answers
		// with the method's RESULT type, so `xs.len()` arrives here with an int
		// callee and would read as uncallable. Nine upstream behavior files failed
		// exactly that way before this narrowing. Any/Unknown stay silent as
		// everywhere else.
		if _, isName := v.Callee.(*ast.IdentExpr); isName {
			switch calleeTyp.(type) {
			case *types.PrimitiveType:
				if calleeTyp != types.Any && calleeTyp != types.Unknown {
					c.errorfc(v.Pos, TypeMismatch, "%s is not callable", calleeTyp.TypeName())
				}
			case *types.ListType, *types.MapType:
				c.errorfc(v.Pos, TypeMismatch, "%s is not callable", calleeTyp.TypeName())
			}
		}
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
// bindTypeArgToName passes a call's explicit `::<T>` spelling to a callee that
// declares a `typeName: str` parameter, when the call itself left that slot empty.
//
// gopherbuzz ERASES type arguments, so a generic assertion helper cannot inspect
// `T` at run time the way upstream's can. But the spelling is known STATICALLY -
// the parser already captures it onto CallExpr.TypeArg - so a helper can opt into
// receiving it by name. That is what lets upstream source written as
// `t.assertOfType::<int>(value)` run here unchanged against a signature of
// `assertOfType(value: any, typeName: str, ...)`.
//
// Deliberately keyed on the exact parameter name: this is an opt-in convention, not
// a general rule about type arguments, and a callee that wants nothing to do with it
// simply does not declare one.
func (c *checker) bindTypeArgToName(v *ast.CallExpr, ft *types.FuncType) {
	if v.TypeArg == "" {
		return
	}
	idx := -1
	for i, name := range ft.ParamNames {
		if name == "typeName" {
			idx = i
			break
		}
	}
	if idx < 0 || idx >= len(ft.Params) || ft.Params[idx] != types.Str {
		return
	}
	// Already supplied, by label or by position: the caller wins.
	for _, name := range v.ArgNames {
		if name == "typeName" {
			return
		}
	}
	if v.ArgNames == nil && idx < len(v.Args) {
		return
	}
	v.Args = append(v.Args, &ast.StringLit{Pos: v.Pos, Val: v.TypeArg})
	if v.ArgNames == nil {
		v.ArgNames = make([]string, len(v.Args)-1)
	}
	v.ArgNames = append(v.ArgNames, "typeName")
}

func (c *checker) resolveNamedArgs(v *ast.CallExpr, ft *types.FuncType) {
	c.bindTypeArgToName(v, ft)
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
	// The bare `t.0` shorthand is defined only on tuples. A named object or an
	// anonymous one with a field literally called `@"0"` has to be spelled that way -
	// upstream has a compile-error test for each. Unknown passes through with the
	// same reasoning as E28 above: an erased `obj{ :str, :str }` return annotation
	// tracks as Unknown, and rejecting there would fail upstream's own tuples.buzz.
	if v.TupleIndex && ot != types.Unknown {
		if mt, ok := ot.(*types.MapType); !ok || !mt.Tuple {
			c.errorf(v.Pos, "tuple index shorthand `.%s` is only allowed on tuples; use `.@%q` to read a field of that name", v.Name, v.Name)
			return types.Unknown
		}
	}
	switch t := ot.(type) {
	case *types.ObjectType:
		if ft, ok := t.Fields[v.Name]; ok {
			return ft
		}
		if mt, ok := t.Methods[v.Name]; ok {
			return mt
		}
		if t.IsNamespace {
			// A namespace's members are collected in full now - funs, objects, enums,
			// AND exported finals/vars - so a miss is a real error rather than an
			// untracked export. This returned Unknown before, which meant a call to a
			// member that does not exist type-checked and failed at RUN time.
			c.errorfc(v.Pos, UnknownMember, "module %s has no member %q", t.Name, v.Name)
			return types.Unknown
		}
		c.errorf(v.Pos, "object %s has no field or method %q", t.Name, v.Name)
		return types.Unknown
	case *types.ListType:
		if v.Name == "len" {
			return types.Int
		}
		c.checkCollectionMutator(v.Pos, t, v.Name)
		c.noteMutatingUse(v)
		if mut, ok := collectionSelfMethodMut(listSelfMethods, t.Mut, v.Name); ok {
			return selfReturning(&types.ListType{Elem: t.Elem, Mut: mut})
		}
		return types.Unknown
	case *types.MapType:
		if v.Name == "len" {
			return types.Int
		}
		c.checkCollectionMutator(v.Pos, t, v.Name)
		c.noteMutatingUse(v)
		if mut, ok := collectionSelfMethodMut(mapSelfMethods, t.Mut, v.Name); ok {
			return selfReturning(&types.MapType{Key: t.Key, Val: t.Val, Mut: mut})
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
			// A str-backed enum's case value is its NAME, not an ordinal. This
			// returned Int unconditionally, so `StrEnum.one.value` typed as int while
			// the VM answered a string - a silently wrong answer that nothing
			// compared against until comparison typing surfaced it.
			if t.Backing == "str" {
				return types.Str
			}
			return types.Int
		}
		c.errorf(v.Pos, "enum %s has no case %q", t.Name, v.Name)
		return types.Unknown
	}
	return types.Unknown
}

// listSelfMethods and mapSelfMethods name the built-in collection methods whose
// return type is the RECEIVER's own collection type, so their result carries the
// receiver's mutability - upstream builds each one's signature from `obj_list` /
// `obj_map` itself (src/obj.zig). The clone family is the exception that makes the
// mutability visible: it re-types the copy, which is what `typeof
// list.cloneMutable() == <mut [int]>` is asking about.
//
// Everything absent here stays Unknown, exactly as before: `map` and `reduce`
// produce a collection of a type only the callback knows, and `keys`/`values`
// return a fresh IMMUTABLE list upstream regardless of the receiver.
var (
	listSelfMethods = map[string]mutability{
		"sub": sameMut, "fill": sameMut, "filter": sameMut, "sort": sameMut, "reverse": sameMut,
		"clone": alwaysImmutable, "cloneImmutable": alwaysImmutable, "copyImmutable": alwaysImmutable,
		"cloneMutable": alwaysMutable, "copyMutable": alwaysMutable,
	}
	mapSelfMethods = map[string]mutability{
		"filter": sameMut, "sort": sameMut, "diff": sameMut, "intersect": sameMut,
		"clone": alwaysImmutable, "cloneImmutable": alwaysImmutable, "copyImmutable": alwaysImmutable,
		"cloneMutable": alwaysMutable, "copyMutable": alwaysMutable,
	}
)

// mutability says how a self-returning collection method derives its result's
// mutability from the receiver's.
type mutability uint8

const (
	sameMut mutability = iota
	alwaysMutable
	alwaysImmutable
)

func collectionSelfMethodMut(table map[string]mutability, recvMut bool, name string) (mut, ok bool) {
	m, ok := table[name]
	if !ok {
		return false, false
	}
	switch m {
	case alwaysMutable:
		return true, true
	case alwaysImmutable:
		return false, true
	}
	return recvMut, true
}

// selfReturning wraps a collection method's result in a signature that checks
// nothing about its arguments. The parameter lists are deliberately absent: these
// methods were untyped (Unknown) until now, so declaring arities here would turn a
// type answer into new argument diagnostics on source that compiles today. Only
// the return type is being claimed.
func selfReturning(ret types.Type) types.Type {
	return &types.FuncType{Ret: ret, Variadic: true}
}

func (c *checker) inferIndex(v *ast.IndexExpr) types.Type {
	ot := c.infer(v.Object)
	c.infer(v.Index)
	switch t := ot.(type) {
	case *types.ListType:
		// A list index must be a non-null int, and `?[` is the form that tolerates
		// one. Optionality is erased in the type system, so this reads the narrow
		// record kept on the declaration instead - enough for the case that bites
		// (an `int?` local used directly as a subscript) without claiming the
		// checker tracks optionals generally.
		if !v.Optional {
			if id, isName := v.Index.(*ast.IdentExpr); isName {
				if e, found := c.lookup(id.Name); found && e.optional {
					c.errorfc(v.Pos, TypeMismatch, "list index must be an int, but %q may be null; unwrap it or use ?[", id.Name)
				}
			}
		}
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
	savedFnBase := c.fnScopeBase
	c.fnScopeBase = len(c.scopes) - 1
	for i, name := range v.Params {
		c.define(name, params[i], false)
	}
	for _, s := range v.Body.Stmts {
		c.checkStmt(s)
	}
	c.popScope()
	c.fnScopeBase = savedFnBase
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
			// A NAMED field the target object does not declare. Without this the
			// literal quietly stayed a map, so the annotation was accepted and every
			// later member access answered against something that was never the
			// object it claimed to be. A positional (tuple) key is exempt: it names
			// no field, and reporting it would just be the tuple-vs-object mismatch
			// twice.
			if isField && !v.Tuple {
				c.errorf(ast.NodePos(k), "object %s has no field %q", ot.Name, name.Val)
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
		return &types.MapType{Key: keyTyp, Val: valTyp, Mut: v.Mut}
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
		return &types.MapType{Key: types.Any, Val: types.Any, Mut: v.Mut}
	}
	keyTyp := c.infer(v.Keys[0])
	valTyp := c.infer(v.Values[0])
	for i := 1; i < len(v.Keys); i++ {
		c.infer(v.Keys[i])
		c.infer(v.Values[i])
	}
	// Carry tuple-ness onto the type: it is what lets inferMember tell `.{ 1, 2 }.0`
	// (legal) from `.{ @"0" = 1 }.0` (not), which are the same map otherwise.
	return &types.MapType{Key: keyTyp, Val: valTyp, Mut: v.Mut, Tuple: v.Tuple}
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
		return &types.ListType{Elem: elemTyp, Mut: v.Mut}
	}
	if len(v.Items) == 0 {
		return &types.ListType{Elem: types.Any, Mut: v.Mut}
	}
	elemTyp := c.infer(v.Items[0])
	for _, item := range v.Items[1:] {
		c.infer(item)
	}
	return &types.ListType{Elem: elemTyp, Mut: v.Mut}
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
		want := c.resolveType(ft)
		got := c.inferExpected(v.Values[i], want)
		// The value was inferred against the field type but never CHECKED against it,
		// so an object literal was the one assignment position with no type
		// enforcement at all. Mutability is what made it visible: `data: mut [int]`
		// happily took an immutable `[]`, which Compat rejects everywhere else.
		// An unresolved NamedType is a generic parameter (`value: T`). Type arguments
		// are ERASED here, so there is nothing to compare against and any answer would
		// be invented - the same reason inferMember returns Unknown rather than
		// erroring on one.
		if _, unresolved := want.(*types.NamedType); unresolved {
			continue
		}
		if !types.Compat(got, want) {
			c.errorfc(ast.NodePos(v.Values[i]), TypeMismatch, "wrong property type: field %s.%s is %s, got %s", v.TypeName, key, want.TypeName(), got.TypeName())
		}
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
		return mutSpelling(v.Mut) + "[" + canonicalTypeName(v.Elem) + "]"
	case *types.MapType:
		return mutSpelling(v.Mut) + "{" + canonicalTypeName(v.Key) + ": " + canonicalTypeName(v.Val) + "}"
	case nil:
		return "any"
	}
	// Unknown is the checker's tracking-failure sentinel, not a type a user can
	// write. Reporting it as `any` keeps a type value printable and comparable
	// instead of leaking `<unknown>` into a program's output.
	if t == types.Unknown || t == nil {
		return "any"
	}
	// `typeof null` is `<void>` upstream, not `<null>` - types-as-value.buzz asserts
	// it outright. Only the SPELLING of a type value changes here; types.Null stays a
	// distinct type everywhere else, which is what keeps null assignable to a
	// nullable target while void is not.
	if t == types.Null {
		return "void"
	}
	return t.TypeName()
}

// mutSpelling renders the `mut ` modifier for canonicalTypeName. It duplicates
// types.mutPrefix rather than exporting it because the two renderers answer
// different questions - canonicalTypeName follows upstream's spacing, TypeName
// follows the compact annotation form - and a shared helper would tie them
// together in the one place they are allowed to differ.
func mutSpelling(mut bool) string {
	if mut {
		return "mut "
	}
	return ""
}

// --- match arm analysis ---
//
// Upstream rejects a `match` whose arms cannot all be reached (a repeated
// condition, a range overlapping another range or swallowing a literal), whose
// conditions cannot possibly compare against the subject, or which does not cover
// the subject. gopherbuzz performed none of these checks, which made the largest
// single cluster in tests/compile_errors: eleven files that compiled clean here.

// matchCondKind classifies an arm condition by what it can be compared against.
// matchCondOther is the escape hatch for anything not statically known (a call, a
// variable): it is recorded but never reported, because a false positive here
// rejects a correct program.
type matchCondKind int

const (
	matchCondOther matchCondKind = iota
	matchCondNumber
	matchCondString
	matchCondBool
	matchCondEnumCase
	matchCondRange
	matchCondPattern
	matchCondType
)

// matchCondShape is one condition reduced to the form the analyses compare.
type matchCondShape struct {
	kind   matchCondKind
	num    float64 // matchCondNumber
	text   string  // matchCondString contents, or matchCondEnumCase case name
	lo, hi float64 // matchCondRange, half-open [lo, hi)
	pos    ast.Pos
}

// foldConstNumber evaluates a condition to a constant number when it is one.
// Upstream folds before comparing, so `1 + 1` duplicates `2` and `1..(2 + 3)` is
// the range `1..5`. Division is deliberately NOT folded: `1 / 2` is integer
// division for two ints and folding it as float64 would invent a value the VM
// never produces.
func foldConstNumber(n ast.Node) (float64, bool) {
	switch e := n.(type) {
	case *ast.IntLit:
		return float64(e.Val), true
	case *ast.FloatLit:
		return e.Val, true
	case *ast.UnaryExpr:
		v, ok := foldConstNumber(e.Operand)
		if !ok {
			return 0, false
		}
		switch e.Op {
		case "-":
			return -v, true
		case "+":
			return v, true
		}
	case *ast.BinaryExpr:
		l, lok := foldConstNumber(e.Left)
		r, rok := foldConstNumber(e.Right)
		if !lok || !rok {
			return 0, false
		}
		switch e.Op {
		case "+":
			return l + r, true
		case "-":
			return l - r, true
		case "*":
			return l * r, true
		}
	}
	return 0, false
}

// matchCondOf reduces one condition to its comparable shape.
func (c *checker) matchCondOf(n ast.Node) matchCondShape {
	pos := ast.NodePos(n)
	if v, ok := foldConstNumber(n); ok {
		return matchCondShape{kind: matchCondNumber, num: v, pos: pos}
	}
	switch e := n.(type) {
	case *ast.StringLit:
		return matchCondShape{kind: matchCondString, text: e.Val, pos: pos}
	case *ast.BoolLit:
		// text carries the value so `true`/`false` coverage can be counted, the same
		// way enum cases are: a bool subject naming both arms needs no else.
		return matchCondShape{kind: matchCondBool, text: strconv.FormatBool(e.Val), pos: pos}
	case *ast.PatLit:
		return matchCondShape{kind: matchCondPattern, pos: pos}
	case *ast.TypeExpr:
		return matchCondShape{kind: matchCondType, pos: pos}
	case *ast.EnumCaseExpr:
		return matchCondShape{kind: matchCondEnumCase, text: e.Name, pos: pos}
	case *ast.MemberExpr:
		// `Kind.two`: an enum case written explicitly. Only treated as one when the
		// base is a bare identifier naming an enum, so `someRecord.field` stays
		// matchCondOther and is never compared.
		if id, isID := e.Object.(*ast.IdentExpr); isID {
			if _, isEnum := c.types[id.Name].(*types.EnumType); isEnum {
				return matchCondShape{kind: matchCondEnumCase, text: e.Name, pos: pos}
			}
		}
	case *ast.RangeExpr:
		lo, lok := foldConstNumber(e.Lo)
		hi, rok := foldConstNumber(e.Hi)
		if !lok || !rok {
			break
		}
		// Normalized so lo < hi. A descending range is legal (matchTest handles
		// hi < lo separately) and covers the same span for overlap purposes.
		if hi < lo {
			lo, hi = hi, lo
		}
		return matchCondShape{kind: matchCondRange, lo: lo, hi: hi, pos: pos}
	}
	return matchCondShape{kind: matchCondOther, pos: pos}
}

// checkMatchArms reports unreachable, ill-typed and non-exhaustive arms.
func (c *checker) checkMatchArms(v *ast.MatchExpr, subjTyp types.Type) {
	hasElse := false
	covered := map[string]bool{}
	var seen []matchCondShape
	for _, br := range v.Branches {
		if len(br.Conds) == 0 {
			hasElse = true
			continue
		}
		for _, cond := range br.Conds {
			s := c.matchCondOf(cond)
			c.checkMatchCondType(s, subjTyp)
			if s.kind == matchCondEnumCase || s.kind == matchCondBool {
				covered[s.text] = true
			}
			// Compared against every EARLIER condition, including others in this same
			// arm: `1, 1 -> ...` is a duplicate upstream rejects.
			c.checkMatchCondReachable(s, seen)
			if s.kind != matchCondOther {
				seen = append(seen, s)
			}
		}
	}
	if hasElse {
		return
	}
	if et := enumOf(subjTyp); et != nil {
		var missing []string
		for _, name := range et.Cases {
			if !covered[name] {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			c.errorf(v.Pos, "non-exhaustive match over enum %s, missing case(s): %s", et.Name, strings.Join(missing, ", "))
		}
		return
	}
	// A bool is the other finitely-enumerable subject: naming both arms is
	// exhaustive without an else, which upstream's match.buzz asserts in so many
	// words ("boolean match can be exhaustive without else").
	if subjTyp == types.Bool {
		if !covered["true"] || !covered["false"] {
			c.errorf(v.Pos, "non-exhaustive match over bool: cover both `true` and `false`, or add an `else` branch")
		}
		return
	}
	// Nothing else has a finite case set to enumerate, so it needs an else.
	c.errorf(v.Pos, "non-exhaustive match: an `else` branch is required")
}

// checkMatchCondType reports a condition that could never compare against the
// subject. Only the subject types whose comparable set upstream pins are checked;
// an enum, list or unknown subject is left alone rather than guessed at.
func (c *checker) checkMatchCondType(s matchCondShape, subjTyp types.Type) {
	// A type value (`<[str]>`) is legal against ANY subject, and an unrecognized
	// condition is never reported.
	if s.kind == matchCondType || s.kind == matchCondOther {
		return
	}
	switch subjTyp {
	case types.Int, types.Double:
		if s.kind != matchCondNumber && s.kind != matchCondRange {
			c.errorf(s.pos, "match condition must be of type `int`, `double`, `rng` or `type`")
		}
	case types.Str:
		if s.kind != matchCondString && s.kind != matchCondPattern {
			c.errorf(s.pos, "match condition must be of type `str`, `pat` or `type`")
		}
	case types.Bool:
		if s.kind != matchCondBool {
			c.errorf(s.pos, "bad match condition type: a `bool` subject compares against `bool` or `type`")
		}
	}
}

// checkMatchCondReachable reports a condition already covered by an earlier one.
// Ranges are HALF-OPEN, matching the VM's matchTest (`n >= lo && n < hi`), which is
// what lets `0..5` and `5..10` sit side by side while `1..5` and `4..8` overlap.
func (c *checker) checkMatchCondReachable(s matchCondShape, seen []matchCondShape) {
	for _, p := range seen {
		switch {
		case s.kind == matchCondNumber && p.kind == matchCondNumber && s.num == p.num:
			// Compared numerically, so `1` and `1.0` are one condition.
			c.errorf(s.pos, "duplicate match condition")
		case s.kind == matchCondString && p.kind == matchCondString && s.text == p.text:
			c.errorf(s.pos, "duplicate match condition")
		case s.kind == matchCondEnumCase && p.kind == matchCondEnumCase && s.text == p.text:
			c.errorf(s.pos, "duplicate match condition")
		case s.kind == matchCondBool && p.kind == matchCondBool && s.text == p.text:
			c.errorf(s.pos, "duplicate match condition")
		case s.kind == matchCondRange && p.kind == matchCondRange && s.lo < p.hi && p.lo < s.hi:
			c.errorf(s.pos, "overlapping match condition")
		case s.kind == matchCondRange && p.kind == matchCondNumber && p.num >= s.lo && p.num < s.hi:
			c.errorf(s.pos, "overlapping match condition")
		case s.kind == matchCondNumber && p.kind == matchCondRange && s.num >= p.lo && s.num < p.hi:
			c.errorf(s.pos, "overlapping match condition")
		default:
			continue
		}
		return // one report per condition
	}
}

// --- terminal-flow analysis ---
//
// Upstream rejects a statement that can never run ("Code will never be reached"),
// the second-largest cluster in tests/compile_errors. Everything here rests on one
// question - does this statement transfer control away unconditionally? - so the
// answer is computed once, in terminates, and both callers read it.

// terminates reports whether n unconditionally transfers control away from the
// enclosing block, so nothing after it in that block can run. This is the reading
// checkUnreachable needs, and it is deliberately CONSERVATIVE: when in doubt it
// answers false, because a false negative costs only an unreported error while a
// false positive calls live code dead.
func terminates(n ast.Node) bool { return terminatesWith(n, false) }

// terminatesForReturn is the same question asked for checkFunReturns, which needs
// the opposite bias: it errors when a function can FALL THROUGH, so under-claiming
// termination is what invents a diagnostic on correct code.
//
// The two differ on exactly one construct, try/catch. Upstream will not call the
// statement after a fully-returning try/catch dead (tests/behavior/try-catch.buzz
// ends `returnFromCatch` with a `return 31` it considers reachable), yet it also
// does not demand a further return from a function whose try and catches all
// return. Both are satisfied only by asking the question twice.
func terminatesForReturn(n ast.Node) bool { return terminatesWith(n, true) }

func terminatesWith(n ast.Node, tryCounts bool) bool {
	terminates := func(x ast.Node) bool { return terminatesWith(x, tryCounts) }
	switch s := n.(type) {
	case *ast.ReturnStmt, *ast.ThrowStmt, *ast.BreakStmt, *ast.ContinueStmt:
		return true
	case *ast.OutStmt:
		// `out` leaves the enclosing `from { }` with a value, so it ends that block
		// the way a return ends a function - which is what makes a second `out`, or
		// any statement after one, dead code.
		return true
	case *ast.ExprStmt:
		return terminates(s.Expr)
	case *ast.BlockStmt:
		for _, st := range s.Stmts {
			if terminates(st) {
				return true
			}
		}
		return false
	case *ast.IfStmt:
		// Only an if/ELSE can terminate: with no else the false path falls through.
		return s.Else != nil && terminates(s.Then) && terminates(s.Else)
	case *ast.WhileStmt:
		return isConstTrueCond(s.Cond) && !loopHasEscapingBreak(s.Body, s.Label)
	case *ast.ForStmt:
		// An absent condition is `for (;;)`, which never exits on its own.
		return (s.Cond == nil || isConstTrueCond(s.Cond)) && !loopHasEscapingBreak(s.Body, s.Label)
	case *ast.DoStmt:
		// `do { ... } until (cond)` runs its body at least once, so a body that
		// transfers control away means the loop never completes normally - which is
		// what makes the statement after upstream's labeled `continue outer` dead.
		//
		// A break exiting the do lands after it, as for While and For. A do carries
		// no label, so "" suffices: a labeled break unwinds through it regardless.
		return terminates(s.Body) && !loopHasEscapingBreak(s.Body, "")
	case *ast.MatchExpr:
		return matchTerminatesWith(s, tryCounts)
	case *ast.TryStmt:
		// Only the missing-return reading counts this. See terminatesForReturn: the
		// unreachable reading must answer false here or it calls upstream's own
		// `return 31` after a fully-returning try/catch dead.
		if !tryCounts {
			return false
		}
		if !terminates(s.Body) {
			return false
		}
		for _, cl := range s.Catches {
			if !terminates(cl.Body) {
				return false
			}
		}
		return true
	}
	// ForEachStmt is deliberately absent: an empty iterable falls straight through,
	// so a foreach never terminates no matter what its body does.
	return false
}

// isConstTrueCond reports a literal `true` condition - the only infinite loop this
// analysis claims to recognize. A condition that is merely always true in practice
// (`1 == 1`, a constant final) is left alone rather than folded.
func isConstTrueCond(n ast.Node) bool {
	b, ok := n.(*ast.BoolLit)
	return ok && b.Val
}

// matchTerminatesWith reports a match every arm of which transfers control away. It
// requires an explicit else rather than reusing the exhaustiveness analysis: this
// answer decides whether LATER code is dead, so it is the one place where being
// wrong is expensive, and "has an else" is the reading that cannot be wrong.
func matchTerminatesWith(m *ast.MatchExpr, tryCounts bool) bool {
	hasElse := false
	for _, br := range m.Branches {
		if len(br.Conds) == 0 {
			hasElse = true
		}
		if !terminatesWith(br.Body, tryCounts) {
			return false
		}
	}
	return hasElse
}

// loopHasEscapingBreak reports whether body contains a break that exits THIS loop.
// A bare `break` binds to the innermost enclosing loop, so it only counts when it is
// not nested inside another loop; a labeled one counts wherever it appears, which is
// what lets `break outer` from an inner loop keep the outer one non-terminal.
func loopHasEscapingBreak(body *ast.BlockStmt, label string) bool {
	found := false
	var walk func(n ast.Node, nested bool)
	walk = func(n ast.Node, nested bool) {
		if found || n == nil {
			return
		}
		switch s := n.(type) {
		case *ast.BreakStmt:
			if s.Label == "" {
				found = !nested
				break
			}
			// A LABELED break unwinds through every loop between here and its target,
			// so it exits this one whatever it names - matching it against this loop's
			// own label would miss `break outer` from an inner loop. It also covers the
			// label that resolves to no loop at all: that is a separate error, and
			// backing off here lets that better diagnostic be the one reported.
			found = true
		case *ast.BlockStmt:
			for _, st := range s.Stmts {
				walk(st, nested)
			}
		case *ast.ExprStmt:
			walk(s.Expr, nested)
		case *ast.IfStmt:
			walk(s.Then, nested)
			walk(s.Else, nested)
		case *ast.MatchExpr:
			for _, br := range s.Branches {
				walk(br.Body, nested)
			}
		case *ast.WhileStmt:
			walk(s.Body, true)
		case *ast.ForStmt:
			walk(s.Body, true)
		case *ast.ForEachStmt:
			walk(s.Body, true)
		case *ast.DoStmt:
			walk(s.Body, true)
		}
	}
	walk(body, false)
	return found
}

// checkUnreachable reports the first statement in b that follows a terminal one.
// Only the first: everything after it is dead for the same reason, and one
// diagnostic per block is what upstream emits.
func (c *checker) checkUnreachable(b *ast.BlockStmt) {
	for i, s := range b.Stmts {
		if i+1 < len(b.Stmts) && terminates(s) {
			c.errorf(ast.NodePos(b.Stmts[i+1]), "code will never be reached")
			return
		}
	}
}

// checkFunReturns reports a function that declares a value return but can fall off
// the end of its body without producing one.
//
// This is the one check in this file that reads terminates in the UNSAFE direction:
// an incomplete answer here invents an error on a correct function, rather than
// merely missing one. It is therefore narrowed hard - only an explicit,
// non-nullable, non-void annotation qualifies - so every case it fires on is a
// function that plainly promised a value.
func (c *checker) checkFunReturns(fd *ast.FunDecl) {
	// No annotation is not a promise, and `void` promises nothing to return.
	if fd.RetAnnot == "" || fd.RetAnnot == "void" {
		return
	}
	// A nullable return (`> int?`) is satisfied by falling through, which yields
	// null - upstream's own missing-return test annotates a non-optional `> int`.
	if strings.HasSuffix(fd.RetAnnot, "?") {
		return
	}
	if terminatesForReturn(fd.Body) {
		return
	}
	c.errorf(fd.Pos, "missing return statement: %s declares %s but can fall through without returning", fd.Name, fd.RetAnnot)
}

// checkReservedMethodSig enforces the signatures of the two method names the
// runtime itself calls. They are not ordinary methods: `collect` is the finalizer
// hook and `toString` is what string conversion reaches for, so both are invoked
// with a fixed shape no call site can adapt to. Declaring either with a different
// signature compiles today and then fails - or is silently skipped - at the moment
// the runtime tries to use it.
func (c *checker) checkReservedMethodSig(m *ast.FunDecl, ft *types.FuncType) {
	switch m.Name {
	case "collect":
		if len(ft.Params) != 0 || (ft.Ret != nil && ft.Ret != types.Void) {
			c.errorf(m.Pos, "expected `collect` method to be `fun collect() > void`")
		}
	case "toString":
		if len(ft.Params) != 0 || ft.Ret != types.Str {
			c.errorf(m.Pos, "expected `toString` method to be `fun toString() > str`")
		}
	}
}

// checkMainSig enforces upstream's entry-point signature: `main` takes either
// nothing or a single list of arguments, and returns void or an exit code.
func (c *checker) checkMainSig(fd *ast.FunDecl, ft *types.FuncType) {
	if fd.Name != "main" {
		return
	}
	if len(ft.Params) > 1 {
		c.errorf(fd.Pos, "expected `main` signature to be `fun main([args: [str]]) > void|int`")
		return
	}
	// The element type is deliberately not pinned: upstream's own diagnostic spells
	// it `[int]` where the runtime passes strings, so the checkable part is that the
	// parameter is a LIST at all - which is what rejects `fun main(args: int)`.
	if len(ft.Params) == 1 {
		if _, isList := c.resolveType(ft.Params[0]).(*types.ListType); !isList {
			c.errorf(fd.Pos, "expected `main` signature to be `fun main([args: [str]]) > void|int`")
			return
		}
	}
	if ft.Ret != nil && ft.Ret != types.Void && ft.Ret != types.Int {
		c.errorf(fd.Pos, "expected `main` to return void or int, got %s", ft.Ret.TypeName())
	}
}

// checkMutableDefault reports a default value on a slot whose declared type is
// mutable. A default is evaluated ONCE and shared by every call or instance that
// omits it, so a mutable one is aliased state everybody can write through - the
// classic shared-mutable-default bug. Upstream states it as "default value must be
// constant", which is the same rule from the other side: a mutable collection is
// not a constant.
//
// Only the DEFAULT is restricted. `final xs: mut [int] = mut [1]` is an ordinary
// local with its own value per execution and stays legal.
func (c *checker) checkMutableDefault(pos ast.Pos, what, name, annot string, def ast.Node) {
	if def == nil || !strings.HasPrefix(annot, "mut ") {
		return
	}
	c.errorf(pos, "%s %s declares a mutable type and cannot have a default value: it would be created once and shared", what, name)
}

// listMutators and mapMutators name the built-in collection methods that mutate the
// receiver IN PLACE, so calling one on an immutable collection cannot work. The sets
// mirror the VM's own guards exactly (vm/operators.go, `errImmutable`) - the runtime
// already refuses these, so this only moves a guaranteed failure from run time to
// compile time. Keep the two in step: a mutator added there and missed here silently
// loses the static check.
var (
	listMutators = map[string]bool{"append": true, "insert": true, "remove": true, "pop": true, "fill": true, "sort": true}
	mapMutators  = map[string]bool{"remove": true, "sort": true}
)

// checkCollectionMutator reports an in-place mutator called on an immutable
// receiver. It fires only when the receiver's type is KNOWN to be immutable, so an
// untracked collection stays silent rather than guessing.
func (c *checker) checkCollectionMutator(pos ast.Pos, recv types.Type, name string) {
	switch t := recv.(type) {
	case *types.ListType:
		if !t.Mut && listMutators[name] {
			c.errorf(pos, "method `%s` requires a mutable list: declare it with `mut`", name)
		}
	case *types.MapType:
		if !t.Mut && mapMutators[name] {
			c.errorf(pos, "method `%s` requires a mutable map: declare it with `mut`", name)
		}
	}
}

// checkProtocolConformance verifies that an object declaring `object<P> Name`
// actually implements every method P names.
//
// Conformance was DECLARED and then trusted: `Compat` consults the declaration
// only, so `object<Drawable> Foo {}` with none of Drawable's methods type-checked,
// and every call through a Drawable-typed value failed at run time instead. The
// declaration is the promise; this is what makes it one.
func (c *checker) checkProtocolConformance(v *ast.ObjectDecl, ot *types.ObjectType) {
	for _, name := range v.Conforms {
		pt, ok := c.types[name].(*types.ObjectType)
		if !ok || !pt.IsProtocol {
			// An unresolved or non-protocol name is a different error, reported where
			// the conformance list is built. Nothing to verify against here.
			continue
		}
		// Sorted so the diagnostics are deterministic: Methods is a map.
		missing := make([]string, 0, len(pt.Methods))
		for m := range pt.Methods {
			if _, has := ot.Methods[m]; !has {
				missing = append(missing, m)
			}
		}
		sort.Strings(missing)
		for _, m := range missing {
			c.errorf(v.Pos, "object %s is declared as conforming to protocol %s but does not implement method %q", v.Name, name, m)
		}
	}
}

// checkLocalShadowing reports a local declaration that reuses the name of a local
// already visible from an ENCLOSING block.
//
// Shadowing a GLOBAL stays legal, which is why scopes[0] is skipped: upstream
// allows it and TestConformance_LocalShadowsGlobal pins it here. So does redeclaring
// in a sibling block, since neither is visible from the other - only a name still
// live at the point of declaration counts.
//
// It stops at the enclosing function too: a nested closure may reuse an outer name.
func (c *checker) checkLocalShadowing(v *ast.DeclStmt) {
	// The innermost scope is where this declaration lands. A clash THERE is a
	// redeclaration in the same block, which is a different diagnostic; walk only
	// the enclosing local scopes, stopping before the global one and before any
	// scope belonging to an enclosing function.
	floor := 1
	if c.fnScopeBase > floor {
		floor = c.fnScopeBase
	}
	for i := len(c.scopes) - 2; i >= floor; i-- {
		if _, exists := c.scopes[i][v.Name]; exists {
			c.errorf(v.Pos, "a local named %q already exists in an enclosing scope", v.Name)
			return
		}
	}
}

// markAssigned records a write to name, so checkUnassignedVars can tell a `var`
// that is genuinely reassigned from one that should have been a `final`.
func (c *checker) markAssigned(name string) {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if e, ok := c.scopes[i][name]; ok {
			e.assigned = true
			c.scopes[i][name] = e
			return
		}
	}
}

// checkUnassignedVars reports every `var` in the scope about to be popped that was
// never written to. Upstream states it from the other side - "declared `var` but is
// never assigned" - and the fix is to declare it `final`.
func (c *checker) checkUnassignedVars() {
	scope := c.scopes[len(c.scopes)-1]
	unassigned := make([]scopeEntry, 0, len(scope))
	for _, e := range scope {
		if e.varDecl && !e.assigned {
			unassigned = append(unassigned, e)
		}
	}
	// Deterministic order: a scope is a map.
	sort.Slice(unassigned, func(i, j int) bool {
		if unassigned[i].pos.Line != unassigned[j].pos.Line {
			return unassigned[i].pos.Line < unassigned[j].pos.Line
		}
		return unassigned[i].pos.Col < unassigned[j].pos.Col
	})
	for _, e := range unassigned {
		c.errorf(e.pos, "local %q is declared `var` but is never assigned; declare it `final`", e.name)
	}
}

// noteMutatingUse counts an in-place mutation of a local as a use that justifies
// its `var`. Upstream requires this: anonymous-objects.buzz declares
// `var board: mut [...] = mut []` and only ever calls `board.append(...)`, which it
// accepts, while rejecting a `var` that is neither reassigned nor mutated. Writing
// THROUGH the name is as good a reason to have declared it `var` as rebinding it.
func (c *checker) noteMutatingUse(v *ast.MemberExpr) {
	if !listMutators[v.Name] && !mapMutators[v.Name] {
		return
	}
	if id, ok := v.Object.(*ast.IdentExpr); ok {
		c.markAssigned(id.Name)
	}
}

// markRead records that name was evaluated, so checkUnusedLocals can tell a local
// that is genuinely consumed from one that is dead.
func (c *checker) markRead(name string) {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if e, ok := c.scopes[i][name]; ok {
			if !e.read {
				e.read = true
				c.scopes[i][name] = e
			}
			return
		}
	}
}

// declareTracked marks the local just defined in the innermost scope as one whose
// use is worth reporting. Parameters and locals both qualify; a name beginning with
// `_` does not, which is the conventional spelling for "deliberately unused" and is
// how magus's own handlers write an ignored parameter (`_a: [str]`).
func (c *checker) declareTracked(name string, pos ast.Pos) {
	if name == "_" || strings.HasPrefix(name, "_") || len(c.scopes) <= 1 {
		return
	}
	e := c.scopes[len(c.scopes)-1][name]
	e.declaredLocal, e.pos, e.name = true, pos, name
	c.scopes[len(c.scopes)-1][name] = e
}

// checkUnusedLocals reports every tracked local in the scope about to be popped
// that nothing ever read.
func (c *checker) checkUnusedLocals() {
	scope := c.scopes[len(c.scopes)-1]
	unused := make([]scopeEntry, 0, len(scope))
	for _, e := range scope {
		// A local that is WRITTEN counts as used even if nothing reads it back:
		// upstream's protocols.buzz assigns `nameable` a second value and never reads
		// it, and accepts that. Only a local neither read nor written is dead.
		if e.declaredLocal && !e.read && !e.assigned {
			unused = append(unused, e)
		}
	}
	sort.Slice(unused, func(i, j int) bool {
		if unused[i].pos.Line != unused[j].pos.Line {
			return unused[i].pos.Line < unused[j].pos.Line
		}
		return unused[i].pos.Col < unused[j].pos.Col
	})
	for _, e := range unused {
		c.errorf(e.pos, "local %q is never used; remove it or rename it with a leading underscore", e.name)
	}
}

// inferUnannotatedReturns fills in the return type of a function or method that
// declares none but whose whole body is `return expr` - upstream's arrow sugar,
// `fun prepare() => this.prepareRaw()`.
//
// It runs as its OWN pass, after every type is registered and before any body is
// checked, because the call site may precede the declaration: upstream's
// placeholder-nested-call.buzz has `Query.execute` calling `db.prepare().missing()`
// with Database declared further down the file. Left unannotated the return was
// Unknown, so member access on the result asserted nothing and the typo went
// through.
//
// Errors are discarded here. This pass evaluates expressions out of order purely
// to learn their types; anything genuinely wrong is reported when the body is
// checked for real, and reporting it twice - or reporting a spurious ordering
// error - is worse than staying quiet.
func (c *checker) inferUnannotatedReturns(prog *ast.Program) {
	saved, savedWarnings := c.errors, c.warnings
	defer func() { c.errors, c.warnings = saved, savedWarnings }()
	for _, stmt := range prog.Stmts {
		switch d := stmt.(type) {
		case *ast.FunDecl:
			if ft, ok := c.lookupFuncType(d.Name); ok {
				c.fillReturn(d, ft, nil)
			}
		case *ast.ObjectDecl:
			ot, _ := c.types[d.Name].(*types.ObjectType)
			if ot == nil {
				continue
			}
			for _, m := range d.Methods {
				if ft, ok := ot.Methods[m.Name]; ok {
					c.fillReturn(m, ft, ot)
				}
			}
		}
	}
}

// lookupFuncType returns the recorded signature of a top-level function.
func (c *checker) lookupFuncType(name string) (*types.FuncType, bool) {
	e, ok := c.lookup(name)
	if !ok {
		return nil, false
	}
	ft, ok := e.typ.(*types.FuncType)
	return ft, ok
}

// fillReturn infers fd's return type from a lone `return expr` body and writes it
// onto ft in place, so every call site that already holds this signature sees it.
// recv is the enclosing object for a method, nil for a free function.
func (c *checker) fillReturn(fd *ast.FunDecl, ft *types.FuncType, recv types.Type) {
	if fd.RetAnnot != "" || fd.IsExtern || fd.Body == nil || len(fd.Body.Stmts) != 1 {
		return
	}
	ret, isReturn := fd.Body.Stmts[0].(*ast.ReturnStmt)
	if !isReturn || ret.Value == nil {
		return
	}
	c.pushScope()
	if recv != nil {
		c.define("this", recv, false)
	}
	for i, name := range fd.Params {
		pt := types.Type(types.Unknown)
		if i < len(ft.Params) {
			pt = ft.Params[i]
		}
		c.define(name, pt, false)
	}
	got := c.infer(ret.Value)
	c.scopes = c.scopes[:len(c.scopes)-1] // not popScope: this scope's diagnostics are discarded
	if got != types.Unknown {
		ft.Ret = got
	}
}

// possiblyNullName reports whether n can evaluate to a value that was DECLARED
// optional, naming it when so.
//
// It looks through the forms that pass a value straight out - a match or inline-if
// arm - because that is how upstream's arrow-return-match-optional.buzz smuggles an
// `int?` parameter out of a `> int` function. It deliberately does NOT look through
// `??`, a force-unwrap, or anything computed: those produce a new value whose
// nullability this narrow record cannot speak to.
func (c *checker) possiblyNullName(n ast.Node) (string, bool) {
	switch e := n.(type) {
	case *ast.IdentExpr:
		if entry, found := c.lookup(e.Name); found && entry.optional {
			return e.Name, true
		}
	case *ast.MatchExpr:
		for _, br := range e.Branches {
			if name, ok := c.possiblyNullName(br.Body); ok {
				return name, true
			}
		}
	case *ast.IfExpr:
		if name, ok := c.possiblyNullName(e.Then); ok {
			return name, true
		}
		return c.possiblyNullName(e.Else)
	}
	return "", false
}
