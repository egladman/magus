package std

import (
	"bytes"
	"context"
	"testing"
	"time"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cyclicList builds a single-element Buzz list containing itself (l[0] == l),
// the same reference cycle `[any] l = mut []; l.append(l);` produces at the
// language level: Buzz lists are heap objects mutable in place, and
// vm.ListValue stores the backing slice as-is (no copy), so mutating it after
// construction is a legitimate way to build the fixture in Go.
func cyclicList() vm.Value {
	items := make([]vm.Value, 1)
	l := vm.ListValue(items)
	items[0] = l
	return l
}

// TestEncodeJSONCircular is the regression for encodeJSON recursing into a
// genuine reference cycle: naive recursion would stack-overflow, a FATAL,
// unrecoverable Go error. encodeJSON must instead report errCircularReference.
// Run on a goroutine and bounded with select + time.After (the repo's
// hang/crash-test idiom; see pool_test.go TestDispatchRejectsCycleWithMemo)
// since a regression here is unsafe to call inline.
func TestEncodeJSONCircular(t *testing.T) {
	l := cyclicList()

	type result struct {
		err error
	}
	done := make(chan result, 1)
	go func() {
		var buf bytes.Buffer
		done <- result{encodeJSON(l, &buf, nil)}
	}()

	select {
	case r := <-done:
		require.Error(t, r.err, "want a circular-reference error")
		assert.ErrorIs(t, r.err, errCircularReference)
	case <-time.After(5 * time.Second):
		t.Fatal("encodeJSON did not return within bound on a cyclic list")
	}
}

// TestBuzzToGoCircular is buzzToGo's twin of TestEncodeJSONCircular.
func TestBuzzToGoCircular(t *testing.T) {
	l := cyclicList()

	type result struct {
		err error
	}
	done := make(chan result, 1)
	go func() {
		_, err := buzzToGo(l, nil)
		done <- result{err}
	}()

	select {
	case r := <-done:
		require.Error(t, r.err, "want a circular-reference error")
		assert.ErrorIs(t, r.err, errCircularReference)
	case <-time.After(5 * time.Second):
		t.Fatal("buzzToGo did not return within bound on a cyclic list")
	}
}

// TestSerializeSerializeCircular exercises serializeSerialize's own cycle
// check (checkCircular), the ergonomic place upstream promises to catch this
// before jsonEncode ever runs (see serializeSerialize's doc comment).
func TestSerializeSerializeCircular(t *testing.T) {
	l := cyclicList()

	done := make(chan error, 1)
	go func() {
		_, err := serializeSerialize(t.Context(), []vm.Value{l})
		done <- err
	}()

	select {
	case err := <-done:
		require.Error(t, err, "want a circular-reference error")
		assert.ErrorIs(t, err, errCircularReference)
	case <-time.After(5 * time.Second):
		t.Fatal("serializeSerialize did not return within bound on a cyclic list")
	}
}

// JSONEncode is what an embedder returns a script's value through, so a Boxed
// value (serialize.jsonDecode's result) must encode as its data at any depth,
// not as the method values a Boxed map also carries.
func TestJSONEncodeUnwrapsBoxedAtAnyDepth(t *testing.T) {
	boxed, err := serializeJSONDecode(t.Context(), []vm.Value{vm.StrValue(`{"n":2}`)})
	require.NoError(t, err)

	top, err := JSONEncode(boxed)
	require.NoError(t, err)
	assert.JSONEq(t, `{"n":2}`, top)

	nested, err := JSONEncode(vm.ListValue([]vm.Value{boxed}))
	require.NoError(t, err)
	assert.JSONEq(t, `[{"n":2}]`, nested)
}

func TestJSONDecodeValueKeepsIntegralNumbersInts(t *testing.T) {
	v, err := JSONDecodeValue([]byte(`{"n":2,"f":1.5}`))
	require.NoError(t, err)
	n, ok := v.MapGet("n")
	require.True(t, ok)
	assert.True(t, n.IsInt(), "2 decodes as %s", n.Kind())
	f, ok := v.MapGet("f")
	require.True(t, ok)
	assert.True(t, f.IsFloat())
	_, boxed := v.MapGet(boxedRawKey)
	assert.False(t, boxed, "the value is plain, not Boxed")
}

func TestSession_SerializeBoxedIsDeclared(t *testing.T) {
	ctx := context.Background()
	exec := func(src string) (*buzz.Session, error) {
		s := buzz.NewSession(ctx, buzz.WithEmbedded())
		Register(s)
		return s, s.Exec(ctx, src)
	}
	s, err := exec(`import "serialize";
fun amount(b: serialize\Boxed) > int { return b.q(["a"]).integerValue() + b.q("a").integerValue(); }
final n = amount(serialize\jsonDecode("\{\"a\": 3}"));
final m = amount(serialize\Boxed.init({"a": 4}));`)
	require.NoError(t, err)
	assert.Equal(t, int64(6), s.Globals()["n"].AsInt())
	assert.Equal(t, int64(8), s.Globals()["m"].AsInt())

	_, err = exec(`import "serialize"; fun amount(b: serialize\Boxed) > int { return b.integerValue(); } final _n = amount(42);`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `cannot pass int as argument "b" of type Boxed`)
}
