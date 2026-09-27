// Package stutter reports an exported package-level name that repeats its
// package's name: url.URLParse reads as url.url... at every call site.
//
// Stricter than revive's stutter check, which needs a word boundary after the
// package name: here any exported name opening with the package name, case
// folded, is reported, so run.Runner is too. The package name is the one its
// package clause declares, which is what a call site spells; a main package
// has no call sites and is not checked.
package stutter

import (
	"errors"
	"go/ast"
	"go/token"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

const message = "%s.%s stutters: it reads as %s.%s... at every call site; drop the package name from the " +
	"symbol, or add %q to stutter's allow setting and say why"

const defaultMinPackageLen = 3

// Options configures the analyzer returned by [New]. The zero value checks
// every package whose name is three bytes or longer.
type Options struct {
	// MinPackageLen is the shortest package name checked, in bytes; zero means
	// 3. A two-letter package shares a prefix with too many ordinary words for
	// the match to mean much.
	MinPackageLen int `json:"min-package-len"`

	// Allow names packages whose exported names repeat the package on purpose.
	Allow []string `json:"allow"`
}

// New returns the analyzer configured by opts, erroring on a negative
// MinPackageLen.
func New(opts Options) (*analysis.Analyzer, error) {
	if opts.MinPackageLen < 0 {
		return nil, errors.New("stutter: min-package-len is negative")
	}
	if opts.MinPackageLen == 0 {
		opts.MinPackageLen = defaultMinPackageLen
	}
	return &analysis.Analyzer{
		Name: "stutter",
		Doc:  "report exported names that repeat their package's name",
		Run:  func(pass *analysis.Pass) (any, error) { return nil, run(pass, opts) },
	}, nil
}

func run(pass *analysis.Pass, opts Options) error {
	pkg := strings.TrimSuffix(pass.Pkg.Name(), "_test")
	if pkg == "main" || len(pkg) < opts.MinPackageLen || slices.Contains(opts.Allow, pkg) {
		return nil
	}
	files, err := source.Files(pass)
	if err != nil {
		return err
	}
	check := func(id *ast.Ident) {
		name := id.Name
		if ast.IsExported(name) && len(name) > len(pkg) && strings.EqualFold(name[:len(pkg)], pkg) {
			pass.Reportf(id.Pos(), message, pkg, name, pkg, pkg, pkg)
		}
	}
	for _, f := range files {
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					check(d.Name)
				}
			case *ast.GenDecl:
				if d.Tok == token.IMPORT {
					continue
				}
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						check(s.Name)
					case *ast.ValueSpec:
						for _, id := range s.Names {
							check(id)
						}
					}
				}
			}
		}
	}
	return nil
}
