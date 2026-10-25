package benchbeside

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

const message = "%s is an optimization with no Benchmark in %s: add one beside it, or exempt the package with a reason"

// Options configures the analyzer returned by [New].
type Options struct {
	// Module is the import path [Options.Exempt] keys are relative to. When
	// set, every exempt directory must hold Go files and no exempt directory's
	// tests may hold a func Benchmark.
	Module string `json:"module"`

	// Exempt maps a module-relative, slash-separated package directory to why
	// its optimizations carry no benchmark. The reason is required.
	Exempt map[string]string `json:"exempt"`
}

// New returns the analyzer configured by opts. It errors on an exempt entry
// with no reason, one naming a package with no Go files, and one whose tests
// now hold a func Benchmark: the exemption is shrink-only.
func New(opts Options) (*analysis.Analyzer, error) {
	for _, dir := range slices.Sorted(maps.Keys(opts.Exempt)) {
		if opts.Exempt[dir] == "" {
			return nil, fmt.Errorf("benchbeside: exempt entry %q needs a reason", dir)
		}
	}
	if err := source.InModule("benchbeside", opts.Module, func(root string) error {
		for _, dir := range slices.Sorted(maps.Keys(opts.Exempt)) {
			if err := source.RequirePackage("benchbeside", "exempt", root, opts.Module, fmt.Sprintf("%s/%s", opts.Module, dir)); err != nil {
				return err
			}
			has, err := hasBenchmark(filepath.Join(root, filepath.FromSlash(dir)))
			if err != nil {
				return err
			}
			if has {
				return fmt.Errorf("benchbeside: exempt entry %q: its tests now hold a func Benchmark, so the exemption is stale: delete it", dir)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return &analysis.Analyzer{
		Name: "benchbeside",
		Doc:  "report an optimization: comment or a cache type in a package whose tests hold no benchmark",
		Run:  func(pass *analysis.Pass) (any, error) { return nil, run(pass, opts) },
	}, nil
}

type finding struct {
	pos  token.Pos
	what string
}

func run(pass *analysis.Pass, opts Options) error {
	files, err := source.Files(pass)
	if err != nil {
		return err
	}
	var found []finding
	var pkgDir, diskDir string
	methods := map[string]bool{}
	var caches []*ast.TypeSpec
	for _, f := range files {
		if source.IsTest(pass, f) {
			continue
		}
		if pkgDir == "" {
			rel, ok := source.Rel(pass, opts.Module, f)
			if !ok {
				rel, _ = source.Rel(pass, "", f)
			}
			pkgDir = path.Dir(rel)
			diskDir = filepath.Dir(source.Name(pass, f))
		}
		for _, group := range f.Comments {
			for _, c := range group.List {
				if isOptimization(c.Text) {
					found = append(found, finding{c.Pos(), "this optimization: comment"})
				}
			}
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if name, ok := receiverName(d); ok {
					methods[name] = true
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok && isCacheName(ts.Name.Name) {
						caches = append(caches, ts)
					}
				}
			}
		}
	}
	for _, ts := range caches {
		if methods[ts.Name.Name] {
			found = append(found, finding{ts.Pos(), "type " + ts.Name.Name})
		}
	}
	if len(found) == 0 {
		return nil
	}
	if _, exempt := opts.Exempt[pkgDir]; exempt {
		return nil
	}
	has, err := hasBenchmark(diskDir)
	if err != nil || has {
		return err
	}
	for _, f := range found {
		pass.Reportf(f.pos, message, f.what, pkgDir)
	}
	return nil
}

// isOptimization reports whether a // comment opens with the repository's
// optimization: marker. A mid-sentence use of the word is prose, not a marker.
func isOptimization(text string) bool {
	body, ok := strings.CutPrefix(text, "//")
	return ok && strings.HasPrefix(strings.TrimSpace(body), "optimization:")
}

func isCacheName(name string) bool {
	return strings.HasSuffix(name, "Cache") || strings.HasSuffix(name, "Memo")
}

// receiverName returns the base type name of d's receiver, through a pointer
// or type parameters.
func receiverName(d *ast.FuncDecl) (string, bool) {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return "", false
	}
	expr := d.Recv.List[0].Type
	for {
		switch e := expr.(type) {
		case *ast.StarExpr:
			expr = e.X
		case *ast.IndexExpr:
			expr = e.X
		case *ast.IndexListExpr:
			expr = e.X
		case *ast.ParenExpr:
			expr = e.X
		case *ast.Ident:
			return e.Name, true
		default:
			return "", false
		}
	}
}

// hasBenchmark reports whether a _test.go file in dir declares a top-level
// func Benchmark*. It reads the directory rather than the pass, which holds
// the test files only in some of the package variants a driver loads.
func hasBenchmark(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, fmt.Errorf("benchbeside: %w", err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Benchmark") {
				return true, nil
			}
		}
	}
	return false, nil
}
