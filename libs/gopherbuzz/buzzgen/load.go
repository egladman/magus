package buzzgen

import (
	"errors"
	"fmt"
	"go/types"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Program is the config's packages, loaded and type-checked once.
type Program struct {
	Config Config
	dir    string
	pkgs   map[string]*packages.Package
}

const loadMode = packages.NeedName | packages.NeedTypes | packages.NeedSyntax |
	packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps

// Load type-checks every package cfg names, and the extra ones a caller
// reached, from the module at dir in one load. A type error inside an
// output buzzgen writes is tolerated, since a stale output is what a run
// replaces; any other load error stops it.
func Load(dir string, cfg Config, extra ...string) (*Program, error) {
	paths := cfg.PackagePaths(extra...)
	loaded, err := packages.Load(&packages.Config{Mode: loadMode, Dir: dir}, paths...)
	if err != nil {
		return nil, fmt.Errorf("buzzgen: load: %w", err)
	}
	p := &Program{Config: cfg, dir: dir, pkgs: map[string]*packages.Package{}}
	var errs []error
	for _, pkg := range loaded {
		p.pkgs[pkg.PkgPath] = pkg
		for _, e := range pkg.Errors {
			if !p.inOutput(e.Pos) {
				errs = append(errs, fmt.Errorf("%s: %s", pkg.PkgPath, e))
			}
		}
	}
	for _, path := range paths {
		if pkg, ok := p.pkgs[path]; !ok || pkg.Types == nil {
			errs = append(errs, fmt.Errorf("package %s did not load", path))
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("buzzgen: %w", errors.Join(errs...))
	}
	return p, nil
}

// inOutput reports whether pos, a "file:line:col" string, names a file
// under one of the config's outputs.
func (p *Program) inOutput(pos string) bool {
	file, _, _ := strings.Cut(pos, ":")
	if file == "" {
		return false
	}
	for _, out := range p.Config.Outputs {
		root := filepath.Join(p.dir, out.Path)
		if file == root || strings.HasPrefix(file, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// Package returns a loaded package by import path.
func (p *Program) Package(path string) (*packages.Package, error) {
	pkg, ok := p.pkgs[path]
	if !ok {
		return nil, fmt.Errorf("buzzgen: package %s is not in the config", path)
	}
	return pkg, nil
}

// Named returns the named type a TypeRef lists.
func (p *Program) Named(ref TypeRef) (*types.Named, error) {
	pkg, err := p.Package(ref.Package)
	if err != nil {
		return nil, err
	}
	obj, ok := pkg.Types.Scope().Lookup(ref.Name).(*types.TypeName)
	if !ok {
		return nil, fmt.Errorf("buzzgen: packages.%s.types.%s: %s declares no type %s", ref.Package, ref.Name, ref.Package, ref.Name)
	}
	named, ok := obj.Type().(*types.Named)
	if !ok {
		return nil, fmt.Errorf("buzzgen: packages.%s.types.%s is an alias, not a declared type", ref.Package, ref.Name)
	}
	return named, nil
}

// Enum derives the enum a listed named string type mirrors as.
func (p *Program) Enum(ref TypeRef) (Enum, error) {
	named, err := p.Named(ref)
	if err != nil {
		return Enum{}, err
	}
	if ref.OpenReason() != "" {
		return Enum{}, fmt.Errorf("buzzgen: %s.%s is open, so it crosses as str", ref.Package, ref.Name)
	}
	from := []*types.Package{named.Obj().Pkg()}
	if ref.Settings.CasesFrom != nil {
		from = nil
		for _, path := range ref.Settings.CasesFrom {
			pkg, err := p.Package(path)
			if err != nil {
				return Enum{}, fmt.Errorf("buzzgen: %s.%s casesFrom: %w", ref.Package, ref.Name, err)
			}
			from = append(from, pkg.Types)
		}
	}
	return DeriveEnum(named, ref, from)
}
