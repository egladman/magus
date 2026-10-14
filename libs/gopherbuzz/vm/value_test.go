package vm

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValueConstructorsAndPredicates(t *testing.T) {
	noop := func(_ context.Context, _ []Value) (Value, error) { return NullValue(), nil }

	t.Run("IntValue", func(t *testing.T) {
		v := IntValue(42)
		assert.True(t, v.IsInt())
		assert.Equal(t, "int", v.Kind())
	})
	t.Run("FloatValue", func(t *testing.T) {
		v := FloatValue(3.14)
		assert.True(t, v.IsFloat())
		assert.Equal(t, "double", v.Kind())
	})
	t.Run("BoolValue true", func(t *testing.T) {
		v := BoolValue(true)
		assert.True(t, v.IsBool())
		assert.Equal(t, "bool", v.Kind())
	})
	t.Run("BoolValue false", func(t *testing.T) {
		v := BoolValue(false)
		assert.True(t, v.IsBool())
		assert.Equal(t, "bool", v.Kind())
	})
	t.Run("StrValue", func(t *testing.T) {
		v := StrValue("hello")
		assert.True(t, v.IsStr())
		assert.Equal(t, "str", v.Kind())
	})
	t.Run("ListValue", func(t *testing.T) {
		v := ListValue(nil)
		assert.True(t, v.IsList())
		assert.Equal(t, "list", v.Kind())
	})
	t.Run("NewMap", func(t *testing.T) {
		v := NewMap()
		assert.True(t, v.IsMap())
		assert.Equal(t, "map", v.Kind())
	})
	t.Run("NullValue", func(t *testing.T) {
		v := NullValue()
		assert.True(t, v.IsNull())
		assert.Equal(t, "null", v.Kind())
	})
	t.Run("DirectValue", func(t *testing.T) {
		v := DirectValue("myfn", noop)
		assert.True(t, v.IsDirect())
		assert.Equal(t, "direct", v.Kind())
	})
}

func TestValueAsInt(t *testing.T) {
	assert.Equal(t, int64(99), IntValue(99).AsInt())
}

func TestValueAsIntNegative(t *testing.T) {
	assert.Equal(t, int64(-7), IntValue(-7).AsInt())
}

func TestValueAsFloat(t *testing.T) {
	assert.Equal(t, 2.718, FloatValue(2.718).AsFloat())
}

func TestValueAsBool(t *testing.T) {
	assert.True(t, BoolValue(true).AsBool())
	assert.False(t, BoolValue(false).AsBool())
}

func TestValueAsString(t *testing.T) {
	assert.Equal(t, "world", StrValue("world").AsString())
}

func TestValueListItems(t *testing.T) {
	items := []Value{IntValue(1), IntValue(2), IntValue(3)}
	got := ListValue(items).ListItems()
	require.Len(t, got, 3)
	for i, item := range got {
		assert.Equalf(t, int64(i+1), item.AsInt(), "ListItems()[%d].AsInt()", i)
	}
}

func TestValueListItemsNil(t *testing.T) {
	got := ListValue(nil).ListItems()
	assert.Nil(t, got, "ListItems() on nil-backed list")
}

func TestValueString(t *testing.T) {
	assert.Equal(t, "null", NullValue().String())
	assert.Equal(t, "true", BoolValue(true).String())
	assert.Equal(t, "false", BoolValue(false).String())
	assert.Equal(t, "42", IntValue(42).String())
	assert.Equal(t, "hi", StrValue("hi").String())
	assert.Equal(t, "[1, 2]", ListValue([]Value{IntValue(1), IntValue(2)}).String())
}

// TestValueStringCircular is the regression for String recursing into a
// genuine reference cycle: Buzz lists are heap objects mutable in place
// (list.append), so `[any] l = mut []; l.append(l);` makes l[0] == l. String
// backs str()/print/string interpolation (upstream-visible), so it must render
// a placeholder for the revisited list instead of stack-overflowing (a FATAL,
// unrecoverable Go error) or erroring. Run on a goroutine and bounded with
// select + time.After (the repo's hang/crash-test idiom; see pool_test.go
// TestDispatchRejectsCycleWithMemo) since a regression here is unsafe to call
// inline.
func TestValueStringCircular(t *testing.T) {
	items := make([]Value, 1)
	l := ListValue(items)
	items[0] = l // l[0] == l: the same cycle list.append(l) would create

	done := make(chan string, 1)
	go func() { done <- l.String() }()

	select {
	case s := <-done:
		assert.Equal(t, "[[...]]", s, "a cyclic list should render a placeholder for the revisit, not recurse")
	case <-time.After(5 * time.Second):
		t.Fatal("Value.String did not return within bound on a cyclic list")
	}
}

func TestValueRawEqual(t *testing.T) {
	// This is a scalar-only spec: RawEqual compares raw tag+num bits, so heap
	// values (str, list, map, ...) are not covered here: under buzz_safe and
	// buzz_unsafe their num is 0 and any two same-tag heap values compare equal.
	// Use Equal (see TestValueEqual) for heap and language-level equality.
	// Scalars with same tag and payload must be equal.
	assert.True(t, IntValue(5).RawEqual(IntValue(5)))
	assert.False(t, IntValue(5).RawEqual(IntValue(6)))
	assert.True(t, NullValue().RawEqual(NullValue()))
	assert.True(t, BoolValue(true).RawEqual(BoolValue(true)))
	assert.False(t, BoolValue(true).RawEqual(BoolValue(false)))
	// Different types are not raw-equal even for the same numeric payload.
	assert.False(t, IntValue(0).RawEqual(NullValue()))
}

// TestValueEqual pins down Buzz `==` semantics as exposed by Value.Equal. This
// source runs under every value representation (nanbox, buzz_safe, buzz_unsafe)
// and must agree in all three: RawEqual would diverge here for the heap cases.
func TestValueEqual(t *testing.T) {
	// String content equality, including a string built at runtime (not a
	// compile-time literal) versus a literal of the same content.
	built := string([]byte{'b'})
	assert.True(t, StrValue(built).Equal(StrValue("b")))
	assert.True(t, StrValue("hello").Equal(StrValue("hello")))
	assert.False(t, StrValue("a").Equal(StrValue("b")))

	// int/float numeric coercion, matching the == operator.
	assert.True(t, IntValue(1).Equal(FloatValue(1.0)))
	assert.True(t, FloatValue(2.0).Equal(IntValue(2)))
	assert.False(t, IntValue(1).Equal(FloatValue(1.5)))
	assert.False(t, IntValue(1).Equal(IntValue(2)))

	// null and bool scalars.
	assert.True(t, NullValue().Equal(NullValue()))
	assert.True(t, BoolValue(true).Equal(BoolValue(true)))
	assert.False(t, BoolValue(true).Equal(BoolValue(false)))
	assert.False(t, NullValue().Equal(IntValue(0)))

	// Lists compare by reference identity: two distinct values with equal
	// content are NOT equal, but a value is equal to itself.
	l1 := ListValue([]Value{IntValue(1)})
	l2 := ListValue([]Value{IntValue(1)})
	assert.False(t, l1.Equal(l2), "distinct content-equal lists must not be Equal")
	assert.True(t, l1.Equal(l1), "a list value must be Equal to itself")

	// Maps compare by reference identity too.
	m1 := NewMap()
	m1.MapSet("a", IntValue(1))
	m2 := NewMap()
	m2.MapSet("a", IntValue(1))
	assert.False(t, m1.Equal(m2), "distinct content-equal maps must not be Equal")
	assert.True(t, m1.Equal(m1), "a map value must be Equal to itself")
}

// TestValueEqualFunctionIdentity pins reference identity for function values.
// These cases are load-bearing for magus.needs, which recovers which exported
// target a passed function value refers to by matching it against the exports
// it handed out.
func TestValueEqualFunctionIdentity(t *testing.T) {
	noop := func(_ context.Context, _ []Value) (Value, error) { return NullValue(), nil }

	// A function value equals itself, so host code can match a callable it
	// handed out earlier against one a script passes back.
	fnA := DirectValue("a", noop)
	assert.True(t, fnA.Equal(fnA), "a function value equals itself")

	// Two distinct function values are never equal, even with the same name and
	// underlying Go func: identity, not structure, for heap kinds.
	fnB := DirectValue("a", noop)
	assert.False(t, fnA.Equal(fnB), "distinct function values are not equal")
}

func TestValueEnumDef(t *testing.T) {
	def := EnumDefValue("Tier", []string{"low", "high"}, nil)
	name, cases, ok := def.EnumDef()
	require.True(t, ok)
	assert.Equal(t, "Tier", name)
	assert.Equal(t, []string{"low", "high"}, cases, "cases come back in declaration order")

	cases[0] = "edited"
	_, again, _ := def.EnumDef()
	assert.Equal(t, "low", again[0], "the returned cases are a copy, not the definition's own")

	high := NewVM(context.Background()).enumCase(def.asEnumDef(), 1)
	for _, v := range []Value{high, NewMap(), StrValue("Tier"), NullValue()} {
		name, cases, ok := v.EnumDef()
		assert.False(t, ok, "%s is not an enum definition", v.Kind())
		assert.Empty(t, name)
		assert.Nil(t, cases)
	}
}

func TestValueEnumCase(t *testing.T) {
	vm := NewVM(context.Background())
	tier := EnumDefValue("Tier", []string{"low", "high"}, nil)
	enum, name, ok := vm.enumCase(tier.asEnumDef(), 1).EnumCase()
	require.True(t, ok)
	assert.Equal(t, "Tier", enum)
	assert.Equal(t, "high", name)

	// An enum<str> case may assign a value other than its name: EnumCase still
	// names the case, and EnumValue reads the assigned value.
	mode := EnumDefValue("Mode", []string{"fast"}, []Value{StrValue("f")})
	fast := vm.enumCase(mode.asEnumDef(), 0)
	_, name, ok = fast.EnumCase()
	require.True(t, ok)
	assert.Equal(t, "fast", name)
	backing, _ := fast.EnumValue()
	assert.Equal(t, "f", backing.AsString())

	for _, v := range []Value{tier, IntValue(1), StrValue("high"), NullValue()} {
		enum, name, ok := v.EnumCase()
		assert.False(t, ok, "%s is not an enum case", v.Kind())
		assert.Empty(t, enum)
		assert.Empty(t, name)
	}
}

// MapKeys reduces the int 1 and the str "1" to the same display string;
// MapKeyValues keeps each key, an enum case included.
func TestValueMapKeyValues(t *testing.T) {
	tier := EnumDefValue("Tier", []string{"low", "high"}, nil)
	high := NewVM(context.Background()).enumCase(tier.asEnumDef(), 1)
	m := NewMap()
	m.asMap().setVal(high, StrValue("by case"))
	m.asMap().setVal(IntValue(1), StrValue("by int"))
	m.asMap().setVal(StrValue("1"), StrValue("by str"))

	keys := m.MapKeyValues()
	require.Len(t, keys, 3)
	require.Len(t, m.MapValues(), len(keys), "keys and values are parallel")

	enum, name, ok := keys[0].EnumCase()
	require.True(t, ok)
	assert.Equal(t, "Tier", enum)
	assert.Equal(t, "high", name)
	assert.Equal(t, "by case", m.MapValues()[0].AsString())

	assert.True(t, keys[1].IsInt())
	assert.Equal(t, int64(1), keys[1].AsInt())
	assert.True(t, keys[2].IsStr())
	assert.Equal(t, "1", keys[2].AsString())
	assert.Equal(t, m.MapKeys()[1], m.MapKeys()[2], "MapKeys is lossy where MapKeyValues is not")

	for _, v := range []Value{StrValue("m"), ListValue(nil), NullValue(), high} {
		assert.Nil(t, v.MapKeyValues(), "%s is not a map", v.Kind())
	}
}
