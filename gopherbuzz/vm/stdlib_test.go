package vm_test

import (
	"testing"

	"github.com/egladman/gopherbuzz/vm"
)

func TestRegisterStdlibNoPanic(t *testing.T) {
	e := vm.NewEnv()
	// Must not panic.
	vm.RegisterStdlib(e)
}

func TestRegisterStdlibPopulatesNames(t *testing.T) {
	e := vm.NewEnv()
	vm.RegisterStdlib(e)
	if len(e.Names()) == 0 {
		t.Error("Names() is empty after RegisterStdlib, want non-empty")
	}
}

func TestRegisterStdlibKnownNames(t *testing.T) {
	e := vm.NewEnv()
	vm.RegisterStdlib(e)

	// zdef is the only VM-level intrinsic global.
	// resume/resolve are session-bound (registered in session.go, not stdlib).
	// All other stdlib functions (print, assert, toInt, …) live in
	// magus/buzz/std and require `import "std"` etc.
	for _, name := range []string{"zdef"} {
		_, ok := e.Get(name)
		if !ok {
			t.Errorf("stdlib name %q not found in env after RegisterStdlib", name)
		}
	}
}

func TestRegisterStdlibValuesAreDirect(t *testing.T) {
	e := vm.NewEnv()
	vm.RegisterStdlib(e)

	v, ok := e.Get("zdef")
	if !ok {
		t.Fatal("'zdef' not found in env")
	}
	if !v.IsDirect() {
		t.Errorf("stdlib 'zdef' value IsDirect() = false, want true (got kind %q)", v.Kind())
	}
}

// TestRegisterStdlibLegacyGlobalsRemoved verifies that the old non-Buzz global
// functions (print, len, str, int, append, …) are NOT present after
// RegisterStdlib: they were removed as part of reconciling to Buzz's stdlib
// spec. These functions are now available via `import "std"` (buzz/std package).
func TestRegisterStdlibLegacyGlobalsRemoved(t *testing.T) {
	e := vm.NewEnv()
	vm.RegisterStdlib(e)

	removed := []string{"print", "len", "str", "int", "double", "bool", "append", "type", "keys", "values", "range", "error", "assert"}
	for _, name := range removed {
		if _, ok := e.Get(name); ok {
			t.Errorf("legacy global %q still present after RegisterStdlib; it should have been removed (use `import \"std\"` instead)", name)
		}
	}
}
