package vm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewVMNotNil(t *testing.T) {
	v := NewVM(context.Background())
	require.NotNil(t, v, "NewVM() returned nil")
}

func TestIsFiberIntIsFalse(t *testing.T) {
	assert.False(t, IsFiber(IntValue(1)), "IsFiber(IntValue(1))")
}

func TestIsFiberNullIsFalse(t *testing.T) {
	assert.False(t, IsFiber(NullValue()), "IsFiber(NullValue())")
}

func TestIsFiberBoolIsFalse(t *testing.T) {
	assert.False(t, IsFiber(BoolValue(false)), "IsFiber(BoolValue(false))")
}

func TestIsFiberStrIsFalse(t *testing.T) {
	assert.False(t, IsFiber(StrValue("fiber")), "IsFiber(StrValue('fiber'))")
}

func TestIsFiberListIsFalse(t *testing.T) {
	assert.False(t, IsFiber(ListValue(nil)), "IsFiber(ListValue(nil))")
}

func TestNewVMCallDepthZero(t *testing.T) {
	v := NewVM(context.Background())
	assert.Equal(t, 0, v.CallDepth(), "CallDepth() for a fresh VM")
}
