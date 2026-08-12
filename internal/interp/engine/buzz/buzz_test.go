package buzz_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/interp"
	_ "github.com/egladman/magus/internal/interp/bindings" // init() wires the magus host bindings (magus.project, etc.)
	"github.com/egladman/magus/internal/interp/engine"
	_ "github.com/egladman/magus/internal/interp/engine/buzz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuzzEngine_Registered(t *testing.T) {
	e := engine.Lookup("buzz")
	require.NotNil(t, e, "buzz engine not registered after package import")
}

func TestBuzzEngine_NewSession(t *testing.T) {
	e := engine.Lookup("buzz")
	if e == nil {
		t.Skip("buzz engine not registered")
	}
	s, err := e.NewSession(context.Background())
	require.NoError(t, err)
	defer s.Close()

	assert.NoError(t, s.DoString(`var x: int = 1;`))
}

func TestBuzzEngine_GetSetGlobal(t *testing.T) {
	e := engine.Lookup("buzz")
	if e == nil {
		t.Skip("buzz engine not registered")
	}
	s, err := e.NewSession(context.Background())
	require.NoError(t, err)
	defer s.Close()

	s.SetGlobal("msg", engine.StringValue("hi"))
	v := s.GetGlobal("msg")
	require.NotNil(t, v)
	require.False(t, v.IsNil(), "GetGlobal returned nil after SetGlobal")
	got, ok := v.AsString()
	assert.True(t, ok)
	assert.Equal(t, "hi", got)
}

func TestIntegration_ParseTargets(t *testing.T) {
	src := &interp.Source{
		Dir:    t.TempDir(),
		Engine: "buzz",
	}

	// Write a minimal magusfile.buzz to a temp file.
	path := filepath.Join(src.Dir, "magusfile.buzz")
	content := `
import "magus";

export fun build(ctx: magus\Context, args: [str]) > void {}
export fun test(ctx: magus\Context, args: [str]) > void {}
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	src.Files = []string{path}

	targets, err := interp.Parse(context.Background(), src)
	require.NoError(t, err)

	got := make(map[string]bool)
	for _, tgt := range targets {
		got[tgt.Key] = true
	}
	for _, want := range []string{"build", "test"} {
		assert.Truef(t, got[want], "target %q not found; got %v", want, targets)
	}
}

func TestIntegration_RunTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	// We can't capture the side effect of an empty function body,
	// so we test that Run succeeds without error.
	content := `
import "magus";
export fun greet(ctx: magus\Context, args: [str]) > void {}
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	_, err := interp.RunDir(context.Background(), dir, "greet", nil)
	require.NoError(t, err)
}

func TestIntegration_UnknownTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	content := `import "magus";
export fun build(ctx: magus\Context, args: [str]) > void {}
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	_, err := interp.RunDir(context.Background(), dir, "notexist", nil)
	assert.Error(t, err, "expected error for unknown target")
}

func TestIntegration_ProjectRegister(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	content := `
import "magus";
magus.project(".", {
    "outputs": ["bin/*"],
});
export fun build(ctx: magus\Context, args: [str]) > void {}
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	_, err := interp.RunDir(context.Background(), dir, "build", nil)
	require.NoError(t, err)
}

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
// producing results named engine.Engines/<workload>/<engine>.
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
