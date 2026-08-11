// Package gen is the generated native-to-Buzz adapter layer. Its hand-maintained
// runtime and module registry sit beside generated module trampolines so both
// consumers use the exact same VM boundary.
package gen

import (
	"context"
	"maps"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	vm "github.com/egladman/magus/libs/gopherbuzz/vm"
)

// RegisterFunc installs a host module on a Buzz session and returns its module map.
type RegisterFunc func(context.Context, *buzz.Session) vm.Value

// Capability is a host-module property that a consumer can require.
type Capability uint8

const (
	// WASM marks pure-compute modules the browser playground can install.
	WASM Capability = 1 << iota
)

// Capabilities is a compact, allocation-free capability set.
type Capabilities uint8

// Has reports whether c includes want.
func (c Capabilities) Has(want Capability) bool { return c&Capabilities(want) != 0 }

// ModuleReg is one host module's registration and its supported capabilities.
// The browser currently requires WASM; future consumers can add independent
// requirements without widening this record with another boolean.
//
// The types live here (not in modules.go) because modules.go is //go:build
// !wasm - it references the IO trampolines - while the wasm build needs these
// types for modules_wasm.go's parallel table.
type ModuleReg struct {
	Register     RegisterFunc
	Capabilities Capabilities
	// Path is the import spelling when it differs from the registry key: the key
	// is the identifier the module binds as (`json`), while Path is what an
	// import line says (`encoding/json`). Empty means the two are the same.
	//
	// It mirrors std.Module.Path, and TestModulesMatchStd checks the two agree -
	// a Path here that std does not declare would register a module at an import
	// path nothing else in magus knows about.
	Path string
}

// Set is a module registry. Its With method returns a copy so tests can replace
// one module without mutating the process-wide default or racing parallel tests.
type Set map[string]ModuleReg

// With returns a copy of s with name registered as reg.
func (s Set) With(name string, reg ModuleReg) Set {
	out := maps.Clone(s)
	if out == nil {
		out = Set{}
	}
	out[name] = reg
	return out
}
