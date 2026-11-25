package thin

import (
	"bytes"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"slices"
	"strconv"

	"golang.org/x/tools/go/analysis"
)

// inline returns the edits that replace one site with the helper's body. It
// returns nil when no edit is known to keep the program's meaning. That is a
// site in a go or defer statement, a variadic call, or an argument with effects
// the body would drop, repeat or reorder. It is also a name the site resolves
// differently, or an import the site's file cannot take under the body's name.
// Every edit lands in the site's own file, as golangci-lint applies a fix to
// the file of its diagnostic.
func inline(pass *analysis.Pass, h *Helper, s site) []analysis.TextEdit {
	if h.Guarded {
		return nil
	}
	ed := &editor{pass: pass, h: h, site: s}
	var text string
	var ok bool
	if s.Call == nil {
		text, ok = ed.funcLit()
	} else {
		text, ok = ed.call()
	}
	if !ok {
		return nil
	}
	at := ast.Node(s.Ident)
	if s.Call != nil {
		at = s.Call
	}
	edits := []analysis.TextEdit{{Pos: at.Pos(), End: at.End(), NewText: []byte(text)}}
	return append(edits, ed.imports...)
}

// remove returns the edits that remove a helper nothing references, with the
// imports only it used.
func remove(pass *analysis.Pass, h *Helper) []analysis.TextEdit {
	src, err := pass.ReadFile(pass.Fset.File(h.File.FileStart).Name())
	if err != nil {
		return nil
	}
	tf := pass.Fset.File(h.File.FileStart)
	start := h.Decl.Pos()
	if h.Decl.Doc != nil {
		start = h.Decl.Doc.Pos()
	}
	from, to := wholeLines(src, tf.Offset(start), tf.Offset(h.Decl.End()))
	edits := []analysis.TextEdit{{Pos: tf.Pos(from), End: tf.Pos(to)}}
	for _, spec := range orphanedImports(pass, h) {
		a, b := wholeLines(src, tf.Offset(spec.Pos()), tf.Offset(spec.End()))
		edits = append(edits, analysis.TextEdit{Pos: tf.Pos(a), End: tf.Pos(b)})
	}
	return edits
}

// wholeLines widens [from, to) to the lines holding it, plus one blank line
// after them, so a removed declaration leaves no gap of its own.
func wholeLines(src []byte, from, to int) (int, int) {
	for from > 0 && src[from-1] != '\n' {
		from--
	}
	for to < len(src) && src[to] != '\n' {
		to++
	}
	if to < len(src) {
		to++
	}
	if to < len(src) && src[to] == '\n' {
		to++
	}
	return from, to
}

func orphanedImports(pass *analysis.Pass, h *Helper) []*ast.ImportSpec {
	inside := map[*types.PkgName]bool{}
	outside := map[*types.PkgName]bool{}
	ast.Inspect(h.File, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		pn, ok := pass.TypesInfo.Uses[id].(*types.PkgName)
		if !ok {
			return true
		}
		if id.Pos() >= h.Decl.Pos() && id.End() <= h.Decl.End() {
			inside[pn] = true
		} else {
			outside[pn] = true
		}
		return true
	})
	var out []*ast.ImportSpec
	for _, spec := range h.File.Imports {
		pn := importName(pass, spec)
		if pn != nil && inside[pn] && !outside[pn] {
			out = append(out, spec)
		}
	}
	return out
}

func importName(pass *analysis.Pass, spec *ast.ImportSpec) *types.PkgName {
	if spec.Name != nil {
		pn, _ := pass.TypesInfo.Defs[spec.Name].(*types.PkgName)
		return pn
	}
	pn, _ := pass.TypesInfo.Implicits[spec].(*types.PkgName)
	return pn
}

type editor struct {
	pass    *analysis.Pass
	h       *Helper
	site    site
	imports []analysis.TextEdit
	added   map[string]bool
}

func (ed *editor) source(n ast.Node) ([]byte, bool) {
	tf := ed.pass.Fset.File(n.Pos())
	src, err := ed.pass.ReadFile(tf.Name())
	if err != nil {
		return nil, false
	}
	return src[tf.Offset(n.Pos()):tf.Offset(n.End())], true
}

// funcLit writes the helper as a function literal, for a site that passes it
// as a value.
func (ed *editor) funcLit() (string, bool) {
	sig, ok := ed.source(ed.h.Decl.Type.Params)
	if !ok {
		return "", false
	}
	if !ed.resolvable(ed.h.Decl.Type, nil) || !ed.resolvable(ed.h.Decl.Body, nil) {
		return "", false
	}
	tail, ok := ed.source(&span{ed.h.Decl.Type.Params.End(), ed.h.Decl.Body.End()})
	if !ok {
		return "", false
	}
	return "func" + string(sig) + string(tail), true
}

// call writes the helper's body with each parameter replaced by its argument.
func (ed *editor) call() (string, bool) {
	call := ed.site.Call
	sig := ed.h.Func.Signature()
	if call.Ellipsis.IsValid() || sig.Variadic() || len(call.Args) != sig.Params().Len() {
		return "", false
	}
	if _, ok := ed.site.Parent.(*ast.GoStmt); ok {
		return "", false
	}
	if _, ok := ed.site.Parent.(*ast.DeferStmt); ok {
		return "", false
	}
	params := map[*types.Var]int{}
	for i := range sig.Params().Len() {
		params[sig.Params().At(i)] = i
	}
	uses := make([]int, len(call.Args))
	type repl struct {
		from, to token.Pos
		text     string
	}
	var repls []repl
	ok := true
	inspect(ed.h.Expr, func(id *ast.Ident, parent ast.Node) {
		v, isVar := ed.pass.TypesInfo.Uses[id].(*types.Var)
		i, isParam := params[v]
		if !isVar || !isParam {
			return
		}
		uses[i]++
		text, fits := ed.argument(call.Args[i], v.Type(), ed.paramType(i), needsParens(parent, id))
		if !fits {
			ok = false
			return
		}
		repls = append(repls, repl{id.Pos(), id.End(), text})
	})
	if !ok || !ed.argumentsSafe(call.Args, uses) || !ed.resolvable(ed.h.Expr, params) {
		return "", false
	}
	body, found := ed.source(ed.h.Expr)
	if !found {
		return "", false
	}
	base := ed.h.Expr.Pos()
	slices.SortFunc(repls, func(a, b repl) int { return int(a.from - b.from) })
	var out bytes.Buffer
	last := 0
	for _, r := range repls {
		out.Write(body[last : r.from-base])
		out.WriteString(r.text)
		last = int(r.to - base)
	}
	out.Write(body[last:])
	text, wrapped, fits := ed.result(out.String())
	if !fits {
		return "", false
	}
	if !wrapped && !primary(ed.h.Expr) && needsParens(ed.site.Parent, call) {
		text = "(" + text + ")"
	}
	return text, true
}

func (ed *editor) paramType(i int) ast.Expr {
	n := 0
	for _, field := range ed.h.Decl.Type.Params.List {
		count := max(len(field.Names), 1)
		if i < n+count {
			return field.Type
		}
		n += count
	}
	return nil
}

// argument is the text a parameter use becomes: the argument, converted when
// its type is not the parameter's, so the body still sees the type it was
// written against.
func (ed *editor) argument(arg ast.Expr, want types.Type, typeExpr ast.Expr, tight bool) (string, bool) {
	src, ok := ed.source(arg)
	if !ok {
		return "", false
	}
	text := string(src)
	got := ed.natural(arg)
	if got == nil {
		return "", false
	}
	if converted(got, want) {
		if typeExpr == nil || !ed.resolvable(typeExpr, nil) {
			return "", false
		}
		typeSrc, ok := ed.source(typeExpr)
		if !ok {
			return "", false
		}
		return conversion(typeExpr, typeSrc, text), true
	}
	if tight && !primary(arg) {
		text = "(" + text + ")"
	}
	return text, true
}

// natural is the type e has standing alone, as in `x := e`. go/types records
// an untyped constant at the type its context converted it to, so a constant
// that is not a typed constant's name takes its kind's default type here.
func (ed *editor) natural(e ast.Expr) types.Type {
	tv, ok := ed.pass.TypesInfo.Types[e]
	if !ok || tv.Type == nil {
		return nil
	}
	if tv.IsNil() {
		return types.Typ[types.UntypedNil]
	}
	if tv.Value == nil || typedConst(ed.pass, e) {
		return tv.Type
	}
	switch tv.Value.Kind() {
	case constant.Bool:
		return types.Typ[types.Bool]
	case constant.String:
		return types.Typ[types.String]
	case constant.Float:
		return types.Typ[types.Float64]
	case constant.Complex:
		return types.Typ[types.Complex128]
	}
	if lit, ok := ast.Unparen(e).(*ast.BasicLit); ok && lit.Kind == token.CHAR {
		return types.Typ[types.Int32]
	}
	return types.Typ[types.Int]
}

func typedConst(pass *analysis.Pass, e ast.Expr) bool {
	var id *ast.Ident
	switch e := ast.Unparen(e).(type) {
	case *ast.Ident:
		id = e
	case *ast.SelectorExpr:
		id = e.Sel
	default:
		return false
	}
	c, ok := pass.TypesInfo.Uses[id].(*types.Const)
	if !ok {
		return false
	}
	b, untyped := c.Type().(*types.Basic)
	return !untyped || b.Info()&types.IsUntyped == 0
}

// converted reports whether a value of type got must be converted to stand
// where want was declared: unless the two are identical, got is untyped nil,
// or want is an interface got fills.
func converted(got, want types.Type) bool {
	if types.Identical(got, want) {
		return false
	}
	if b, ok := got.(*types.Basic); ok && b.Kind() == types.UntypedNil {
		return false
	}
	return !types.IsInterface(want)
}

// result converts the inlined body to the declared result type when the
// body's own type differs, so a site's `x := f()` keeps x's type.
func (ed *editor) result(text string) (string, bool, bool) {
	if !ed.h.Returns {
		return text, false, true
	}
	results := ed.h.Func.Signature().Results()
	natural := ed.natural(ed.h.Expr)
	if natural == nil {
		return "", false, false
	}
	if results.Len() != 1 {
		got, ok := natural.(*types.Tuple)
		if !ok || got.Len() != results.Len() {
			return "", false, false
		}
		for i := range got.Len() {
			if !types.Identical(got.At(i).Type(), results.At(i).Type()) {
				return "", false, false
			}
		}
		return text, false, true
	}
	want := results.At(0).Type()
	if b, ok := natural.(*types.Basic); types.Identical(natural, want) || (ok && b.Kind() == types.UntypedNil) {
		return text, false, true
	}
	if types.IsInterface(want) && ed.dynamicOnly() {
		return text, false, true
	}
	typeExpr := ed.h.Decl.Type.Results.List[0].Type
	if !ed.resolvable(typeExpr, nil) {
		return "", false, false
	}
	typeSrc, ok := ed.source(typeExpr)
	if !ok {
		return "", false, false
	}
	return conversion(typeExpr, typeSrc, text), true, true
}

// conversion writes text converted to the type typeExpr spells, wrapping a
// type that is not a name, such as *T, so it parses as a conversion.
func conversion(typeExpr ast.Expr, typeSrc []byte, text string) string {
	switch typeExpr.(type) {
	case *ast.Ident, *ast.SelectorExpr:
		return string(typeSrc) + "(" + text + ")"
	}
	return "(" + string(typeSrc) + ")(" + text + ")"
}

// dynamicOnly reports whether the site hands its value straight to a typed
// destination, where an interface result and the concrete value it held
// behave alike. A definition such as `err := f()` takes the static type, so
// it does not qualify, and neither does a selector or a generic call.
func (ed *editor) dynamicOnly() bool {
	switch p := ed.site.Parent.(type) {
	case *ast.ReturnStmt, *ast.KeyValueExpr, *ast.CompositeLit, *ast.SendStmt:
		return true
	case *ast.AssignStmt:
		return p.Tok == token.ASSIGN
	case *ast.CallExpr:
		if ast.Unparen(p.Fun) == ast.Expr(ed.site.Call) {
			return false
		}
		fun := ast.Unparen(p.Fun)
		if sel, ok := fun.(*ast.SelectorExpr); ok {
			fun = sel.Sel
		}
		if id, ok := fun.(*ast.Ident); ok {
			if _, generic := ed.pass.TypesInfo.Instances[id]; generic {
				return false
			}
		}
		_, ok := ed.pass.TypesInfo.TypeOf(p.Fun).(*types.Signature)
		return ok
	}
	return false
}

// argumentsSafe reports whether evaluating the body in place of the call
// evaluates each argument's effects exactly as the call did. An argument
// that is a name, a literal or a field read has none, so it may be dropped,
// repeated or moved. One argument with effects is safe when its parameter is
// read once and the body makes no other call it could be reordered against.
func (ed *editor) argumentsSafe(args []ast.Expr, uses []int) bool {
	effectful := 0
	for i, a := range args {
		if pure(a) {
			continue
		}
		if uses[i] != 1 {
			return false
		}
		effectful++
	}
	if effectful == 0 {
		return true
	}
	if effectful > 1 {
		return false
	}
	calls := 0
	ast.Inspect(ed.h.Expr, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && !ed.isConversion(c) {
			calls++
		}
		return true
	})
	outer := 0
	if c, ok := ed.h.Expr.(*ast.CallExpr); ok && !ed.isConversion(c) {
		outer = 1
	}
	return calls == outer
}

func (ed *editor) isConversion(c *ast.CallExpr) bool {
	tv, ok := ed.pass.TypesInfo.Types[c.Fun]
	return ok && tv.IsType()
}

func pure(e ast.Expr) bool {
	switch e := ast.Unparen(e).(type) {
	case *ast.Ident, *ast.BasicLit:
		return true
	case *ast.SelectorExpr:
		return pure(e.X)
	case *ast.UnaryExpr:
		return (e.Op == token.AND || e.Op == token.SUB) && pure(e.X)
	}
	return false
}

func primary(e ast.Expr) bool {
	switch e.(type) {
	case *ast.Ident, *ast.BasicLit, *ast.SelectorExpr, *ast.CallExpr, *ast.IndexExpr,
		*ast.IndexListExpr, *ast.CompositeLit, *ast.ParenExpr, *ast.SliceExpr, *ast.TypeAssertExpr:
		return true
	}
	return false
}

// needsParens reports whether an expression that is not primary must be
// parenthesized to stand as child of parent.
func needsParens(parent, child ast.Node) bool {
	switch p := parent.(type) {
	case *ast.SelectorExpr, *ast.TypeAssertExpr, *ast.StarExpr, *ast.UnaryExpr, *ast.BinaryExpr:
		return true
	case *ast.IndexExpr:
		return p.X == child
	case *ast.IndexListExpr:
		return p.X == child
	case *ast.SliceExpr:
		return p.X == child
	case *ast.CallExpr:
		return ast.Unparen(p.Fun) == child
	}
	return false
}

// resolvable reports whether every name n reads, other than the parameters
// it substitutes, means the same thing at the site, adding an import the
// site's file lacks.
func (ed *editor) resolvable(n ast.Node, params map[*types.Var]int) bool {
	ok := true
	inspect(n, func(id *ast.Ident, _ ast.Node) {
		if !ok {
			return
		}
		obj := ed.pass.TypesInfo.Uses[id]
		if obj == nil {
			if _, def := ed.pass.TypesInfo.Defs[id]; def {
				return
			}
			ok = false
			return
		}
		if v, isVar := obj.(*types.Var); isVar {
			if _, isParam := params[v]; isParam || v.IsField() || inside(v.Pos(), ed.h.Decl) {
				return
			}
		}
		if pn, isPkg := obj.(*types.PkgName); isPkg {
			ok = ed.importAt(id.Name, pn.Imported())
			return
		}
		scope := ed.pass.Pkg.Scope().Innermost(ed.site.Ident.Pos())
		if scope == nil {
			ok = false
			return
		}
		if _, found := scope.LookupParent(id.Name, ed.site.Ident.Pos()); found != obj {
			ok = false
		}
	})
	return ok
}

func inside(pos token.Pos, n ast.Node) bool { return pos >= n.Pos() && pos < n.End() }

// importAt makes sure the site's file reads name as pkg, adding the import
// when the file has none for pkg and name is free there.
func (ed *editor) importAt(name string, pkg *types.Package) bool {
	f := ed.site.File
	for _, spec := range f.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != pkg.Path() {
			continue
		}
		pn := importName(ed.pass, spec)
		return pn != nil && pn.Name() == name
	}
	if ed.added[pkg.Path()] {
		return true
	}
	scope := ed.pass.Pkg.Scope().Innermost(ed.site.Ident.Pos())
	if scope == nil {
		return false
	}
	if _, found := scope.LookupParent(name, ed.site.Ident.Pos()); found != nil {
		return false
	}
	for _, spec := range f.Imports {
		if pn := importName(ed.pass, spec); pn != nil && pn.Name() == name {
			return false
		}
	}
	spec := strconv.Quote(pkg.Path())
	if name != pkg.Name() {
		spec = name + " " + spec
	}
	ed.imports = append(ed.imports, importEdit(f, spec))
	if ed.added == nil {
		ed.added = map[string]bool{}
	}
	ed.added[pkg.Path()] = true
	return true
}

func importEdit(f *ast.File, spec string) analysis.TextEdit {
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.IMPORT {
			continue
		}
		if gd.Lparen.IsValid() {
			return analysis.TextEdit{Pos: gd.Lparen + 1, End: gd.Lparen + 1, NewText: []byte("\n\t" + spec)}
		}
		return analysis.TextEdit{Pos: gd.Pos(), End: gd.Pos(), NewText: []byte("import " + spec + "\n")}
	}
	return analysis.TextEdit{Pos: f.Name.End(), End: f.Name.End(), NewText: []byte("\n\nimport " + spec)}
}

// inspect calls fn on every identifier n reads as a name, with the node
// holding it, skipping a selector's field or method.
func inspect(n ast.Node, fn func(id *ast.Ident, parent ast.Node)) {
	var stack []ast.Node
	ast.Inspect(n, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if id, ok := n.(*ast.Ident); ok {
			var up ast.Node
			for i := len(stack) - 1; i >= 0; i-- {
				if _, paren := stack[i].(*ast.ParenExpr); !paren {
					up = stack[i]
					break
				}
			}
			if sel, ok := up.(*ast.SelectorExpr); !ok || sel.Sel != id {
				fn(id, up)
			}
		}
		stack = append(stack, n)
		return true
	})
}

// span is a node covering [from, to), for reading source between two nodes.
type span struct{ from, to token.Pos }

func (s *span) Pos() token.Pos { return s.from }
func (s *span) End() token.Pos { return s.to }
