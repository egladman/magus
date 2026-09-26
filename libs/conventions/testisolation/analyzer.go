// Package testisolation holds every test binary that links a configured package
// to a TestMain calling an isolating helper.
//
// In magus the package is the one resolving the per-user runtime directory. A
// test binary linking it can reach the person's real directory, dial their
// broker, claim capacity from it, and bind a pool beside their server. The
// helper points the process at a private directory first.
//
// Reach is a package fact carried along imports, so the link graph is the
// compiler's rather than a hand-kept list.
package testisolation

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

const message = "this test binary links %s but no TestMain in its directory calls %s, so its tests can reach " +
	"the person's real runtime directory: add `func TestMain(m *testing.M) { %s(m) }`"

// Options configures the analyzer returned by [New].
type Options struct {
	// Package is the import path whose reach demands isolation.
	Package string `json:"package"`

	// Calls are the helpers a TestMain may call, as pkg.Func.
	Calls []string `json:"calls"`
}

// reaches marks a package that is, or transitively imports, [Options.Package].
type reaches struct{}

func (*reaches) AFact()         {}
func (*reaches) String() string { return "reaches" }

// New returns the analyzer configured by opts, erroring on a missing package,
// no calls, or a call not spelled pkg.Func.
func New(opts Options) (*analysis.Analyzer, error) {
	if opts.Package == "" || len(opts.Calls) == 0 {
		return nil, errors.New("testisolation: package and calls are both required")
	}
	for _, c := range opts.Calls {
		if pkg, fn, ok := strings.Cut(c, "."); !ok || pkg == "" || fn == "" {
			return nil, errors.New("testisolation: call " + c + " is not pkg.Func")
		}
	}
	return &analysis.Analyzer{
		Name:      "testisolation",
		Doc:       "require an isolating TestMain in every test binary that links a package",
		FactTypes: []analysis.Fact{new(reaches)},
		Run:       func(pass *analysis.Pass) (any, error) { return nil, run(pass, opts) },
	}, nil
}

func run(pass *analysis.Pass, opts Options) error {
	linked := pass.Pkg.Path() == opts.Package ||
		slices.ContainsFunc(pass.Pkg.Imports(), func(imp *types.Package) bool {
			return imp.Path() == opts.Package || pass.ImportPackageFact(imp, new(reaches))
		})
	if !linked {
		return nil
	}
	// Nothing imports an external test package or a generated test main.
	if path := pass.Pkg.Path(); !strings.HasSuffix(path, "_test") && !strings.HasSuffix(path, ".test") {
		pass.ExportPackageFact(new(reaches))
	}

	var tests []*ast.File
	for _, f := range pass.Files {
		if source.IsTest(pass, f) {
			tests = append(tests, f)
		}
	}
	if len(tests) == 0 {
		return nil
	}
	// A TestMain in either the internal or the external test package serves the
	// binary, and a pass holds only one of them, so the directory is read whole.
	isolated, err := isolates(filepath.Dir(source.Name(pass, tests[0])), opts.Calls)
	if err != nil || isolated {
		return err
	}
	first := slices.MinFunc(tests, func(a, b *ast.File) int {
		return strings.Compare(source.Name(pass, a), source.Name(pass, b))
	})
	pass.Reportf(first.Name.Pos(), message, opts.Package, strings.Join(opts.Calls, " or "), opts.Calls[0])
	return nil
}

func isolates(dir string, calls []string) (bool, error) {
	names, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return false, err
	}
	for _, name := range names {
		src, err := os.ReadFile(name)
		if err != nil {
			return false, err
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Name.Name != "TestMain" || fn.Body == nil {
				continue
			}
			found := false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok {
					if pkg, ok := sel.X.(*ast.Ident); ok && slices.Contains(calls, pkg.Name+"."+sel.Sel.Name) {
						found = true
					}
				}
				return !found
			})
			if found {
				return true, nil
			}
		}
	}
	return false, nil
}
