package buzz

import (
	"context"
	"testing"

	vmpackage "github.com/egladman/gopherbuzz/vm"
)

var _benchCtx = context.Background()

// benchSession creates a session and defines src once; returns a precompiled
// chunk for the "hot" portion and the session's env so globals are available.
func benchSetup(b *testing.B, init, hot string) (*Chunk, *vmpackage.Env) {
	b.Helper()
	sess := newSession(_benchCtx)
	if init != "" {
		if err := sess.Exec(_benchCtx, init); err != nil {
			b.Fatalf("bench setup: %v", err)
		}
	}
	prog, err := ParseEmbedded(hot)
	if err != nil {
		b.Fatalf("bench parse: %v", err)
	}
	chunk, err := CompileWith(prog, CompileOptions{})
	if err != nil {
		b.Fatalf("bench compile: %v", err)
	}
	return chunk, sess.env
}

// BenchmarkFib measures recursive fibonacci(30) — call/return overhead, int
// arithmetic, and conditional branching.
func BenchmarkFib(b *testing.B) {
	chunk, env := benchSetup(b,
		`fun fib(n: int) > int {
    if (n <= 1) { return n; }
    return fib(n - 1) + fib(n - 2);
}`,
		`final __r = fib(30);`,
	)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLoopSum measures a tight while-loop summing 1 000 000 ints —
// local variables, integer arithmetic, backward jumps, context-cancel poll.
func BenchmarkLoopSum(b *testing.B) {
	chunk, env := benchSetup(b, "", `
var sum = 0;
var i = 0;
while (i < 1000000) {
    sum = sum + i;
    i = i + 1;
}
`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLoopSumFloat is BenchmarkLoopSum with double operands — it exercises
// the JIT's SSE float fast path (and the interpreter's float arithmetic when the
// JIT is disabled).
func BenchmarkLoopSumFloat(b *testing.B) {
	chunk, env := benchSetup(b, "", `
var sum = 0.0;
var i = 0.0;
while (i < 1000000.0) {
    sum = sum + i;
    i = i + 1.0;
}
`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLoopSumShared measures the same tight while-loop as BenchmarkLoopSum
// but compiled in SharedGlobals mode — the Env-based top-level path magus uses
// for magusfiles. Here sum/i are runtime Env bindings accessed via
// OpLoadName/OpStoreName (with the VM name cache), not stack slots, so this is
// the benchmark that exercises the Env load/store hot path.
func BenchmarkLoopSumShared(b *testing.B) {
	sess := newSession(_benchCtx)
	prog, err := ParseEmbedded(`
var sum = 0;
var i = 0;
while (i < 1000000) {
    sum = sum + i;
    i = i + 1;
}
`)
	if err != nil {
		b.Fatalf("bench parse: %v", err)
	}
	chunk, err := CompileWith(prog, CompileOptions{SharedGlobals: true})
	if err != nil {
		b.Fatalf("bench compile: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, sess.env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLoopSumPromoted is BenchmarkLoopSumShared compiled with PromoteTopLevel:
// sum/i are chunk-private (never captured, never exported), so they slot-promote
// even though the chunk runs against the session Env. This is the win the
// magusfile entrypoint path unlocks — it should approach the slot-based
// BenchmarkLoopSum rather than the Env-bound BenchmarkLoopSumShared.
func BenchmarkLoopSumPromoted(b *testing.B) {
	sess := newSession(_benchCtx)
	prog, err := ParseEmbedded(`
var sum = 0;
var i = 0;
while (i < 1000000) {
    sum = sum + i;
    i = i + 1;
}
`)
	if err != nil {
		b.Fatalf("bench parse: %v", err)
	}
	chunk, err := CompileWith(prog, CompileOptions{SharedGlobals: true, PromoteTopLevel: true})
	if err != nil {
		b.Fatalf("bench compile: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, sess.env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLoopEq measures a tight while-loop whose body is dominated by
// integer equality tests (OpEqual), counting how many i in [0,1e6) are even via
// i % 2 == 0. Gates the OpEqual/OpNotEqual int fast paths.
func BenchmarkLoopEq(b *testing.B) {
	chunk, env := benchSetup(b, "", `
var count = 0;
var i = 0;
while (i < 1000000) {
    if (i % 2 == 0) { count = count + 1; }
    i = i + 1;
}
`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkForeachList measures list iteration and element access.
func BenchmarkForeachList(b *testing.B) {
	chunk, env := benchSetup(b,
		`var items = mut []; var k = 0; while (k < 1000) { items.append(k); k = k + 1; }`,
		`var sum = 0;
foreach (x in items) { sum = sum + x; }`,
	)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkForeachMap measures map iteration (insertion-ordered keys).
func BenchmarkForeachMap(b *testing.B) {
	chunk, env := benchSetup(b,
		`final m = {"a": 1, "b": 2, "c": 3, "d": 4, "e": 5,
                    "f": 6, "g": 7, "h": 8, "i": 9, "j": 10};`,
		`var sum = 0;
foreach (k, v in m) { sum = sum + v; }`,
	)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStringInterp measures string interpolation in a loop.
func BenchmarkStringInterp(b *testing.B) {
	chunk, env := benchSetup(b, "", `
var s = "";
var i = 0;
while (i < 100) {
    s = "item {i} of 100";
    i = i + 1;
}
`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStringInterpPromoted is BenchmarkStringInterp compiled with
// PromoteTopLevel: s/i slot-promote, isolating how much of the interpolation-loop
// cost was the top-level Env access path versus the string building itself.
func BenchmarkStringInterpPromoted(b *testing.B) {
	sess := newSession(_benchCtx)
	prog, err := ParseEmbedded(`
var s = "";
var i = 0;
while (i < 100) {
    s = "item {i} of 100";
    i = i + 1;
}
`)
	if err != nil {
		b.Fatalf("bench parse: %v", err)
	}
	chunk, err := CompileWith(prog, CompileOptions{SharedGlobals: true, PromoteTopLevel: true})
	if err != nil {
		b.Fatalf("bench compile: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, sess.env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkParse measures lexer + parser throughput on a realistic magusfile.
func BenchmarkParse(b *testing.B) {
	src := `
import "host";
fun helper(x: int) > int {
    return x * x + 1;
}
object Config {
    name: str = "default",
    count: int = 0,
    fun describe() > str {
        return "Config({this.name}, {this.count})";
    }
}
enum Status { Ok, Err, Unknown }
host.project.register(".");
export fun build(_args: [str]) > void {}
export fun test(_args: [str]) > void {}
`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ParseEmbedded(src); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCompile measures parse + compile throughput.
func BenchmarkCompile(b *testing.B) {
	src := `
import "host";
fun helper(x: int) > int { return x * x + 1; }
object Config {
    name: str = "default",
    count: int = 0,
}
enum Status { Ok, Err, Unknown }
host.project.register(".");
export fun build(_args: [str]) > void {}
`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		prog, err := ParseEmbedded(src)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := CompileWith(prog, CompileOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCall measures the overhead of calling a simple Buzz function
// repeatedly — frame allocation, parameter binding, and return.
func BenchmarkCall(b *testing.B) {
	chunk, env := benchSetup(b,
		`fun add(a: int, b: int) > int { return a + b; }`,
		`final __r = add(1, 2);`,
	)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkMethodCall measures a tight loop calling an object method 100 000
// times. Each call exercises: OpGetMember method binding (copies the funObj +
// sets This), the OpCall this-env path (newEnv + define("this")), and
// OpLoadName "this" inside the method body. This is the primary benchmark for
// the method-call deallocation work (plan items 2.1/2.2).
func BenchmarkMethodCall(b *testing.B) {
	chunk, env := benchSetup(b,
		`object Point {
    x: int = 0,
    y: int = 0,
    fun dist() > int {
        return this.x * this.x + this.y * this.y;
    }
}
final p = Point{ x = 3, y = 4 };`,
		`var sum = 0;
var i = 0;
while (i < 100000) {
    sum = sum + p.dist();
    i = i + 1;
}`,
	)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFieldAccess measures a tight loop reading and writing a single
// object field 1 000 000 times. Exercises OpGetMember and OpSetMember →
// mapObj.get/set, which is the primary benchmark for the small-map fast path
// (plan item 1).
func BenchmarkFieldAccess(b *testing.B) {
	chunk, env := benchSetup(b,
		`object Counter {
    n: int = 0,
}
final c = mut Counter{};`,
		`var i = 0;
while (i < 1000000) {
    c.n = c.n + 1;
    i = i + 1;
}`,
	)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFieldAccessLocal measures a tight loop reading and writing a single
// object field on a LOCAL variable (not a global) 1 000 000 times. Unlike
// BenchmarkFieldAccess (which uses a global loaded via OpLoadName+mcache),
// here `c` is a slot-local inside a fun body, so A3 (slotObjFields) kicks in:
// the compiler emits OpGetField/OpSetField instead of OpGetMember/OpSetMember.
// Counter must be in the same compilation unit so it lands in typeDecls before
// the function body is compiled.
func BenchmarkFieldAccessLocal(b *testing.B) {
	chunk, env := benchSetup(b,
		"",
		`object Counter {
    n: int = 0,
}
fun run() {
    var c = mut Counter{};
    var i = 0;
    while (i < 1000000) {
        c.n = c.n + 1;
        i = i + 1;
    }
}
run();`,
	)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDirectCall measures a tight loop calling a direct (Go) callable
// 1 000 000 times. Each call exercises the OpCall tagDirect path, which does
// args := make([]Value, argCount); copy(...) — the target for plan item 4
// (drop direct-call per-call allocation). A direct callable is injected from Go
// (the old `len`/`range` globals were moved into the std module), so the bench no
// longer depends on `import "std"`.
func BenchmarkDirectCall(b *testing.B) {
	sess := newSession(_benchCtx)
	sess.SetGlobal("nat", DirectValue("nat", func(_ context.Context, args []Value) (Value, error) {
		return IntValue(int64(len(args))), nil
	}))
	prog, err := ParseEmbedded(`var sum = 0;
var i = 0;
while (i < 1000000) {
    sum = sum + nat(i);
    i = i + 1;
}`)
	if err != nil {
		b.Fatalf("bench parse: %v", err)
	}
	chunk, err := CompileWith(prog, CompileOptions{})
	if err != nil {
		b.Fatalf("bench compile: %v", err)
	}
	env := sess.env
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkThreeReg measures a tight loop where each iteration performs a
// true 3-address operation (dst ≠ src1 ≠ src2): `c = a + b` with all three
// variables being distinct stack slots. Before A2, the compiler emitted
// OpBinLL (3-instr fusion, C=0) + OpSetLocal — two dispatches. After
// A2 Pass 1L absorbs the SetLocal at compile time into OpBinLL with
// C=dst+1 (4-instr fusion), saving one dispatch and one push/pop round-trip.
func BenchmarkThreeReg(b *testing.B) {
	chunk, env := benchSetup(b, "", `
var a = 1;
var b = 2;
var c = 0;
var i = 0;
while (i < 1000000) {
    c = a + b;
    i = i + 1;
}
`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLoopSumSharedScoped measures the same SharedGlobals tight loop as
// BenchmarkLoopSumShared but the body is wrapped in a block scope — this
// exercises OpPushScope/OpPopScope on every iteration, which invalidates the
// VM name cache and forces ncache re-population. Establishes a baseline for
// any per-entry invalidation optimization (REC-19).
func BenchmarkLoopSumSharedScoped(b *testing.B) {
	sess := newSession(_benchCtx)
	prog, err := ParseEmbedded(`
var sum = 0;
var i = 0;
while (i < 1000000) {
    {
        sum = sum + i;
        i = i + 1;
    }
}
`)
	if err != nil {
		b.Fatalf("bench parse: %v", err)
	}
	chunk, err := CompileWith(prog, CompileOptions{SharedGlobals: true})
	if err != nil {
		b.Fatalf("bench compile: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, sess.env); err != nil {
			b.Fatal(err)
		}
	}
}
