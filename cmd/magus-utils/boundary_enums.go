package main

import (
	"errors"
	"fmt"
	gotypes "go/types"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/gopherbuzz/buzzgen"
)

// load type-checks every package a reached type lives in, in one load.
func (r *reach) load(root string) (*buzzgen.Program, error) {
	var pkgs []string
	for t := range r.from {
		if !slices.Contains(pkgs, t.PkgPath()) {
			pkgs = append(pkgs, t.PkgPath())
		}
	}
	return buzzgen.Load(root, r.cfg, pkgs...)
}

// enums derives each reached named string that is not open, and each enum
// a module setting declares, reading the cases from Go constants. open is
// filled with the strings that cross as str.
func (r *reach) enums(prog *buzzgen.Program, open map[reflect.Type]string) ([]boundaryEnum, error) {
	var enums []boundaryEnum
	var errs []error
	derived := map[string]bool{}
	for _, t := range r.named {
		ref := r.cfg.Ref(t.PkgPath(), t.Name())
		if reason := ref.OpenReason(); reason != "" {
			open[t] = reason
			continue
		}
		e, err := prog.Enum(ref)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s, reached from %s: %w", t, r.from[t], err))
			continue
		}
		derived[goKey(ref)] = true
		enums = append(enums, newBoundaryEnum(e, t, ref))
	}
	for _, ref := range r.cfg.Types() {
		if ref.Settings.Module == nil || derived[goKey(ref)] || ref.OpenReason() != "" {
			continue
		}
		named, err := prog.Named(ref)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if basic, ok := named.Underlying().(*gotypes.Basic); !ok || basic.Kind() != gotypes.String {
			continue
		}
		e, err := prog.Enum(ref)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		enums = append(enums, newBoundaryEnum(e, nil, ref))
	}
	slices.SortFunc(enums, func(a, b boundaryEnum) int { return strings.Compare(a.goKey, b.goKey) })
	return enums, errors.Join(errs...)
}

func goKey(ref buzzgen.TypeRef) string {
	return fmt.Sprintf("%s.%s", ref.Package, ref.Name)
}

func newBoundaryEnum(e buzzgen.Enum, rt reflect.Type, ref buzzgen.TypeRef) boundaryEnum {
	out := boundaryEnum{Name: e.Name, Type: rt, goKey: goKey(ref)}
	if ref.Settings.Module != nil {
		out.Module = *ref.Settings.Module
	}
	for _, c := range e.Cases {
		out.Cases = append(out.Cases, enumCase{Name: c.Name, Value: c.Value})
	}
	return out
}

// buzzgenRoot is the nearest directory at or above the working directory
// holding a .buzzgen.yaml, since go generate runs in each package's own.
func buzzgenRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".buzzgen.yaml")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no .buzzgen.yaml at or above the working directory")
		}
		dir = parent
	}
}
