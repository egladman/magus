// Package e2e holds end-to-end orchestration tests for the public magus
// API. It lives in its own package because exercising Run/RunCI requires
// blank-importing the host bindings, whose init() registers the built-in spells
// process-wide — a side effect that would collide with the spell fixtures in the
// magus package's own tests.
package e2e

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"

	// Link the host bindings so magusfile.buzz targets execute.
	_ "github.com/egladman/magus/internal/interp/bindings"
)

// writeProject creates root/name/magusfile.buzz with body. No magus.project.register
// call is written: a bare magusfile that defines targets is expected to run via
// the auto-bound magusfile spell.
func writeProject(t *testing.T, root, name, body string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "magusfile.buzz"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestRunMultipleTargetsRunsAllProjectTargetPairs is a behavioural baseline:
// running two custom targets across two projects must execute all four
// (project,target) pairs. It guards the scheduler against dropping or
// collapsing work as the run engine evolves.
func TestRunMultipleTargetsRunsAllProjectTargetPairs(t *testing.T) {
	root := t.TempDir()
	body := `
import "magus";
import "fs";
export fun alpha(_args: [str]) > void {
    fs.writeFile("ran-alpha", "1");
}
export fun beta(_args: [str]) > void {
    fs.writeFile("ran-beta", "1");
}
`
	for _, name := range []string{"svc-a", "svc-b"} {
		writeProject(t, root, name, body)
	}

	ctx := context.Background()
	m, err := magus.Open(ctx, root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = m.Close() }()

	alpha, err := m.ExpandPath(types.Target{Name: "alpha"})
	if err != nil {
		t.Fatalf("ExpandPath alpha: %v", err)
	}
	beta, err := m.ExpandPath(types.Target{Name: "beta"})
	if err != nil {
		t.Fatalf("ExpandPath beta: %v", err)
	}
	if err := m.Run(ctx, append(alpha, beta...)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, svc := range []string{"svc-a", "svc-b"} {
		for _, tgt := range []string{"alpha", "beta"} {
			p := filepath.Join(root, svc, "ran-"+tgt)
			if _, err := os.Stat(p); err != nil {
				t.Errorf("expected %s:%s to have run (missing %s)", svc, tgt, p)
			}
		}
	}
}

// TestRunToolchainChangeRebuilds is the R1 proof: a project's cached build is
// replayed when nothing changes, but a change in the spell's probed toolchain
// version (with identical sources) produces a miss and re-runs. The fake spell
// has no sources, so the only key input that varies here is ToolVersions.
func TestRunToolchainChangeRebuilds(t *testing.T) {
	t.Setenv("MAGUS_CACHE_TOOL_VERSION", "project")

	root := t.TempDir()
	projDir := filepath.Join(root, "svc")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	versionFile := filepath.Join(projDir, "VERSION")
	if err := os.WriteFile(versionFile, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	fake := types.NewSpell(
		"faketool",
		types.WithInvoker(func(_ context.Context, req types.InvokeRequest) (any, error) {
			f, err := os.OpenFile(filepath.Join(req.Dir, "count"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				return nil, err
			}
			defer f.Close()
			_, err = f.WriteString("x")
			return nil, err
		}),
		types.WithVersionProbe(func(_ context.Context, dir string) (string, error) {
			return readFile(t, filepath.Join(dir, "VERSION")), nil
		}),
	)
	project.DefaultSpellRegistry().RegisterSpell(fake)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell("faketool") })

	// Register the project explicitly via a magusfile instead of marker-based auto-detection.
	if err := os.WriteFile(filepath.Join(projDir, "magusfile.buzz"), []byte(
		`import "magus";`+"\n"+
			`magus.project.register("svc", fun(p, cb) > bool { cb({"spells": [magus.spell.get("faketool")]}); return true; });`+"\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	m, err := magus.Open(ctx, root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = m.Close() }()

	targets, err := m.ExpandPath(types.Target{Name: "build"})
	if err != nil {
		t.Fatalf("ExpandPath: %v", err)
	}
	count := func() int { return len(readFile(t, filepath.Join(projDir, "count"))) }

	// Run 1: cache miss → the spell runs once.
	if err := m.Run(ctx, targets); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if got := count(); got != 1 {
		t.Fatalf("after run 1: count=%d, want 1", got)
	}

	// Run 2: identical inputs → cache hit → the spell is skipped.
	if err := m.Run(ctx, targets); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if got := count(); got != 1 {
		t.Fatalf("after run 2 (expected cache hit): count=%d, want 1", got)
	}

	// Toolchain upgrade: the probe now returns a new version → key changes →
	// miss → the spell runs again.
	if err := os.WriteFile(versionFile, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Run(ctx, targets); err != nil {
		t.Fatalf("run 3: %v", err)
	}
	if got := count(); got != 2 {
		t.Fatalf("after run 3 (toolchain change → expected miss): count=%d, want 2", got)
	}
}

// TestExplicitRegisterDoesNotDoubleBind guards the idempotence of auto-bind: a
// magusfile that explicitly binds the magusfile spell must not also get an
// auto-bound copy, or its target would run twice.
func TestExplicitRegisterDoesNotDoubleBind(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "svc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `import "magus";
import "os";
magus.project.register("svc", fun(p, cb) > bool { cb({"spells": [magus.spell.get("magusfile")]}); return true; });
export fun hit(_args: [str]) > void {
    os.execSh("printf x >> count", "");
}
`
	if err := os.WriteFile(filepath.Join(dir, "magusfile.buzz"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	m, err := magus.Open(ctx, root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = m.Close() }()

	targets, err := m.ExpandPath(types.Target{Name: "hit"})
	if err != nil {
		t.Fatalf("ExpandPath: %v", err)
	}
	if err := m.Run(ctx, targets); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(readFile(t, filepath.Join(dir, "count"))); got != 1 {
		t.Errorf("target ran %d times, want 1 (double-bind regression)", got)
	}
}

// TestBuiltinSpellVersionProbeIsDataDriven proves the version probe is wired
// from each spell's Teal-declared version_cmd (via spells.json), not a Go table:
// "go" declares version_cmd and gets a probe; "json" declares none and gets no
// probe (so it never touches the cache key).
func TestBuiltinSpellVersionProbeIsDataDriven(t *testing.T) {
	goSpell, ok := project.DefaultSpellRegistry().Lookup("go")
	if !ok {
		t.Fatal("go spell not registered")
	}
	if !goSpell.HasVersionProbe() {
		t.Error("go spell has no version probe; meta.version_cmd is not wired")
	}
	jsonSpell, ok := project.DefaultSpellRegistry().Lookup("json")
	if ok && jsonSpell.HasVersionProbe() {
		t.Error("json spell unexpectedly has a version probe (it declares no version_cmd)")
	}
}

// TestRunWithReportWriter verifies the public WithReportWriter option: an
// embedder passing a plain io.Writer receives JSONL run events without
// importing any internal package, and the engine flushes/closes the writer it
// owns by the time Run returns.
func TestRunWithReportWriter(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, "svc", "import \"magus\";\nexport fun build(_args: [str]) > void {}\n")

	ctx := context.Background()
	m, err := magus.Open(ctx, root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = m.Close() }()

	targets, err := m.ExpandPath(types.Target{Name: "build"})
	if err != nil {
		t.Fatalf("ExpandPath: %v", err)
	}
	var buf bytes.Buffer
	if err := m.Run(ctx, targets, magus.WithReportWriter(&buf)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	if out == "" {
		t.Fatal("WithReportWriter produced no output (writer not wired or not flushed)")
	}
	if !strings.Contains(out, "svc") {
		t.Errorf("report output missing project; got %q", out)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// TestRunCIComposesMagusfileTarget verifies ci is an ordinary magusfile target:
// the magusfile defines `ci` and composes the pipeline with magus.depends_on, so
// running it executes the declared steps. magus no longer hardcodes the chain —
// it only forces read-only mode. Step ordering is the magusfile's concern (here
// test depends on build), so the recorded order is build-then-test.
func TestRunCIComposesMagusfileTarget(t *testing.T) {
	root := t.TempDir()
	body := `
import "magus";
import "os";
fun record(name: str) > void {
    os.execSh("printf '%s\n' " + name + " >> ci-order", "");
}
export fun build(_args: [str]) > void { record("build"); }
export fun test(_args: [str]) > void {
    magus.depends_on(["build"]);
    record("test");
}
export fun ci(_args: [str]) > void {
    magus.depends_on(["test"]);
}
`
	writeProject(t, root, "svc", body)

	ctx := context.Background()
	m, err := magus.Open(ctx, root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = m.Close() }()

	targets, err := m.ExpandPath(types.Target{Name: "ci"})
	if err != nil {
		t.Fatalf("ExpandPath: %v", err)
	}
	if err := m.RunCI(ctx, targets); err != nil {
		t.Fatalf("RunCI: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(root, "svc", "ci-order"))
	if err != nil {
		t.Fatalf("ci-order log not written: %v", err)
	}
	if string(got) != "build\ntest\n" {
		t.Errorf("CI step order = %q, want %q", got, "build\ntest\n")
	}
}
