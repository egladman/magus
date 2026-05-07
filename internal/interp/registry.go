// Package interp compiles and runs magusfile sources via a pluggable scripting backend.
// Owns backend registration, host-binding seam, REPL, and compiled-source cache.
package interp

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/egladman/magus/internal/interp/engine"
	lua "github.com/egladman/magus/internal/interp/engine/lua"
)

// luaEngineNames lists Lua engine IDs in preference order (cgo first, then pure-Go).
var luaEngineNames = []string{"luajit", "gopherlua"}

var luaEngineOverride atomic.Value // string; zero = preference order

// SetLuaEngine overrides the engine selected by ActiveBackend. Empty clears the override.
func SetLuaEngine(name string) {
	luaEngineOverride.Store(name)
}

// RegisterBackend registers a Lua engine. Called from engine init() functions.
func RegisterBackend(name string, b engine.Engine) {
	engine.Register(name, b)
}

// ActiveBackend returns the active Lua backend: SetLuaEngine override →
// MAGUS_INTERPRETER_LUA_ENGINE env var → luaEngineNames preference order.
// Panics if no Lua engine is registered.
func ActiveBackend() engine.Engine {
	if v, ok := luaEngineOverride.Load().(string); ok && v != "" {
		if e := engine.Lookup(v); e != nil {
			return e
		}
	}

	if v := os.Getenv("MAGUS_INTERPRETER_LUA_ENGINE"); v != "" {
		if e := engine.Lookup(v); e != nil {
			return e
		}
	}

	for _, name := range luaEngineNames {
		if e := engine.Lookup(name); e != nil {
			return e
		}
	}
	panic("interp: no Lua engine registered; blank-import engine/lua/gopherlua or engine/lua/luajit")
}

// CompiledEngines returns the names of registered Lua engines in preference order.
func CompiledEngines() []string {
	var names []string
	for _, name := range luaEngineNames {
		if engine.Lookup(name) != nil {
			names = append(names, name)
		}
	}
	return names
}

// Available reports whether the interp layer can parse magusfiles (at least one Lua engine
// registered and host bindings installed). Both are always present in a real magus binary.
func Available() bool {
	if hostBindingsFn == nil {
		return false
	}
	for _, name := range luaEngineNames {
		if engine.Lookup(name) != nil {
			return true
		}
	}
	return false
}

// NewLuaSession creates a new Lua session from the active Lua backend.
func NewLuaSession(ctx context.Context) (lua.Session, error) {
	eng := ActiveBackend()
	s, err := eng.NewSession(ctx)
	if err != nil {
		return nil, err
	}
	r, ok := s.(lua.Session)
	if !ok {
		_ = s.Close()
		return nil, fmt.Errorf("interp: backend %s does not provide a Lua session", eng.ID())
	}
	return r, nil
}

// HostBindingsFn registers Go-backed script modules into r.
// parseMode=true → magus.target records names only; false → stores functions.
type HostBindingsFn func(r lua.Session, parseMode bool) error

var hostBindingsFn HostBindingsFn

// RegisterHostBindings is called from the bindings package init(). Panics on double-call.
func RegisterHostBindings(fn HostBindingsFn) {
	if hostBindingsFn != nil {
		panic("interp: host bindings already registered")
	}
	hostBindingsFn = fn
}
