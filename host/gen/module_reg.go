package gen

import (
	"context"

	buzz "github.com/egladman/gopherbuzz"
	vm "github.com/egladman/gopherbuzz/vm"
)

// RegisterFunc installs a host module on a Buzz session and returns its module map.
type RegisterFunc func(context.Context, *buzz.Session) vm.Value

// ModuleReg is one host module's registration: how to install it, and whether it
// is safe under WASM (pure compute, no filesystem, process, network, or OS
// randomness), which the browser playground requires.
//
// The types live here (not in registry.go) because registry.go is //go:build
// !wasm - it references the IO trampolines - while the wasm build needs these
// types for registry_wasm.go's parallel table.
type ModuleReg struct {
	Register       RegisterFunc
	WASMCompatible bool
}
