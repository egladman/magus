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
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	// Link the host bindings so magusfile.buzz targets execute.
	_ "github.com/egladman/magus/internal/interp/bindings"
)

// writeProject creates root/name/magusfile.buzz with body. No magus.project
// call is written: a bare magusfile that defines targets is expected to run via
// the auto-bound magusfile spell.
func writeProject(t *testing.T, root, name, body string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), []byte(body), 0o644))
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
export fun alpha(ctx: magus\Context, args: [str]) > void !> any {
    fs.writeFile("ran-alpha", "1");
}
export fun beta(ctx: magus\Context, args: [str]) > void !> any {
    fs.writeFile("ran-beta", "1");
}
`
	for _, name := range []string{"svc-a", "svc-b"} {
		writeProject(t, root, name, body)
	}

	ctx := context.Background()
	m, err := magus.Open(ctx, root)
	require.NoError(t, err, "Open")
	defer func() { _ = m.Close() }()

	alpha, err := m.ExpandPath(types.Target{Name: "alpha"})
	require.NoError(t, err, "ExpandPath alpha")
	beta, err := m.ExpandPath(types.Target{Name: "beta"})
	require.NoError(t, err, "ExpandPath beta")
	require.NoError(t, m.Run(ctx, append(alpha, beta...)), "Run")

	for _, svc := range []string{"svc-a", "svc-b"} {
		for _, tgt := range []string{"alpha", "beta"} {
			p := filepath.Join(root, svc, "ran-"+tgt)
			_, err := os.Stat(p)
			assert.NoErrorf(t, err, "expected %s:%s to have run (missing %s)", svc, tgt, p)
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
	require.NoError(t, os.MkdirAll(projDir, 0o755))
	versionFile := filepath.Join(projDir, "VERSION")
	require.NoError(t, os.WriteFile(versionFile, []byte("v1"), 0o644))

	fake := spells.NewSpell(
		"faketool",
		spells.WithInvoker(func(_ context.Context, req spells.InvokeRequest) (any, error) {
			f, err := os.OpenFile(filepath.Join(req.Dir, "count"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				return nil, err
			}
			defer f.Close()
			_, err = f.WriteString("x")
			return nil, err
		}),
		spells.WithTools(map[string]spells.Tool{"tool": {Probe: spells.Command{Bin: "tool", Args: []string{"--version"}}}}),
		spells.WithVersionProber(func(_ context.Context, _ spells.Command, dir string) (string, error) {
			return readFile(t, filepath.Join(dir, "VERSION")), nil
		}),
	)
	project.DefaultSpellRegistry().RegisterSpell(fake)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell("faketool") })

	// Register the project explicitly via a magusfile instead of marker-based auto-detection.
	require.NoError(t, os.WriteFile(filepath.Join(projDir, "magusfile.buzz"), []byte(
		`import "magus";`+"\n"+
			`import "magus/spell/faketool";`+"\n"+
			`magus.project("svc", {"spells": [faketool]});`+"\n",
	), 0o644))

	ctx := context.Background()
	m, err := magus.Open(ctx, root)
	require.NoError(t, err, "Open")
	defer func() { _ = m.Close() }()

	targets, err := m.ExpandPath(types.Target{Name: "build"})
	require.NoError(t, err, "ExpandPath")
	count := func() int { return len(readFile(t, filepath.Join(projDir, "count"))) }

	// Run 1: cache miss → the spell runs once.
	require.NoError(t, m.Run(ctx, targets), "run 1")
	require.Equal(t, 1, count(), "after run 1")

	// Run 2: identical inputs → cache hit → the spell is skipped.
	require.NoError(t, m.Run(ctx, targets), "run 2")
	require.Equal(t, 1, count(), "after run 2 (expected cache hit)")

	// Toolchain upgrade: the probe now returns a new version → key changes →
	// miss → the spell runs again.
	require.NoError(t, os.WriteFile(versionFile, []byte("v2"), 0o644))
	require.NoError(t, m.Run(ctx, targets), "run 3")
	require.Equal(t, 2, count(), "after run 3 (toolchain change → expected miss)")
}

// TestMagusfileTargetsRunWithoutBeingDeclared is the contract that replaced the
// old double-bind guard: a magusfile declares no spell at all and its own targets
// still run, exactly once. Binding the magusfile driver is magus's job - it is
// what makes the file runnable, not a toolchain the author opts into.
func TestMagusfileTargetsRunWithoutBeingDeclared(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "svc")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	src := `import "magus";
import "os";
import "proc";
magus.project("svc", {});
export fun hit(ctx: magus\Context, args: [str]) > void !> any {
    final c = proc.shell("printf x >> count");
    proc.exec(c.bin, c.args, "", {});
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), []byte(src), 0o644))

	ctx := context.Background()
	m, err := magus.Open(ctx, root)
	require.NoError(t, err, "Open")
	defer func() { _ = m.Close() }()

	targets, err := m.ExpandPath(types.Target{Name: "hit"})
	require.NoError(t, err, "ExpandPath")
	require.NoError(t, m.Run(ctx, targets), "Run")
	assert.Equal(t, 1, len(readFile(t, filepath.Join(dir, "count"))), "target ran more than once (double-bind regression)")
}

// TestDeclaringMagusfileAsASpellIsRejected pins the migration. Declaring it was a
// no-op that still taught every reader magusfile was a toolchain adapter, so it
// now fails with MGS1017 and the fix rather than being silently ignored.
//
// The import is the case a real magusfile hits: the `"spells": [magusfile]` entry
// can only be written with the handle in scope, and the handle only comes from
// this import, so the import check is what fires. The list check behind it stays
// as the backstop for a locally-defined spell that names itself "magusfile".
func TestDeclaringMagusfileAsASpellIsRejected(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "svc")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	src := `import "magus";
import "magus/spell/magusfile";
magus.project("svc", {"spells": [magusfile]});
export fun hit(ctx: magus\Context, args: [str]) > void {}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), []byte(src), 0o644))

	_, err := magus.Open(context.Background(), root)
	require.Error(t, err, "declaring the magusfile driver must fail")
	assert.Contains(t, err.Error(), string(types.MagusfileIsNotASpell))
	assert.Contains(t, err.Error(), "magusfile is not a spell")
	assert.Contains(t, err.Error(), "delete the", "the error must carry the fix")
}

// TestBuiltinSpellVersionProbeIsDataDriven proves the version probe is wired
// from each spell's Teal-declared version_cmd (via spells.json), not a Go table:
// "go" declares version_cmd and gets a probe; "json" declares none and gets no
// probe (so it never touches the cache key).
func TestBuiltinSpellVersionProbeIsDataDriven(t *testing.T) {
	goSpell, ok := project.DefaultSpellRegistry().Lookup("go")
	require.True(t, ok, "go spell not registered")
	assert.True(t, goSpell.HasVersionProbe(), "go spell has no version probe; mgs_getTools is not wired")

	jsonSpell, ok := project.DefaultSpellRegistry().Lookup("json")
	if ok {
		assert.False(t, jsonSpell.HasVersionProbe(), "json spell unexpectedly has a version probe (it declares no tools)")
	}
}

// TestRunWithReportWriter verifies the public WithReportWriter option: an
// embedder passing a plain io.Writer receives JSONL run events without
// importing any internal package, and the engine flushes/closes the writer it
// owns by the time Run returns.
func TestRunWithReportWriter(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, "svc", "import \"magus\";\nexport fun build(ctx: magus\\Context, args: [str]) > void {}\n")

	ctx := context.Background()
	m, err := magus.Open(ctx, root)
	require.NoError(t, err, "Open")
	defer func() { _ = m.Close() }()

	targets, err := m.ExpandPath(types.Target{Name: "build"})
	require.NoError(t, err, "ExpandPath")
	var buf bytes.Buffer
	require.NoError(t, m.Run(ctx, targets, magus.WithReportWriter(&buf)), "Run")
	out := buf.String()
	require.NotEmpty(t, out, "WithReportWriter produced no output (writer not wired or not flushed)")
	assert.Contains(t, out, "svc", "report output missing project")
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
// the magusfile defines `ci` and composes the pipeline with magus.needs, so
// running it executes the declared steps. magus no longer hardcodes the chain —
// it only forces read-only mode. Step ordering is the magusfile's concern (here
// test depends on build), so the recorded order is build-then-test.
func TestRunCIComposesMagusfileTarget(t *testing.T) {
	root := t.TempDir()
	body := `
import "magus";
import "os";
import "proc";
fun record(name: str) > void !> any {
    final c = proc.shell("printf '%s\n' " + name + " >> ci-order");
    proc.exec(c.bin, c.args, "", {});
}
export fun build(ctx: magus\Context, args: [str]) > void !> any { record("build"); }
export fun test(ctx: magus\Context, args: [str]) > void !> any {
    ctx.needs(build);
    record("test");
}
export fun ci(ctx: magus\Context, args: [str]) > void {
    ctx.needs(test);
}
`
	writeProject(t, root, "svc", body)

	ctx := context.Background()
	m, err := magus.Open(ctx, root)
	require.NoError(t, err, "Open")
	defer func() { _ = m.Close() }()

	targets, err := m.ExpandPath(types.Target{Name: "ci"})
	require.NoError(t, err, "ExpandPath")
	require.NoError(t, m.RunCI(ctx, targets), "RunCI")

	got, err := os.ReadFile(filepath.Join(root, "svc", "ci-order"))
	require.NoError(t, err, "ci-order log not written")
	assert.Equal(t, "build\ntest\n", string(got), "CI step order")
}

// TestNestedSDKRunKnowsItsAncestry is the regression for the CI break this package
// found: shard 0 refused svc-a alpha with MGS3009 because the SDK consumer could not
// name the invocation whose claim had filled the machine budget.
//
// The chain: the shard's own `magus` run claims the machine's memory, then execs `go
// test`, which runs THIS process, which drives magus in-process. Only the CLI and the
// daemon stamp invocation ancestry onto a context, so a library caller has none - and
// admission read the context alone, judged this process a nested magus that had lost
// its ancestry, and refused it against its own parent's claim.
//
// magus puts the ancestry on every op subprocess it starts (run.childEnv), so the
// variable is right here in the environment. This asserts the two travel TOGETHER,
// which is the invariant admission now relies on. It skips when run outside magus,
// where neither is set and there is nothing to be a descendant of.
func TestNestedSDKRunKnowsItsAncestry(t *testing.T) {
	if os.Getenv("MAGUS_LEVEL") == "" {
		t.Skip("not running under magus; there is no ancestry to inherit")
	}
	assert.NotEmpty(t, os.Getenv("MAGUS_INVOCATION_ANCESTORS"),
		"a magus-spawned process inherits MAGUS_LEVEL, so it must inherit the ancestry too;"+
			" without it every in-process SDK run is refused against its own parent's claim")
}
