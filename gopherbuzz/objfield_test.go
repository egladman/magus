package buzz

import (
	"context"
	"testing"
)

// TestThisFieldAccess exercises the OpGetField/OpSetField path: reading and
// writing object fields through `this` inside method bodies, including a method
// that mutates `this` and one whose fields are declared out of access order, to
// confirm the decl-index inline cache resolves correctly.
func TestThisFieldAccess(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		src  string
		want int64
	}{
		// read this.x/this.y (the BenchmarkMethodCall shape)
		"read fields": {`object P { x: int = 0, y: int = 0,
fun dist() int { return this.x * this.x + this.y * this.y; } }
final p = P{ x = 3, y = 4 };
final __r = p.dist();`, 25},
		// write this.field, then read it back
		"write then read": {`object C { n: int = 0,
mut fun bump() int { this.n = this.n + 1; this.n = this.n + 10; return this.n; } }
final c = mut C{};
final __r = c.bump();`, 11},
		// access fields in an order different from declaration order
		"out-of-order access": {`object T { a: int = 1, b: int = 2, c: int = 3,
fun mix() int { return this.c * 100 + this.a * 10 + this.b; } }
final t = T{ a = 4, b = 5, c = 6 };
final __r = t.mix();`, 645},
		// field whose value is mutated via an external setter still reads back
		// correctly inside a method (in-place update preserves slot order)
		"external set then method read": {`object Box { v: int = 0,
fun get() int { return this.v; } }
final b = mut Box{};
b.v = 99;
final __r = b.get();`, 99},
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
