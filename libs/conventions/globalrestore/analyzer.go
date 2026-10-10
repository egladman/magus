// Package globalrestore keeps a test from leaving process-global state changed.
//
// A package-level variable that commands read as a default is shared by every
// test in the binary. A test that assigns it and never puts it back leaks the
// new value into whichever test a narrowed run puts next, so the failure
// appears only under `-run`. A test function, a subtest closure or a helper
// taking a *testing.T that assigns a configured variable, or a field or element
// of one, must register a restore: a t.Cleanup or a deferred call that assigns
// the variable, directly or through a package function that does.
package globalrestore

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

const message = "%s assigns %s, process-global state every test in the binary shares, with no t.Cleanup " +
	"or defer restoring it: the value leaks into whichever test runs next"

// Options configures the analyzer returned by [New].
type Options struct {
	// Module, when set, is the module holding [Options.Package], which must then
	// exist on disk and declare every one of [Options.Vars].
	Module string `json:"module"`

	// Package is the import path declaring the variables.
	Package string `json:"package"`

	// Vars are the package-level variables a test must restore.
	Vars []string `json:"vars"`

	// Hint is appended to every diagnostic: the repository's own restore helper.
	Hint string `json:"hint"`
}

// New returns the analyzer configured by opts, erroring on a missing package or
// vars, or, with a module, on a var the package does not declare.
func New(opts Options) (*analysis.Analyzer, error) {
	if opts.Package == "" || len(opts.Vars) == 0 {
		return nil, errors.New("globalrestore: package and vars are both required")
	}
	if err := source.InModule("globalrestore", opts.Module, func(root string) error {
		if err := source.RequirePackage("globalrestore", "package", root, opts.Module, opts.Package); err != nil {
			return err
		}
		return requireVars(root, opts)
	}); err != nil {
		return nil, err
	}
	return &analysis.Analyzer{
		Name: "globalrestore",
		Doc:  "require a test that assigns process-global state to restore it",
		Run:  func(pass *analysis.Pass) (any, error) { return nil, run(pass, opts) },
	}, nil
}

// requireVars errors on a configured name no non-test file of the package
// declares at package level: a renamed variable would otherwise turn the rule
// off without a word.
func requireVars(root string, opts Options) error {
	dir := root
	if opts.Package != opts.Module {
		dir = filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(opts.Package, opts.Module+"/")))
	}
	names, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return err
	}
	declared := map[string]bool{}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				for _, id := range spec.(*ast.ValueSpec).Names {
					declared[id.Name] = true
				}
			}
		}
	}
	for _, v := range opts.Vars {
		if !declared[v] {
			return fmt.Errorf("globalrestore: vars entry %q is no package-level var of %s under %s; it was renamed or moved, fix the setting",
				v, opts.Package, root)
		}
	}
	return nil
}

type checker struct {
	pass *analysis.Pass
	opts Options

	// restorers are the package functions that assign a configured variable,
	// the shape of a snapshot helper whose result goes to t.Cleanup.
	restorers map[types.Object]bool
}

func run(pass *analysis.Pass, opts Options) error {
	var tests []*ast.File
	for _, f := range pass.Files {
		if source.IsTest(pass, f) {
			tests = append(tests, f)
		}
	}
	if len(tests) == 0 {
		return nil
	}
	c := &checker{pass: pass, opts: opts, restorers: map[types.Object]bool{}}
	for _, f := range pass.Files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !c.assigns(fn.Body) {
				continue
			}
			if obj := pass.TypesInfo.Defs[fn.Name]; obj != nil {
				c.restorers[obj] = true
			}
		}
	}
	for _, f := range tests {
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil && c.takesTesting(fn.Type) {
				c.scope(fn.Name.Name, fn.Body, false)
			}
		}
	}
	return nil
}

// scope walks one test function's body. A subtest closure is a scope of its
// own, but a restore registered by an enclosing test covers it: the leak the
// rule exists for is into the next test, not the next subtest. Only the first
// unrestored assignment of a scope is reported.
func (c *checker) scope(name string, body *ast.BlockStmt, restored bool) {
	restored = restored || c.registersRestore(body)
	reported := false
	report := func(lhs ast.Expr) {
		v := c.target(lhs)
		if v == "" || restored || reported {
			return
		}
		reported = true
		c.pass.Reportf(lhs.Pos(), "%s", source.Hint(fmt.Sprintf(message, name, v), c.opts.Hint))
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			if c.takesTesting(n.Type) {
				c.scope(name+" subtest", n.Body, restored)
				return false
			}
		case *ast.CallExpr:
			// A closure handed to t.Cleanup is the restore, not a leak.
			if c.isCleanup(n) {
				return false
			}
		case *ast.DeferStmt:
			return false
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				report(lhs)
			}
		case *ast.IncDecStmt:
			report(n.X)
		}
		return true
	})
}

// target is the configured variable an assignment to e writes, directly or
// through a field or element of it, or "" when it writes something else.
func (c *checker) target(e ast.Expr) string {
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok {
				if _, qualifier := c.pass.TypesInfo.Uses[id].(*types.PkgName); qualifier {
					return c.configured(x.Sel)
				}
			}
			e = x.X
		case *ast.Ident:
			return c.configured(x)
		default:
			return ""
		}
	}
}

func (c *checker) configured(id *ast.Ident) string {
	v, ok := c.pass.TypesInfo.Uses[id].(*types.Var)
	if !ok || v.Pkg() == nil || v.Pkg().Path() != c.opts.Package || v.Parent() != v.Pkg().Scope() {
		return ""
	}
	if slices.Contains(c.opts.Vars, v.Name()) {
		return v.Name()
	}
	return ""
}

// assigns reports whether n contains an assignment to a configured variable.
func (c *checker) assigns(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				found = found || c.target(lhs) != ""
			}
		case *ast.IncDecStmt:
			found = found || c.target(n.X) != ""
		}
		return !found
	})
	return found
}

// registersRestore reports whether body, outside its subtest closures, hands
// t.Cleanup or defer something that puts a configured variable back.
func (c *checker) registersRestore(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return !c.takesTesting(n.Type)
		case *ast.CallExpr:
			if c.isCleanup(n) && len(n.Args) == 1 {
				found = found || c.restores(n.Args[0])
			}
		case *ast.DeferStmt:
			found = found || c.restores(n.Call)
		}
		return !found
	})
	return found
}

// restores reports whether e holds a closure assigning a configured variable
// or names a function that does, which covers t.Cleanup(snapshotGlobals()) and
// defer snapshotGlobals()().
func (c *checker) restores(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			found = found || c.assigns(n.Body)
		case *ast.Ident:
			if obj := c.pass.TypesInfo.Uses[n]; obj != nil {
				found = found || c.restorers[obj]
			}
		}
		return !found
	})
	return found
}

func (c *checker) isCleanup(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Cleanup" && isTesting(c.pass.TypesInfo.TypeOf(sel.X))
}

// takesTesting reports whether a function of type ft is handed a test's
// *testing.T, *testing.B, *testing.F or testing.TB.
func (c *checker) takesTesting(ft *ast.FuncType) bool {
	for _, field := range ft.Params.List {
		if isTesting(c.pass.TypesInfo.TypeOf(field.Type)) {
			return true
		}
	}
	return false
}

func isTesting(t types.Type) bool {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != "testing" {
		return false
	}
	switch named.Obj().Name() {
	case "T", "B", "F", "TB":
		return true
	}
	return false
}
