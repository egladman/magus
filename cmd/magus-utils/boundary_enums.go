package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"

	"github.com/egladman/magus/libs/gopherbuzz/buzzgen"
)

// boundaryEnumList is every enum .buzzgen.yaml lists, with cases buzzgen
// derives from the Go constants. It loads once, on first use, because
// most subcommands never read it.
var boundaryEnumList = sync.OnceValue(func() []boundaryEnum {
	enums, err := loadBoundaryEnums()
	if err != nil {
		fmt.Fprintln(os.Stderr, "magus-utils:", err)
		os.Exit(1)
	}
	return enums
})

func loadBoundaryEnums() ([]boundaryEnum, error) {
	root, err := buzzgenRoot()
	if err != nil {
		return nil, err
	}
	cfg, err := buzzgen.ReadConfig(filepath.Join(root, ".buzzgen.yaml"))
	if err != nil {
		return nil, err
	}
	prog, err := buzzgen.Load(root, cfg)
	if err != nil {
		return nil, err
	}
	byKey := map[string]reflect.Type{}
	for _, rt := range boundaryEnumTypes {
		byKey[rt.PkgPath()+"."+rt.Name()] = rt
	}
	var enums []boundaryEnum
	var errs []error
	for _, ref := range cfg.Types() {
		key := ref.Package + "." + ref.Name
		rt, ok := byKey[key]
		if !ok {
			continue
		}
		delete(byKey, key)
		e, err := prog.Enum(ref)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		enums = append(enums, newBoundaryEnum(e, rt, ref))
	}
	for key := range byKey {
		errs = append(errs, fmt.Errorf("%s is in boundaryEnumTypes but not in .buzzgen.yaml", key))
	}
	return enums, errors.Join(errs...)
}

func newBoundaryEnum(e buzzgen.Enum, rt reflect.Type, ref buzzgen.TypeRef) boundaryEnum {
	out := boundaryEnum{Name: e.Name, Type: rt}
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
