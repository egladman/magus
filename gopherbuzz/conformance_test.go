package buzz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Conformance suite for advanced language semantics — fibers, closures/upvalues,
// scope, and recursion — the bug-prone areas surfaced while writing the
// bubblegum example. Every expected value here was differentially verified
// against the upstream Buzz interpreter (zig build of buzz-language/buzz, run via
// `buzz run-script` / `buzz test`): a program was run on BOTH runtimes and the
// outputs compared. A divergence is therefore a real conformance defect, not a
// guess at intended semantics.
//
// Programs are driven through globals (resume/resolve results stored in finals)
// and asserted with GetGlobal, mirroring TestFiberBasic, so no main()/auto-main
// or stdout capture is involved.
//
// Reproduce the differential check (with upstream built at ~/.local/bin/buzz):
//
//	{ echo 'import "buzz:std";'; cat body.buzz; } | buzz run-script /dev/stdin
//	{ echo 'import "std";';      cat body.buzz; } | go run ./cmd/buzz /dev/stdin

// confRejects asserts that src is REJECTED (lenient syntax upstream forbids must
// not slip through gopherbuzz's checker) and the error mentions wantSubstr. This
// is the strict-parity direction: every leniency removed from the parser/checker
// gets a test here proving the lenient form now fails. Each snippet was confirmed
// to error on the upstream Buzz interpreter too.
func confRejects(t *testing.T, src, wantSubstr string) {
	t.Helper()
	sess := newSession(context.Background())
	defer func() { _ = sess.Close() }()
	err := sess.Exec(context.Background(), src)
	require.Error(t, err, "expected lenient program to be rejected")
	assert.Contains(t, err.Error(), wantSubstr)
}

// TestConformance_StrictParityRejections pins the leniencies gopherbuzz no longer
// accepts, matching upstream Buzz. Each case is valid upstream-Buzz-rejected and
// gopherbuzz-rejected.
func TestConformance_StrictParityRejections(t *testing.T) {
	t.Run("non-optional fiber yield type", func(t *testing.T) {
		confRejects(t, `fun g() > void *> int { _ = yield 1; } final f = &g();`,
			"expected optional type or void")
	})
	t.Run("untyped parameter", func(t *testing.T) {
		confRejects(t, `fun f(n) > int { return n; } final r = f(1);`,
			"must have a type annotation")
	})
	t.Run("untyped lambda parameter", func(t *testing.T) {
		confRejects(t, `final g = fun(x) > int { return x; }; final r = g(1);`,
			"must have a type annotation")
	})
	t.Run("return type without >", func(t *testing.T) {
		confRejects(t, `fun f() int { return 1; } final r = f();`,
			"return type must be preceded by '>'")
	})
	t.Run("bare void return type without >", func(t *testing.T) {
		confRejects(t, `fun f() void { } f();`,
			"return type must be preceded by '>'")
	})
	t.Run("function-type return without >", func(t *testing.T) {
		confRejects(t, `fun mk() fun(int) > int { return fun(x: int) > int { return x; }; } final r = mk();`,
			"return type must be preceded by '>'")
	})
	t.Run("reserved word as variable name", func(t *testing.T) {
		confRejects(t, `final out = 1; final r = out;`,
			"reserved word")
	})
	t.Run("reserved word as parameter name", func(t *testing.T) {
		confRejects(t, `fun f(type: int) > int { return type; } final r = f(1);`,
			"reserved word")
	})
	t.Run("reserved word as function name", func(t *testing.T) {
		confRejects(t, `fun match() > int { return 1; } final r = match();`,
			"reserved word")
	})
}

// confStrictRejects asserts src parses fine in the embedding-lenient mode but is
// rejected by strict Parse (the default script-conformance mode), and the strict error
// mentions wantSubstr. These are the leniencies that cannot be removed from the
// default path without breaking the REPL/eval/magusfile embedding, so they are
// enforced only in strict (portable-script) mode.
func confStrictRejects(t *testing.T, src, wantSubstr string) {
	t.Helper()
	if _, err := ParseEmbedded(src); err != nil {
		t.Fatalf("expected lenient ParseEmbedded to accept, got: %v", err)
	}
	_, err := Parse(src)
	require.Error(t, err, "expected strict Parse to reject")
	assert.Contains(t, err.Error(), wantSubstr)
}

// TestConformance_StrictModeRejections pins the leniencies enforced only in
// strict Parse (upstream script conformance): no top-level control flow, and
// labeled call arguments. Each is accepted by the lenient embedding parser.
func TestConformance_StrictModeRejections(t *testing.T) {
	t.Run("top-level if", func(t *testing.T) {
		confStrictRejects(t, `if (true) { final a = 1; }`, "not allowed at the top level")
	})
	t.Run("top-level foreach", func(t *testing.T) {
		confStrictRejects(t, `foreach (i in 0..3) { final a = i; }`, "not allowed at the top level")
	})
	t.Run("top-level while", func(t *testing.T) {
		confStrictRejects(t, `var i = 0; while (i < 3) { i = i + 1; }`, "not allowed at the top level")
	})
	t.Run("top-level return", func(t *testing.T) {
		confStrictRejects(t, `return;`, "not allowed at the top level")
	})
	t.Run("unlabeled second argument (literal)", func(t *testing.T) {
		confStrictRejects(t, `fun f(a: int, b: int) > int { return a + b; } final r = f(1, 2);`,
			"must be labeled")
	})
	t.Run("top-level call is still allowed", func(t *testing.T) {
		if _, err := Parse(`fun f() > void {} f();`); err != nil {
			t.Fatalf("strict mode should allow top-level calls, got: %v", err)
		}
	})
	t.Run("bare-identifier arg is still allowed (implicit label)", func(t *testing.T) {
		if _, err := Parse(`fun f(a: int, b: int) > int { return a + b; } final b = 5; final r = f(1, b);`); err != nil {
			t.Fatalf("strict mode should allow bare-identifier args, got: %v", err)
		}
	})
}

func conf(t *testing.T, src string) *Session {
	t.Helper()
	sess := newSession(context.Background())
	t.Cleanup(func() { _ = sess.Close() })
	require.NoError(t, sess.Exec(context.Background(), src))
	return sess
}

// --- Fibers -----------------------------------------------------------------

// A resumed `yield X` expression evaluates to the value it yielded (upstream:
// `final a = yield 7;` binds a == 7). Regression test for the OpYield fix that
// previously pushed Null as the expression result.
func TestConformance_YieldExpressionValue(t *testing.T) {
	s := conf(t, `
fun g() > int *> int? { final a = yield 7; return a; }
final f = &g();
final y = resume f;    // 7 (the yielded value)
final done = resume f; // null (fiber completed)
final r = resolve f;   // 7 (return value == a, proving the yield expr was 7)
`)
	assert.Equal(t, int64(7), s.GetGlobal("y").AsInt(), "resume returns yielded value")
	assert.True(t, s.GetGlobal("done").IsNull(), "resume after completion is null")
	assert.Equal(t, int64(7), s.GetGlobal("r").AsInt(), "yield expression evaluated to the yielded value")
}

// foreach over &fn() drives the fiber, binding each yielded value in turn.
func TestConformance_ForeachOverFiber(t *testing.T) {
	s := conf(t, `
fun squares(n: int) > void *> int? { foreach (i in 1..n) { _ = yield (i * i); } }
var sum = 0;
foreach (v in &squares(5)) { sum = sum + v; }
final total = sum; // 1+4+9+16 = 30
`)
	assert.Equal(t, int64(30), s.GetGlobal("total").AsInt())
}

// Two instances of the same fiber function keep independent local state.
func TestConformance_FiberInstancesIndependent(t *testing.T) {
	s := conf(t, `
fun ticker() > int *> int? { var i = 0; while (true) { _ = yield i; i = i + 1; } return 0; }
final a = &ticker();
final b = &ticker();
final a0 = resume a; final a1 = resume a; // 0, 1
final b0 = resume b;                        // 0 (independent)
final a2 = resume a;                        // 2
`)
	assert.Equal(t, int64(0), s.GetGlobal("a0").AsInt())
	assert.Equal(t, int64(1), s.GetGlobal("a1").AsInt())
	assert.Equal(t, int64(0), s.GetGlobal("b0").AsInt(), "second fiber instance has its own state")
	assert.Equal(t, int64(2), s.GetGlobal("a2").AsInt())
}

// A fiber may drive another fiber (nested resume).
func TestConformance_NestedFibers(t *testing.T) {
	s := conf(t, `
fun inner() > int *> int? { _ = yield 10; _ = yield 20; return 0; }
fun outer() > int *> int? {
    final g = &inner();
    _ = yield (resume g) ?? -1;
    _ = yield (resume g) ?? -1;
    return 0;
}
final f = &outer();
final x = resume f; // 10
final y = resume f; // 20
`)
	assert.Equal(t, int64(10), s.GetGlobal("x").AsInt())
	assert.Equal(t, int64(20), s.GetGlobal("y").AsInt())
}

// Ordinary recursion inside a fiber body works across yields.
func TestConformance_RecursionInFiber(t *testing.T) {
	s := conf(t, `
fun fact(n: int) > int { if (n <= 1) { return 1; } return n * fact(n - 1); }
fun gen() > int *> int? { _ = yield fact(5); _ = yield fact(6); return 0; }
final f = &gen();
final a = resume f; // 120
final b = resume f; // 720
`)
	assert.Equal(t, int64(120), s.GetGlobal("a").AsInt())
	assert.Equal(t, int64(720), s.GetGlobal("b").AsInt())
}

// --- Closures / upvalues ----------------------------------------------------

// A closure captures and mutates an upvalue; calls share the same cell.
func TestConformance_ClosureCounter(t *testing.T) {
	s := conf(t, `
fun makeCounter() > fun () > int {
    var n = 0;
    return fun () > int { n = n + 1; return n; };
}
final c = makeCounter();
final a = c(); final b = c(); final d = c(); // 1, 2, 3
`)
	assert.Equal(t, int64(1), s.GetGlobal("a").AsInt())
	assert.Equal(t, int64(2), s.GetGlobal("b").AsInt())
	assert.Equal(t, int64(3), s.GetGlobal("d").AsInt())
}

// Each loop iteration's closure captures the iteration's value (inside a fn).
func TestConformance_LoopVarCaptureInFunction(t *testing.T) {
	s := conf(t, `
fun build() > int {
    var sum = 0;
    foreach (i in 0..3) {
        final f = fun () > int { return i; };
        sum = sum + f();
    }
    return sum; // 0+1+2 = 3
}
final total = build();
`)
	assert.Equal(t, int64(3), s.GetGlobal("total").AsInt())
}

// Nested closures resolve upvalues through multiple enclosing scopes.
func TestConformance_NestedClosureUpvalues(t *testing.T) {
	s := conf(t, `
fun outer(x: int) > fun (y: int) > int {
    return fun (y: int) > int {
        final inner = fun (z: int) > int { return x + y + z; };
        return inner(100);
    };
}
final r = outer(1)(20); // 1 + 20 + 100 = 121
`)
	assert.Equal(t, int64(121), s.GetGlobal("r").AsInt())
}

// --- Scope: global vs local -------------------------------------------------

// A local shadows a global; mutating the local leaves the global untouched.
func TestConformance_LocalShadowsGlobal(t *testing.T) {
	s := conf(t, `
var g = 100;
fun f() > int { var g = 5; g = g + 1; return g; }
final local = f(); // 6
final global = g;  // 100 (unchanged)
`)
	assert.Equal(t, int64(6), s.GetGlobal("local").AsInt())
	assert.Equal(t, int64(100), s.GetGlobal("global").AsInt())
}

// A function with no shadowing mutates the global directly.
func TestConformance_GlobalMutationFromFunction(t *testing.T) {
	s := conf(t, `
var counter = 0;
fun bump() > void { counter = counter + 1; }
bump(); bump(); bump();
final result = counter; // 3
`)
	assert.Equal(t, int64(3), s.GetGlobal("result").AsInt())
}

// --- Recursion --------------------------------------------------------------

func TestConformance_Recursion(t *testing.T) {
	s := conf(t, `
fun fact(n: int) > int { if (n <= 1) { return 1; } return n * fact(n - 1); }
fun isEven(n: int) > bool { if (n == 0) { return true; } return isOdd(n - 1); }
fun isOdd(n: int) > bool { if (n == 0) { return false; } return isEven(n - 1); }
final f = fact(6);    // 720
final e = isEven(10); // true
final o = isOdd(7);   // true
`)
	assert.Equal(t, int64(720), s.GetGlobal("f").AsInt())
	assert.True(t, s.GetGlobal("e").AsBool())
	assert.True(t, s.GetGlobal("o").AsBool())
}

// --- Known conformance gaps (differentially confirmed; tracked, not fixed) --
//
// These are documented divergences from upstream Buzz, each confirmed by running
// the same source on both runtimes. They are skipped rather than deleted so the
// expected (upstream) behavior is recorded and the tests start passing once the
// underlying defect is fixed.

// GAP: yield operand precedence. Upstream parses `yield a + b` as `(yield a) + b`
// (yield is a .Primary-precedence prefix); gopherbuzz consumes the whole
// expression as `yield (a + b)`. Fixing the parser is blocked on the mid-
// expression resume gap below, so it is held to preserve the superset invariant.
func TestConformance_GAP_YieldPrecedence(t *testing.T) {
	t.Skip("known gap: `yield a + b` parses as yield(a+b); upstream is (yield a)+b. See parser.go yield note.")
	s := conf(t, `
fun g() > void *> int? { _ = yield 2 + 10; }
final f = &g();
final y = resume f; // upstream: 2  (gopherbuzz currently: 12)
`)
	assert.Equal(t, int64(2), s.GetGlobal("y").AsInt())
}

// GAP: resuming a fiber whose `yield` sits mid-expression. Upstream evaluates
// `(yield 5) + 3` to 8 after resume; gopherbuzz yields null for the resumed
// sub-expression and errors ("null in arithmetic"). Blocks the precedence fix.
func TestConformance_GAP_YieldMidExpressionResume(t *testing.T) {
	t.Skip("known gap: resuming a fiber with pending stack ops after the yield loses the resumed value")
	s := conf(t, `
fun g() > int *> int? { final r = (yield 5) + 3; return r; }
final f = &g();
final y = resume f;  // 5
final _c = resume f; // completes
final r = resolve f; // upstream: 8
`)
	assert.Equal(t, int64(5), s.GetGlobal("y").AsInt())
	assert.Equal(t, int64(8), s.GetGlobal("r").AsInt())
}

// GAP: `=>` expression-body (arrow) functions/lambdas. Upstream supports
// `fun (n: int) > int => n * 2`; gopherbuzz has no `=>` token and requires a
// `{ return ...; }` block body.
func TestConformance_GAP_ArrowLambda(t *testing.T) {
	t.Skip("known gap: gopherbuzz lacks `=>` expression-body lambdas; use a block body")
	s := conf(t, `final double = fun (n: int) > int => n * 2; final r = double(21);`)
	assert.Equal(t, int64(42), s.GetGlobal("r").AsInt())
}
