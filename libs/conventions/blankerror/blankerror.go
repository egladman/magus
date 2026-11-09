package blankerror

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"

	"github.com/egladman/magus/libs/conventions/internal/gitdiff"
	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

const message = "this drops an error with a blank assignment, and %s holds more of them than at %s: " +
	"handle it, return it, or say why dropping it is safe in a comment on the same line"

// Options configures the analyzer returned by [New].
type Options struct {
	// Base is the revision a change is measured against, such as origin/main.
	// The comparison point is its merge base with HEAD.
	Base string `json:"base"`

	// Hint is appended to every diagnostic: the repository's own remedy.
	Hint string `json:"hint"`
}

// New returns the analyzer configured by opts. It reports a blank-assigned
// error on a changed line of a touched function, when the function holds more
// of them than at the base. A comment on the assignment's line is its reason.
// New errors on no base, which would leave every function a bystander.
func New(opts Options) (*analysis.Analyzer, error) {
	if opts.Base == "" {
		return nil, errors.New("blankerror: no base revision to measure a change against")
	}
	return newWith(opts, gitdiff.New(opts.Base)), nil
}

func newWith(opts Options, changes changeSource) *analysis.Analyzer {
	return &analysis.Analyzer{
		Name: "blankerror",
		Doc:  "report a blank-assigned error that a change adds to a function it touches",
		Run: func(pass *analysis.Pass) (any, error) {
			return nil, run(pass, opts, changes)
		},
	}
}

type changeSource interface {
	// File returns the changed lines of the working copy and the file's text
	// at the base, nil when the base lacks the file.
	File(path string) (changed []gitdiff.Span, base []byte, err error)

	Rev() string
}

func run(pass *analysis.Pass, opts Options, changes changeSource) error {
	files, err := source.Files(pass)
	if err != nil {
		return err
	}
	for _, f := range files {
		if source.IsTest(pass, f) {
			continue
		}
		name := source.Name(pass, f)
		changed, base, err := changes.File(name)
		if err != nil {
			return fmt.Errorf("blankerror: %w", err)
		}
		if len(changed) == 0 {
			continue
		}
		before, err := baseCounts(base)
		if err != nil {
			return fmt.Errorf("blankerror: base %s: %w", name, err)
		}
		checkFile(pass, opts, f, changed, before, changes.Rev())
	}
	return nil
}

func checkFile(pass *analysis.Pass, opts Options, f *ast.File, changed []gitdiff.Span, before map[string]int, rev string) {
	touched := map[string]bool{}
	sites := map[string][]*ast.AssignStmt{}
	var order []string
	for _, decl := range f.Decls {
		d, ok := decl.(*ast.FuncDecl)
		if !ok || d.Body == nil {
			continue
		}
		key := funcKey(d)
		if _, seen := sites[key]; !seen {
			order = append(order, key)
		}
		start, end := pass.Fset.Position(d.Pos()).Line, pass.Fset.Position(d.End()).Line
		touched[key] = touched[key] || gitdiff.Overlaps(changed, start, end)
		sites[key] = append(sites[key], sitesIn(pass.Fset, f, d)...)
	}
	for _, key := range order {
		if !touched[key] || len(sites[key]) <= before[key] {
			continue
		}
		for _, as := range sites[key] {
			from, to := pass.Fset.Position(as.Pos()).Line, pass.Fset.Position(as.End()).Line
			if gitdiff.Overlaps(changed, from, to) && dropsError(pass, as) {
				pass.Reportf(as.Pos(), "%s", source.Hint(fmt.Sprintf(message, key, rev), opts.Hint))
			}
		}
	}
}

// sitesIn lists the reasonless blank assignments from a call in d. It reads
// syntax only, so the count means the same at the base, where nothing is typed.
func sitesIn(fset *token.FileSet, f *ast.File, d *ast.FuncDecl) []*ast.AssignStmt {
	var sites []*ast.AssignStmt
	ast.Inspect(d.Body, func(n ast.Node) bool {
		if as, ok := n.(*ast.AssignStmt); ok && blankFromCall(as) && !hasReason(fset, f, as) {
			sites = append(sites, as)
		}
		return true
	})
	return sites
}

func isBlank(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "_"
}

func isCall(e ast.Expr) bool {
	_, ok := ast.Unparen(e).(*ast.CallExpr)
	return ok
}

func blankFromCall(as *ast.AssignStmt) bool {
	if as.Tok != token.ASSIGN {
		return false
	}
	hasBlank := false
	for _, l := range as.Lhs {
		hasBlank = hasBlank || isBlank(l)
	}
	if !hasBlank {
		return false
	}
	for _, r := range as.Rhs {
		if isCall(r) {
			return true
		}
	}
	return false
}

func hasReason(fset *token.FileSet, f *ast.File, as *ast.AssignStmt) bool {
	end := fset.Position(as.End()).Line
	for _, group := range f.Comments {
		for _, c := range group.List {
			if fset.Position(c.Pos()).Line == end && strings.TrimSpace(strings.TrimLeft(c.Text, "/*")) != "" {
				return true
			}
		}
	}
	return false
}

func dropsError(pass *analysis.Pass, as *ast.AssignStmt) bool {
	errType := types.Universe.Lookup("error").Type()
	isError := func(t types.Type) bool {
		if b, ok := t.(*types.Basic); ok && b.Kind() == types.UntypedNil {
			return false
		}
		return t != nil && types.AssignableTo(t, errType)
	}
	if len(as.Lhs) == len(as.Rhs) {
		for i, r := range as.Rhs {
			if isBlank(as.Lhs[i]) && isCall(r) && isError(pass.TypesInfo.TypeOf(r)) {
				return true
			}
		}
		return false
	}
	if len(as.Rhs) != 1 {
		return false
	}
	tuple, ok := pass.TypesInfo.TypeOf(as.Rhs[0]).(*types.Tuple)
	if !ok {
		return false
	}
	for i, l := range as.Lhs {
		if i < tuple.Len() && isBlank(l) && isError(tuple.At(i).Type()) {
			return true
		}
	}
	return false
}

// funcKey names a declaration "Recv.Name" or "Name", so a method and a
// function of one name keep separate counts.
func funcKey(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return d.Name.Name
	}
	t := d.Recv.List[0].Type
	for {
		switch x := t.(type) {
		case *ast.StarExpr:
			t = x.X
		case *ast.IndexExpr:
			t = x.X
		case *ast.IndexListExpr:
			t = x.X
		case *ast.Ident:
			return x.Name + "." + d.Name.Name
		default:
			return d.Name.Name
		}
	}
}

// baseCounts counts the reasonless blank assignments per function in the base
// text. A file the base lacks counts nothing.
func baseCounts(base []byte) (map[string]int, error) {
	counts := map[string]int{}
	if base == nil {
		return counts, nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "base.go", base, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	for _, decl := range f.Decls {
		if d, ok := decl.(*ast.FuncDecl); ok && d.Body != nil {
			counts[funcKey(d)] += len(sitesIn(fset, f, d))
		}
	}
	return counts, nil
}
