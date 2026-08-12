package std

import (
	"context"
	"fmt"
	"testing"
	"time"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestDeepEqualCircular is the regression for deepEqualValue recursing into a
// genuine reference cycle: Buzz lists are heap objects mutable in place
// (list.append), so `[any] l = mut []; l.append(l);` makes l[0] == l. Naive
// recursion would stack-overflow, a FATAL Go error unsafe to reproduce via
// recover, so this drives deepEqualValue on a goroutine and bounds it with
// select + time.After (the repo's hang/crash-test idiom; see pool_test.go
// TestDispatchRejectsCycleWithMemo) rather than calling it inline.
func TestDeepEqualCircular(t *testing.T) {
	items := make([]vm.Value, 1)
	l := vm.ListValue(items)
	items[0] = l // l[0] == l: the same cycle list.append(l) would create

	done := make(chan bool, 1)
	go func() { done <- deepEqualValue(l, l) }()

	select {
	case eq := <-done:
		assert.True(t, eq, "a cyclic list must equal itself instead of stack-overflowing")
	case <-time.After(5 * time.Second):
		t.Fatal("deepEqualValue did not return within bound on a cyclic list")
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

// TestTesterModule drives the ported `testing` module (gopherbuzz's rendition of
// upstream Buzz's Tester) end to end: static init, lifecycle hooks via the
// optional-call-narrowing `->`, `it`, and every assertion — including the three
// that upstream expresses with generics (assertOfType / assertThrows /
// assertDoesNotThrow), adapted here since gopherbuzz erases generics. summary()
// is not called (it would os.exit); the pass/fail tallies are read directly.
func TestTesterModule(t *testing.T) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	Register(sess)

	src := `
import "testing";

var hooks = mut [<str>];
final t = testing\Tester.init(
    fun (t: testing\Tester) { hooks.append("beforeAll"); },
    fun (t: testing\Tester) { hooks.append("beforeEach"); },
    fun (t: testing\Tester) { hooks.append("afterAll"); },
    fun (t: testing\Tester) { hooks.append("afterEach"); },
);

t.it("passing", fun () {
    t.assertEqual(2 + 2, 4, "");
    t.assertNotEqual(1, 2, "");
    t.assertOfType(42, "int", "");
    t.assertOfType("hi", "str", "");
    t.assertAreEqual([3, 3, 3], "");
    t.assertThrows(fun () > void { throw "boom"; }, "");
    t.assertDoesNotThrow(fun () > void {}, "");
});

t.it("failing", fun () {
    t.assertEqual(1, 2, "");
});

var passed = t.succeededTests();
var failed = t.failedTests();
var hookTrail = hooks.join(",");
`
	require.NoError(t, sess.Exec(ctx, src), "exec Tester program")
	assert.Equal(t, "1", sess.GetGlobal("passed").String(), "one test should pass")
	assert.Equal(t, "1", sess.GetGlobal("failed").String(), "one test should fail")
	assert.Equal(t,
		"beforeAll,beforeEach,afterEach,beforeEach,afterEach",
		sess.GetGlobal("hookTrail").String(),
		"lifecycle hooks fire around each it via -> narrowing",
	)
}
