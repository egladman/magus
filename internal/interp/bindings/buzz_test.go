package bindings

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/engine"
	_ "github.com/egladman/magus/internal/interp/engine/buzz"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
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
	dialectBuzz dialect = iota
)

// enginesUnderTest lists the registered engine IDs to benchmark, paired with
// the dialect each one executes. An engine absent from the build is skipped at
// run time.
var enginesUnderTest = []struct {
	id      string
	dialect dialect
}{
	{"buzz", dialectBuzz},
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
			dialectBuzz: {setup: "fun fib(n) int { if (n <= 1) { return n; } return fib(n - 1) + fib(n - 2); }", hot: "fib(30);"},
		},
	},
	{
		name: "LoopSum", // tight while-loop summing 1e6 ints
		src: map[dialect]src{
			dialectBuzz: {hot: "var sum = 0; var i = 0; while (i < 1000000) { sum = sum + i; i = i + 1; }"},
		},
	},
	{
		name: "ForeachList", // iterate a 1000-element list
		src: map[dialect]src{
			dialectBuzz: {setup: "var items = mut []; var i = 0; while (i < 1000) { items.append(i); i = i + 1; }", hot: "var sum = 0; foreach (x in items) { sum = sum + x; }"},
		},
	},
	{
		name: "ForeachMap", // iterate a 10-entry map
		src: map[dialect]src{
			dialectBuzz: {setup: `final m = {"a":1,"b":2,"c":3,"d":4,"e":5,"f":6,"g":7,"h":8,"i":9,"j":10};`, hot: "var sum = 0; foreach (k, v in m) { sum = sum + v; }"},
		},
	},
	{
		name: "StringInterp", // build an interpolated string 100x
		src: map[dialect]src{
			dialectBuzz: {hot: `var s = ""; var i = 0; while (i < 100) { s = "item {i} of 100"; i = i + 1; }`},
		},
	},
	{
		name: "Call", // overhead of one trivial function call
		src: map[dialect]src{
			dialectBuzz: {setup: "fun add(a, b) int { return a + b; }", hot: "add(1, 2);"},
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

// surfaceLockPath is the committed snapshot of everything a magusfile can call on the
// magus namespace, one dotted member per line, sorted.
var surfaceLockPath = filepath.Join("testdata", "magus-api.lock")

// magusSurfaceNames flattens the magusfile-surface magus namespace to dotted member
// names, two levels deep: the top-level members plus the members of each namespace
// member (project, cache, ci, secret, workspace). Two levels is what the removal
// history needs - `magus.project.register` and `magus.target.literal` were both
// nested - and going deeper would snapshot returned data rather than the surface.
func magusSurfaceNames(t *testing.T) []string {
	t.Helper()
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	t.Cleanup(func() { _ = sess.Close() })

	ns := buildMagusNS(ctx, sess, interp.NewHostCallObserver(ctx), false, magusfileSurface)
	require.True(t, ns.IsMap(), "the magus namespace is a map")

	var out []string
	for _, k := range ns.MapKeys() {
		out = append(out, k)
		v, ok := ns.MapGet(k)
		if !ok || !v.IsMap() {
			continue
		}
		for _, sub := range v.MapKeys() {
			out = append(out, k+"."+sub)
		}
	}
	sort.Strings(out)
	return out
}

// TestMagusSurfaceLocked is the gate the MGS1025 table cannot provide for itself.
//
// removedMagusfileAPI (internal/interp/runtime.go) is hand-maintained, so it only ever
// describes removals someone remembered to write down. Deleting a binding is otherwise
// silent in both directions: Buzz reads a missing member as null, so the magusfiles that
// still call it keep loading and fail later with "null is not callable", and no test
// anywhere notices the surface got smaller.
//
// This makes the surface a committed artifact. A removed member fails here, naming the
// member and the table that has to describe it; an added one fails too, which is the
// cheap price of the snapshot and is settled by regenerating.
func TestMagusSurfaceLocked(t *testing.T) {
	got := magusSurfaceNames(t)

	if os.Getenv("UPDATE_MAGUS_API_LOCK") != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(surfaceLockPath), 0o755))
		require.NoError(t, os.WriteFile(surfaceLockPath, []byte(strings.Join(got, "\n")+"\n"), 0o644))
		t.Logf("wrote %s (%d members)", surfaceLockPath, len(got))
		return
	}

	data, err := os.ReadFile(surfaceLockPath)
	require.NoError(t, err, "the surface lock must be committed; regenerate with UPDATE_MAGUS_API_LOCK=1 go test ./internal/interp/bindings/")
	want := strings.Fields(strings.TrimSpace(string(data)))

	gotSet := map[string]bool{}
	for _, n := range got {
		gotSet[n] = true
	}
	for _, n := range want {
		assert.Truef(t, gotSet[n],
			"magus.%s was REMOVED from the magusfile surface.\n"+
				"A magusfile still calling it loads fine and fails at run time with "+
				"\"null is not callable\".\n"+
				"Add it to removedMagusfileAPI in internal/interp/runtime.go so it is "+
				"rejected at load with MGS1025, document it in "+
				"docs/reference/codes/magusfile/MGS1025.md, then regenerate this lock with "+
				"UPDATE_MAGUS_API_LOCK=1 go test ./internal/interp/bindings/", n)
	}

	wantSet := map[string]bool{}
	for _, n := range want {
		wantSet[n] = true
	}
	for _, n := range got {
		assert.Truef(t, wantSet[n],
			"magus.%s was ADDED to the magusfile surface; regenerate the lock with "+
				"UPDATE_MAGUS_API_LOCK=1 go test ./internal/interp/bindings/", n)
	}
}

// TestRemovedAPIIsActuallyRemoved keeps the table honest in the other direction: an
// entry naming a member that still exists would reject a magusfile for calling
// something that works.
func TestRemovedAPIIsActuallyRemoved(t *testing.T) {
	live := map[string]bool{}
	for _, n := range magusSurfaceNames(t) {
		live[n] = true
	}
	for _, name := range interp.RemovedAPINames() {
		assert.Falsef(t, live[name],
			"removedMagusfileAPI lists magus.%s, but the magus namespace still binds it; "+
				"MGS1025 would reject a magusfile for calling something that works", name)
	}
}

// Host-implemented versus Buzz-implemented, measured side by side to decide which side a
// than covering a file, and this one deliberately spans two implementations of the same thing.
// These benchmarks answer one question: for the same computation, is it faster as
// a Go host method reached across the binding boundary, or as Buzz source running
// on the VM?
//
// collapseWs is the comparison because BOTH already exist and agree on semantics -
// std.StringsCollapseWs (Go, via strings.Fields) and the hand-rolled version in
// docs/lib/text.buzz. Nothing had to be written twice to measure it.
//
// The shape to read for, rather than a single number: a host call pays a FIXED
// marshalling cost per call and then runs the body at Go speed, while a Buzz
// implementation pays nothing at a boundary and runs the body on the VM. So the
// answer has to move with input size, and one measurement at one size would be
// the misleading version.
//
// callsPerExec is large enough that parse+compile of the surrounding chunk is
// noise rather than the thing being timed.
const callsPerExec = 20000

// buzzCollapseWs is docs/lib/text.buzz's implementation, verbatim in behaviour:
// fold every whitespace run to one space and trim.
const buzzCollapseWs = `
fun collapseWsBuzz(s: str) > str {
    final flat = s.split("\n").join(" ").split("\t").join(" ").split("\r").join(" ");
    var parts = mut [];
    foreach (p in flat.split(" ")) {
        if (p != "") { parts.append(p); }
    }
    return parts.join(" ").trim();
}
`

// benchInput builds an input with whitespace runs to collapse. words controls the
// size; the shape (a run of spaces between each word) is what both
// implementations actually walk.
func benchInput(words int) string {
	var b strings.Builder
	for i := 0; i < words; i++ {
		b.WriteString("word   ")
	}
	return b.String()
}

// runCollapseBench executes body callsPerExec times per iteration and reports the
// per-CALL cost, so host and Buzz numbers are directly comparable.
func runCollapseBench(b *testing.B, prelude, call string, words int) {
	b.Helper()
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	b.Cleanup(func() { _ = sess.Close() })
	RegisterModuleSurface(ctx, sess)

	src := fmt.Sprintf(`
import "strings";
%s
final input = "%s";
var sink = "";
var i = 0;
while (i < %d) {
    sink = %s;
    i = i + 1;
}
`, prelude, benchInput(words), callsPerExec, call)

	// Compile and run once outside the timer: a failure here is a broken
	// benchmark, not a slow one, and it must not be reported as a result.
	if err := sess.Exec(ctx, src); err != nil {
		b.Fatalf("warmup: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := sess.Exec(ctx, src); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*callsPerExec), "ns/call")
}

func BenchmarkCollapseWsHostShort(b *testing.B) {
	runCollapseBench(b, "", `strings\collapseWs(input)`, 3)
}

func BenchmarkCollapseWsBuzzShort(b *testing.B) {
	runCollapseBench(b, buzzCollapseWs, `collapseWsBuzz(input)`, 3)
}

func BenchmarkCollapseWsHostMedium(b *testing.B) {
	runCollapseBench(b, "", `strings\collapseWs(input)`, 50)
}

func BenchmarkCollapseWsBuzzMedium(b *testing.B) {
	runCollapseBench(b, buzzCollapseWs, `collapseWsBuzz(input)`, 50)
}

func BenchmarkCollapseWsHostLong(b *testing.B) {
	runCollapseBench(b, "", `strings\collapseWs(input)`, 400)
}

func BenchmarkCollapseWsBuzzLong(b *testing.B) {
	runCollapseBench(b, buzzCollapseWs, `collapseWsBuzz(input)`, 400)
}

// The boundary cost with the body removed: strings\upperFirst on a 1-character
// string is about the cheapest host method there is, so what this measures is
// almost entirely the cost of crossing into Go and back. Its Buzz counterpart
// does the same trivial work with no crossing at all.
func BenchmarkBoundaryOnlyHost(b *testing.B) {
	runCollapseBench(b, "", `strings\upperFirst("a")`, 1)
}

func BenchmarkBoundaryOnlyBuzz(b *testing.B) {
	runCollapseBench(b, `
fun upperFirstBuzz(s: str) > str {
    if (s == "") { return s; }
    return s.sub(0, len: 1).upper() + s.sub(1);
}
`, `upperFirstBuzz("a")`, 1)
}
