package magus

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/symbols"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

func TestDueToIndex(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	quiet := 60 * time.Second
	minInterval := 5 * time.Minute

	cases := []struct {
		name string
		st   *projIndexState
		now  time.Time
		want bool
	}{
		{"nil", nil, base, false},
		{"clean", &projIndexState{dirty: false, lastChange: base}, base.Add(time.Hour), false},
		{"within quiet window", &projIndexState{dirty: true, lastChange: base}, base.Add(30 * time.Second), false},
		{"quiet elapsed, never run", &projIndexState{dirty: true, lastChange: base}, base.Add(90 * time.Second), true},
		{"min interval not elapsed", &projIndexState{dirty: true, lastChange: base, lastRun: base.Add(time.Minute)}, base.Add(2 * time.Minute), false},
		{"min interval elapsed", &projIndexState{dirty: true, lastChange: base, lastRun: base}, base.Add(6 * time.Minute), true},
		{"in backoff", &projIndexState{dirty: true, lastChange: base, backoffTill: base.Add(10 * time.Minute)}, base.Add(2 * time.Minute), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, dueToIndex(c.st, c.now, quiet, minInterval))
		})
	}
}

func TestBackoffDuration(t *testing.T) {
	assert.Equal(t, time.Duration(0), backoffDuration(0))
	assert.Equal(t, symbolIndexBackoffBase, backoffDuration(1))
	assert.Equal(t, 2*symbolIndexBackoffBase, backoffDuration(2))
	assert.Equal(t, 4*symbolIndexBackoffBase, backoffDuration(3))
	// Grows exponentially but is capped, and a huge failure count cannot overflow the
	// shift into a negative/zero duration.
	assert.Equal(t, symbolIndexBackoffMax, backoffDuration(100))
	assert.LessOrEqual(t, backoffDuration(60), symbolIndexBackoffMax)
}

func TestMatchProject(t *testing.T) {
	sep := string(filepath.Separator)
	root := sep + "ws"
	projects := []capableProject{
		{path: ".", dir: root},
		{path: "gopherbuzz", dir: root + sep + "gopherbuzz"},
		{path: "foo", dir: root + sep + "foo"},
	}
	cases := []struct {
		file string
		want string
		ok   bool
	}{
		{root + sep + "gopherbuzz" + sep + "compiler.go", "gopherbuzz", true}, // nested wins over root
		{root + sep + "main.go", ".", true},                                   // root project
		{root + sep + "foobar" + sep + "x.go", ".", true},                     // "foo" must not claim "foobar"
		{sep + "elsewhere" + sep + "x.go", "", false},                         // outside every project
	}
	for _, c := range cases {
		got, ok := matchProject(c.file, projects)
		assert.Equal(t, c.ok, ok, "ok for %q", c.file)
		assert.Equal(t, c.want, got, "owner of %q", c.file)
	}
}

// newTestIndexer builds a symbolIndexer with a controllable clock and a recording
// runIndex, wired idle and uncontended by default.
func newTestIndexer(t *testing.T) (*symbolIndexer, *[]string, *time.Time) {
	t.Helper()
	clock := time.Unix(2_000_000, 0)
	var mu sync.Mutex
	var runs []string
	si := &symbolIndexer{
		log:            slog.Default(),
		quiet:          60 * time.Second,
		minInterval:    5 * time.Minute,
		now:            func() time.Time { return clock },
		state:          map[string]*projIndexState{},
		projectForPath: func(p string) (string, bool) { return "pkg/a", true },
		runIndex: func(ctx context.Context, project string) error {
			mu.Lock()
			runs = append(runs, project)
			mu.Unlock()
			return nil
		},
		idle:      func() bool { return true },
		contended: func() bool { return false },
	}
	return si, &runs, &clock
}

func TestSymbolIndexerMarkAndPick(t *testing.T) {
	si, _, clock := newTestIndexer(t)

	si.mark([]string{"/ws/pkg/a/a.go"})
	_, ok := si.pickDue()
	assert.False(t, ok, "not due within the quiet window")

	*clock = clock.Add(90 * time.Second)
	proj, ok := si.pickDue()
	require.True(t, ok, "due once the quiet window elapses")
	assert.Equal(t, "pkg/a", proj)
}

func TestSymbolIndexerExecuteSuccess(t *testing.T) {
	si, runs, _ := newTestIndexer(t)
	si.state["pkg/a"] = &projIndexState{dirty: true}

	si.execute(context.Background(), "pkg/a")

	assert.Equal(t, []string{"pkg/a"}, *runs)
	st := si.state["pkg/a"]
	assert.False(t, st.dirty, "a completed run clears dirty")
	assert.Zero(t, st.failures)
	assert.False(t, st.lastRun.IsZero(), "lastRun is stamped")
}

func TestSymbolIndexerExecuteFailureBacksOff(t *testing.T) {
	si, _, _ := newTestIndexer(t)
	si.runIndex = func(ctx context.Context, project string) error { return errors.New("scip-go: not found") }
	si.state["pkg/a"] = &projIndexState{dirty: true}

	si.execute(context.Background(), "pkg/a")

	st := si.state["pkg/a"]
	assert.Equal(t, 1, st.failures)
	assert.True(t, st.dirty, "a failed run stays dirty to retry")
	assert.False(t, st.backoffTill.IsZero(), "a failure sets a backoff deadline")
}

func TestSymbolStatusCache(t *testing.T) {
	val := []types.SymbolIndexStatus{{Project: types.NewProjectRef("pkg/a", "/ws/pkg/a")}}

	var c symbolStatusCache
	// Unwatched: never caches, so it can't go stale without a watcher to invalidate it.
	c.store(val)
	_, ok := c.get()
	assert.False(t, ok, "an unwatched cache holds nothing")

	c.setWatched(true)
	c.store(val)
	got, ok := c.get()
	assert.True(t, ok, "a watched cache serves the stored value")
	assert.Equal(t, val, got)

	c.invalidate()
	_, ok = c.get()
	assert.False(t, ok, "invalidate drops the memo")

	c.store(val)
	c.setWatched(false)
	_, ok = c.get()
	assert.False(t, ok, "dropping the watcher clears and distrusts the cache")
}

func TestSymbolRunError(t *testing.T) {
	base := errors.New("exit status 127")

	withHint := symbolRunError(types.NewProjectRef("docs", "/ws/docs"), "typescript", base)
	assert.ErrorIs(t, withHint, base, "wraps the cause")
	assert.Contains(t, withHint.Error(), "docs", "names the project")
	assert.Contains(t, withHint.Error(), "scip-typescript", "adds the actionable indexer hint")

	// The workspace-root project reads as its repo name, never the bare ".".
	root := symbolRunError(types.NewProjectRef(".", "/ws/magus"), "go", base)
	assert.Contains(t, root.Error(), "magus", "the root project shows its name, not '.'")

	noLang := symbolRunError(types.NewProjectRef("pkg/x", "/ws/pkg/x"), "", base)
	assert.Contains(t, noLang.Error(), "pkg/x")
	assert.NotContains(t, noLang.Error(), "install", "no hint when the language is unknown")
}

func TestSymbolIndexerExecuteYieldNoBackoff(t *testing.T) {
	si, _, _ := newTestIndexer(t)
	// Simulate a run cancelled to yield: the parent context is already cancelled and
	// the indexer returns the context error.
	si.runIndex = func(ctx context.Context, project string) error { return context.Canceled }
	si.state["pkg/a"] = &projIndexState{dirty: true}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	si.execute(ctx, "pkg/a")

	st := si.state["pkg/a"]
	assert.Zero(t, st.failures, "yielding is not a failure")
	assert.True(t, st.dirty, "a yielded run stays dirty to retry")
	assert.True(t, st.backoffTill.IsZero(), "yielding sets no backoff")
}

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
