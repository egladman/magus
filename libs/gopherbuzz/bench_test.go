package buzz

import (
	"context"
	"testing"

	vmpackage "github.com/egladman/magus/libs/gopherbuzz/vm"
)

var _benchCtx = context.Background()

// benchSession creates a session and defines src once; returns a precompiled
// chunk for the "hot" portion and the session's env so globals are available.
func benchSetup(b *testing.B, init, hot string) (*vmpackage.Chunk, *vmpackage.Env) {
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

// benchRun times b.N executions of an already-compiled chunk on a fresh VM, the
// protocol every benchmark here uses.
func benchRun(b *testing.B, chunk *vmpackage.Chunk, env *vmpackage.Env) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
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
// the float arithmetic path.
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
	sess.SetGlobal("nat", vmpackage.DirectValue("nat", func(_ context.Context, args []vmpackage.Value) (vmpackage.Value, error) {
		return vmpackage.IntValue(int64(len(args))), nil
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

// BenchmarkStrIndexOfLateHit measures indexOf on a large ASCII haystack where the
// needle sits near the END. This is the docs site generator's hot shape: lib/glossary
// and lib/html scan a rendered page (tens of KB) for markers, and every hit paid a
// utf8.RuneCountInString over the whole prefix to convert the byte offset into the rune
// index the language exposes. str.sub already had a cached-isASCII fast path for exactly
// this reason; indexOf did not, so a scan that walks a page marker by marker was
// quadratic in the page size before a single character was rewritten.
func BenchmarkStrIndexOfLateHit(b *testing.B) {
	chunk, env := benchSetup(b,
		`final haystack = "x".repeat(200000) + "NEEDLE";`,
		`final __r = haystack.indexOf("NEEDLE");`,
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

// BenchmarkStrByteScan measures a character-at-a-time walk of a large ASCII string,
// the shape lib/glossary's word-boundary check and lib/conventions' fence scanner use.
// Before the isASCII fast path in str.byte, each call rebuilt a []rune of the whole
// string, making the walk quadratic and allocating once per character.
func BenchmarkStrByteScan(b *testing.B) {
	chunk, env := benchSetup(b,
		`final haystack = "abcdefghij".repeat(2000);`,
		`var n = 0;
var i = 0;
while (i < haystack.len()) {
    if (haystack.byte(i) == 99) { n = n + 1; }
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

// BenchmarkStrByteScanMultibyte is BenchmarkStrByteScan's twin for a string the ASCII
// fast path REJECTS. The isASCII test is all-or-nothing, so one accented character in a
// document puts every lookup in it on the multibyte path - which is not a corner case:
// 31 of the magus docs' 200 Markdown sources contain one, and a CPU profile of the site
// render attributed 14% of the whole build to this single method.
func BenchmarkStrByteScanMultibyte(b *testing.B) {
	chunk, env := benchSetup(b,
		`final haystack = "abcdéfghij".repeat(2000);`,
		`var n = 0;
var i = 0;
while (i < haystack.len()) {
    if (haystack.byte(i) == 99) { n = n + 1; }
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

// The benchmarks below cover the language surface gopherbuzz gained after the set
// above was written - fibers, match, closures over cells, optionals, mut
// collections, higher-order collection methods, static dispatch and tuples - so the
// benchmark set tracks what conformance now tests rather than lagging it.

// BenchmarkFiberForeach drives a generator fiber 1 000 times through foreach, the
// shape upstream's own examples use. Each yield suspends the fiber's private VM and
// resumes the driver, so this measures fiber switch cost, not loop cost.
func BenchmarkFiberForeach(b *testing.B) {
	chunk, env := benchSetup(b,
		`fun squares(n: int) > void *> int? {
    foreach (i in 0..n) {
        _ = yield (i * i);
    }
}`,
		`var sum = 0;
foreach (v in &squares(1000)) {
    sum = sum + v;
}`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkFiberResume measures explicit resume/resolve rather than foreach: the
// handle is bound, so each step pays the OpResume path and the status check.
func BenchmarkFiberResume(b *testing.B) {
	chunk, env := benchSetup(b,
		`fun counter(n: int) > int *> int? {
    var i = 0;
    while (i < n) {
        _ = yield i;
        i = i + 1;
    }
    return i;
}`,
		`final f = &counter(1000);
var sum = 0;
foreach (v in f) {
    sum = sum + v;
}
final __r = resolve f;`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkMatchEnum measures an exhaustive match over an enum subject - the arm
// comparison is enum-case identity, and no else arm is present.
func BenchmarkMatchEnum(b *testing.B) {
	chunk, env := benchSetup(b,
		`enum Kind { one, two, three, four }

fun label(k: Kind) > str {
    return match (k) {
        .one -> "one",
        .two -> "two",
        .three -> "three",
        .four -> "four",
    };
}
final kinds = [Kind.one, Kind.two, Kind.three, Kind.four];`,
		`var n = 0;
var i = 0;
while (i < 25000) {
    if (label(kinds[i % 4]).len() > 2) { n = n + 1; }
    i = i + 1;
}`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkMatchRange measures range arms, which dispatch on CONTAINMENT rather
// than equality - a different comparison path from the enum and literal arms.
func BenchmarkMatchRange(b *testing.B) {
	chunk, env := benchSetup(b, "",
		`var n = 0;
var i = 0;
while (i < 25000) {
    final band = match (i % 100) {
        0..25 -> 1,
        25..50 -> 2,
        50..75 -> 3,
        else -> 4,
    };
    n = n + band;
    i = i + 1;
}`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkMatchPattern measures pattern (regex) arms, where each arm runs the
// compiled pattern against the subject string.
func BenchmarkMatchPattern(b *testing.B) {
	chunk, env := benchSetup(b,
		`final subjects = ["hello joe", "1234", "  ", "zz-99"];`,
		`var n = 0;
var i = 0;
while (i < 5000) {
    final kind = match (subjects[i % 4]) {
        $"^[a-z]+ [a-z]+$" -> 1,
        $"^\d+$" -> 2,
        $"^\s+$" -> 3,
        else -> 4,
    };
    n = n + kind;
    i = i + 1;
}`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkTryCatchThrow measures a throw caught one frame up, the cross-frame
// unwind path. The loop throws on half its iterations so both the raising and the
// non-raising arm are represented.
func BenchmarkTryCatchThrow(b *testing.B) {
	chunk, env := benchSetup(b,
		`fun risky(n: int) > int !> str {
    if (n % 2 == 0) { throw "even"; }
    return n;
}`,
		`var caught = 0;
var ok = 0;
var i = 0;
while (i < 20000) {
    try {
        ok = ok + risky(i);
    } catch (e: str) {
        caught = caught + e.len();
    }
    i = i + 1;
}`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkClosureUpvalue measures a closure assigning to a captured local. Capture
// is by reference through a shared cell, so every read and write in both the owning
// frame and the closure goes through OpGetLocalCell/OpSetLocalCell.
func BenchmarkClosureUpvalue(b *testing.B) {
	chunk, env := benchSetup(b, "",
		`var sum = 0;
final add = fun (n: int) > void { sum = sum + n; };
var i = 0;
while (i < 100000) {
    add(i);
    i = i + 1;
}`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkForeachRange measures foreach over a range, which iterates without
// materialising a list.
func BenchmarkForeachRange(b *testing.B) {
	chunk, env := benchSetup(b, "",
		`var sum = 0;
foreach (n in 0..200000) {
    sum = sum + n;
}`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkForeachStr measures foreach over a string. Buzz iterates a string
// BYTEWISE, so this walks the raw bytes rather than decoding runes.
func BenchmarkForeachStr(b *testing.B) {
	chunk, env := benchSetup(b,
		`final doc = "abcdefghij".repeat(2000);`,
		`var n = 0;
foreach (c in doc) {
    n = n + c.len();
}`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkOptionalUnwrap measures the optional operators together: null-coalesce
// on a null subject, force-unwrap on a bound one, and optional subscript.
func BenchmarkOptionalUnwrap(b *testing.B) {
	chunk, env := benchSetup(b,
		`final present: int? = 7;
final absent: int? = null;
final xs: [int]? = [1, 2, 3];`,
		`var n = 0;
var i = 0;
while (i < 50000) {
    n = n + present! + (absent ?? 1) + (xs?[i % 3] ?? 0);
    i = i + 1;
}`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkListHigherOrder measures map/filter/reduce over a list. Each call
// invokes a Buzz closure per element, so this is dominated by callback dispatch
// rather than by the traversal.
func BenchmarkListHigherOrder(b *testing.B) {
	chunk, env := benchSetup(b,
		`var seed = mut [<int>];
foreach (i in 0..2000) {
    seed.append(i);
}`,
		`final doubled = seed.map(fun (_: int, v: int) > int => v * 2);
final evens = doubled.filter(fun (_: int, v: int) > bool => v % 4 == 0);
final __r = evens.reduce::<int>(fun (_: int, v: int, acc: int) > int => acc + v, initial: 0);`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkMutListAppend measures repeated append to a mut list, so the growth of
// the backing slice is on the clock alongside the mutability check each call makes.
func BenchmarkMutListAppend(b *testing.B) {
	chunk, env := benchSetup(b, "",
		`final xs = mut [<int>];
var i = 0;
while (i < 20000) {
    xs.append(i);
    i = i + 1;
}`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkStaticCall measures a static method call, which resolves on the TYPE
// value and binds no receiver - a different dispatch path from BenchmarkMethodCall.
func BenchmarkStaticCall(b *testing.B) {
	chunk, env := benchSetup(b,
		`object Point {
    x: int = 0,
    y: int = 0,
    static fun at(x: int, y: int) > Point { return Point{ x = x, y = y }; }
}`,
		`var n = 0;
var i = 0;
while (i < 20000) {
    n = n + Point.at(x: i, y: i).x;
    i = i + 1;
}`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkTupleAccess measures building and reading a tuple, which is an anonymous
// object keyed by each element's decimal index. The first element is parenthesised
// because a bare identifier in `.{ }` is the `name = name` field shorthand.
func BenchmarkTupleAccess(b *testing.B) {
	chunk, env := benchSetup(b, "",
		`var n = 0;
var i = 0;
while (i < 20000) {
    final t = .{ (i), i + 1, i + 2 };
    n = n + t.0 + t.1 + t.2;
    i = i + 1;
}`,
	)
	benchRun(b, chunk, env)
}
