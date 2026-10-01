package vm

import (
	"context"
	"errors"
	"fmt"
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

// structured is a host error that opts into the map form.
type structured struct{ code, msg, url string }

func (e structured) Error() string { return fmt.Sprintf("[%s] %s", e.code, e.msg) }
func (e structured) BuzzError() map[string]string {
	return map[string]string{"code": e.code, "message": e.msg, "url": e.url}
}

// emptyFields opts in but supplies nothing, which must degrade to the string form rather
// than handing a magusfile an empty map to index into.
type emptyFields struct{}

func (emptyFields) Error() string                { return "nothing here" }
func (emptyFields) BuzzError() map[string]string { return nil }

// noMessage supplies fields but omits "message"; the raiser fills it so a caught value is
// always printable.
type noMessage struct{}

func (noMessage) Error() string                { return "rendered form" }
func (noMessage) BuzzError() map[string]string { return map[string]string{"code": "MGS9999"} }

func TestCaughtValueRendersStructuredErrors(t *testing.T) {
	t.Run("a plain error stays a string, per upstream", func(t *testing.T) {
		// Upstream Buzz surfaces a host failure as a str, and this package's conformance
		// fixtures pin that. Enriching is the EMBEDDER's opt-in, not a VM default.
		v := caughtValue(errors.New("boom"))
		if v.tag() == tagMap {
			t.Fatal("a plain error must stay a string; changing that diverges from upstream")
		}
		if v.String() != "boom" {
			t.Fatalf("got %q", v.String())
		}
	})

	t.Run("a structured error becomes an indexable map", func(t *testing.T) {
		v := caughtValue(structured{code: "MGS2001", msg: "denied", url: "https://x/MGS2001"})
		if v.tag() != tagMap {
			t.Fatal("a structured error must reach catch as a map, not a sentence to parse")
		}
		for key, want := range map[string]string{
			"code": "MGS2001", "message": "denied", "url": "https://x/MGS2001",
		} {
			got, ok := v.MapGet(key)
			if !ok || got.String() != want {
				t.Fatalf("%s: got %q present=%v, want %q", key, got.String(), ok, want)
			}
		}
	})

	t.Run("it is found through a wrap", func(t *testing.T) {
		// Host errors are routinely wrapped on the way out; the structure must survive.
		v := caughtValue(fmt.Errorf("while doing the thing: %w", structured{code: "MGS1", msg: "m"}))
		if v.tag() != tagMap {
			t.Fatal("errors.As must reach a wrapped structured error")
		}
	})

	t.Run("empty fields degrade to the string form", func(t *testing.T) {
		v := caughtValue(emptyFields{})
		if v.tag() == tagMap {
			t.Fatal("an empty map would be worse than the string it replaced")
		}
	})

	t.Run("a missing message is filled from Error()", func(t *testing.T) {
		v := caughtValue(noMessage{})
		got, ok := v.MapGet("message")
		if !ok || got.String() != "rendered form" {
			t.Fatalf("message: got %q present=%v", got.String(), ok)
		}
	})
}

// invokeChunk builds `recv.name(1)` with the call on line 3 of probe.buzz.
func invokeChunk(recv Value, name string, lines bool) *Chunk {
	c := &Chunk{SourceFile: "probe.buzz"}
	if lines {
		c.Lines = []int32{}
	}
	c.CurLine = 1
	c.Emit(OpLoadConst, c.AddConst(recv), 0)
	c.CurLine = 3
	c.Emit(OpLoadConst, c.AddConst(IntValue(1)), 0)
	c.Emit(OpInvoke, c.AddConst(StrValue(name)), 1)
	c.Emit(OpReturn, 0, 0)
	return c
}

// TestUnknownMethodCarriesItsLine covers the call the checker cannot see: an
// untyped receiver whose member does not exist reached the caller as a bare
// "null is not callable".
func TestUnknownMethodCarriesItsLine(t *testing.T) {
	run := func(c *Chunk) error {
		_, err := NewVM(context.Background()).Run(c, NewEnv())
		return err
	}
	list := ListValue([]Value{IntValue(1)})

	assert.EqualError(t, run(invokeChunk(list, "push", true)), "buzz: probe.buzz:3: unknown method push on list")
	assert.EqualError(t, run(invokeChunk(StrValue("abc"), "toUpperCase", true)), "buzz: probe.buzz:3: unknown method toUpperCase on str")

	noFile := invokeChunk(list, "push", true)
	noFile.SourceFile = ""
	assert.EqualError(t, run(noFile), "buzz: line 3: unknown method push on list")
	assert.EqualError(t, run(invokeChunk(list, "push", false)), "buzz: unknown method push on list")

	// A map that STORES the name holds a value, so calling it is not a missing method.
	m := NewMap()
	m.MapSet("cb", Null)
	assert.EqualError(t, run(invokeChunk(m, "cb", true)), "buzz: null is not callable")
	assert.EqualError(t, run(invokeChunk(NewMap(), "cb", true)), "buzz: probe.buzz:3: unknown method cb on map")
}
