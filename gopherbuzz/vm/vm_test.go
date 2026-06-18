package vm_test

import (
	"context"
	"testing"

	"github.com/egladman/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewVMNotNil(t *testing.T) {
	v := vm.NewVM(context.Background())
	require.NotNil(t, v, "NewVM() returned nil")
}

func TestIsFiberIntIsFalse(t *testing.T) {
	assert.False(t, vm.IsFiber(vm.IntValue(1)), "IsFiber(IntValue(1))")
}

func TestIsFiberNullIsFalse(t *testing.T) {
	assert.False(t, vm.IsFiber(vm.NullValue()), "IsFiber(NullValue())")
}

func TestIsFiberBoolIsFalse(t *testing.T) {
	assert.False(t, vm.IsFiber(vm.BoolValue(false)), "IsFiber(BoolValue(false))")
}

func TestIsFiberStrIsFalse(t *testing.T) {
	assert.False(t, vm.IsFiber(vm.StrValue("fiber")), "IsFiber(StrValue('fiber'))")
}

func TestIsFiberListIsFalse(t *testing.T) {
	assert.False(t, vm.IsFiber(vm.ListValue(nil)), "IsFiber(ListValue(nil))")
}

func TestNewVMCallDepthZero(t *testing.T) {
	v := vm.NewVM(context.Background())
	assert.Equal(t, 0, v.CallDepth(), "CallDepth() for a fresh VM")
}
