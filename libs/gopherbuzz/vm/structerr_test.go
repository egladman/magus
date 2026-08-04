package vm

import (
	"errors"
	"fmt"
	"testing"
)

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
	t.Run("a plain error stays a string", func(t *testing.T) {
		v := caughtValue(errors.New("boom"))
		if v.tag() == tagMap {
			t.Fatal("a plain error must not become a map; every existing catch expects text")
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
		if v.String() != "nothing here" {
			t.Fatalf("got %q", v.String())
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
