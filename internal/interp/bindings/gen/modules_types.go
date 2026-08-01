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
// The types live here (not in registry.go) because registry.go is //go:build
// !wasm - it references the IO trampolines - while the wasm build needs these
// types for registry_wasm.go's parallel table.
type ModuleReg struct {
	Register     RegisterFunc
	Capabilities Capabilities
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
