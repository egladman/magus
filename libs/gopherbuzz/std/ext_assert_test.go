package std

import (
	"fmt"
	"testing"

	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
)

func TestSkipMessage(t *testing.T) {
	// A skip error, however the VM wraps it, is recognized and its reason recovered.
	skipErr := fmt.Errorf("buzz: uncaught error: %s%s%s", skipPrefix, "needs fixture", skipSuffix)
	reason, ok := SkipMessage(skipErr)
	assert.True(t, ok)
	assert.Equal(t, "needs fixture", reason)

	// An ordinary assertion failure is not a skip.
	_, ok = SkipMessage(fmt.Errorf("assert\\equal failed: got 1 want 2"))
	assert.False(t, ok)

	_, ok = SkipMessage(nil)
	assert.False(t, ok)
}

func mapVal(pairs map[string]vm.Value) vm.Value {
	m := vm.NewMap()
	for k, v := range pairs {
		m.MapSet(k, v)
	}
	return m
}

func TestDeepEqualValue(t *testing.T) {
	cases := []struct {
		name string
		a, b vm.Value
		want bool
	}{
		{"equal ints", vm.IntValue(1), vm.IntValue(1), true},
		{"int vs double cross-type", vm.IntValue(1), vm.FloatValue(1), true},
		{"unequal strings", vm.StrValue("a"), vm.StrValue("b"), false},
		{"null equals null", vm.Null, vm.Null, true},
		{"null vs value", vm.Null, vm.IntValue(0), false},
		{
			"maps equal regardless of insertion order",
			mapVal(map[string]vm.Value{"a": vm.IntValue(1), "b": vm.IntValue(2)}),
			mapVal(map[string]vm.Value{"b": vm.IntValue(2), "a": vm.IntValue(1)}),
			true,
		},
		{
			"maps differ by value",
			mapVal(map[string]vm.Value{"a": vm.IntValue(1)}),
			mapVal(map[string]vm.Value{"a": vm.IntValue(2)}),
			false,
		},
		{
			"maps differ by key set",
			mapVal(map[string]vm.Value{"a": vm.IntValue(1)}),
			mapVal(map[string]vm.Value{"a": vm.IntValue(1), "b": vm.IntValue(2)}),
			false,
		},
		{
			"nested list of maps equal",
			vm.ListValue([]vm.Value{mapVal(map[string]vm.Value{"k": vm.StrValue("v")})}),
			vm.ListValue([]vm.Value{mapVal(map[string]vm.Value{"k": vm.StrValue("v")})}),
			true,
		},
		{
			"lists differ by order",
			vm.ListValue([]vm.Value{vm.IntValue(1), vm.IntValue(2)}),
			vm.ListValue([]vm.Value{vm.IntValue(2), vm.IntValue(1)}),
			false,
		},
		{
			"list vs map",
			vm.ListValue(nil),
			vm.NewMap(),
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, deepEqualValue(tc.a, tc.b))
			assert.Equal(t, tc.want, deepEqualValue(tc.b, tc.a), "symmetric")
		})
	}
}

func TestLengthValue(t *testing.T) {
	check := func(v vm.Value, wantN int, wantOK bool) {
		n, ok := lengthValue(v)
		assert.Equal(t, wantOK, ok)
		if wantOK {
			assert.Equal(t, wantN, n)
		}
	}
	check(vm.StrValue("héllo"), 5, true) // codepoints, not bytes
	check(vm.ListValue([]vm.Value{vm.IntValue(1), vm.IntValue(2)}), 2, true)
	check(mapVal(map[string]vm.Value{"a": vm.IntValue(1)}), 1, true)
	check(vm.IntValue(3), 0, false) // a number has no length
}

func TestContainsValue(t *testing.T) {
	assert.True(t, containsValue(vm.StrValue("hello world"), vm.StrValue("world")))
	assert.False(t, containsValue(vm.StrValue("hello"), vm.StrValue("z")))
	list := vm.ListValue([]vm.Value{vm.IntValue(1), mapVal(map[string]vm.Value{"k": vm.StrValue("v")})})
	assert.True(t, containsValue(list, mapVal(map[string]vm.Value{"k": vm.StrValue("v")}))) // deep element match
	assert.False(t, containsValue(list, vm.IntValue(9)))
	m := mapVal(map[string]vm.Value{"key": vm.IntValue(1)})
	assert.True(t, containsValue(m, vm.StrValue("key")))
	assert.False(t, containsValue(m, vm.StrValue("nope")))
}

func TestCompareValue(t *testing.T) {
	check := func(a, b vm.Value, wantC int, wantOK bool) {
		c, ok := compareValue(a, b)
		assert.Equal(t, wantOK, ok)
		if wantOK {
			assert.Equal(t, wantC, c)
		}
	}
	check(vm.IntValue(5), vm.IntValue(3), 1, true)
	check(vm.IntValue(3), vm.FloatValue(3), 0, true) // cross-type
	check(vm.FloatValue(1), vm.IntValue(2), -1, true)
	check(vm.StrValue("a"), vm.StrValue("b"), -1, true)
	check(vm.IntValue(1), vm.StrValue("a"), 0, false) // not comparable
}
