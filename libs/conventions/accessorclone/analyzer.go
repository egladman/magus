// Package accessorclone reports an exported accessor that returns its own
// slice or map. A caller that sorts or appends to the result then edits the
// owner's state, so the accessor returns slices.Clone, maps.Clone or an
// iterator.
//
// Most accessors in a tree predate the rule, so a baseline file lists today's
// violations, one JSON object per line, and a violation outside it fails at
// once. A line leaves the baseline when its accessor is fixed, and the analyzer
// reports the line stale until it does. A //nolint naming the linter is itself
// reported.
package accessorclone

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

const name = "accessorclone"

const message = "%s returns its own %s %s: return slices.Clone or maps.Clone of it, or an iterator, " +
	"so a caller never edits the owner's state"

const staleMessage = "the baseline lists %s, which no longer returns its own slice or map: " +
	"delete that line from the baseline so it only shrinks"

const nolintMessage = "a //nolint cannot exempt " + name + ": clone what the accessor returns"

// Options configures the analyzer returned by [New].
type Options struct {
	// Module is the module whose files the baseline paths are relative to. A
	// plugin run fills it from the linted tree's go.mod.
	Module string `json:"module"`

	// Baseline is a JSON Lines file of today's violations, relative to the root
	// of Module, or absolute. Each line is {"path": ..., "symbol": ...}.
	Baseline string `json:"baseline"`

	// Hint is appended to every diagnostic: the repository's own remedy.
	Hint string `json:"hint"`
}

// Entry is one accessor the baseline excuses: the file inside the module and
// the function as "Recv.Name" or "Name".
type Entry struct {
	Path   string `json:"path"`
	Symbol string `json:"symbol"`
}

// New returns the analyzer configured by opts. It reports an exported function
// or method of an exported type that returns a slice or map held by its
// receiver or a package-level variable, whole or resliced, unless the baseline
// lists it. It also reports a baseline line whose accessor is fixed or gone.
//
// New errors on no baseline, an unreadable or malformed one, a line naming a
// file that does not exist, or a line listed twice.
func New(opts Options) (*analysis.Analyzer, error) {
	if opts.Baseline == "" {
		return nil, errors.New(name + ": no baseline file: a rule with no baseline would charge every existing accessor")
	}
	entries, err := load(opts)
	if err != nil {
		return nil, err
	}
	return newWith(opts, entries), nil
}

func newWith(opts Options, baseline []Entry) *analysis.Analyzer {
	excused := map[Entry]bool{}
	for _, e := range baseline {
		excused[e] = true
	}
	return &analysis.Analyzer{
		Name: name,
		Doc:  "report an exported accessor that returns its own slice or map instead of a copy",
		Run: func(pass *analysis.Pass) (any, error) {
			return nil, run(pass, opts, excused)
		},
	}
}

func load(opts Options) ([]Entry, error) {
	path := opts.Baseline
	root := ""
	if opts.Module != "" {
		var err error
		if root, err = source.Root(name, opts.Module); err != nil {
			return nil, err
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, filepath.FromSlash(path))
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: baseline: %w", name, err)
	}
	var entries []Entry
	seen := map[Entry]bool{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e Entry
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&e); err != nil {
			return nil, fmt.Errorf("%s: baseline %s:%d: %w", name, opts.Baseline, n, err)
		}
		if e.Path == "" || e.Symbol == "" {
			return nil, fmt.Errorf("%s: baseline %s:%d: a line names a path and a symbol", name, opts.Baseline, n)
		}
		if seen[e] {
			return nil, fmt.Errorf("%s: baseline %s:%d lists %s %s twice", name, opts.Baseline, n, e.Path, e.Symbol)
		}
		seen[e] = true
		if root != "" {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(e.Path))); err != nil {
				return nil, fmt.Errorf("%s: baseline %s:%d names %s, which is not a file under %s: it moved, delete or fix the line",
					name, opts.Baseline, n, e.Path, root)
			}
		}
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: baseline: %w", name, err)
	}
	return entries, nil
}

func run(pass *analysis.Pass, opts Options, excused map[Entry]bool) error {
	files, err := source.Files(pass)
	if err != nil {
		return err
	}
	for _, f := range files {
		if source.IsTest(pass, f) || ast.IsGenerated(f) {
			continue
		}
		rel, ok := source.Rel(pass, opts.Module, f)
		if !ok {
			continue
		}
		reportNolint(pass, opts, f)
		leaks := leaksIn(pass, f)
		for _, l := range leaks {
			if !excused[Entry{Path: rel, Symbol: l.symbol}] {
				pass.Reportf(l.pos, "%s", source.Hint(fmt.Sprintf(message, l.symbol, l.kind, l.expr), opts.Hint))
			}
		}
		for e := range excused {
			if e.Path == rel && !hasSymbol(leaks, e.Symbol) {
				pass.Reportf(f.Name.Pos(), "%s", source.Hint(fmt.Sprintf(staleMessage, e.Symbol), opts.Hint))
			}
		}
	}
	return nil
}

func hasSymbol(leaks []leak, symbol string) bool {
	for _, l := range leaks {
		if l.symbol == symbol {
			return true
		}
	}
	return false
}

// reportNolint reports at the package clause, which a directive on a
// declaration does not cover.
func reportNolint(pass *analysis.Pass, opts Options, f *ast.File) {
	for _, group := range f.Comments {
		for _, c := range group.List {
			text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
			rest, ok := strings.CutPrefix(text, "nolint:")
			if !ok {
				continue
			}
			list, _, _ := strings.Cut(rest, " ")
			for _, n := range strings.Split(list, ",") {
				if n == name {
					pass.Reportf(f.Name.Pos(), "%s", source.Hint(nolintMessage, opts.Hint))
					return
				}
			}
		}
	}
}

type leak struct {
	symbol string
	kind   string
	expr   string
	pos    token.Pos
}

// leaksIn lists, once per function, the first return of its own slice or map.
func leaksIn(pass *analysis.Pass, f *ast.File) []leak {
	var leaks []leak
	for _, decl := range f.Decls {
		d, ok := decl.(*ast.FuncDecl)
		if !ok || d.Body == nil || !d.Name.IsExported() {
			continue
		}
		var recv *types.Var
		if d.Recv != nil && len(d.Recv.List) > 0 {
			if !exportedRecv(d.Recv.List[0].Type) {
				continue
			}
			if names := d.Recv.List[0].Names; len(names) > 0 {
				recv, _ = pass.TypesInfo.Defs[names[0]].(*types.Var)
			}
		}
		if l, found := firstLeak(pass, d, recv); found {
			leaks = append(leaks, l)
		}
	}
	return leaks
}

func firstLeak(pass *analysis.Pass, d *ast.FuncDecl, recv *types.Var) (leak, bool) {
	var out leak
	found := false
	ast.Inspect(d.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		if _, lit := n.(*ast.FuncLit); lit {
			return false
		}
		ret, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		for _, r := range ret.Results {
			if kind, expr, ok := ownState(pass, r, recv); ok {
				out = leak{symbol: funcKey(d), kind: kind, expr: expr, pos: r.Pos()}
				found = true
				return false
			}
		}
		return true
	})
	return out, found
}

// ownState reports whether e is a slice or map reached from the receiver or a
// package-level variable by field selection, whole or resliced.
func ownState(pass *analysis.Pass, e ast.Expr, recv *types.Var) (kind, text string, ok bool) {
	e = ast.Unparen(e)
	if s, isSlice := e.(*ast.SliceExpr); isSlice {
		e = ast.Unparen(s.X)
	}
	t := pass.TypesInfo.TypeOf(e)
	if t == nil {
		return "", "", false
	}
	switch t.Underlying().(type) {
	case *types.Slice:
		kind = "slice"
	case *types.Map:
		kind = "map"
	default:
		return "", "", false
	}
	root := e
	for {
		sel, isSel := root.(*ast.SelectorExpr)
		if !isSel {
			break
		}
		root = ast.Unparen(sel.X)
	}
	id, isIdent := root.(*ast.Ident)
	if !isIdent {
		return "", "", false
	}
	v, isVar := pass.TypesInfo.Uses[id].(*types.Var)
	if !isVar || v.IsField() {
		return "", "", false
	}
	if v != recv && v.Parent() != pass.Pkg.Scope() {
		return "", "", false
	}
	return kind, types.ExprString(e), true
}

// exportedRecv keeps an accessor on an unexported type out of the API this
// rule guards.
func exportedRecv(t ast.Expr) bool {
	for {
		switch x := t.(type) {
		case *ast.StarExpr:
			t = x.X
		case *ast.ParenExpr:
			t = x.X
		case *ast.IndexExpr:
			t = x.X
		case *ast.IndexListExpr:
			t = x.X
		case *ast.Ident:
			return x.IsExported()
		default:
			return false
		}
	}
}

// funcKey names a declaration "Recv.Name" or "Name".
func funcKey(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return d.Name.Name
	}
	t := d.Recv.List[0].Type
	for {
		switch x := t.(type) {
		case *ast.StarExpr:
			t = x.X
		case *ast.ParenExpr:
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
