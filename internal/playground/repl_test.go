package playground

import (
	"context"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/spell"
)

var testInfo = BuildInfo{
	Compiler:  "tinygo 0.40.0",
	Target:    "js/wasm",
	Scheduler: "asyncify",
	GoVersion: "go1.24.7",
}

func TestBanner_conveysSandbox(t *testing.T) {
	got := joinHTML(NewShell(testInfo).Banner())
	for _, want := range []string{"sandbox", "WebAssembly", "executed", "tinygo 0.40.0", "js/wasm"} {
		if !strings.Contains(got, want) {
			t.Errorf("banner should mention %q", want)
		}
	}
}

func TestShell_versionCommand(t *testing.T) {
	got := joinHTML(exec(newTestShell(t), "version").Lines)
	for _, want := range []string{"tinygo 0.40.0", "js/wasm", "asyncify", "go1.24.7"} {
		if !strings.Contains(got, want) {
			t.Errorf("version should report %q", want)
		}
	}
}

func TestEvalBuzz_value(t *testing.T) {
	r := EvalBuzz(context.Background(), "return (1 + 2) * 10;")
	if !r.OK {
		t.Fatalf("eval failed: %+v", r.Diag)
	}
	if r.Result != "30" {
		t.Fatalf("result = %q, want 30", r.Result)
	}
}

func TestEvalBuzz_capturesPrint(t *testing.T) {
	r := EvalBuzz(context.Background(), `import "std"; std.print("hello"); std.print("world");`)
	if !r.OK {
		t.Fatalf("eval failed: %+v", r.Diag)
	}
	if r.Output != "hello\nworld\n" {
		t.Fatalf("output = %q", r.Output)
	}
}

func TestEvalBuzz_errorPosition(t *testing.T) {
	r := EvalBuzz(context.Background(), "return 1 +;")
	if r.OK {
		t.Fatal("expected a parse error")
	}
	if r.Diag == nil || r.Diag.Line == 0 {
		t.Fatalf("expected a positioned diag, got %+v", r.Diag)
	}
}

func newTestShell(t *testing.T) *Shell {
	t.Helper()
	s := NewShell(testInfo)
	ok, status := s.SetSource(context.Background(), sampleMagusfile)
	if !ok {
		t.Fatalf("sample magusfile did not parse: %s", status)
	}
	return s
}

// exec is a test shorthand for s.Exec with a background context.
func exec(s *Shell, line string) ExecResult {
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

func TestShell_status(t *testing.T) {
	s := NewShell(testInfo)
	ok, status := s.SetSource(context.Background(), sampleMagusfile)
	if !ok || !strings.Contains(status, "target") {
		t.Fatalf("status = %q ok=%v", status, ok)
	}

	ok, status = s.SetSource(context.Background(), "export fun x(_a: [str]) > void { let ; }")
	if ok || !strings.HasPrefix(status, "✗") {
		t.Fatalf("expected parse error badge, got %q ok=%v", status, ok)
	}
}

func TestShell_ls(t *testing.T) {
	s := newTestShell(t)
	res := exec(s, "ls")
	out := joinHTML(res.Lines)
	for _, want := range []string{"format", "lint", "build", "ci"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls missing %q in:\n%s", want, out)
		}
	}
	if len(res.Lines) == 0 || !strings.Contains(res.Lines[0].HTML, "magus") ||
		!strings.Contains(res.Lines[0].HTML, "ls") {
		t.Error("ls did not echo the command with the magus prompt")
	}
}

func TestShell_run(t *testing.T) {
	s := newTestShell(t)
	out := joinHTML(exec(s, "run ci").Lines)
	// deps appear before ci in the order line, and ops are recorded.
	if !strings.Contains(out, "order:") || !strings.Contains(out, "go-fmt") || !strings.Contains(out, "go-vet") {
		t.Fatalf("run ci output:\n%s", out)
	}
	if !strings.Contains(out, "recorded") {
		t.Error("run ci should mark ops as recorded")
	}
}

func TestShell_evalBareExpression(t *testing.T) {
	s := newTestShell(t)
	out := joinHTML(exec(s, "return 6 * 7;").Lines)
	if !strings.Contains(out, "⇒ 42") {
		t.Fatalf("bare eval output:\n%s", out)
	}
}

func TestShell_evalSeesMagusfileDefs(t *testing.T) {
	s := NewShell(testInfo)
	s.SetSource(context.Background(), `import "magus";
fun triple(n: int) > int { return n * 3; }
export fun build(_a: [str]) > void {}`)
	out := joinHTML(exec(s, "triple(14)").Lines)
	if !strings.Contains(out, "⇒ 42") {
		t.Fatalf("expression should see the magusfile's functions:\n%s", out)
	}
}

func TestShell_clear(t *testing.T) {
	s := newTestShell(t)
	if res := exec(s, "clear"); !res.Clear || len(res.Lines) != 0 {
		t.Fatalf("clear should signal Clear with no lines, got %+v", res)
	}
}

func TestShell_completeCommands(t *testing.T) {
	s := newTestShell(t)
	if got, _ := s.Complete("ru"); got != "run " {
		t.Errorf("complete(ru) = %q, want %q", got, "run ")
	}
	// ambiguous prefix lists candidates and completes the common prefix.
	repl, listing := s.Complete("")
	_ = repl
	if len(listing) == 0 {
		t.Error("empty completion should list commands")
	}
}

func TestShell_completeTargets(t *testing.T) {
	s := newTestShell(t)
	if got, _ := s.Complete("run b"); got != "run build " {
		t.Errorf("complete(run b) = %q, want %q", got, "run build ")
	}
	repl, listing := s.Complete("run ")
	if repl != "run " || len(listing) != 1 {
		t.Fatalf("complete(run ) = %q listing=%d", repl, len(listing))
	}
	for _, want := range []string{"build", "ci", "format", "lint"} {
		if !strings.Contains(listing[0].HTML, want) {
			t.Errorf("target listing missing %q: %s", want, listing[0].HTML)
		}
	}
}

func TestShell_history(t *testing.T) {
	s := newTestShell(t)
	exec(s, "ls")
	exec(s, "graph")
	if got, ok := s.HistPrev(); !ok || got != "graph" {
		t.Fatalf("HistPrev = %q,%v", got, ok)
	}
	if got, ok := s.HistPrev(); !ok || got != "ls" {
		t.Fatalf("HistPrev = %q,%v", got, ok)
	}
	if got, _ := s.HistNext(); got != "graph" {
		t.Fatalf("HistNext = %q", got)
	}
}

func TestShell_historyBottomDoesNotClobber(t *testing.T) {
	s := newTestShell(t)
	exec(s, "ls")
	// At the newest entry, ↓ must report "no change" so the caller leaves an
	// in-progress (non-history) line alone instead of clearing it.
	if _, ok := s.HistNext(); ok {
		t.Fatal("HistNext at the bottom should return ok=false")
	}
}

func TestShell_completeToleratesWhitespace(t *testing.T) {
	s := newTestShell(t)
	if got, _ := s.Complete("  ru"); got != "run " {
		t.Errorf("complete(  ru) = %q, want %q", got, "run ")
	}
	if got, _ := s.Complete("run  b"); got != "run build " {
		t.Errorf("complete(run  b) = %q, want %q", got, "run build ")
	}
}

const sampleMagusfile = `
import "magus/spell/go";

magus.project.register(fun(p, cb) > bool { cb({
    "spells": [go],
    "outputs": ["bin/**"],
    "targets": {"regen-pgo": {"cachable": false}},
}); return true; });

export fun format(_args: [str]) > void { go["go-fmt"](); }
export fun lint(_args: [str]) > void { magus.depends_on(["format"]); go["go-vet"](); }
export fun build(_args: [str]) > void { magus.depends_on(["format"]); go["go-build"](); }
export fun ci(_args: [str]) > void { magus.depends_on(["lint", "build"]); }
`

func TestLoadMagusfile_graph(t *testing.T) {
	g := LoadMagusfile(context.Background(), sampleMagusfile)
	if !g.OK {
		t.Fatalf("load failed: %+v", g.Diag)
	}
	if len(g.Projects) != 1 || g.Projects[0].Path != "." {
		t.Fatalf("projects = %+v", g.Projects)
	}
	if len(g.Projects[0].NoCache) != 1 || g.Projects[0].NoCache[0] != "regen-pgo" {
		t.Fatalf("noCache = %+v", g.Projects[0].NoCache)
	}
	if len(g.Projects[0].Spells) != 1 || g.Projects[0].Spells[0] != "go" {
		t.Fatalf("spells = %+v", g.Projects[0].Spells)
	}

	gotTargets := map[string]bool{}
	for _, tg := range g.Targets {
		gotTargets[tg.Key] = true
	}
	for _, want := range []string{"format", "lint", "build", "ci"} {
		if !gotTargets[want] {
			t.Errorf("missing target %q (got %v)", want, gotTargets)
		}
	}

	if !hasEdge(g.Edges, "ci", "lint") || !hasEdge(g.Edges, "ci", "build") ||
		!hasEdge(g.Edges, "lint", "format") || !hasEdge(g.Edges, "build", "format") {
		t.Fatalf("edges = %+v", g.Edges)
	}
}

func TestDryRun_orderAndTrace(t *testing.T) {
	r := DryRun(context.Background(), sampleMagusfile, "ci")
	if !r.OK {
		t.Fatalf("dry-run failed: %+v", r.Diag)
	}
	// format must precede lint and build; everything precedes ci.
	pos := map[string]int{}
	for i, k := range r.Order {
		pos[k] = i
	}
	if !(pos["format"] < pos["lint"] && pos["format"] < pos["build"] &&
		pos["lint"] < pos["ci"] && pos["build"] < pos["ci"]) {
		t.Fatalf("bad order: %v", r.Order)
	}
	// The trace must include the recorded spell ops from the dependencies.
	ops := map[string]bool{}
	for _, op := range r.Trace {
		ops[op.Name] = true
	}
	for _, want := range []string{"go-fmt", "go-vet", "go-build"} {
		if !ops[want] {
			t.Errorf("trace missing op %q (got %v)", want, ops)
		}
	}
}

func TestDryRun_unknownTarget(t *testing.T) {
	r := DryRun(context.Background(), sampleMagusfile, "nope")
	if r.OK || r.Diag == nil {
		t.Fatal("expected an unknown-target diag")
	}
}

// TestManifestMatchesBuiltins gates the hand-written spell manifest against the
// real built-in registry: every spell and op the playground claims must exist.
// (Host-only — the spell package's embedded bytecode never enters the wasm
// build.)
func TestManifestMatchesBuiltins(t *testing.T) {
	builtins := spell.Builtins()
	for name, ops := range builtinSpellOps {
		spec, ok := builtins[name]
		if !ok {
			t.Errorf("manifest spell %q is not a built-in", name)
			continue
		}
		for _, op := range ops {
			if _, ok := spec.Targets[op]; !ok {
				t.Errorf("manifest op %q.%q is not a real target", name, op)
			}
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
