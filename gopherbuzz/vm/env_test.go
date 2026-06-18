package vm_test

import (
	"testing"

	"github.com/egladman/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEnvNotNil(t *testing.T) {
	e := vm.NewEnv()
	require.NotNil(t, e, "NewEnv() returned nil")
}

func TestEnvDefineGetRoundTrip(t *testing.T) {
	e := vm.NewEnv()
	e.Define("x", vm.IntValue(42))
	v, ok := e.Get("x")
	require.True(t, ok, "Get('x') ok = false, want true")
	assert.True(t, v.IsInt(), "Get('x') IsInt()")
	assert.Equal(t, int64(42), v.AsInt(), "Get('x')")
}

func TestEnvGetMissingReturnsFalse(t *testing.T) {
	e := vm.NewEnv()
	_, ok := e.Get("notdefined")
	assert.False(t, ok, "Get on undefined name returned ok=true, want false")
}

func TestEnvMultipleDefinesAccumulateInNames(t *testing.T) {
	e := vm.NewEnv()
	e.Define("a", vm.IntValue(1))
	e.Define("b", vm.StrValue("hello"))
	e.Define("c", vm.BoolValue(true))

	names := e.Names()
	assert.Len(t, names, 3, "Names() len")
	for _, name := range []string{"a", "b", "c"} {
		_, ok := names[name]
		assert.Truef(t, ok, "Names() missing key %q", name)
	}
}

func TestEnvSlotsGrowWithDefine(t *testing.T) {
	e := vm.NewEnv()
	assert.Empty(t, e.Slots(), "Slots() initial len")
	e.Define("x", vm.IntValue(1))
	assert.Len(t, e.Slots(), 1, "Slots() after 1 define")
	e.Define("y", vm.IntValue(2))
	assert.Len(t, e.Slots(), 2, "Slots() after 2 defines")
}

func TestEnvRedefineUpdatesSlot(t *testing.T) {
	e := vm.NewEnv()
	e.Define("v", vm.IntValue(1))
	e.Define("v", vm.IntValue(99))

	// Re-defining should update the existing slot, not add a new one.
	assert.Len(t, e.Slots(), 1, "Slots() after redefine")
	v, ok := e.Get("v")
	require.True(t, ok, "Get('v') ok = false after redefine")
	assert.Equal(t, int64(99), v.AsInt(), "Get('v')")
}
