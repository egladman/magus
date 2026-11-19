// Package accessorclone reports an exported accessor that returns its own
// slice or map. A caller that sorts or appends to the result then edits the
// owner's state, so the accessor returns slices.Clone, maps.Clone or an
// iterator.
//
// Most accessors in a tree predate the rule, so the repository's policy file
// lists today's violations under its accessorclone key, and a violation outside
// it fails at once. A baseline entry leaves when its accessor is fixed, and the
// analyzer reports the entry stale until it does. An exit is an accessor a
// person excused for good, with a reason. A //nolint naming the linter is
// itself reported.
package accessorclone

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
	"gopkg.in/yaml.v3"
)

const name = "accessorclone"

const message = "%s returns its own %s %s: return slices.Clone or maps.Clone of it, or an iterator, " +
	"so a caller never edits the owner's state"

const staleMessage = "%s lists %s, which no longer returns its own slice or map: " +
	"delete it from %s.%s in %s so the list only shrinks"

const nolintMessage = "a //nolint cannot exempt " + name + ": clone what the accessor returns"

// Options configures the analyzer returned by [New].
type Options struct {
	// Module is the module whose files the policy paths are relative to. A
	// plugin run fills it from the linted tree's go.mod.
	Module string `json:"module"`

	// Policy is the repository's analyzer policy file, relative to the root of
	// Module, or absolute. New reads its accessorclone key:
	//
	//	accessorclone:
	//	  settings:
	//	    hint: <appended to every diagnostic>
	//	  baseline:
	//	    <file>: [<Recv.Name or Name>, ...]
	//	  exits:
	//	    - {path: <file>, symbol: <Recv.Name or Name>, reason: <why>}
	Policy string `json:"policy"`
}

// Entry is one accessor the policy excuses: the file inside the module and
// the function as "Recv.Name" or "Name".
type Entry struct {
	Path   string
	Symbol string
}

type exit struct {
	Path   string `yaml:"path"`
	Symbol string `yaml:"symbol"`
	Reason string `yaml:"reason"`
}

type policy struct {
	Settings struct {
		Hint string `yaml:"hint"`
	} `yaml:"settings"`
	Baseline map[string][]string `yaml:"baseline"`
	Exits    []exit              `yaml:"exits"`
}

// loaded is the policy as run reads it: each excused accessor maps to the
// list that excuses it, baseline or exits.
type loaded struct {
	module  string
	policy  string
	hint    string
	excused map[Entry]string
}

// New returns the analyzer configured by opts. It reports an exported function
// or method of an exported type that returns a slice or map held by its
// receiver or a package-level variable, whole or resliced, unless the policy
// excuses it. It also reports an entry whose accessor is fixed or gone.
//
// New errors on no policy file, an unreadable or malformed one, one with no
// accessorclone key, an entry naming a file that does not exist, an accessor
// listed twice, or an exit with no reason.
func New(opts Options) (*analysis.Analyzer, error) {
	if opts.Policy == "" {
		return nil, errors.New(name + ": no policy file: a rule with no baseline would charge every existing accessor")
	}
	l, err := load(opts)
	if err != nil {
		return nil, err
	}
	return &analysis.Analyzer{
		Name: name,
		Doc:  "report an exported accessor that returns its own slice or map instead of a copy",
		Run: func(pass *analysis.Pass) (any, error) {
			return nil, run(pass, l)
		},
	}, nil
}

func load(opts Options) (loaded, error) {
	path := opts.Policy
	root := ""
	if opts.Module != "" {
		var err error
		if root, err = source.Root(name, opts.Module); err != nil {
			return loaded{}, err
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, filepath.FromSlash(path))
		}
	}
	p, err := decode(path)
	if err != nil {
		return loaded{}, fmt.Errorf("%s: policy %s: %w", name, opts.Policy, err)
	}
	l := loaded{module: opts.Module, policy: opts.Policy, hint: p.Settings.Hint, excused: map[Entry]string{}}
	for _, file := range slices.Sorted(maps.Keys(p.Baseline)) {
		for _, symbol := range p.Baseline[file] {
			if err := l.excuse(Entry{Path: file, Symbol: symbol}, "baseline", root); err != nil {
				return loaded{}, err
			}
		}
	}
	for _, x := range p.Exits {
		if strings.TrimSpace(x.Reason) == "" {
			return loaded{}, fmt.Errorf("%s: policy %s: %s.exits names %s %s with no reason: an exit says why",
				name, opts.Policy, name, x.Path, x.Symbol)
		}
		if err := l.excuse(Entry{Path: x.Path, Symbol: x.Symbol}, "exits", root); err != nil {
			return loaded{}, err
		}
	}
	return l, nil
}

// excuse adds e under list. root is empty when no module locates the files,
// and then the file's existence goes unchecked.
func (l loaded) excuse(e Entry, list, root string) error {
	if e.Path == "" || e.Symbol == "" {
		return fmt.Errorf("%s: policy %s: an entry of %s.%s names a path and a symbol", name, l.policy, name, list)
	}
	if prior, ok := l.excused[e]; ok {
		return fmt.Errorf("%s: policy %s lists %s %s twice, in %s.%s and %s.%s",
			name, l.policy, e.Path, e.Symbol, name, prior, name, list)
	}
	if root != "" {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(e.Path))); err != nil {
			return fmt.Errorf("%s: policy %s: %s.%s names %s, which is not a file under %s: it moved, delete or fix the entry",
				name, l.policy, name, list, e.Path, root)
		}
	}
	l.excused[e] = list
	return nil
}

// decode reads the accessorclone key strictly, so a misspelled field fails;
// the other top-level keys belong to other analyzers and are not read.
func decode(path string) (policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return policy{}, err
	}
	var doc map[string]yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return policy{}, err
	}
	node, ok := doc[name]
	if !ok {
		return policy{}, fmt.Errorf("holds no %s key", name)
	}
	raw, err := yaml.Marshal(&node)
	if err != nil {
		return policy{}, err
	}
	var p policy
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return policy{}, fmt.Errorf("%s: %w", name, err)
	}
	return p, nil
}

func run(pass *analysis.Pass, l loaded) error {
	files, err := source.Files(pass)
	if err != nil {
		return err
	}
	for _, f := range files {
		if source.IsTest(pass, f) || ast.IsGenerated(f) {
			continue
		}
		rel, ok := source.Rel(pass, l.module, f)
		if !ok {
			continue
		}
		reportNolint(pass, l.hint, f)
		leaks := leaksIn(pass, f)
		for _, lk := range leaks {
			if _, ok := l.excused[Entry{Path: rel, Symbol: lk.symbol}]; !ok {
				pass.Reportf(lk.pos, "%s", source.Hint(fmt.Sprintf(message, lk.symbol, lk.kind, lk.expr), l.hint))
			}
		}
		for e, list := range l.excused {
			if e.Path == rel && !hasSymbol(leaks, e.Symbol) {
				pass.Reportf(f.Name.Pos(), "%s", source.Hint(fmt.Sprintf(staleMessage, listedBy(list),e.Symbol, name, list, l.policy), l.hint))
			}
		}
	}
	return nil
}

func listedBy(list string) string {
	if list == "exits" {
		return "an exit"
	}
	return "the baseline"
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
func reportNolint(pass *analysis.Pass, hint string, f *ast.File) {
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
					pass.Reportf(f.Name.Pos(), "%s", source.Hint(nolintMessage, hint))
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
