package scipbuzz

import (
	"fmt"
	"strings"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/egladman/magus/libs/gopherbuzz/token"
	"github.com/scip-code/scip/bindings/go/scip"
)

// file is one parsed workspace file.
type file struct {
	rel     string // workspace-relative slash path, the namespace of its symbols
	abs     string
	docPath string // project-relative slash path when the file is a document of this index
	src     *source
	toks    *stream
	prog    *ast.Program
	decls   map[string]*decl
	order   []*decl // decls in source order
}

func (f *file) parse(text string) error {
	prog, err := buzz.ParseEmbedded(text)
	if err != nil {
		return err
	}
	src := newSource(text)
	toks, err := fileStream(src)
	if err != nil {
		return err
	}
	f.src, f.toks, f.prog = src, toks, prog
	return nil
}

// decl is a top-level declaration.
type decl struct {
	name, symbol string
	kind         declKind
	exported     bool
	// nameTok is the token index of the declared name in the file stream, and
	// start and end bound the whole declaration for its enclosing range; -1 where
	// unknown.
	nameTok, start, end int
	doc, signature      string
}

// info is the SymbolInformation a declaration contributes.
func (d *decl) info() *scip.SymbolInformation {
	info := &scip.SymbolInformation{Symbol: d.symbol, DisplayName: d.name, Kind: d.kind.scipKind()}
	if d.doc != "" {
		info.Documentation = []string{d.doc}
	}
	if d.signature != "" {
		info.SignatureDocumentation = &scip.Signature{Language: "buzz", Text: d.signature}
	}
	return info
}

// collectDecls records f's top-level declarations and returns a line for each one
// whose name it could not place.
func (f *file) collectDecls() []string {
	var problems []string
	for _, st := range f.prog.Stmts {
		d, ok := f.declOf(st)
		if d == nil {
			continue
		}
		if !ok {
			problems = append(problems, fmt.Sprintf("%d:%d: cannot place the name of %s", ast.NodePos(st).Line, ast.NodePos(st).Col, d.name))
		}
		if _, dup := f.decls[d.name]; dup {
			continue
		}
		d.symbol = declSymbol(f.rel, d.kind, d.name)
		f.decls[d.name] = d
		f.order = append(f.order, d)
	}
	return problems
}

// declOf describes a top-level statement that declares a name, reporting false
// when the name's token could not be found. It returns nil for anything else.
func (f *file) declOf(st ast.Node) (*decl, bool) {
	s := f.toks
	d := &decl{nameTok: -1, start: -1, end: -1}
	idx, found := s.index(ast.NodePos(st))
	switch n := st.(type) {
	case *ast.FunDecl:
		d.name, d.exported, d.doc, d.kind = n.Name, n.IsExported, n.Doc, kindFun
		if n.IsExtern {
			d.kind = kindExtern
		}
		d.signature = funSignature(n)
		if h, ok := s.funHeader(idx, true); found && ok {
			d.nameTok = h.name
			if end, ok := s.bodyEnd(h.body); ok {
				d.end = end
			}
		}
	case *ast.DeclStmt:
		if n.Name == "_" {
			return nil, true
		}
		d.name, d.exported, d.kind = n.Name, n.IsExported, kindVar
		if n.IsConst {
			d.kind = kindFinal
		}
		d.signature = declSignature(n)
		if found {
			d.nameTok, _ = s.declName(idx)
		}
	case *ast.ObjectDecl:
		d.name, d.exported, d.kind = n.Name, n.IsExported, kindObject
		if n.IsProtocol {
			d.kind = kindProtocol
		}
		d.signature = objectSignature(n)
		if found {
			if name, _, brace, ok := s.objectHeader(idx); ok {
				d.nameTok = name
				if end, ok := s.closer(brace); ok {
					d.end = end
				}
			}
		}
	case *ast.EnumDecl:
		d.name, d.exported, d.kind = n.Name, n.IsExported, kindEnum
		d.signature = enumSignature(n)
		if found {
			if name, brace, ok := s.enumHeader(idx); ok {
				d.nameTok = name
				if end, ok := s.closer(brace); ok {
					d.end = end
				}
			}
		}
	default:
		return nil, true
	}
	if d.nameTok < 0 || s.toks[d.nameTok].Val != d.name {
		d.nameTok, d.end = -1, -1
		return d, false
	}
	d.start = idx
	if s.kind(idx-1) == token.Export {
		d.start = idx - 1
	}
	if d.doc == "" {
		d.doc = s.toks[d.start].Doc
		if d.doc == "" {
			d.doc = s.toks[idx].Doc
		}
	}
	if d.kind == kindFinal || d.kind == kindVar {
		d.end = -1
	}
	return d, true
}

// declName returns the index of the name a declaration at i binds, and the type
// names in its annotation. The parser builds a DeclStmt from three shapes:
// `final|var name[: T]`, a keyword-less `name: T` in a for loop or an annotated
// discard, and `export X as Y`, a re-export whose name is Y.
func (s *stream) declName(i int) (int, []typeRef) {
	var refs []typeRef
	switch s.kind(i) {
	case token.Final, token.Var:
		if s.kind(i+1) != token.Ident {
			return -1, nil
		}
		if s.kind(i+2) == token.Colon {
			s.skipType(i+3, &refs)
		}
		return i + 1, refs
	case token.Ident:
		if s.kind(i+1) == token.Colon {
			s.skipType(i+2, &refs)
		}
		return i, refs
	case token.Export:
		depth := 0
		for j := i + 1; j < len(s.toks); j++ {
			switch s.kind(j) {
			case token.LParen, token.LBracket, token.LBrace:
				depth++
			case token.RParen, token.RBracket, token.RBrace:
				depth--
			case token.As:
				if depth == 0 && s.kind(j+1) == token.Ident {
					return j + 1, nil
				}
			case token.Semicolon, token.EOF:
				return -1, nil
			}
		}
	}
	return -1, nil
}

// objectHeader reads `object<P, Q> Name::<T> {` or `protocol Name {` at i and
// returns the name's index, the conformance list's indexes and the opening brace.
func (s *stream) objectHeader(i int) (name int, conforms []int, brace int, ok bool) {
	j := i + 1
	if s.kind(i) == token.Object && s.kind(j) == token.Lt {
		j++
		for s.kind(j) == token.Ident {
			conforms = append(conforms, j)
			j++
			if s.kind(j) != token.Comma {
				break
			}
			j++
		}
		if s.kind(j) != token.Gt {
			return -1, nil, -1, false
		}
		j++
	}
	if s.kind(j) != token.Ident {
		return -1, nil, -1, false
	}
	name = j
	j++
	if s.kind(j) == token.Colon && s.kind(j+1) == token.Colon && s.kind(j+2) == token.Lt {
		if j, ok = s.skipAngles(j + 2); !ok {
			return -1, nil, -1, false
		}
	}
	if s.kind(j) != token.LBrace {
		return -1, nil, -1, false
	}
	return name, conforms, j, true
}

// enumHeader reads `enum<T> Name(T) {` at i and returns the name's index and the
// opening brace.
func (s *stream) enumHeader(i int) (name, brace int, ok bool) {
	j := i + 1
	var ignored []typeRef
	if s.kind(j) == token.Lt {
		if j, ok = s.skipType(j+1, &ignored); !ok || s.kind(j) != token.Gt {
			return -1, -1, false
		}
		j++
	}
	if s.kind(j) != token.Ident {
		return -1, -1, false
	}
	name = j
	j++
	if s.kind(j) == token.LParen {
		if j, ok = s.skipBalanced(j); !ok {
			return -1, -1, false
		}
	}
	if s.kind(j) != token.LBrace {
		return -1, -1, false
	}
	return name, j, true
}

// fieldTypes returns the type names in the field annotations of the object body
// opening at brace. ObjField keeps no position, so a field is found the way the
// parser meets one: an identifier at the body's own depth, starting a member, and
// followed by a colon. Method parameters and bodies sit deeper, and a method name
// follows `fun`, so neither matches.
func (s *stream) fieldTypes(brace int) []typeRef {
	var refs []typeRef
	end, ok := s.closer(brace)
	if !ok {
		return nil
	}
	depth := 0
	for j := brace + 1; j < end; j++ {
		switch s.kind(j) {
		case token.LParen, token.LBracket, token.LBrace:
			depth++
			continue
		case token.RParen, token.RBracket, token.RBrace:
			depth--
			continue
		}
		if depth != 0 || s.kind(j) != token.Ident || s.kind(j+1) != token.Colon {
			continue
		}
		prev := j - 1
		if s.isIdent(prev, "static") {
			prev--
		}
		// RBrace is the end of a method body: the parser needs no separator after one.
		switch s.kind(prev) {
		case token.LBrace, token.RBrace, token.Comma, token.Semicolon:
			s.skipType(j+2, &refs)
		}
	}
	return refs
}

func funSignature(n *ast.FunDecl) string {
	var b strings.Builder
	if n.IsExtern {
		b.WriteString("extern ")
	}
	b.WriteString("fun ")
	b.WriteString(n.Name)
	if len(n.TypeParams) > 0 {
		b.WriteString("::<" + strings.Join(n.TypeParams, ", ") + ">")
	}
	b.WriteString("(")
	for i, p := range n.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p)
		if i < len(n.ParamAnnots) && n.ParamAnnots[i] != "" {
			b.WriteString(": " + n.ParamAnnots[i])
		}
	}
	b.WriteString(")")
	if n.RetAnnot != "" {
		b.WriteString(" > " + n.RetAnnot)
	}
	if n.YieldAnnot != "" {
		b.WriteString(" *> " + n.YieldAnnot)
	}
	if n.ErrAnnot != "" {
		b.WriteString(" !> " + n.ErrAnnot)
	}
	return b.String()
}

func declSignature(n *ast.DeclStmt) string {
	kw := "var"
	if n.IsConst {
		kw = "final"
	}
	if n.TypeAnnot == "" {
		return kw + " " + n.Name
	}
	return kw + " " + n.Name + ": " + n.TypeAnnot
}

func objectSignature(n *ast.ObjectDecl) string {
	if n.IsProtocol {
		return "protocol " + n.Name
	}
	if len(n.Conforms) > 0 {
		return "object<" + strings.Join(n.Conforms, ", ") + "> " + n.Name
	}
	return "object " + n.Name
}

func enumSignature(n *ast.EnumDecl) string {
	if n.Backing != "" {
		return "enum<" + n.Backing + "> " + n.Name
	}
	return "enum " + n.Name
}
