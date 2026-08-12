package std

import (
	"bytes"
	"testing"
	"time"

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
