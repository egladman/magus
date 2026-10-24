package knowledge

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/egladman/magus/types"
)

const (
	figureModule = "magus/figure"
	magusModule  = "magus"

	// attrEyebrow is a figure's eyebrow, the short topic line above its title.
	attrEyebrow = "eyebrow"
	// attrUnresolved names the figure arguments that are not string literals, comma-joined.
	// A figure keyed by its function instead of its id carries "id" here.
	attrUnresolved = "unresolved"
	// attrDuplicate is every other source that builds a figure with the same id.
	attrDuplicate = "duplicate"
)

func figureID(id string) string { return fmt.Sprintf("%s:%s", types.KindFigure, id) }

// figureExtractor reads figure\of calls across the Buzz files of one shard, so a figure id
// built twice is reported on the first node instead of merged away.
type figureExtractor struct {
	scanned map[string]bool
	byID    map[string]int // figure node id -> index in the shard's Nodes
}

// figureNamespaces returns the names prog binds magus/figure and magus under, and false
// when it imports no figure namespace and so builds no figure.
func figureNamespaces(prog *ast.Program) (figure, magus string, ok bool) {
	for _, stmt := range prog.Stmts {
		imp, isImport := stmt.(*ast.ImportStmt)
		if !isImport || imp.Alias == "_" || len(imp.Only) > 0 {
			continue
		}
		name := imp.Alias
		if name == "" {
			name = path.Base(imp.Path)
		}
		switch imp.Path {
		case figureModule:
			figure = name
		case magusModule:
			magus = name
		}
	}
	return figure, magus, figure != ""
}

// stringConsts maps each top-level final whose value folds to a string to that string.
func stringConsts(prog *ast.Program) map[string]string {
	consts := map[string]string{}
	for _, stmt := range prog.Stmts {
		if d, ok := stmt.(*ast.DeclStmt); ok && d.IsConst {
			if v, ok := literalString(d.Value, consts); ok {
				consts[d.Name] = v
			}
		}
	}
	return consts
}

// literalString folds n to the string it always evaluates to: a literal, an interpolation
// with no expression in it, a top-level string constant, or a + of those.
func literalString(n ast.Node, consts map[string]string) (string, bool) {
	switch e := n.(type) {
	case *ast.StringLit:
		return e.Val, true
	case *ast.InterpExpr:
		var b strings.Builder
		for _, p := range e.Parts {
			if p.Expr != nil {
				return "", false
			}
			b.WriteString(p.Lit)
		}
		return b.String(), true
	case *ast.IdentExpr:
		v, ok := consts[e.Name]
		return v, ok
	case *ast.BinaryExpr:
		if e.Op != "+" {
			return "", false
		}
		l, ok := literalString(e.Left, consts)
		if !ok {
			return "", false
		}
		r, ok := literalString(e.Right, consts)
		if !ok {
			return "", false
		}
		return l + r, true
	}
	return "", false
}

// namespaceCall reports the member name when n calls ns\<member>.
func namespaceCall(n ast.Node, ns string) (*ast.CallExpr, string, bool) {
	ce, ok := n.(*ast.CallExpr)
	if !ok {
		return nil, "", false
	}
	m, ok := ce.Callee.(*ast.MemberExpr)
	if !ok || !m.Namespaced {
		return nil, "", false
	}
	obj, ok := m.Object.(*ast.IdentExpr)
	if !ok || obj.Name != ns {
		return nil, "", false
	}
	return ce, m.Name, true
}

// labeledArg returns the argument passed under label, or the first positional argument
// for label "".
func labeledArg(ce *ast.CallExpr, label string) (ast.Node, bool) {
	for i, arg := range ce.Args {
		name := ""
		if i < len(ce.ArgNames) {
			name = ce.ArgNames[i]
		}
		if name == label || (label == "" && i == 0 && name == "") {
			return arg, true
		}
	}
	return nil, false
}

// figureCalls finds the figure\of calls in body and the directories its magus\dir
// calls name, sorted and deduplicated. dirUnresolved reports a magus\dir argument that
// does not fold to a string.
func figureCalls(body ast.Node, figureNS, magusNS string, consts map[string]string) (calls []*ast.CallExpr, dirs []string, dirUnresolved bool) {
	ast.Inspect(body, func(n ast.Node) bool {
		if ce, member, ok := namespaceCall(n, figureNS); ok && member == "of" {
			calls = append(calls, ce)
		}
		if magusNS == "" {
			return true
		}
		if ce, member, ok := namespaceCall(n, magusNS); ok && member == "dir" {
			arg, _ := labeledArg(ce, "")
			if dir, ok := literalString(arg, consts); ok {
				dirs = append(dirs, strings.Trim(path.Clean(dir), "/"))
			} else {
				dirUnresolved = true
			}
		}
		return true
	})
	slices.Sort(dirs)
	return calls, slices.Compact(dirs), dirUnresolved
}

// appendFigures adds a node per figure\of call in d and a depicts edge from it to every
// directory a magus\dir call in d names, and to the project rooted there when one is.
// An argument that does not fold to a string is listed in the node's unresolved attr,
// never dropped: a figure with no literal id is keyed by its function instead.
func (x *figureExtractor) appendFigures(s *Shard, rel string, d *ast.FunDecl, figureNS, magusNS string, consts map[string]string) {
	if d.Body == nil {
		return
	}
	calls, dirs, dirUnresolved := figureCalls(d.Body, figureNS, magusNS, consts)
	for _, ce := range calls {
		node, text := figureNode(rel, d.Name, ce, consts, dirUnresolved)
		if i, seen := x.byID[node.ID]; seen {
			kept := &s.Nodes[i]
			if kept.Attrs == nil {
				kept.Attrs = map[string]string{}
			}
			kept.Attrs[attrDuplicate] = strings.TrimPrefix(fmt.Sprintf("%s,%s", kept.Attrs[attrDuplicate], node.Source), ",")
			continue
		}
		x.byID[node.ID] = len(s.Nodes)
		s.Nodes = append(s.Nodes, node)
		if text != "" {
			if s.Text == nil {
				s.Text = map[string]string{}
			}
			s.Text[node.ID] = text
		}
		s.Edges = append(s.Edges, extractedEdge(fileID(rel), node.ID, types.RelationContains, rel))
		for _, dir := range dirs {
			s.Edges = append(s.Edges, extractedEdge(node.ID, dirID(dir), types.RelationDepicts, rel))
			if x.scanned[path.Join(dir, "magusfile.buzz")] {
				s.Edges = append(s.Edges, extractedEdge(node.ID, projectID(dir), types.RelationDepicts, rel))
			}
		}
	}
}

// figureNode builds the node for one figure\of call and the text the lexical index reads
// for it: the eyebrow and the whole desc, which a node doc would cut.
func figureNode(rel, fn string, ce *ast.CallExpr, consts map[string]string, dirUnresolved bool) (types.KnowledgeNode, string) {
	attrs := map[string]string{}
	var unresolved []string
	field := func(label string) string {
		arg, ok := labeledArg(ce, label)
		if !ok {
			return ""
		}
		v, ok := literalString(arg, consts)
		if !ok {
			unresolved = append(unresolved, cmp.Or(label, "id"))
		}
		return v
	}
	id := field("")
	title := field("title")
	eyebrow := field("eyebrow")
	desc := field("desc")
	if dirUnresolved {
		unresolved = append(unresolved, "dir")
	}

	nodeID := figureID(id)
	if id == "" {
		nodeID = figureID(fmt.Sprintf("%s:%s", rel, fn))
	}
	if eyebrow != "" {
		attrs[attrEyebrow] = eyebrow
	}
	if len(unresolved) > 0 {
		attrs[attrUnresolved] = strings.Join(unresolved, ",")
	}
	node := types.KnowledgeNode{
		ID: nodeID, Kind: types.KindFigure, Label: cmp.Or(title, id), Doc: desc,
		Source: fmt.Sprintf("%s:%s", rel, strconv.Itoa(ce.Line)), Attrs: nilIfEmpty(attrs),
	}
	return node, strings.TrimSpace(fmt.Sprintf("%s\n%s", eyebrow, desc))
}
