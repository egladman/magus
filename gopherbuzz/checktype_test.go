package buzz

import (
	"context"
	"strings"
	"testing"
)

// TestCheckTypeSoundness verifies that OpCheckType makes typed local slots
// runtime-sound: an `any` value laundered into a typed slot is asserted at the
// bind point, so a mismatch is a clear error instead of silently corrupting a
// slot that later reads (and future type-specialized opcodes) trust. Slot-based
// locals live in function bodies (and non-shared top-level); the session
// top-level runs in SharedGlobals (Env) mode, so the assignment cases are wrapped
// in a fun to exercise the slot path the optimization targets.
func TestCheckTypeSoundness(t *testing.T) {
	ctx := context.Background()

	// A string laundered through `any` into an int slot used to evaluate to
	// "hello1" (the str's heap pointer reinterpreted as an int, then concatenated).
	// The reassignment must now assert the type and error instead.
	t.Run("laundered str into int errors", func(t *testing.T) {
		sess := newSession(ctx)
		err := sess.Exec(ctx, `fun f() int { var i = 0; var a = "hello"; var b: any = a; i = b; return i + 1; }
final __r = f();`)
		if err == nil {
			t.Fatalf("expected a type error, got none (__r=%q)", sess.GetGlobal("__r").String())
		}
		if !strings.Contains(err.Error(), "expected int, got str") {
			t.Fatalf("error = %q, want it to mention expected int, got str", err.Error())
		}
	})

	// The matching case still works: an `any` that actually holds an int passes
	// the assertion and the program runs normally.
	t.Run("matching any into int passes", func(t *testing.T) {
		sess := newSession(ctx)
		if err := sess.Exec(ctx, `fun f() int { var i = 0; var a = 41; var b: any = a; i = b; return i + 1; }
final __r = f();`); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := sess.GetGlobal("__r").AsInt(); got != 42 {
			t.Errorf("__r = %d, want 42", got)
		}
	})

	// An annotated declaration from an any source is checked at the decl in either
	// mode (the assertion is emitted before the bind, slot or Env), so this fires
	// at the session top level.
	t.Run("annotated decl from any is checked", func(t *testing.T) {
		sess := newSession(ctx)
		err := sess.Exec(ctx, `var a = "x"; var u: any = a; var n: int = u; final __r = n;`)
		if err == nil || !strings.Contains(err.Error(), "expected int") {
			t.Fatalf("expected 'expected int' error, got %v", err)
		}
	})
}

// TestCheckTypeNoFalsePositives verifies the inserted checks never fire for
// well-typed code — the common path stays untouched and correct.
func TestCheckTypeNoFalsePositives(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		src  string
		want int64
	}{
		"int loop":          {`var s = 0; var i = 0; while (i < 5) { s = s + i; i = i + 1; } final __r = s;`, 10},
		"typed int decl":    {`var x: int = 7; final __r = x + 1;`, 8},
		"int from typed fn": {`fun id(n: int) int { return n; } var x: int = id(5); final __r = x + 1;`, 6},
		"reassign int":      {`var x = 1; x = 2; x = x + 3; final __r = x;`, 5},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			sess := newSession(ctx)
			if err := sess.Exec(ctx, tc.src); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := sess.GetGlobal("__r").AsInt(); got != tc.want {
				t.Errorf("__r = %d, want %d", got, tc.want)
			}
		})
	}
}
