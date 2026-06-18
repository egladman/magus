// External test package so it can blank-import the engine backends (which
// themselves import engine) without an import cycle. Each workload runs through
// every registered engine via the common engine.Session interface — the
// shared-globals (Env) path magus uses for magusfiles, not the standalone
// slot-mode fast path (measured in the buzz package's own bench suite).
package engine_test

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/interp/engine"
	_ "github.com/egladman/magus/internal/interp/engine/buzz"
)

// dialect identifies a source language; engines that share one reuse its source.
type dialect int

const (
	buzz dialect = iota
)

// enginesUnderTest lists the registered engine IDs to benchmark, paired with
// the dialect each one executes. An engine absent from the build is skipped at
// run time.
var enginesUnderTest = []struct {
	id      string
	dialect dialect
}{
	{"buzz", buzz},
}

// src is a workload's source: an optional setup executed once, and the hot
// chunk that is compiled once and invoked b.N times.
type src struct{ setup, hot string }

type workload struct {
	name string
	src  map[dialect]src
}

// workloads mirror the per-engine benchmarks in the buzz suite so results line
// up across engines.
var workloads = []workload{
	{
		name: "Fib", // recursive fib(30): call/return, int arithmetic, branching
		src: map[dialect]src{
			buzz: {setup: "fun fib(n) int { if (n <= 1) { return n; } return fib(n - 1) + fib(n - 2); }", hot: "fib(30);"},
		},
	},
	{
		name: "LoopSum", // tight while-loop summing 1e6 ints
		src: map[dialect]src{
			buzz: {hot: "var sum = 0; var i = 0; while (i < 1000000) { sum = sum + i; i = i + 1; }"},
		},
	},
	{
		name: "ForeachList", // iterate a 1000-element list
		src: map[dialect]src{
			buzz: {setup: "var items = mut []; var i = 0; while (i < 1000) { items.append(i); i = i + 1; }", hot: "var sum = 0; foreach (x in items) { sum = sum + x; }"},
		},
	},
	{
		name: "ForeachMap", // iterate a 10-entry map
		src: map[dialect]src{
			buzz: {setup: `final m = {"a":1,"b":2,"c":3,"d":4,"e":5,"f":6,"g":7,"h":8,"i":9,"j":10};`, hot: "var sum = 0; foreach (k, v in m) { sum = sum + v; }"},
		},
	},
	{
		name: "StringInterp", // build an interpolated string 100x
		src: map[dialect]src{
			buzz: {hot: `var s = ""; var i = 0; while (i < 100) { s = "item {i} of 100"; i = i + 1; }`},
		},
	},
	{
		name: "Call", // overhead of one trivial function call
		src: map[dialect]src{
			buzz: {setup: "fun add(a, b) int { return a + b; }", hot: "add(1, 2);"},
		},
	},
}

// BenchmarkEngines runs every workload through every available engine,
// producing results named Engines/<workload>/<engine>.
func BenchmarkEngines(b *testing.B) {
	for _, w := range workloads {
		b.Run(w.name, func(b *testing.B) {
			for _, e := range enginesUnderTest {
				b.Run(e.id, func(b *testing.B) {
					eng := engine.Lookup(e.id)
					if eng == nil {
						b.Skipf("engine %q not built into this binary", e.id)
					}
					prog, ok := w.src[e.dialect]
					if !ok {
						b.Skipf("no source for %s in this dialect", w.name)
					}
					benchSource(b, eng, prog)
				})
			}
		})
	}
}

// benchSource opens a session, runs setup once, compiles the hot chunk once,
// then times repeated invocation of the compiled chunk.
func benchSource(b *testing.B, eng engine.Engine, prog src) {
	b.Helper()
	sess, err := eng.NewSession(context.Background())
	if err != nil {
		b.Fatalf("new session: %v", err)
	}
	defer func() { _ = sess.Close() }()
	if prog.setup != "" {
		if err := sess.DoString(prog.setup); err != nil {
			b.Fatalf("setup: %v", err)
		}
	}
	fn, err := sess.LoadString(prog.hot)
	if err != nil {
		b.Fatalf("load hot: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := sess.Call(engine.CallParams{Fn: fn}); err != nil {
			b.Fatalf("call: %v", err)
		}
	}
}
