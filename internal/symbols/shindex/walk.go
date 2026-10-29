package shindex

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/scip-code/scip/bindings/go/scip"
	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/symbols"
)

// walker records the occurrences of one script. A name resolves to the first of the
// script and the scripts it sources (script.closure) that defines it; a variable a script
// assigns is its own, as a function a script declares is.
type walker struct {
	s       *script
	closure []*script
	doc     symbols.Document
	stack   []syntax.Node
	locals  map[*syntax.FuncDecl]map[string]string
	nlocals int
	infos   map[string]bool
}

func newWalker(s *script) *walker {
	return &walker{s: s, closure: s.closure(), locals: map[*syntax.FuncDecl]map[string]string{}, infos: map[string]bool{}}
}

func (w *walker) document() symbols.Document {
	w.doc = symbols.Document{Path: w.s.unit, Language: language}
	syntax.Walk(w.s.file, w.visit)
	slices.SortStableFunc(w.doc.Occurrences, func(a, b symbols.Occurrence) int {
		if c := slices.Compare(a.Range[:2], b.Range[:2]); c != 0 {
			return c
		}
		return cmp.Compare(a.Symbol, b.Symbol)
	})
	return w.doc
}

func (w *walker) visit(n syntax.Node) bool {
	if n == nil {
		w.stack = w.stack[:len(w.stack)-1]
		return true
	}
	fn := enclosingFunc(w.stack)
	arithmetic := inArithmetic(w.stack)
	w.stack = append(w.stack, n)
	switch n := n.(type) {
	case *syntax.FuncDecl:
		w.function(n)
	case *syntax.CallExpr:
		for _, a := range n.Assigns {
			w.assign(a, fn, "")
		}
		w.command(n)
	case *syntax.DeclClause:
		w.declare(n, fn)
	case *syntax.WordIter:
		w.assign(&syntax.Assign{Name: n.Name}, fn, "")
	case *syntax.ParamExp:
		if n.Param != nil {
			w.use(n.Param, fn)
		}
	case *syntax.Word:
		if lit, ok := soleLit(n); ok && arithmetic {
			w.use(lit, fn)
		}
	}
	return true
}

func (w *walker) function(n *syntax.FuncDecl) {
	if n.Name == nil {
		return
	}
	sym := funcSymbol(w.s.rel, n.Name.Value)
	w.occur(n.Name, sym, scip.SymbolRole_Definition, span(n.Pos(), n.End()))
	w.info(sym, symbols.SymbolInfo{
		DisplayName: n.Name.Value, Kind: int32(scip.SymbolInformation_Function),
		Signature: n.Name.Value + "()", Documentation: docComment(parentStmt(w.stack[:len(w.stack)-1])),
	})
}

func (w *walker) declare(d *syntax.DeclClause, fn *syntax.FuncDecl) {
	local := isLocal(d, fn)
	for _, a := range d.Args {
		switch {
		case a.Name == nil:
		case local:
			w.occur(a.Name, w.local(fn, a.Name.Value), scip.SymbolRole_Definition, nil)
		default:
			w.assign(a, fn, d.Variant.Value)
		}
	}
}

// command records a call of a function, and the script a `source` names.
func (w *walker) command(c *syntax.CallExpr) {
	if len(c.Args) == 0 {
		return
	}
	if src := sourcePath(c); src != nil {
		for _, s := range w.s.sourced {
			if s.word == src {
				w.doc.Occurrences = append(w.doc.Occurrences, symbols.Occurrence{
					Symbol: symbolName(s.target.rel), Range: span(src.Pos(), src.End()), Roles: int32(scip.SymbolRole_Import),
				})
			}
		}
		return
	}
	lit, ok := soleLit(c.Args[0])
	if !ok {
		return
	}
	for _, t := range w.closure {
		if t.funcs[lit.Value] {
			w.occur(lit, funcSymbol(t.rel, lit.Value), 0, nil)
			return
		}
	}
}

// assign records a write to a variable: a function's local, else the script's own
// global, defined at its first assignment in the file.
func (w *walker) assign(a *syntax.Assign, fn *syntax.FuncDecl, variant string) {
	if a.Name == nil {
		return
	}
	name := a.Name.Value
	if sym, ok := w.locals[fn][name]; ok && fn != nil {
		w.occur(a.Name, sym, scip.SymbolRole_WriteAccess, nil)
		return
	}
	def := w.s.vars[name]
	sym := varSymbol(w.s.rel, name)
	if def == nil || def.at != a.Name {
		w.occur(a.Name, sym, scip.SymbolRole_WriteAccess, nil)
		return
	}
	w.occur(a.Name, sym, scip.SymbolRole_Definition, nil)
	kind := scip.SymbolInformation_Variable
	if variant == "readonly" {
		kind = scip.SymbolInformation_Constant
	}
	w.info(sym, symbols.SymbolInfo{
		DisplayName: name, Kind: int32(kind),
		Signature: strings.TrimSpace(strings.Join([]string{variant, name}, " ")), Documentation: docComment(def.stmt),
	})
}

// use records a read of a variable: a function's local, else the global the script or
// one it sources defines. A name none defines comes from the environment and has no
// symbol.
func (w *walker) use(lit *syntax.Lit, fn *syntax.FuncDecl) {
	if sym, ok := w.locals[fn][lit.Value]; ok && fn != nil {
		w.occur(lit, sym, 0, nil)
		return
	}
	for _, t := range w.closure {
		if t.vars[lit.Value] != nil {
			w.occur(lit, varSymbol(t.rel, lit.Value), 0, nil)
			return
		}
	}
}

// local is the document-local symbol of fn's local name, minted at its first declaration.
func (w *walker) local(fn *syntax.FuncDecl, name string) string {
	if w.locals[fn] == nil {
		w.locals[fn] = map[string]string{}
	}
	sym, ok := w.locals[fn][name]
	if !ok {
		sym = "local " + strconv.Itoa(w.nlocals)
		w.nlocals++
		w.locals[fn][name] = sym
	}
	return sym
}

func (w *walker) occur(lit *syntax.Lit, sym string, role scip.SymbolRole, enclosing []int32) {
	w.doc.Occurrences = append(w.doc.Occurrences, symbols.Occurrence{
		Symbol: sym, Range: span(lit.ValuePos, lit.ValueEnd), Roles: int32(role), Enclosing: enclosing,
	})
}

// info records sym's information once, at its first definition in the document.
func (w *walker) info(sym string, si symbols.SymbolInfo) {
	if w.infos[sym] {
		return
	}
	w.infos[sym] = true
	si.Symbol = sym
	w.doc.Symbols = append(w.doc.Symbols, si)
}

// docComment is the comment block on the lines directly above st.
func docComment(st *syntax.Stmt) []string {
	if st == nil {
		return nil
	}
	var lines []string
	want := st.Pos().Line() - 1
	for i := len(st.Comments) - 1; i >= 0; i-- {
		c := st.Comments[i]
		if c.Hash.Line() >= st.Pos().Line() {
			continue
		}
		if c.Hash.Line() != want {
			break
		}
		lines = append(lines, strings.TrimSpace(c.Text))
		want--
	}
	if len(lines) == 0 {
		return nil
	}
	slices.Reverse(lines)
	return []string{strings.Join(lines, "\n")}
}

// inArithmetic reports whether the node about to be pushed on stack sits in an
// arithmetic expression, where a bare word names a variable.
func inArithmetic(stack []syntax.Node) bool {
	for i := len(stack) - 1; i >= 0; i-- {
		switch stack[i].(type) {
		case *syntax.ArithmExp, *syntax.ArithmCmd, *syntax.LetClause:
			return true
		case *syntax.CmdSubst, *syntax.Stmt, *syntax.ParamExp:
			return false
		}
	}
	return false
}

// soleLit is the word's one literal part when it spells a valid name.
func soleLit(w *syntax.Word) (*syntax.Lit, bool) {
	if len(w.Parts) != 1 {
		return nil, false
	}
	lit, ok := w.Parts[0].(*syntax.Lit)
	if !ok || !syntax.ValidName(lit.Value) {
		return nil, false
	}
	return lit, true
}

// span is SCIP's packed range for [pos, end): three elements on one line, four across
// lines. Columns are byte offsets.
func span(pos, end syntax.Pos) []int32 {
	if pos.Line() == end.Line() {
		return []int32{int32(pos.Line() - 1), int32(pos.Col() - 1), int32(end.Col() - 1)}
	}
	return []int32{int32(pos.Line() - 1), int32(pos.Col() - 1), int32(end.Line() - 1), int32(end.Col() - 1)}
}
