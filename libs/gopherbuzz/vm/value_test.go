package vm_test

import (
	"context"
	"testing"

	"github.com/egladman/magus/libs/gopherbuzz/vm"
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
	// This is a scalar-only spec: RawEqual compares raw tag+num bits, so heap
	// values (str, list, map, ...) are not covered here - under buzz_safe and
	// buzz_unsafe their num is 0 and any two same-tag heap values compare equal.
	// Use Equal (see TestValueEqual) for heap and language-level equality.
	// Scalars with same tag and payload must be equal.
	assert.True(t, vm.IntValue(5).RawEqual(vm.IntValue(5)))
	assert.False(t, vm.IntValue(5).RawEqual(vm.IntValue(6)))
	assert.True(t, vm.NullValue().RawEqual(vm.NullValue()))
	assert.True(t, vm.BoolValue(true).RawEqual(vm.BoolValue(true)))
	assert.False(t, vm.BoolValue(true).RawEqual(vm.BoolValue(false)))
	// Different types are not raw-equal even for the same numeric payload.
	assert.False(t, vm.IntValue(0).RawEqual(vm.NullValue()))
}

// TestValueEqual pins down Buzz `==` semantics as exposed by Value.Equal. This
// source runs under every value representation (nanbox, buzz_safe, buzz_unsafe)
// and must agree in all three - RawEqual would diverge here for the heap cases.
func TestValueEqual(t *testing.T) {
	// String content equality, including a string built at runtime (not a
	// compile-time literal) versus a literal of the same content.
	built := string([]byte{'b'})
	assert.True(t, vm.StrValue(built).Equal(vm.StrValue("b")))
	assert.True(t, vm.StrValue("hello").Equal(vm.StrValue("hello")))
	assert.False(t, vm.StrValue("a").Equal(vm.StrValue("b")))

	// int/float numeric coercion, matching the == operator.
	assert.True(t, vm.IntValue(1).Equal(vm.FloatValue(1.0)))
	assert.True(t, vm.FloatValue(2.0).Equal(vm.IntValue(2)))
	assert.False(t, vm.IntValue(1).Equal(vm.FloatValue(1.5)))
	assert.False(t, vm.IntValue(1).Equal(vm.IntValue(2)))

	// null and bool scalars.
	assert.True(t, vm.NullValue().Equal(vm.NullValue()))
	assert.True(t, vm.BoolValue(true).Equal(vm.BoolValue(true)))
	assert.False(t, vm.BoolValue(true).Equal(vm.BoolValue(false)))
	assert.False(t, vm.NullValue().Equal(vm.IntValue(0)))

	// Lists compare by reference identity: two distinct values with equal
	// content are NOT equal, but a value is equal to itself.
	l1 := vm.ListValue([]vm.Value{vm.IntValue(1)})
	l2 := vm.ListValue([]vm.Value{vm.IntValue(1)})
	assert.False(t, l1.Equal(l2), "distinct content-equal lists must not be Equal")
	assert.True(t, l1.Equal(l1), "a list value must be Equal to itself")

	// Maps compare by reference identity too.
	m1 := vm.NewMap()
	m1.MapSet("a", vm.IntValue(1))
	m2 := vm.NewMap()
	m2.MapSet("a", vm.IntValue(1))
	assert.False(t, m1.Equal(m2), "distinct content-equal maps must not be Equal")
	assert.True(t, m1.Equal(m1), "a map value must be Equal to itself")
}

// TestValueEqualFunctionIdentity pins reference identity for function values.
// These cases are load-bearing for magus.needs, which recovers which exported
// target a passed function value refers to by matching it against the exports
// it handed out.
func TestValueEqualFunctionIdentity(t *testing.T) {
	noop := func(_ context.Context, _ []vm.Value) (vm.Value, error) { return vm.NullValue(), nil }

	// A function value equals itself, so host code can match a callable it
	// handed out earlier against one a script passes back.
	fnA := vm.DirectValue("a", noop)
	assert.True(t, fnA.Equal(fnA), "a function value equals itself")

	// Two distinct function values are never equal, even with the same name and
	// underlying Go func: identity, not structure, for heap kinds.
	fnB := vm.DirectValue("a", noop)
	assert.False(t, fnA.Equal(fnB), "distinct function values are not equal")
}
