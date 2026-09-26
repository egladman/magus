// Package stutter reports an exported package-level name that repeats its
// package's name: url.URLParse reads as url.url... at every call site.
//
// Stricter than revive's stutter check, which needs a word boundary after the
// package name: here any exported name opening with the package name, case
// folded, is reported, so run.Runner is too. The package name is the last
// element of the import path.
package stutter

import (
	"errors"
	"go/ast"
	"go/token"
	"path"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
)

const message = "%s.%s stutters: it reads as %s.%s... at every call site; drop the package name from the " +
	"symbol, or add %q to stutter's allow setting and say why"

// Options configures the analyzer returned by [New].
type Options struct {
	// MinPackage is the shortest package name checked. A two-letter package
	// shares a prefix with too many ordinary words for the match to mean much.
	MinPackage int `json:"min-package"`

	// Allow names packages whose exported names repeat the package on purpose.
	Allow []string `json:"allow"`
}

// New returns the analyzer configured by opts, erroring on a MinPackage below 1.
func New(opts Options) (*analysis.Analyzer, error) {
	if opts.MinPackage < 1 {
		return nil, errors.New("stutter: min-package must be at least 1")
	}
	return &analysis.Analyzer{
		Name: "stutter",
		Doc:  "report exported names that repeat their package's name",
		Run:  func(pass *analysis.Pass) (any, error) { return nil, run(pass, opts) },
	}, nil
}

func run(pass *analysis.Pass, opts Options) error {
	pkg := path.Base(strings.TrimSuffix(pass.Pkg.Path(), "_test"))
	if len(pkg) < opts.MinPackage || slices.Contains(opts.Allow, pkg) {
		return nil
	}
	check := func(id *ast.Ident) {
		name := id.Name
		if ast.IsExported(name) && len(name) > len(pkg) && strings.EqualFold(name[:len(pkg)], pkg) {
			pass.Reportf(id.Pos(), message, pkg, name, pkg, pkg, pkg)
		}
	}
	for _, f := range pass.Files {
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
