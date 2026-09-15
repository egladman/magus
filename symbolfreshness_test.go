package magus

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/symbols"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// freshnessWorkspace builds a one-project workspace bound to a spell that exposes the
// reserved scip op, runs that op once so the cache holds its manifest, and writes an index
// where ingestion looks for one. It returns the workspace and the single source file the
// index's key covers.
//
// The op body is a no-op and the index is written by hand: what is under test is the
// FRESHNESS question, which reads the cache manifest and the index's existence, and a real
// indexer would only make the fixture depend on an installed binary.
func freshnessWorkspace(t *testing.T) (*Magus, string) {
	t.Helper()
	const spellName = "zzz-scip-freshness-test-spell"
	spell := spells.NewSpell(spellName,
		spells.WithTargets(symbols.IndexOp),
		spells.WithSources("**/*.go"),
		// A probed tool is not decoration: its version is a key input the run scheduler
		// stamps and buildStep does not, so without one every assertion here would hold
		// just as well for the broken probe this fixture exists to catch. `go` is present
		// wherever these tests run.
		spells.WithTools(map[string]spells.Tool{
			"go": {Probe: spells.Command{Bin: "go", Args: []string{"version"}}},
		}),
		spells.WithInvoker(func(context.Context, spells.InvokeRequest) (any, error) { return nil, nil }),
	)
	project.DefaultSpellRegistry().RegisterSpell(spell)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))
	src := filepath.Join(root, "main.go")
	require.NoError(t, os.WriteFile(src, []byte("package main\n"), 0o644))

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	m, err := Open(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	ctx := context.Background()
	require.NoError(t, m.Run(ctx, []types.Target{{Path: ".", Name: symbols.IndexOp}}), "scip run")

	index := symbols.IndexPath(resolveCacheDir(m.Root(), m.cfg), m.Root())
	require.NoError(t, os.MkdirAll(filepath.Dir(index), 0o755))
	require.NoError(t, os.WriteFile(index, []byte("scip"), 0o644))
	return m, src
}

func freshnessOf(t *testing.T, m *Magus) types.SymbolIndexFreshness {
	t.Helper()
	for _, s := range m.SymbolIndexStatus(context.Background()) {
		if s.Project.Path == "." {
			return s.Freshness
		}
	}
	t.Fatal("the root project is missing from SymbolIndexStatus")
	return ""
}

// The probe has to find the manifest the run in freshnessWorkspace just wrote. Nothing
// else asserted that, which is how the probe came to hash a step no run ever mints
// (buildStep without applyRunKeying): the lookup missed every time, every built index read
// as out-of-date, and `magus status` said so permanently with nothing to contradict it.
func TestSymbolIndexFreshnessFindsTheManifestTheRunWrote(t *testing.T) {
	m, _ := freshnessWorkspace(t)
	assert.Equal(t, types.SymbolIndexFresh, freshnessOf(t, m),
		"an index built from the current sources is up to date")
}

// The bug this pins: freshness used to be two mtimes, and `format` and `generate` rewrite
// files with identical bytes on every run, so an index magus had just built read as
// out-of-date and `magus graph build` could not clear it (a replayed scip run does not
// rewrite the index, so its mtime never caught up). The cache compares content, so a
// rewrite that changes no bytes is not a change.
func TestSymbolIndexFreshnessIgnoresAnIdenticalRewrite(t *testing.T) {
	m, src := freshnessWorkspace(t)
	require.Equal(t, types.SymbolIndexFresh, freshnessOf(t, m))

	body, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(src, body, 0o644))
	later := time.Now().Add(time.Hour)
	require.NoError(t, os.Chtimes(src, later, later))

	assert.Equal(t, types.SymbolIndexFresh, freshnessOf(t, m),
		"a file rewritten with identical bytes is not a source change")
}

// The other direction, so the probe is not merely always-fresh: real new bytes must still
// report out-of-date, or the banner and the symbol-search deny that reads it are dead.
func TestSymbolIndexFreshnessSeesChangedBytes(t *testing.T) {
	m, src := freshnessWorkspace(t)
	require.Equal(t, types.SymbolIndexFresh, freshnessOf(t, m))

	require.NoError(t, os.WriteFile(src, []byte("package main\n\nfunc Added() {}\n"), 0o644))

	assert.Equal(t, types.SymbolIndexStale, freshnessOf(t, m),
		"a definition added since the index was built is not in it")
}

// The freshness probe must hash the step a run MINTS. buildStep alone omits the tool
// versions and probed observations the run scheduler stamps, and both are cache-key
// inputs, so a probe that skipped applyRunKeying looked up a key no run had ever written
// and reported every built index out-of-date forever.
func TestSymbolIndexStepKeysLikeTheRunThatBuiltIt(t *testing.T) {
	m, _ := freshnessWorkspace(t)
	ctx := context.Background()

	p := m.Get(".")
	require.NotNil(t, p)
	step := m.symbolIndexStep(p,
		m.toolVersionsByProject(ctx, []*types.Project{p})[p.Path],
		m.probeObservations(ctx, []*types.Project{p})[p.Path])
	probeKey, _, err := m.cache.StepKey(ctx, &step)
	require.NoError(t, err)

	// ReindexSymbols runs the op with no RunOptions, so the key it mints is the charmless one.
	runKey, _, err := m.ComputeTargetKey(ctx, ".", symbols.IndexOp, nil)
	require.NoError(t, err)

	assert.Equal(t, runKey, probeKey)

	// And the shape the probe used to have, so a revert to it fails here rather than
	// quietly reporting every index out-of-date.
	bare := m.buildStep(p, symbols.IndexOp)
	bareKey, _, err := m.cache.StepKey(ctx, &bare)
	require.NoError(t, err)
	assert.NotEqual(t, runKey, bareKey, "buildStep alone is not the key any run mints")
}
