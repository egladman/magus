package vm_test

import (
	"testing"

	"github.com/egladman/gopherbuzz/vm"
)

func TestNewEnvNotNil(t *testing.T) {
	e := vm.NewEnv()
	if e == nil {
		t.Fatal("NewEnv() returned nil")
	}
}

func TestEnvDefineGetRoundTrip(t *testing.T) {
	e := vm.NewEnv()
	e.Define("x", vm.IntValue(42))
	v, ok := e.Get("x")
	if !ok {
		t.Fatal("Get('x') ok = false, want true")
	}
	if !v.IsInt() || v.AsInt() != 42 {
		t.Errorf("Get('x') = %v, want IntValue(42)", v)
	}
}

func TestEnvGetMissingReturnsFalse(t *testing.T) {
	e := vm.NewEnv()
	_, ok := e.Get("notdefined")
	if ok {
		t.Error("Get on undefined name returned ok=true, want false")
	}
}

func TestEnvMultipleDefinesAccumulateInNames(t *testing.T) {
	e := vm.NewEnv()
	e.Define("a", vm.IntValue(1))
	e.Define("b", vm.StrValue("hello"))
	e.Define("c", vm.BoolValue(true))

	names := e.Names()
	if len(names) != 3 {
		t.Errorf("Names() len = %d, want 3", len(names))
	}
	for _, name := range []string{"a", "b", "c"} {
		if _, ok := names[name]; !ok {
			t.Errorf("Names() missing key %q", name)
		}
	}
}

func TestEnvSlotsGrowWithDefine(t *testing.T) {
	e := vm.NewEnv()
	if len(e.Slots()) != 0 {
		t.Errorf("Slots() initial len = %d, want 0", len(e.Slots()))
	}
	e.Define("x", vm.IntValue(1))
	if len(e.Slots()) != 1 {
		t.Errorf("Slots() after 1 define len = %d, want 1", len(e.Slots()))
	}
	e.Define("y", vm.IntValue(2))
	if len(e.Slots()) != 2 {
		t.Errorf("Slots() after 2 defines len = %d, want 2", len(e.Slots()))
	}
}

func TestEnvRedefineUpdatesSlot(t *testing.T) {
	e := vm.NewEnv()
	e.Define("v", vm.IntValue(1))
	e.Define("v", vm.IntValue(99))

	// Re-defining should update the existing slot, not add a new one.
	if len(e.Slots()) != 1 {
		t.Errorf("Slots() after redefine len = %d, want 1", len(e.Slots()))
	}
	v, ok := e.Get("v")
	if !ok {
		t.Fatal("Get('v') ok = false after redefine")
	}
	if v.AsInt() != 99 {
		t.Errorf("Get('v') = %d, want 99", v.AsInt())
	}
}
