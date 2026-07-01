package playground

import (
	"context"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/spell"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testInfo = BuildInfo{
	Compiler:  "tinygo 0.40.0",
	Target:    "js/wasm",
	Scheduler: "asyncify",
	GoVersion: "go1.24.7",
}

func TestBanner_conveysSandbox(t *testing.T) {
	got := joinHTML(NewConsole(testInfo).Banner())
	for _, want := range []string{"sandbox", "WebAssembly", "executed", "tinygo 0.40.0", "js/wasm"} {
		assert.Contains(t, got, want, "banner should mention %q", want)
	}
}

func TestConsole_versionCommand(t *testing.T) {
	got := joinHTML(exec(newTestConsole(t), "version").Lines)
	for _, want := range []string{"tinygo 0.40.0", "js/wasm", "asyncify", "go1.24.7"} {
		assert.Contains(t, got, want, "version should report %q", want)
	}
}

func TestEvalBuzz_value(t *testing.T) {
	r := EvalBuzz(context.Background(), "return (1 + 2) * 10;")
	require.True(t, r.OK, "eval failed: %+v", r.Diag)
	assert.Equal(t, "30", r.Result)
}

func TestEvalBuzz_capturesPrint(t *testing.T) {
	r := EvalBuzz(context.Background(), `import "std"; std.print("hello"); std.print("world");`)
	require.True(t, r.OK, "eval failed: %+v", r.Diag)
	assert.Equal(t, "hello\nworld\n", r.Output)
}

func TestEvalBuzz_errorPosition(t *testing.T) {
	r := EvalBuzz(context.Background(), "return 1 +;")
	require.False(t, r.OK, "expected a parse error")
	require.NotNil(t, r.Diag)
	assert.NotZero(t, r.Diag.Line, "expected a positioned diag, got %+v", r.Diag)
}

func newTestConsole(t *testing.T) *Console {
	t.Helper()
	s := NewConsole(testInfo)
	ok, status := s.SetSource(context.Background(), sampleMagusfile)
	require.True(t, ok, "sample magusfile did not parse: %s", status)
	return s
}

// exec is a test shorthand for s.Exec with a background context.
func exec(s *Console, line string) ExecResult {
	return s.Exec(context.Background(), line)
}

func joinHTML(lines []Line) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.HTML)
		b.WriteString("\n")
	}
	return b.String()
}

func TestConsole_status(t *testing.T) {
	s := NewConsole(testInfo)
	ok, status := s.SetSource(context.Background(), sampleMagusfile)
	require.True(t, ok)
	assert.Contains(t, status, "target")

	ok, status = s.SetSource(context.Background(), "export fun x(_a: [str]) > void { let ; }")
	require.False(t, ok, "expected parse error badge")
	assert.True(t, strings.HasPrefix(status, "[fail]"), "expected parse error badge, got %q", status)
}

func TestConsole_ls(t *testing.T) {
	s := newTestConsole(t)
	res := exec(s, "ls")
	out := joinHTML(res.Lines)
	for _, want := range []string{"format", "lint", "build", "ci"} {
		assert.Contains(t, out, want, "ls missing %q", want)
	}
	require.NotEmpty(t, res.Lines, "ls did not echo the command")
	assert.Contains(t, res.Lines[0].HTML, "magus", "ls did not echo the command with the magus prompt")
	assert.Contains(t, res.Lines[0].HTML, "ls", "ls did not echo the command with the magus prompt")
}

func TestConsole_run(t *testing.T) {
	s := newTestConsole(t)
	out := joinHTML(exec(s, "run ci").Lines)
	// deps appear before ci in the order line, and ops are recorded.
	assert.Contains(t, out, "order:", "run ci output:\n%s", out)
	assert.Contains(t, out, "go-fmt", "run ci output:\n%s", out)
	assert.Contains(t, out, "go-vet", "run ci output:\n%s", out)
	assert.Contains(t, out, "recorded", "run ci should mark ops as recorded")
}

func TestConsole_evalBareExpression(t *testing.T) {
	s := newTestConsole(t)
	out := joinHTML(exec(s, "return 6 * 7;").Lines)
	assert.Contains(t, out, "⇒ 42", "bare eval output:\n%s", out)
}

func TestConsole_evalSeesMagusfileDefs(t *testing.T) {
	s := NewConsole(testInfo)
	s.SetSource(context.Background(), `import "magus";
fun triple(n: int) > int { return n * 3; }
export fun build(_a: [str]) > void {}`)
	out := joinHTML(exec(s, "triple(14)").Lines)
	assert.Contains(t, out, "⇒ 42", "expression should see the magusfile's functions:\n%s", out)
}

func TestConsole_clear(t *testing.T) {
	s := newTestConsole(t)
	res := exec(s, "clear")
	assert.True(t, res.Clear, "clear should signal Clear")
	assert.Empty(t, res.Lines, "clear should produce no lines")
}

func TestConsole_completeCommands(t *testing.T) {
	s := newTestConsole(t)
	got, _ := s.Complete("ru")
	assert.Equal(t, "run ", got)
	// ambiguous prefix lists candidates and completes the common prefix.
	_, listing := s.Complete("")
	assert.NotEmpty(t, listing, "empty completion should list commands")
}

func TestConsole_completeTargets(t *testing.T) {
	s := newTestConsole(t)
	got, _ := s.Complete("run b")
	assert.Equal(t, "run build ", got)
	repl, listing := s.Complete("run ")
	assert.Equal(t, "run ", repl)
	require.Len(t, listing, 1)
	for _, want := range []string{"build", "ci", "format", "lint"} {
		assert.Contains(t, listing[0].HTML, want, "target listing missing %q", want)
	}
}

func TestConsole_history(t *testing.T) {
	s := newTestConsole(t)
	exec(s, "ls")
	exec(s, "graph")
	got, ok := s.HistPrev()
	require.True(t, ok)
	assert.Equal(t, "graph", got)
	got, ok = s.HistPrev()
	require.True(t, ok)
	assert.Equal(t, "ls", got)
	got, _ = s.HistNext()
	assert.Equal(t, "graph", got)
}

func TestConsole_historyBottomDoesNotClobber(t *testing.T) {
	s := newTestConsole(t)
	exec(s, "ls")
	// At the newest entry, ↓ must report "no change" so the caller leaves an
	// in-progress (non-history) line alone instead of clearing it.
	_, ok := s.HistNext()
	assert.False(t, ok, "HistNext at the bottom should return ok=false")
}

func TestConsole_completeToleratesWhitespace(t *testing.T) {
	s := newTestConsole(t)
	got, _ := s.Complete("  ru")
	assert.Equal(t, "run ", got)
	got, _ = s.Complete("run  b")
	assert.Equal(t, "run build ", got)
}

const sampleMagusfile = `
import "magus/spell/go";

magus.project({
    "spells": [go],
    "outputs": ["bin/**"],
    "targets": {"regen-pgo": {"cachable": false}},
});

export fun format(args: [str]) > void { go["go-fmt"](); }
export fun lint(args: [str]) > void { magus.needs(magus.target.literal("format")); go["go-vet"](); }
export fun build(args: [str]) > void { magus.needs(magus.target.literal("format")); go["go-build"](); }
export fun ci(args: [str]) > void { magus.needs(magus.target.literal("lint"), magus.target.literal("build")); }
`

func TestLoadMagusfile_graph(t *testing.T) {
	g := LoadMagusfile(context.Background(), sampleMagusfile)
	require.True(t, g.OK, "load failed: %+v", g.Diag)
	require.Len(t, g.Projects, 1)
	assert.Equal(t, ".", g.Projects[0].Path)
	assert.Equal(t, []string{"regen-pgo"}, g.Projects[0].NoCache)
	assert.Equal(t, []string{"go"}, g.Projects[0].Spells)

	gotTargets := map[string]bool{}
	for _, tg := range g.Targets {
		gotTargets[tg.Key] = true
	}
	for _, want := range []string{"format", "lint", "build", "ci"} {
		assert.True(t, gotTargets[want], "missing target %q (got %v)", want, gotTargets)
	}

	assert.True(t, hasEdge(g.Edges, "ci", "lint"), "edges = %+v", g.Edges)
	assert.True(t, hasEdge(g.Edges, "ci", "build"), "edges = %+v", g.Edges)
	assert.True(t, hasEdge(g.Edges, "lint", "format"), "edges = %+v", g.Edges)
	assert.True(t, hasEdge(g.Edges, "build", "format"), "edges = %+v", g.Edges)
}

func TestDryRun_orderAndTrace(t *testing.T) {
	r := DryRun(context.Background(), sampleMagusfile, "ci")
	require.True(t, r.OK, "dry-run failed: %+v", r.Diag)
	// format must precede lint and build; everything precedes ci.
	pos := map[string]int{}
	for i, k := range r.Order {
		pos[k] = i
	}
	assert.Less(t, pos["format"], pos["lint"], "bad order: %v", r.Order)
	assert.Less(t, pos["format"], pos["build"], "bad order: %v", r.Order)
	assert.Less(t, pos["lint"], pos["ci"], "bad order: %v", r.Order)
	assert.Less(t, pos["build"], pos["ci"], "bad order: %v", r.Order)
	// The trace must include the recorded spell ops from the dependencies.
	ops := map[string]bool{}
	for _, op := range r.Trace {
		ops[op.Name] = true
	}
	for _, want := range []string{"go-fmt", "go-vet", "go-build"} {
		assert.True(t, ops[want], "trace missing op %q (got %v)", want, ops)
	}
}

func TestDryRun_unknownTarget(t *testing.T) {
	r := DryRun(context.Background(), sampleMagusfile, "nope")
	require.False(t, r.OK, "expected an unknown-target diag")
	assert.NotNil(t, r.Diag, "expected an unknown-target diag")
}

// TestManifestMatchesBuiltins gates the hand-written spell manifest against the
// real built-in registry: every spell and op the playground claims must exist.
// (Host-only — the spell package's embedded bytecode never enters the wasm
// build.)
func TestManifestMatchesBuiltins(t *testing.T) {
	builtins := spell.Builtins()
	for name, ops := range builtinSpellOps {
		spec, ok := builtins[name]
		if !assert.True(t, ok, "manifest spell %q is not a built-in", name) {
			continue
		}
		for _, op := range ops {
			_, ok := spec.Ops[op]
			assert.True(t, ok, "manifest op %q.%q is not a real target", name, op)
		}
	}
}

func hasEdge(edges []Edge, from, to string) bool {
	for _, e := range edges {
		if e.From == from && e.To == to {
			return true
		}
	}
	return false
}
