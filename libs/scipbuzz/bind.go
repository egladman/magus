package scipbuzz

import (
	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/egladman/magus/libs/gopherbuzz/token"
	"github.com/scip-code/scip/bindings/go/scip"
)

// binding is what a name in scope refers to.
type binding struct {
	// symbol is empty for a namespace segment, which has no declaration to name.
	symbol string
	decl   *decl   // a top-level declaration of a workspace file
	module *module // the namespace an import binds
	// children are the next segments of a `namespace a\b` path an import binds.
	children map[string]*binding
	isType   bool // the name may appear in a type annotation
}

type occKey struct {
	symbol string
	rng    scip.Range
	roles  int32
}

// walker binds the names of one document and records their occurrences. Top-level
// names are visible throughout the file; block, parameter and loop names from
// their declaration to the end of their scope.
type walker struct {
	ix     *indexer
	f      *file
	s      *stream // the stream the current node's positions refer to
	scopes []map[string]*binding
	// owner is the global symbol whose body is being walked, the enclosing_symbol
	// of the locals declared there.
	owner  string
	occs   []*scip.Occurrence
	seen   map[occKey]bool
	locals []*scip.SymbolInformation
}

func newWalker(ix *indexer, f *file) *walker {
	return &walker{ix: ix, f: f, s: f.toks, seen: map[occKey]bool{}}
}

func (w *walker) push() { w.scopes = append(w.scopes, map[string]*binding{}) }
func (w *walker) pop()  { w.scopes = w.scopes[:len(w.scopes)-1] }

func (w *walker) define(name string, b *binding) {
	if name == "" || name == "_" {
		return
	}
	w.scopes[len(w.scopes)-1][name] = b
}

func (w *walker) lookup(name string) *binding {
	for i := len(w.scopes) - 1; i >= 0; i-- {
		if b, ok := w.scopes[i][name]; ok {
			return b
		}
	}
	return nil
}

func (w *walker) warn(format string, args ...any) {
	w.ix.warn("%s: "+format, append([]any{w.f.rel}, args...)...)
}

// occurAt records an occurrence over r and returns it, or nil when the same one
// is already recorded.
func (w *walker) occurAt(r scip.Range, symbol string, roles scip.SymbolRole) *scip.Occurrence {
	key := occKey{symbol: symbol, rng: r, roles: int32(roles)}
	if w.seen[key] {
		return nil
	}
	w.seen[key] = true
	occ := &scip.Occurrence{Symbol: symbol, SymbolRoles: int32(roles)}
	occ.SetSourceRange(r)
	w.occs = append(w.occs, occ)
	return occ
}

// occur records an occurrence over the identifier at i of the current stream.
func (w *walker) occur(i int, symbol string, roles scip.SymbolRole) {
	r, ok := w.s.identRange(i)
	if !ok {
		if i >= 0 && i < len(w.s.toks) {
			w.warn("%d:%d: cannot place an occurrence of %s", w.s.toks[i].Line, w.s.toks[i].Col, symbol)
		} else {
			w.warn("cannot place an occurrence of %s", symbol)
		}
		return
	}
	w.occurAt(r, symbol, roles)
}

// reference records a use of b at the identifier at i. A namespace segment has
// no symbol and records nothing.
func (w *walker) reference(i int, b *binding, roles scip.SymbolRole) {
	if b.decl != nil {
		w.ix.noteDecl(b.decl)
	}
	if b.symbol != "" {
		w.occur(i, b.symbol, roles)
	}
}

// newLocal mints the next local symbol of this document.
func (w *walker) newLocal(name string, kind scip.SymbolInformation_Kind) string {
	sym := localSymbol(len(w.locals))
	w.locals = append(w.locals, &scip.SymbolInformation{
		Symbol: sym, DisplayName: name, Kind: kind, EnclosingSymbol: w.owner,
	})
	return sym
}

// bindLocal declares a local named by the identifier at i and binds it in the
// current scope. It binds the name even when i does not place it, so a use still
// resolves to the local rather than to an outer declaration of the same name.
func (w *walker) bindLocal(i int, name string, kind scip.SymbolInformation_Kind) *binding {
	if name == "" || name == "_" {
		return nil
	}
	b := &binding{symbol: w.newLocal(name, kind)}
	if i >= 0 && w.s.isIdent(i, name) {
		w.occur(i, b.symbol, scip.SymbolRole_Definition)
	} else {
		w.warn("cannot place the declaration of local %s", name)
	}
	w.define(name, b)
	return b
}

// bindTypeParams binds the `::<T, U>` names of a generic function or object in
// the current scope, at the identifiers idx when they spell them.
func (w *walker) bindTypeParams(idx []int, names []string) {
	for k, name := range names {
		at := -1
		if k < len(idx) {
			at = idx[k]
		}
		if b := w.bindLocal(at, name, scip.SymbolInformation_TypeParameter); b != nil {
			b.isType = true
		}
	}
}

// at returns the current stream's token index for pos, warning when there is none.
func (w *walker) at(pos ast.Pos, what string) (int, bool) {
	i, ok := w.s.at[pos]
	if !ok {
		w.warn("%d:%d: cannot place %s", pos.Line, pos.Col, what)
	}
	return i, ok
}

func (w *walker) walkFile() {
	w.push()
	for _, st := range w.f.prog.Stmts {
		if imp, ok := st.(*ast.ImportStmt); ok {
			w.importStmt(imp)
		}
	}
	for _, d := range w.f.order {
		w.define(d.name, &binding{symbol: d.symbol, decl: d, isType: d.kind.isType()})
	}
	w.standaloneExports()
	for _, st := range w.f.prog.Stmts {
		w.topLevel(st)
	}
	w.pop()
}

// standaloneExports records the name in each `export name;`, which the parser
// folds into the declaration it names and leaves out of the AST.
func (w *walker) standaloneExports() {
	s := w.s
	for i := range s.toks {
		if s.kind(i) != token.Export || s.kind(i+1) != token.Ident {
			continue
		}
		if k := s.kind(i + 2); k != token.Semicolon && k != token.EOF {
			continue
		}
		if b := w.lookup(s.toks[i+1].Val); b != nil && b.decl != nil && b.decl.symbol != "" {
			w.reference(i+1, b, 0)
		}
	}
}

// importStmt binds what an import makes visible, the way gopherbuzz's session
// does. A selective import binds the names it lists. An aliased one binds the
// alias. A flat one, with no alias or `as _`, binds the file's exported names
// unqualified, the path's basename, and the path of a `namespace a\b` the file
// declares.
func (w *walker) importStmt(n *ast.ImportStmt) {
	m := w.ix.resolve(w.f, n.Path)
	i, placed := w.at(n.Pos, "import")
	var names []int
	pathTok, aliasTok := -1, -1
	if placed {
		j := i + 1
		if len(n.Only) > 0 {
			for w.s.kind(j) == token.Ident && !w.s.isIdent(j, "from") {
				names = append(names, j)
				j++
				if w.s.kind(j) == token.Comma {
					j++
				}
			}
			j++ // from
		}
		pathTok = j
		if w.s.kind(j+1) == token.As && w.s.kind(j+2) == token.Ident {
			aliasTok = j + 2
		}
	}
	if len(n.Only) > 0 {
		for k, name := range n.Only {
			b := w.memberBinding(m, name)
			if b == nil {
				continue
			}
			if k < len(names) && w.s.toks[names[k]].Val == name {
				w.reference(names[k], b, scip.SymbolRole_Import)
			}
			w.define(name, b)
		}
		return
	}
	flat := n.Alias == "" || n.Alias == "_"
	if flat && m.file != nil {
		for _, d := range m.file.order {
			if d.exported {
				w.define(d.name, &binding{symbol: d.symbol, decl: d, isType: d.kind.isType()})
			}
		}
	}
	name := n.Alias
	if flat {
		name = bindingName(n.Path)
	}
	b := &binding{symbol: w.newLocal(name, scip.SymbolInformation_Module), module: m}
	switch {
	case n.Alias == "_":
	case aliasTok >= 0:
		w.occur(aliasTok, b.symbol, scip.SymbolRole_Definition)
	case pathTok >= 0:
		if r, ok := w.s.segmentRange(pathTok, name); ok {
			w.occurAt(r, b.symbol, scip.SymbolRole_Definition)
		}
	}
	w.define(name, b)
	if flat && m.file != nil {
		w.bindNamespace(m.file.namespace(), m)
	}
}

// bindNamespace binds the segments of a `namespace a\b` path to m, nesting under
// a namespace another import already started. Like gopherbuzz it never replaces
// any other binding of the first segment.
func (w *walker) bindNamespace(segments []string, m *module) {
	if len(segments) == 0 {
		return
	}
	scope := w.scopes[len(w.scopes)-1]
	head, ok := scope[segments[0]]
	if !ok {
		head = &binding{}
		scope[segments[0]] = head
	} else if head.module != nil || head.symbol != "" {
		return
	}
	for _, seg := range segments[1:] {
		if head.children == nil {
			head.children = map[string]*binding{}
		}
		next, ok := head.children[seg]
		if !ok {
			next = &binding{}
			head.children[seg] = next
		}
		head = next
	}
	if head.module == nil {
		head.module = m
	}
}

// memberBinding resolves name as a member of m: an exported declaration of a
// workspace file, or a member of an external module. It returns nil for a
// workspace file that exports no such name.
func (w *walker) memberBinding(m *module, name string) *binding {
	if m.file != nil {
		d := m.file.decls[name]
		if d == nil || !d.exported {
			return nil
		}
		return &binding{symbol: d.symbol, decl: d, isType: d.kind.isType()}
	}
	sym, kind := hostSymbol(m.path, name)
	w.ix.noteHost(sym, name, kind)
	return &binding{symbol: sym, isType: kind != scip.SymbolInformation_Function}
}

// member records the member named by the identifier at i, reached through m.
func (w *walker) member(m *module, i int) {
	if w.s.kind(i) != token.Ident {
		return
	}
	if b := w.memberBinding(m, w.s.toks[i].Val); b != nil {
		w.reference(i, b, 0)
	}
}

// recordTypes records the type names an annotation's typeRefs point at.
func (w *walker) recordTypes(refs []typeRef) {
	for _, r := range refs {
		if r.ns < 0 {
			if b := w.lookup(w.s.toks[r.name].Val); b != nil && b.isType {
				w.reference(r.name, b, 0)
			}
			continue
		}
		b := w.lookup(w.s.toks[r.ns].Val)
		if b == nil || b.module == nil {
			continue
		}
		w.reference(r.ns, b, 0)
		w.member(b.module, r.name)
	}
}

// typeAt records the type names of the annotation starting at i.
func (w *walker) typeAt(i int) {
	var refs []typeRef
	w.s.skipType(i, &refs)
	w.recordTypes(refs)
}

// topLevel walks a statement of the file's top level, where declarations bind
// the global symbols collectDecls assigned.
func (w *walker) topLevel(st ast.Node) {
	switch n := st.(type) {
	case *ast.ImportStmt, *ast.NamespaceStmt:
	case *ast.FunDecl:
		w.function(n, w.f.decls[n.Name])
	case *ast.DeclStmt:
		w.declStmt(n, w.f.decls[n.Name])
	case *ast.ObjectDecl:
		w.objectDecl(n, w.f.decls[n.Name])
	case *ast.EnumDecl:
		w.enumDecl(n, w.f.decls[n.Name])
	default:
		w.visit(st)
	}
}

// signature places the signature of the named function or method n, warning and
// returning one that places nothing when it cannot.
func (w *walker) signature(n *ast.FunDecl, what string) funcSig {
	i, ok := w.at(n.Pos, what)
	var h funcSig
	if ok {
		h, ok = w.s.signatureAt(i)
	}
	if !ok || h.name < 0 || len(h.params) != len(n.Params) {
		w.warn("%d:%d: cannot place the signature of %s", n.Pos.Line, n.Pos.Col, what)
		return funcSig{name: -1}
	}
	return h
}

// function walks a named function. d is its top-level declaration, or nil for a
// function declared in a block, which binds a local.
func (w *walker) function(n *ast.FunDecl, d *decl) {
	h := w.signature(n, "function "+n.Name)
	owner := w.owner
	if d != nil {
		role := scip.SymbolRole_Definition
		if n.IsExtern {
			role = scip.SymbolRole_ForwardDefinition
		}
		w.definition(d, role)
		w.owner = d.symbol
	} else {
		w.bindLocal(h.name, n.Name, scip.SymbolInformation_Function)
	}
	w.callable(h, n.TypeParams, n.Params, n.ParamDefaults, n.Body)
	w.owner = owner
}

// definition records the defining occurrence of a top-level declaration, with
// the enclosing range of a function, object or enum.
func (w *walker) definition(d *decl, role scip.SymbolRole) {
	r, ok := w.f.toks.identRange(d.nameTok)
	if !ok {
		return
	}
	occ := w.occurAt(r, d.symbol, role)
	if occ == nil || d.end < 0 {
		return
	}
	toks := w.f.toks.toks
	if d.start < 0 || d.end < d.start || d.end >= len(toks) || toks[d.start].off < 0 || toks[d.end].off < 0 {
		return
	}
	occ.SetEnclosingSourceRange(w.f.src.span(toks[d.start].off, toks[d.end].off+1))
}

// callable walks a function's type parameters and annotations, then its parameter
// defaults, then its parameters and body in a scope of their own.
func (w *walker) callable(h funcSig, typeParams, params []string, defaults []ast.Node, body *ast.BlockStmt) {
	w.push()
	w.bindTypeParams(h.typeParams, typeParams)
	w.recordTypes(h.refs)
	for _, def := range defaults {
		w.visit(def)
	}
	w.push()
	for k, p := range params {
		at := -1
		if k < len(h.params) {
			at = h.params[k]
		}
		w.bindLocal(at, p, scip.SymbolInformation_Parameter)
	}
	if body != nil {
		w.visit(body)
	}
	w.pop()
	w.pop()
}

// declStmt walks `final|var name[: T] = value`. d is its top-level declaration,
// or nil for one in a block, which binds a local after its value is walked.
func (w *walker) declStmt(n *ast.DeclStmt, d *decl) {
	w.visit(n.Value)
	i, ok := w.at(n.Pos, "declaration "+n.Name)
	name := -1
	if ok {
		var refs []typeRef
		name, refs = w.s.declName(i)
		w.recordTypes(refs)
	}
	if d != nil {
		w.definition(d, scip.SymbolRole_Definition)
		return
	}
	if n.Name == "_" {
		return
	}
	kind := scip.SymbolInformation_Variable
	if n.IsConst {
		kind = scip.SymbolInformation_Constant
	}
	w.bindLocal(name, n.Name, kind)
}

func (w *walker) objectDecl(n *ast.ObjectDecl, d *decl) {
	i, ok := w.at(n.Pos, "object "+n.Name)
	h := objHeader{name: -1}
	if ok {
		h, ok = w.s.objectHeader(i)
	}
	if !ok {
		w.warn("%d:%d: cannot place the header of %s", n.Pos.Line, n.Pos.Col, n.Name)
		h = objHeader{name: -1}
	}
	owner := w.owner
	if d != nil {
		w.definition(d, scip.SymbolRole_Definition)
		w.owner = d.symbol
	} else {
		w.bindLocal(h.name, n.Name, scip.SymbolInformation_Object)
	}
	for _, c := range h.conforms {
		w.recordTypes([]typeRef{{ns: -1, name: c}})
	}
	w.push()
	w.bindTypeParams(h.typeParams, n.TypeParams)
	if ok {
		w.recordTypes(w.s.fieldTypes(h.brace))
	}
	for _, f := range n.Fields {
		w.visit(f.Default)
	}
	for _, f := range n.StaticFields {
		w.visit(f.Default)
	}
	for _, m := range n.Methods {
		w.method(m)
	}
	w.pop()
	w.owner = owner
}

// method walks an object's method. Members are not bound in phase 1: the method's
// own name gets no symbol, only its signature and body are walked.
func (w *walker) method(n *ast.FunDecl) {
	h := w.signature(n, "method "+n.Name)
	w.callable(h, n.TypeParams, n.Params, n.ParamDefaults, n.Body)
}

func (w *walker) enumDecl(n *ast.EnumDecl, d *decl) {
	if d != nil {
		w.definition(d, scip.SymbolRole_Definition)
	} else {
		name := -1
		if i, ok := w.at(n.Pos, "enum "+n.Name); ok {
			if at, _, ok := w.s.enumHeader(i); ok {
				name = at
			}
		}
		w.bindLocal(name, n.Name, scip.SymbolInformation_Enum)
	}
	for _, v := range n.Values {
		w.visit(v)
	}
}

func (w *walker) block(b *ast.BlockStmt) {
	if b == nil {
		return
	}
	w.push()
	for _, st := range b.Stmts {
		w.visit(st)
	}
	w.pop()
}

// visit walks any statement or expression below the top level.
func (w *walker) visit(n ast.Node) {
	switch n := n.(type) {
	case nil, *ast.BreakStmt, *ast.ContinueStmt, *ast.ImportStmt, *ast.NamespaceStmt,
		*ast.StringLit, *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.NullLit, *ast.PatLit,
		*ast.EnumCaseExpr:
		// Nothing in these names a binding: a case's enum comes from the checker.
	case *ast.BlockStmt:
		w.block(n)
	case *ast.FunDecl:
		w.function(n, nil)
	case *ast.DeclStmt:
		w.declStmt(n, nil)
	case *ast.ObjectDecl:
		w.objectDecl(n, nil)
	case *ast.EnumDecl:
		w.enumDecl(n, nil)
	case *ast.TestDecl:
		w.block(n.Body)
	case *ast.AssignStmt:
		w.assign(n)
	case *ast.ReturnStmt:
		w.visit(n.Value)
	case *ast.ExprStmt:
		w.visit(n.Expr)
	case *ast.ThrowStmt:
		w.visit(n.Value)
	case *ast.OutStmt:
		w.visit(n.Value)
	case *ast.IfStmt:
		w.ifStmt(n)
	case *ast.WhileStmt:
		w.visit(n.Cond)
		w.block(n.Body)
	case *ast.DoStmt:
		w.block(n.Body)
		w.visit(n.Cond)
	case *ast.ForStmt:
		w.push()
		for _, init := range n.Init {
			w.visit(init)
		}
		w.visit(n.Cond)
		for _, post := range n.Post {
			w.visit(post)
		}
		w.block(n.Body)
		w.pop()
	case *ast.ForEachStmt:
		w.forEach(n)
	case *ast.TryStmt:
		w.block(n.Body)
		for _, c := range n.Catches {
			w.catch(c)
		}
	case *ast.IdentExpr:
		w.ident(n, 0)
	case *ast.MemberExpr:
		w.visit(n.Object)
		w.memberExpr(n)
	case *ast.ObjectLit:
		w.objectLit(n)
		w.fieldValues(n.Values)
	case *ast.InterpExpr:
		w.interp(n)
	case *ast.FunExpr:
		w.funExpr(n)
	case *ast.TypeExpr:
		if i, ok := w.at(n.Pos, "type expression"); ok {
			w.typeAt(i + 1)
		}
	case *ast.IsExpr:
		w.visit(n.Expr)
		if i, ok := w.at(n.Pos, "is"); ok {
			w.typeAt(i + 1)
		}
	case *ast.AsExpr:
		w.visit(n.Expr)
		w.asType(n)
	case *ast.ListExpr:
		if n.ElemType != "" {
			if i, ok := w.at(n.Pos, "list type"); ok && w.s.kind(i+1) == token.Lt {
				w.typeAt(i + 2)
			}
		}
		for _, it := range n.Items {
			w.visit(it)
		}
	case *ast.MapExpr:
		if n.KeyType != "" {
			w.mapTypes(n)
		}
		for _, k := range n.Keys {
			w.visit(k)
		}
		w.fieldValues(n.Values)
	case *ast.MatchExpr:
		w.visit(n.Subject)
		for _, br := range n.Branches {
			for _, c := range br.Conds {
				w.visit(c)
			}
			w.visit(br.Body)
		}
	case *ast.BinaryExpr:
		w.visit(n.Left)
		w.visit(n.Right)
	case *ast.UnaryExpr:
		w.visit(n.Operand)
	case *ast.TypeOfExpr:
		w.visit(n.Operand)
	case *ast.CallExpr:
		w.call(n)
	case *ast.IndexExpr:
		w.visit(n.Object)
		w.visit(n.Index)
	case *ast.ForceExpr:
		w.visit(n.Operand)
	case *ast.RangeExpr:
		w.visit(n.Lo)
		w.visit(n.Hi)
	case *ast.CatchExpr:
		w.visit(n.Expr)
		w.visit(n.Default)
	case *ast.BlockExpr:
		w.block(n.Body)
	case *ast.IfExpr:
		w.visit(n.Cond)
		w.visit(n.Then)
		w.visit(n.Else)
	case *ast.YieldExpr:
		w.visit(n.Value)
	case *ast.FiberExpr:
		if n.Call != nil {
			w.visit(n.Call)
		}
	case *ast.ResumeExpr:
		w.visit(n.Fiber)
	case *ast.ResolveExpr:
		w.visit(n.Fiber)
	default:
		pos := ast.NodePos(n)
		w.warn("%d:%d: cannot walk a %T", pos.Line, pos.Col, n)
	}
}

// assign walks `target = value`. The parser desugars `x op= v` to `x = x op v`
// with the one target node on both sides, which is one use of x, a write.
func (w *walker) assign(n *ast.AssignStmt) {
	if id, ok := n.Target.(*ast.IdentExpr); ok {
		w.ident(id, scip.SymbolRole_WriteAccess)
	} else {
		w.visit(n.Target)
	}
	if bin, ok := n.Value.(*ast.BinaryExpr); ok && bin.Left == n.Target {
		w.visit(bin.Right)
		return
	}
	w.visit(n.Value)
}

// call walks a call. An unlabeled bare identifier after the first argument is an
// implicit label as well as a value (`f(a, b)` is `f(a, b: b)`), so it gets no
// occurrence: a rename rewriting it would rename the label too.
func (w *walker) call(n *ast.CallExpr) {
	w.visit(n.Callee)
	if n.TypeArg != "" {
		w.callTypeArgs(n)
	}
	for k, a := range n.Args {
		if _, bare := a.(*ast.IdentExpr); bare && k > 0 && (k >= len(n.ArgNames) || n.ArgNames[k] == "") {
			continue
		}
		w.visit(a)
	}
}

// callTypeArgs records the type names of `f::<T>(...)` and `ns\f::<T>(...)`.
func (w *walker) callTypeArgs(n *ast.CallExpr) {
	var i int
	switch c := n.Callee.(type) {
	case *ast.IdentExpr:
		at, ok := w.at(c.Pos, c.Name)
		if !ok {
			return
		}
		i = at
	case *ast.MemberExpr:
		at, ok := w.at(c.Pos, "member "+c.Name)
		if !ok {
			return
		}
		i = at + 1
	default:
		return
	}
	if w.s.kind(i+1) != token.Colon || w.s.kind(i+2) != token.Colon || w.s.kind(i+3) != token.Lt {
		return
	}
	var refs []typeRef
	w.s.typeArgs(i+3, &refs)
	w.recordTypes(refs)
}

// fieldValues walks the values of an object literal or a `.{...}` anonymous
// object. A punned field, `Rect{ w }` for `Rect{ w = w }`, is a label as well as
// a value, so it gets no occurrence: a rename rewriting it would rename the field.
func (w *walker) fieldValues(values []ast.Node) {
	for _, v := range values {
		if id, ok := v.(*ast.IdentExpr); ok && w.punned(id) {
			continue
		}
		w.visit(v)
	}
}

// punned reports whether id stands alone between the separators of a field list,
// where the parser reads it as both the field's name and its value.
func (w *walker) punned(id *ast.IdentExpr) bool {
	i, ok := w.s.at[id.Pos]
	if !ok {
		return false
	}
	before, after := w.s.kind(i-1), w.s.kind(i+1)
	return (before == token.LBrace || before == token.Comma) && (after == token.Comma || after == token.RBrace)
}

func (w *walker) ident(n *ast.IdentExpr, roles scip.SymbolRole) {
	b := w.lookup(n.Name)
	if b == nil {
		return
	}
	i, ok := w.at(n.Pos, n.Name)
	if !ok {
		return
	}
	if !w.s.isIdent(i, n.Name) {
		w.warn("%d:%d: %s is not at its position", n.Pos.Line, n.Pos.Col, n.Name)
		return
	}
	w.reference(i, b, roles)
}

// qualifier returns the binding an `a` or `a\b` qualifier names.
func (w *walker) qualifier(n ast.Node) *binding {
	switch n := n.(type) {
	case *ast.IdentExpr:
		return w.lookup(n.Name)
	case *ast.MemberExpr:
		if q := w.qualifier(n.Object); q != nil {
			return q.children[n.Name]
		}
	}
	return nil
}

// memberExpr records ns\member and ns.member where ns names an import or a path
// of a namespace one binds. Members of other values need their receiver's type and
// are left for a later phase.
func (w *walker) memberExpr(n *ast.MemberExpr) {
	b := w.qualifier(n.Object)
	if b == nil || b.module == nil {
		return
	}
	i, ok := w.at(n.Pos, "member "+n.Name)
	if !ok {
		return
	}
	if !w.s.isIdent(i+1, n.Name) {
		w.warn("%d:%d: member %s is not after its operator", n.Pos.Line, n.Pos.Col, n.Name)
		return
	}
	w.member(b.module, i+1)
}

// objectLit records the type name of `Name{...}` or `ns\Name{...}`, which the
// parser keeps only as strings.
func (w *walker) objectLit(n *ast.ObjectLit) {
	i, ok := w.at(n.Pos, "object literal "+n.TypeName)
	if !ok {
		return
	}
	if !w.s.isIdent(i-1, n.TypeName) {
		w.warn("%d:%d: object literal type %s is not before its brace", n.Pos.Line, n.Pos.Col, n.TypeName)
		return
	}
	if n.Namespace == "" {
		if b := w.lookup(n.TypeName); b != nil && b.module == nil {
			w.reference(i-1, b, 0)
		}
		return
	}
	if w.s.kind(i-2) != token.Backslash || !w.s.isIdent(i-3, n.Namespace) {
		w.warn("%d:%d: object literal namespace %s is not before its type", n.Pos.Line, n.Pos.Col, n.Namespace)
		return
	}
	b := w.lookup(n.Namespace)
	if b == nil || b.module == nil {
		return
	}
	w.reference(i-3, b, 0)
	w.member(b.module, i-1)
}

// interp walks the expressions of an interpolated string, each in the stream of
// its own source.
func (w *walker) interp(n *ast.InterpExpr) {
	i, ok := w.at(n.Pos, "interpolated string")
	if !ok || w.s.kind(i) != token.InterpStr {
		return
	}
	subs := w.s.sub(i)
	if len(subs) != len(n.Parts) {
		w.warn("%d:%d: cannot place the parts of an interpolated string", n.Pos.Line, n.Pos.Col)
		return
	}
	outer := w.s
	for j, part := range n.Parts {
		if part.Expr == nil {
			continue
		}
		if subs[j] == nil {
			w.warn("%d:%d: cannot place interpolation %d", n.Pos.Line, n.Pos.Col, j)
			continue
		}
		w.s = subs[j]
		w.visit(part.Expr)
		w.s = outer
	}
}

func (w *walker) funExpr(n *ast.FunExpr) {
	i, ok := w.at(n.Pos, "function expression")
	var h funcSig
	if ok {
		h, ok = w.s.signatureAt(i)
	}
	if !ok || h.name >= 0 || len(h.params) != len(n.Params) {
		w.warn("%d:%d: cannot place the signature of a function expression", n.Pos.Line, n.Pos.Col)
		h = funcSig{name: -1}
	}
	w.callable(h, n.TypeParams, n.Params, n.ParamDefaults, n.Body)
}

// asType records the type of `x as T`. Inside `if (x as name: T)` the token after
// `as` is the binding, which ifStmt declares.
func (w *walker) asType(n *ast.AsExpr) {
	i, ok := w.at(n.Pos, "as")
	if !ok {
		return
	}
	j := i + 1
	if k := w.s.kind(j); k == token.Question || k == token.Bang {
		j++
	}
	if w.s.kind(j) == token.Ident && w.s.kind(j+1) == token.Colon && w.s.kind(j+2) != token.Colon {
		j += 2
	}
	w.typeAt(j)
}

// mapTypes records the types of a `{<K: V>, ...}` literal.
func (w *walker) mapTypes(n *ast.MapExpr) {
	i, ok := w.at(n.Pos, "map type")
	if !ok || w.s.kind(i+1) != token.Lt {
		return
	}
	var refs []typeRef
	j, ok := w.s.skipType(i+2, &refs)
	if ok && w.s.kind(j) == token.Colon {
		w.s.skipType(j+1, &refs)
	}
	w.recordTypes(refs)
}

func (w *walker) ifStmt(n *ast.IfStmt) {
	w.visit(n.Cond)
	w.push()
	if n.BindName != "" {
		w.bindLocal(w.ifBinding(n), n.BindName, scip.SymbolInformation_Variable)
	}
	if n.Then != nil {
		for _, st := range n.Then.Stmts {
			w.visit(st)
		}
	}
	w.pop()
	w.visit(n.Else)
}

// ifBinding returns the index of the name an `if (x -> name)` or
// `if (x as name: T)` binds.
func (w *walker) ifBinding(n *ast.IfStmt) int {
	i, ok := w.at(n.Pos, "if")
	if !ok || w.s.kind(i+1) != token.LParen {
		return -1
	}
	end, ok := w.s.closer(i + 1)
	if !ok {
		return -1
	}
	if w.s.kind(end-2) == token.Arrow && w.s.isIdent(end-1, n.BindName) {
		return end - 1
	}
	for j := end - 1; j > i; j-- {
		if w.s.kind(j) == token.As && w.s.isIdent(j+1, n.BindName) && w.s.kind(j+2) == token.Colon {
			return j + 1
		}
	}
	return -1
}

func (w *walker) forEach(n *ast.ForEachStmt) {
	w.visit(n.Iter)
	w.push()
	i, ok := w.at(n.Pos, "foreach")
	first, second := -1, -1
	if ok && w.s.kind(i+1) == token.LParen {
		var refs []typeRef
		j := i + 2
		first = j
		j++
		if w.s.kind(j) == token.Colon {
			j, _ = w.s.skipType(j+1, &refs)
		}
		if w.s.kind(j) == token.Comma {
			second = j + 1
			if w.s.kind(j+2) == token.Colon {
				w.s.skipType(j+3, &refs)
			}
		}
		w.recordTypes(refs)
	}
	if n.KeyName != "" {
		w.bindLocal(first, n.KeyName, scip.SymbolInformation_Variable)
		w.bindLocal(second, n.ValName, scip.SymbolInformation_Variable)
	} else {
		w.bindLocal(first, n.ValName, scip.SymbolInformation_Variable)
	}
	if n.Body != nil {
		for _, st := range n.Body.Stmts {
			w.visit(st)
		}
	}
	w.pop()
}

func (w *walker) catch(c ast.CatchClause) {
	w.push()
	if c.ErrName != "_" {
		name := -1
		if i, ok := w.at(c.Pos, "catch"); ok && w.s.kind(i+1) == token.LParen {
			name = i + 2
			if w.s.kind(i+3) == token.Colon {
				w.typeAt(i + 4)
			}
		}
		w.bindLocal(name, c.ErrName, scip.SymbolInformation_Variable)
	}
	if c.Body != nil {
		for _, st := range c.Body.Stmts {
			w.visit(st)
		}
	}
	w.pop()
}
