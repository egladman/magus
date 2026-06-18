package vm_test

import (
	"context"
	"testing"

	"github.com/egladman/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValueConstructorsAndPredicates(t *testing.T) {
	noop := func(_ context.Context, _ []vm.Value) (vm.Value, error) { return vm.NullValue(), nil }

	t.Run("IntValue", func(t *testing.T) {
		v := vm.IntValue(42)
		assert.True(t, v.IsInt())
		assert.Equal(t, "int", v.Kind())
	})
	t.Run("FloatValue", func(t *testing.T) {
		v := vm.FloatValue(3.14)
		assert.True(t, v.IsFloat())
		assert.Equal(t, "double", v.Kind())
	})
	t.Run("BoolValue true", func(t *testing.T) {
		v := vm.BoolValue(true)
		assert.True(t, v.IsBool())
		assert.Equal(t, "bool", v.Kind())
	})
	t.Run("BoolValue false", func(t *testing.T) {
		v := vm.BoolValue(false)
		assert.True(t, v.IsBool())
		assert.Equal(t, "bool", v.Kind())
	})
	t.Run("StrValue", func(t *testing.T) {
		v := vm.StrValue("hello")
		assert.True(t, v.IsStr())
		assert.Equal(t, "str", v.Kind())
	})
	t.Run("ListValue", func(t *testing.T) {
		v := vm.ListValue(nil)
		assert.True(t, v.IsList())
		assert.Equal(t, "list", v.Kind())
	})
	t.Run("NewMap", func(t *testing.T) {
		v := vm.NewMap()
		assert.True(t, v.IsMap())
		assert.Equal(t, "map", v.Kind())
	})
	t.Run("NullValue", func(t *testing.T) {
		v := vm.NullValue()
		assert.True(t, v.IsNull())
		assert.Equal(t, "null", v.Kind())
	})
	t.Run("DirectValue", func(t *testing.T) {
		v := vm.DirectValue("myfn", noop)
		assert.True(t, v.IsDirect())
		assert.Equal(t, "direct", v.Kind())
	})
}

func TestValueAsInt(t *testing.T) {
	assert.Equal(t, int64(99), vm.IntValue(99).AsInt())
}

func TestValueAsIntNegative(t *testing.T) {
	assert.Equal(t, int64(-7), vm.IntValue(-7).AsInt())
}

func TestValueAsFloat(t *testing.T) {
	assert.Equal(t, 2.718, vm.FloatValue(2.718).AsFloat())
}

func TestValueAsBool(t *testing.T) {
	assert.True(t, vm.BoolValue(true).AsBool())
	assert.False(t, vm.BoolValue(false).AsBool())
}

func TestValueAsString(t *testing.T) {
	assert.Equal(t, "world", vm.StrValue("world").AsString())
}

func TestValueListItems(t *testing.T) {
	items := []vm.Value{vm.IntValue(1), vm.IntValue(2), vm.IntValue(3)}
	got := vm.ListValue(items).ListItems()
	require.Len(t, got, 3)
	for i, item := range got {
		assert.Equalf(t, int64(i+1), item.AsInt(), "ListItems()[%d].AsInt()", i)
	}
}

func TestValueListItemsNil(t *testing.T) {
	got := vm.ListValue(nil).ListItems()
	assert.Nil(t, got, "ListItems() on nil-backed list")
}

func TestValueString(t *testing.T) {
	assert.Equal(t, "null", vm.NullValue().String())
	assert.Equal(t, "true", vm.BoolValue(true).String())
	assert.Equal(t, "false", vm.BoolValue(false).String())
	assert.Equal(t, "42", vm.IntValue(42).String())
	assert.Equal(t, "hi", vm.StrValue("hi").String())
	assert.Equal(t, "[1, 2]", vm.ListValue([]vm.Value{vm.IntValue(1), vm.IntValue(2)}).String())
}

func TestValueRawEqual(t *testing.T) {
	// Scalars with same tag and payload must be equal.
	assert.True(t, vm.IntValue(5).RawEqual(vm.IntValue(5)))
	assert.False(t, vm.IntValue(5).RawEqual(vm.IntValue(6)))
	assert.True(t, vm.NullValue().RawEqual(vm.NullValue()))
	assert.True(t, vm.BoolValue(true).RawEqual(vm.BoolValue(true)))
	assert.False(t, vm.BoolValue(true).RawEqual(vm.BoolValue(false)))
	// Different types are not raw-equal even for the same numeric payload.
	assert.False(t, vm.IntValue(0).RawEqual(vm.NullValue()))
}
