package vm_test

import (
	"context"
	"testing"

	"github.com/egladman/gopherbuzz/vm"
)

func TestNewVMNotNil(t *testing.T) {
	v := vm.NewVM(context.Background())
	if v == nil {
		t.Fatal("NewVM() returned nil")
	}
}

func TestIsFiberIntIsFalse(t *testing.T) {
	if vm.IsFiber(vm.IntValue(1)) {
		t.Error("IsFiber(IntValue(1)) = true, want false")
	}
}

func TestIsFiberNullIsFalse(t *testing.T) {
	if vm.IsFiber(vm.NullValue()) {
		t.Error("IsFiber(NullValue()) = true, want false")
	}
}

func TestIsFiberBoolIsFalse(t *testing.T) {
	if vm.IsFiber(vm.BoolValue(false)) {
		t.Error("IsFiber(BoolValue(false)) = true, want false")
	}
}

func TestIsFiberStrIsFalse(t *testing.T) {
	if vm.IsFiber(vm.StrValue("fiber")) {
		t.Error("IsFiber(StrValue('fiber')) = true, want false")
	}
}

func TestIsFiberListIsFalse(t *testing.T) {
	if vm.IsFiber(vm.ListValue(nil)) {
		t.Error("IsFiber(ListValue(nil)) = true, want false")
	}
}

func TestNewVMCallDepthZero(t *testing.T) {
	v := vm.NewVM(context.Background())
	if d := v.CallDepth(); d != 0 {
		t.Errorf("CallDepth() = %d, want 0 for a fresh VM", d)
	}
}
