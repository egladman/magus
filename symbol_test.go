package magus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/proc/environ"
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
		st   *indexState
		now  time.Time
		want bool
	}{
		{"nil", nil, base, false},
		{"clean", &indexState{dirty: false, lastChange: base}, base.Add(time.Hour), false},
		{"within quiet window", &indexState{dirty: true, lastChange: base}, base.Add(30 * time.Second), false},
		{"quiet elapsed, never run", &indexState{dirty: true, lastChange: base}, base.Add(90 * time.Second), true},
		{"min interval not elapsed", &indexState{dirty: true, lastChange: base, lastRun: base.Add(time.Minute)}, base.Add(2 * time.Minute), false},
		{"min interval elapsed", &indexState{dirty: true, lastChange: base, lastRun: base}, base.Add(6 * time.Minute), true},
		{"in backoff", &indexState{dirty: true, lastChange: base, backoffTill: base.Add(10 * time.Minute)}, base.Add(2 * time.Minute), false},
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
	projects := []projectIndex{
		{project: &types.Project{Path: ".", Dir: root}},
		{project: &types.Project{Path: "gopherbuzz", Dir: root + sep + "gopherbuzz"}},
		{project: &types.Project{Path: "foo", Dir: root + sep + "foo"}},
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
// runIndex.
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
		state:          map[indexRef]*indexState{},
		indexesForPath: func(p string) []indexRef { return []indexRef{goIndexA} },
		runIndex: func(ctx context.Context, ref indexRef) error {
			mu.Lock()
			runs = append(runs, indexName(ref))
			mu.Unlock()
			return nil
		},
	}
	return si, &runs, &clock
}

// indexName spells ref as project:op, for asserting which indexes ran.
func indexName(ref indexRef) string { return ref.project + ":" + ref.op }

// goIndexA and buzzIndexA are the two indexes of one project bound to go and buzz.
var (
	goIndexA   = indexRef{project: "pkg/a", op: "scip"}
	buzzIndexA = indexRef{project: "pkg/a", op: "scip-buzz"}
)

func TestSymbolIndexerMarkAndPick(t *testing.T) {
	si, _, clock := newTestIndexer(t)

	si.mark([]string{"/ws/pkg/a/a.go"})
	_, ok := si.pickDue()
	assert.False(t, ok, "not due within the quiet window")

	*clock = clock.Add(90 * time.Second)
	ref, ok := si.pickDue()
	require.True(t, ok, "due once the quiet window elapses")
	assert.Equal(t, goIndexA, ref)
}

func TestSymbolIndexerExecuteSuccess(t *testing.T) {
	si, runs, _ := newTestIndexer(t)
	si.state[goIndexA] = &indexState{dirty: true}

	si.execute(context.Background(), goIndexA)

	assert.Equal(t, []string{"pkg/a:scip"}, *runs)
	st := si.state[goIndexA]
	assert.False(t, st.dirty, "a completed run clears dirty")
	assert.Zero(t, st.failures)
	assert.False(t, st.lastRun.IsZero(), "lastRun is stamped")
}

func TestSymbolIndexerExecuteFailureBacksOff(t *testing.T) {
	si, _, _ := newTestIndexer(t)
	si.runIndex = func(ctx context.Context, ref indexRef) error { return errors.New("scip-go: not found") }
	si.state[goIndexA] = &indexState{dirty: true}

	si.execute(context.Background(), goIndexA)

	st := si.state[goIndexA]
	assert.Equal(t, 1, st.failures)
	assert.True(t, st.dirty, "a failed run stays dirty to retry")
	assert.False(t, st.backoffTill.IsZero(), "a failure sets a backoff deadline")
}

// One edit dirties every index of its project, and a missing scip-buzz backs off only the
// Buzz index: the Go index beside it keeps being rebuilt on schedule.
func TestSymbolIndexerBacksOffOneIndexOfAProject(t *testing.T) {
	si, runs, clock := newTestIndexer(t)
	si.indexesForPath = func(string) []indexRef { return []indexRef{goIndexA, buzzIndexA} }
	si.runIndex = func(ctx context.Context, ref indexRef) error {
		*runs = append(*runs, indexName(ref))
		if ref == buzzIndexA {
			return errors.New(`exec: "scip-buzz": executable file not found in $PATH`)
		}
		return nil
	}

	si.mark([]string{"/ws/pkg/a/a.go"})
	*clock = clock.Add(90 * time.Second)
	for range 2 {
		ref, ok := si.pickDue()
		require.True(t, ok)
		si.execute(t.Context(), ref)
	}

	assert.Equal(t, []string{"pkg/a:scip", "pkg/a:scip-buzz"}, *runs, "both indexes ran, Go first")
	assert.Zero(t, si.state[goIndexA].failures)
	assert.False(t, si.state[goIndexA].dirty)
	assert.True(t, si.state[goIndexA].backoffTill.IsZero(), "the Go index carries no backoff")
	assert.Equal(t, 1, si.state[buzzIndexA].failures, "the Buzz index alone backs off")
	assert.False(t, si.state[buzzIndexA].backoffTill.IsZero())
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

// A reindex refused because the outer run holds the project's lock never reached the
// indexer, so blaming a missing scip-go sent the reader to install a tool they already had.
func TestReindexRefusedByAnAncestorLockGetsNoInstallHint(t *testing.T) {
	refusal := fmt.Errorf("run: %w", types.DiagnosticErrorf(types.ProjectLockHeldByAncestor,
		"project . is locked by the magus run this one is nested inside"))

	err := symbolRunError(types.NewProjectRef(".", "/ws/magus"), "go", refusal)
	require.ErrorIs(t, err, types.ProjectLockHeldByAncestor, "the refusal stays matchable for graph build")
	assert.Contains(t, err.Error(), "magus: ", "names the project")
	assert.NotContains(t, err.Error(), "scip-go", "the indexer was never run, so its install hint is wrong")
	assert.NotContains(t, err.Error(), "rerun once", "rerunning inside the same outer run is refused again")
}

// indexerOnPath puts the fixture indexer on PATH, which installedIndexes requires before
// freshenSymbolIndexes will rebuild its index.
func indexerOnPath(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, indexerBin), []byte("#!/bin/sh\n"), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A reader that freshens the index it reads (magus\precedents, Diff) is called from a
// target's body, and the run that body belongs to holds the project's lock for its whole
// invocation. Rebuilding through m.Run nested a second invocation the lock refused
// (MGS3007), so the reader read a stale index. The rebuild now runs inside the outer run.
func TestFreshenInsideATargetReindexesThroughTheOuterRun(t *testing.T) {
	indexerOnPath(t)
	const readerSpell = "zzz-index-reader-spell"
	var m *Magus
	var freshenErr error
	var seen types.SymbolIndexFreshness
	reader := spells.NewSpell(readerSpell,
		spells.WithTargets("lint"),
		spells.WithInvoker(func(ctx context.Context, req spells.InvokeRequest) (any, error) {
			if req.Target != "lint" {
				return nil, nil
			}
			freshenErr = m.freshenSymbolIndexes(ctx, []string{"."})
			seen = freshness(t, m)
			return nil, nil
		}),
	)
	project.DefaultSpellRegistry().RegisterSpell(reader)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(readerSpell) })

	m, src, _ := newIndexedWorkspaceWith(t, readerSpell)
	require.NoError(t, os.WriteFile(src, []byte("package main\n\nfunc Added() {}\n"), 0o644))
	require.Equal(t, types.SymbolIndexStale, freshness(t, m), "the Go edit leaves the index behind")

	require.NoError(t, m.Run(context.Background(), []types.Target{{Path: ".", Name: "lint"}}))
	require.NoError(t, freshenErr, "the reindex ran under the lock this run holds; a nested run is refused MGS3007")
	assert.Equal(t, types.SymbolIndexFresh, seen, "the reader inside the target sees the root index up to date")
}

// Outside any run a reader still rebuilds the index as its own invocation.
func TestFreshenOutsideARunReindexesThroughItsOwnRun(t *testing.T) {
	indexerOnPath(t)
	m, src := newIndexedWorkspace(t)
	require.NoError(t, os.WriteFile(src, []byte("package main\n\nfunc Added() {}\n"), 0o644))
	require.Equal(t, types.SymbolIndexStale, freshness(t, m))

	require.NoError(t, m.freshenSymbolIndexes(context.Background(), []string{"."}))
	assert.Equal(t, types.SymbolIndexFresh, freshness(t, m))
}

func TestSymbolIndexerExecuteYieldNoBackoff(t *testing.T) {
	si, _, _ := newTestIndexer(t)
	// Simulate a run cancelled to yield: the parent context is already cancelled and
	// the indexer returns the context error.
	si.runIndex = func(ctx context.Context, ref indexRef) error { return context.Canceled }
	si.state[goIndexA] = &indexState{dirty: true}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	si.execute(ctx, goIndexA)

	st := si.state[goIndexA]
	assert.Zero(t, st.failures, "yielding is not a failure")
	assert.True(t, st.dirty, "a yielded run stays dirty to retry")
	assert.True(t, st.backoffTill.IsZero(), "yielding sets no backoff")
}

// newIndexedWorkspace builds a one-project workspace bound to a spell that declares a
// symbol indexer and runs that op once, so the cache holds its manifest and the index sits
// where ingestion looks for one. It returns the workspace and the single source file the
// index's key covers.
//
// The op body is writesTheIndex: what is under test is the FRESHNESS question, which reads
// the cache manifest and the index, and a real indexer would only make the fixture depend
// on an installed binary.
func newIndexedWorkspace(t *testing.T) (*Magus, string) {
	t.Helper()
	m, src, _ := newIndexedWorkspaceWith(t)
	return m, src
}

// indexerBin is the fixture indexer's binary. It is on no PATH, so its observation is
// never cached and every probe reads the version the test set.
const indexerBin = "zzz-scip-freshness-indexer"

// newIndexedWorkspaceWith is newIndexedWorkspace that also binds the root project to a
// second spell claiming the installed skills under .claude/skills, the way the real root
// project does, and hands back the indexer version its observe probe reports. extra names
// further registered spells to bind the root project to.
func newIndexedWorkspaceWith(t *testing.T, extra ...string) (*Magus, string, *string) {
	t.Helper()
	const spellName = "zzz-scip-freshness-test-spell"
	const skillsSpell = "zzz-scip-freshness-skills-spell"
	version := "1.0.0"
	spell := spells.NewSpell(spellName,
		spells.WithTargets(spells.DefaultSymbolIndexOp),
		spells.WithSymbolIndexer(&spells.SymbolIndexer{Format: spells.SymbolFormatSCIP, Command: spells.Command{Bin: indexerBin}}),
		spells.WithOps(map[string]spells.Op{
			spells.DefaultSymbolIndexOp: {Kind: spells.OpKindSymbolIndex, Command: spells.Command{Bin: indexerBin}},
		}),
		spells.WithSources("**/*.go"),
		// The observed indexer is not decoration: its version is a key input the run
		// scheduler stamps and buildStep does not, so without one every assertion here
		// would hold just as well for the broken probe this fixture exists to catch. The
		// probed toolchain is there to prove the scip step does NOT key on it.
		spells.WithTools(map[string]spells.Tool{
			indexerBin:      {Observe: spells.Command{Bin: indexerBin, Args: []string{"--version"}}},
			"zzz-toolchain": {Probe: spells.Command{Bin: "zzz-toolchain", Args: []string{"--version"}}},
		}),
		spells.WithVersionProber(func(_ context.Context, cmd spells.Command, _ string) (string, error) {
			if cmd.Bin == indexerBin {
				return version, nil
			}
			return "toolchain " + version, nil
		}),
		spells.WithInvoker(writesTheIndex),
	)
	skills := spells.NewSpell(skillsSpell, spells.WithSources(".claude/skills/**/SKILL.md"))
	project.DefaultSpellRegistry().RegisterSpell(spell)
	project.DefaultSpellRegistry().RegisterSpell(skills)
	t.Cleanup(func() {
		project.DefaultSpellRegistry().UnregisterSpell(spellName)
		project.DefaultSpellRegistry().UnregisterSpell(skillsSpell)
	})

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))
	src := filepath.Join(root, "main.go")
	require.NoError(t, os.WriteFile(src, []byte("package main\n"), 0o644))
	skill := filepath.Join(root, ".claude", "skills", "magus-query", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(skill), 0o755))
	require.NoError(t, os.WriteFile(skill, []byte("# query\n"), 0o644))

	reg := NewWorkspaceRegistry()
	opts := []ProjectOption{WithSpell(spellName), WithSpell(skillsSpell)}
	for _, name := range extra {
		opts = append(opts, WithSpell(name))
	}
	reg.RegisterProject(".", opts...)
	m, err := Open(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	ctx := context.Background()
	require.NoError(t, m.Run(ctx, []types.Target{{Path: ".", Name: spells.DefaultSymbolIndexOp}}), "scip run")
	return m, src, &version
}

// writesTheIndex is a symbol indexer body that writes a fixed index where dispatch tells a
// real indexer to, inside the run, so the entry the run files records it.
func writesTheIndex(ctx context.Context, req spells.InvokeRequest) (any, error) {
	index := symbols.IndexPath(cache.FromContext(ctx).Dir(), req.Dir, req.Target)
	if err := os.MkdirAll(filepath.Dir(index), 0o755); err != nil {
		return nil, err
	}
	return nil, os.WriteFile(index, []byte("scip"), 0o644)
}

func freshness(t *testing.T, m *Magus) types.SymbolIndexFreshness {
	t.Helper()
	for _, s := range m.SymbolIndexStatus(context.Background()) {
		if s.Project.Path == "." {
			return s.Freshness
		}
	}
	t.Fatal("the root project is missing from SymbolIndexStatus")
	return ""
}

// The probe has to find the manifest the run in newIndexedWorkspace just wrote. Nothing
// else asserted that, which is how the probe came to hash a step no run ever mints
// (buildStep without applyRunKeying): the lookup missed every time, every built index read
// as out-of-date, and `magus status` said so permanently with nothing to contradict it.
func TestSymbolIndexFreshnessFindsTheManifestTheRunWrote(t *testing.T) {
	m, _ := newIndexedWorkspace(t)
	assert.Equal(t, types.SymbolIndexFresh, freshness(t, m),
		"an index built from the current sources is up to date")
}

// The bug this pins: freshness used to be two mtimes, and `format` and `generate` rewrite
// files with identical bytes on every run, so an index magus had just built read as
// out-of-date and `magus graph build` could not clear it (a replayed scip run does not
// rewrite the index, so its mtime never caught up). The cache compares content, so a
// rewrite that changes no bytes is not a change.
func TestSymbolIndexFreshnessIgnoresAnIdenticalRewrite(t *testing.T) {
	m, src := newIndexedWorkspace(t)
	require.Equal(t, types.SymbolIndexFresh, freshness(t, m))

	body, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(src, body, 0o644))
	later := time.Now().Add(time.Hour)
	require.NoError(t, os.Chtimes(src, later, later))

	assert.Equal(t, types.SymbolIndexFresh, freshness(t, m),
		"a file rewritten with identical bytes is not a source change")
}

// The other direction, so the probe is not merely always-fresh: real new bytes must still
// report out-of-date, or the banner and the symbol-search deny that reads it are dead.
func TestSymbolIndexFreshnessSeesChangedBytes(t *testing.T) {
	m, src := newIndexedWorkspace(t)
	require.Equal(t, types.SymbolIndexFresh, freshness(t, m))

	require.NoError(t, os.WriteFile(src, []byte("package main\n\nfunc Added() {}\n"), 0o644))

	assert.Equal(t, types.SymbolIndexStale, freshness(t, m),
		"a definition added since the index was built is not in it")
}

// The freshness probe must hash the step a run MINTS. buildStep alone omits the tool
// versions and probed observations the run scheduler stamps, and both are cache-key
// inputs, so a probe that skipped applyRunKeying looked up a key no run had ever written
// and reported every built index out-of-date forever.
func TestSymbolIndexStepKeysLikeTheRunThatBuiltIt(t *testing.T) {
	m, _ := newIndexedWorkspace(t)
	ctx := context.Background()

	p := m.Get(".")
	require.NotNil(t, p)
	ps := []*types.Project{p}
	toolVersions, err := m.toolVersionsByProject(ctx, ps)
	require.NoError(t, err)
	step := m.symbolIndexStep(p, spells.DefaultSymbolIndexOp, toolVersions[p.Path], m.probeObservations(ctx, ps, indexesDriven(projectIndexes(p)))[p.Path])
	probeKey, _, err := m.cache.StepKey(ctx, &step)
	require.NoError(t, err)

	// ReindexSymbols runs the op with no RunOptions, so the key it mints is the charmless one.
	runKey, _, err := m.ComputeTargetKey(ctx, ".", spells.DefaultSymbolIndexOp, nil)
	require.NoError(t, err)

	assert.Equal(t, runKey, probeKey)

	// And the shape the probe used to have, so a revert to it fails here rather than
	// quietly reporting every index out-of-date.
	bare := m.buildStep(p, spells.DefaultSymbolIndexOp)
	bareKey, _, err := m.cache.StepKey(ctx, &bare)
	require.NoError(t, err)
	assert.NotEqual(t, runKey, bareKey, "buildStep alone is not the key any run mints")
}

// The index reads the language's sources. Installed skills sit in the root project's
// source set, and a skill install rewrites them; that marked a Go index out of date
// when no symbol could have moved, and `magus explain` exited 1 over it.
func TestSymbolIndexIgnoresAnInstalledSkillRewrite(t *testing.T) {
	m, _ := newIndexedWorkspace(t)
	ctx := context.Background()
	scipKey := func() string {
		key, _, err := m.ComputeTargetKey(ctx, ".", spells.DefaultSymbolIndexOp, nil)
		require.NoError(t, err)
		return key
	}
	projectKey := func() string {
		step := m.buildStep(m.Get("."), "build")
		key, _, err := m.cache.StepKey(ctx, &step)
		require.NoError(t, err)
		return key
	}
	scipBefore, projectBefore := scipKey(), projectKey()

	skill := filepath.Join(m.Root(), ".claude", "skills", "magus-query", "SKILL.md")
	require.NoError(t, os.WriteFile(skill, []byte("# query, reinstalled\n"), 0o644))

	assert.NotEqual(t, projectBefore, projectKey(), "the skill is one of the project's sources")
	assert.Equal(t, scipBefore, scipKey(), "but not one the indexer reads")
	assert.Equal(t, types.SymbolIndexFresh, freshness(t, m))
}

// The other half of keying the indexer on itself: an upgraded indexer writes a different
// index, so the old one is out of date. The spell's toolchain is not what the op runs, so
// its version does not key the index.
func TestSymbolIndexKeysOnTheIndexerVersionNotTheToolchain(t *testing.T) {
	m, _, version := newIndexedWorkspaceWith(t)
	require.Equal(t, types.SymbolIndexFresh, freshness(t, m))

	p := m.Get(".")
	step := m.symbolIndexStep(p, spells.DefaultSymbolIndexOp, []string{"zzz:zzz-toolchain:9.9.9"}, nil)
	assert.Empty(t, step.ToolVersions, "the scip step drops the spell's tool versions")

	*version = "2.0.0"
	assert.Equal(t, types.SymbolIndexStale, freshness(t, m), "a new indexer version stales the index")
}

// A missing scip-buzz is a gap in one index, never a failure of anything else: the Go
// index beside it still builds, a target that is not the Buzz indexer runs although the
// scip-buzz observation cannot be probed, and the gap and the status name the install.
func TestMissingBuzzIndexerFailsOnlyItsOwnIndex(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no scip-buzz, wherever this runs
	const goSpell = "zzz-missing-buzz-go-spell"
	goOp := spells.DefaultSymbolIndexOp
	missing := errors.New(`exec: "scip-buzz": executable file not found in $PATH`)
	buzzProbes := 0
	goSp := spells.NewSpell(goSpell,
		spells.WithLanguage("go"), spells.WithSources("**/*.go"), spells.WithTargets(goOp, "build"),
		spells.WithSymbolIndexer(&spells.SymbolIndexer{Format: spells.SymbolFormatSCIP, Command: spells.Command{Bin: indexerBin}}),
		spells.WithOps(map[string]spells.Op{goOp: {Kind: spells.OpKindSymbolIndex, Command: spells.Command{Bin: indexerBin}}}),
		spells.WithInvoker(func(ctx context.Context, req spells.InvokeRequest) (any, error) {
			if req.Target == goOp {
				return writesTheIndex(ctx, req)
			}
			return nil, nil
		}),
	)
	buzzSp := spells.NewSpell("buzz",
		spells.WithLanguage("buzz"), spells.WithSources("**/*.buzz"), spells.WithTargets("scip-buzz"),
		spells.WithSymbolIndexer(&spells.SymbolIndexer{Format: spells.SymbolFormatSCIP, Op: "scip-buzz", Command: spells.Command{Bin: "scip-buzz"}}),
		spells.WithOps(map[string]spells.Op{"scip-buzz": {Kind: spells.OpKindSymbolIndex, Command: spells.Command{Bin: "scip-buzz"}}}),
		spells.WithTools(map[string]spells.Tool{"scip-buzz": {Observe: spells.Command{Bin: "scip-buzz", Args: []string{"--version"}}}}),
		spells.WithVersionProber(func(context.Context, spells.Command, string) (string, error) {
			buzzProbes++
			return "", missing
		}),
		spells.WithInvoker(func(ctx context.Context, req spells.InvokeRequest) (any, error) {
			if req.Target == "scip-buzz" {
				return nil, missing
			}
			return nil, nil
		}),
	)
	for _, sp := range []*spells.Spell{goSp, buzzSp} {
		project.DefaultSpellRegistry().RegisterSpell(sp)
		t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(sp.Name()) })
	}
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644))
	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(goSpell), WithSpell("buzz"))
	m, err := Open(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })
	ctx := context.Background()

	built, err := m.ReindexSymbols(ctx)
	assert.Equal(t, 1, built, "the Go index builds")
	require.Error(t, err)
	assert.Contains(t, err.Error(), symbols.InstallHint("buzz"), "the Buzz failure names its fix")
	assert.NotContains(t, err.Error(), indexerBin)

	require.NoError(t, m.Run(ctx, []types.Target{{Path: ".", Name: "build"}}), "a target that is not the Buzz indexer runs")

	buzzProbes = 0
	status := map[string]types.SymbolIndexStatus{}
	for _, s := range m.SymbolIndexStatus(ctx) {
		status[s.Language] = s
	}
	assert.Zero(t, buzzProbes, "judging the Go index never forks scip-buzz, which would warn on every read")
	assert.Equal(t, types.SymbolIndexFresh, status["go"].Freshness)
	assert.Equal(t, types.SymbolIndexStatus{
		Project: types.NewProjectRef(".", m.Get(".").Dir), Op: "scip-buzz", Language: "buzz",
		Freshness: types.SymbolIndexNotBuilt, Detail: symbols.MissingIndexerHint("buzz", "scip-buzz"),
	}, status["buzz"])

	gaps, ok := SymbolGaps(ctx, m, root, m.cfg, nil)
	require.True(t, ok)
	assert.Equal(t, []types.KnowledgeSymbolGap{{
		Project: types.NewProjectRef(".", m.Get(".").Dir), Language: "buzz",
		State: types.SymbolIndexNotBuilt, Hint: symbols.MissingIndexerHint("buzz", "scip-buzz"),
	}}, gaps)
}

// A project with one index gets the install hint too when its indexer is missing, in the
// same shape as a project with two; one whose indexer is installed needs only a build.
func TestSymbolGapNamesTheMissingIndexerOfASingleIndex(t *testing.T) {
	decls := []knowledge.SymbolIndexDeclaration{
		{Project: "web", Dir: "/ws/web", Op: "scip", Bin: "scip-typescript", Language: "typescript", Path: filepath.Join(t.TempDir(), "index.scip")},
		{Project: "svc", Dir: "/ws/svc", Op: "scip", Bin: "scip-go", Language: "go", Path: filepath.Join(t.TempDir(), "index.scip")},
	}
	installed := func(bin string) bool { return bin == "scip-go" }

	assert.Equal(t, []types.KnowledgeSymbolGap{
		{Project: types.NewProjectRef("web", "/ws/web"), Language: "typescript", State: types.SymbolIndexNotBuilt, Hint: symbols.MissingIndexerHint("typescript", "scip-typescript")},
		{Project: types.NewProjectRef("svc", "/ws/svc"), Language: "go", State: types.SymbolIndexNotBuilt},
	}, probeSymbolIndexes(decls, installed))
}

// Whether an indexer is installed is asked of the PATH a run hands its children, not the
// server's own: a run that put scip-buzz on its PATH builds the index, so a review must
// not leave that index out as uninstalled.
func TestInstalledIndexesUsesTheRunsPATH(t *testing.T) {
	server, bin := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(server, "zzz-scip-go"), []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bin, "zzz-scip-go"), []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bin, "zzz-scip-buzz"), []byte("#!/bin/sh\n"), 0o755))
	t.Setenv("PATH", server)
	ctx := environ.With(t.Context())
	environ.From(ctx).Set("PATH", bin)

	p := &types.Project{Path: "."}
	idxs := []projectIndex{{project: p, op: "scip", bin: "zzz-scip-go"}, {project: p, op: "scip-buzz", bin: "zzz-scip-buzz"}}
	assert.Equal(t, idxs, installedIndexes(ctx, idxs))
}

// The indexer's version reaches the scip key and no other target's: it is declared as an
// observation, which keys only the targets whose ops drive the binary. A tool the indexer
// uses (go, for scip-go) keys the scip op the same way and still no build or test.
func TestIndexerObservationKeysOnlyTheScipOp(t *testing.T) {
	sp := spells.NewSpell("go",
		spells.WithSymbolIndexer(&spells.SymbolIndexer{Format: spells.SymbolFormatSCIP, Command: spells.Command{Bin: "scip-go"}, Uses: []string{"go"}}),
		spells.WithOps(map[string]spells.Op{
			spells.DefaultSymbolIndexOp: {Kind: spells.OpKindSymbolIndex, Command: spells.Command{Bin: "scip-go"}},
			"go-build":                  {Command: spells.Command{Bin: "go"}},
		}),
		spells.WithTools(map[string]spells.Tool{
			"go":      {Probe: spells.Command{Bin: "go", Args: []string{"version"}}},
			"scip-go": {Observe: spells.Command{Bin: "scip-go", Args: []string{"--version"}}},
		}),
	)
	p := &types.Project{Path: ".", ResolvedSpells: []*spells.Spell{sp}}
	probed := map[string]string{"go:scip-go": "0.2.7", "go:go": "go version go1.26.6"}

	assert.Equal(t, []string{"go:scip-go:0.2.7", "go:go:go version go1.26.6"}, observationsForTarget(p, spells.DefaultSymbolIndexOp, probed))
	assert.Empty(t, observationsForTarget(p, "go-build", probed), "build keys on go through its version probe, never this line")
	assert.Empty(t, observationsForTarget(p, "test", probed))
	assert.Equal(t, map[string]bool{"go:scip-go": true, "go:go@scip": true}, targetDrivenBins(p, spells.DefaultSymbolIndexOp))
	assert.Equal(t, map[string]bool{"go:go": true}, targetDrivenBins(p, "go-build"))
}

// scip-go loads packages through go, so a toolchain upgrade stales a Go index. The
// TypeScript index beside it declares no such use, so neither the go upgrade nor its own
// toolchain moving touches it.
func TestToolchainUpgradeStalesOnlyTheIndexThatUsesIt(t *testing.T) {
	goVersion, tscVersion := "go1.26.0", "5.9.0"
	prober := func(_ context.Context, cmd spells.Command, _ string) (string, error) {
		switch cmd.Bin {
		case "zzz-uses-go":
			return "go version " + goVersion, nil
		case "zzz-uses-tsc":
			return tscVersion, nil
		default:
			return "indexer 1.0", nil
		}
	}
	indexer := spells.WithInvoker(writesTheIndex)
	goSpell := spells.NewSpell("zzz-uses-go-spell",
		spells.WithTargets(spells.DefaultSymbolIndexOp),
		spells.WithSources("**/*.go"),
		spells.WithSymbolIndexer(&spells.SymbolIndexer{Format: spells.SymbolFormatSCIP, Command: spells.Command{Bin: "zzz-uses-scip-go"}, Uses: []string{"zzz-uses-go"}}),
		spells.WithOps(map[string]spells.Op{spells.DefaultSymbolIndexOp: {Kind: spells.OpKindSymbolIndex, Command: spells.Command{Bin: "zzz-uses-scip-go"}}}),
		spells.WithTools(map[string]spells.Tool{
			"zzz-uses-go":      {Probe: spells.Command{Bin: "zzz-uses-go", Args: []string{"version"}}},
			"zzz-uses-scip-go": {Observe: spells.Command{Bin: "zzz-uses-scip-go", Args: []string{"--version"}}},
		}),
		spells.WithVersionProber(prober), indexer,
	)
	tsSpell := spells.NewSpell("zzz-uses-ts-spell",
		spells.WithTargets(spells.DefaultSymbolIndexOp),
		spells.WithSources("**/*.ts"),
		spells.WithSymbolIndexer(&spells.SymbolIndexer{Format: spells.SymbolFormatSCIP, Command: spells.Command{Bin: "zzz-uses-scip-ts"}}),
		spells.WithOps(map[string]spells.Op{spells.DefaultSymbolIndexOp: {Kind: spells.OpKindSymbolIndex, Command: spells.Command{Bin: "zzz-uses-scip-ts"}}}),
		spells.WithTools(map[string]spells.Tool{
			"zzz-uses-tsc":     {Probe: spells.Command{Bin: "zzz-uses-tsc", Args: []string{"--version"}}},
			"zzz-uses-scip-ts": {Observe: spells.Command{Bin: "zzz-uses-scip-ts", Args: []string{"--version"}}},
		}),
		spells.WithVersionProber(prober), indexer,
	)
	for _, sp := range []*spells.Spell{goSpell, tsSpell} {
		project.DefaultSpellRegistry().RegisterSpell(sp)
		t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(sp.Name()) })
	}

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))
	for dir, file := range map[string]string{"svc": "main.go", "web": "index.ts"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, dir, "magusfile.buzz"), []byte(""), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(root, dir, file), []byte("x\n"), 0o644))
	}
	reg := NewWorkspaceRegistry()
	reg.RegisterProject("svc", WithSpell(goSpell.Name()))
	reg.RegisterProject("web", WithSpell(tsSpell.Name()))
	m, err := Open(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	ctx := context.Background()
	for _, path := range []string{"svc", "web"} {
		require.NoError(t, m.Run(ctx, []types.Target{{Path: path, Name: spells.DefaultSymbolIndexOp}}), "scip run in %s", path)
	}
	status := func() map[string]types.SymbolIndexFreshness {
		out := map[string]types.SymbolIndexFreshness{}
		for _, s := range m.SymbolIndexStatus(ctx) {
			out[s.Project.Path] = s.Freshness
		}
		return out
	}
	require.Equal(t, map[string]types.SymbolIndexFreshness{"svc": types.SymbolIndexFresh, "web": types.SymbolIndexFresh}, status())

	tscVersion = "6.0.0"
	assert.Equal(t, types.SymbolIndexFresh, status()["web"], "the project's own compiler is not what scip-typescript runs")

	goVersion = "go1.27.0"
	assert.Equal(t, map[string]types.SymbolIndexFreshness{"svc": types.SymbolIndexStale, "web": types.SymbolIndexFresh}, status())
}

// A status check probes only what the scip op drives. It passed driven=nil (probe
// everything) and then dropped every observation the op does not drive, so govulncheck's
// database probe forked once per Go project on every graph read and fed nothing.
func TestSymbolStatusForksNoProbeTheScipOpDoesNotDrive(t *testing.T) {
	var mu sync.Mutex
	forked := map[string]int{}
	sp := spells.NewSpell("go",
		spells.WithSymbolIndexer(&spells.SymbolIndexer{Format: spells.SymbolFormatSCIP, Command: spells.Command{Bin: "scip-go"}}),
		spells.WithOps(map[string]spells.Op{
			spells.DefaultSymbolIndexOp: {Kind: spells.OpKindSymbolIndex, Command: spells.Command{Bin: "scip-go"}},
			"govulncheck":               {Command: spells.Command{Bin: "govulncheck"}},
		}),
		spells.WithTools(map[string]spells.Tool{
			"scip-go":     {Observe: spells.Command{Bin: "scip-go", Args: []string{"--version"}}},
			"govulncheck": {Observe: spells.Command{Bin: "govulncheck", Args: []string{"-version"}}},
		}),
		spells.WithVersionProber(func(_ context.Context, cmd spells.Command, _ string) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			forked[cmd.Bin]++
			return "v1", nil
		}),
	)
	binDir := t.TempDir()
	for _, bin := range []string{"scip-go", "govulncheck"} {
		require.NoError(t, os.WriteFile(filepath.Join(binDir, bin), []byte("\x7fELF binary"), 0o755))
	}
	t.Setenv("PATH", binDir)

	root := t.TempDir()
	var ps []*types.Project
	for i := range 7 {
		dir := filepath.Join(root, fmt.Sprintf("p%d", i))
		require.NoError(t, os.MkdirAll(dir, 0o755))
		ps = append(ps, &types.Project{Path: fmt.Sprintf("p%d", i), Dir: dir, ResolvedSpells: []*spells.Spell{sp}})
	}
	m := &Magus{ws: &types.Workspace{Root: root}}
	var idxs []projectIndex
	for _, p := range ps {
		idxs = append(idxs, projectIndexes(p)...)
	}

	got := m.probeObservations(t.Context(), ps, indexesDriven(idxs))
	assert.Equal(t, map[string]int{"scip-go": 1}, forked,
		"one scip-go fork for seven projects that see the same inputs, and no govulncheck")
	assert.Equal(t, "v1", got["p3"]["go:scip-go"])

	m.probeObservations(t.Context(), ps, indexesDriven(idxs))
	assert.Equal(t, map[string]int{"scip-go": 1}, forked, "the second status check is a cache hit")

	m.probeObservations(t.Context(), ps, map[string]map[string]bool{"p0": {"go:govulncheck": true}, "p1": {"go:govulncheck": true}})
	assert.Equal(t, 2, forked["govulncheck"], "the database observation is never cached: it moves on a clock")
}

// An observed tool that is not installed keys UNPROBED without a warning: the index gap and
// any target that runs it already say MGS3003. One that is installed and cannot answer
// still warns, since nothing else would.
func TestProbeObservationsWarnsOnlyForAnInstalledToolThatFails(t *testing.T) {
	sp := spells.NewSpell("buzz",
		spells.WithOps(map[string]spells.Op{"scip-buzz": {Kind: spells.OpKindSymbolIndex, Command: spells.Command{Bin: "scip-buzz"}}}),
		spells.WithTools(map[string]spells.Tool{"scip-buzz": {Observe: spells.Command{Bin: "scip-buzz", Args: []string{"--version"}}}}),
		// Answers as the real prober does: MGS3003 for a binary PATH lacks.
		spells.WithVersionProber(func(_ context.Context, cmd spells.Command, _ string) (string, error) {
			if _, err := exec.LookPath(cmd.Bin); err != nil {
				return "", types.DiagnosticErrorf(types.ToolNotOnPath, "%q is not on PATH", cmd.Bin)
			}
			return "", errors.New("segmentation fault")
		}),
	)
	binDir := t.TempDir()
	t.Setenv("PATH", binDir)
	root := t.TempDir()
	p := &types.Project{Path: ".", Dir: root, ResolvedSpells: []*spells.Spell{sp}}
	m := &Magus{ws: &types.Workspace{Root: root}}
	driven := map[string]map[string]bool{".": {"buzz:scip-buzz": true}}

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	got := m.probeObservations(t.Context(), []*types.Project{p}, driven)
	assert.Equal(t, "UNPROBED", got["."]["buzz:scip-buzz"])
	assert.Empty(t, buf.String(), "a tool that is not installed")

	require.NoError(t, os.WriteFile(filepath.Join(binDir, "scip-buzz"), []byte("\x7fELF binary"), 0o755))
	m.probeObservations(t.Context(), []*types.Project{p}, driven)
	assert.Contains(t, buf.String(), "observation probe failed", "an installed tool that cannot answer")
}

// TestDispatchDueSkipsARunAlreadyInFlight is the one occupancy rule the scheduler still
// keeps: its own. The pool's occupancy is the LIMITER's business, which is FIFO-fair and
// needs no help; busy is only what stops a slow index run being dispatched twice.
//
// Synchronous on purpose. The behaviour is the early return, so asserting it against a
// parked goroutine would let the test pass by winning a scheduling race instead.
func TestDispatchDueSkipsARunAlreadyInFlight(t *testing.T) {
	si, runs, clock := newTestIndexer(t)
	si.busy.Store(true)

	si.mark([]string{"/w/pkg/a/x.go"})
	*clock = clock.Add(2 * si.quiet)
	si.dispatchDue(t.Context())

	assert.Empty(t, *runs, "a run already in flight must not be dispatched again")
	ref, due := si.pickDue()
	assert.True(t, due, "and the project stays due, so the next tick picks it up")
	assert.Equal(t, goIndexA, ref)
}

// indexWorld is freshenIndexes' outside world: which indexes are current, and what a build
// does. current and built name an index by indexName.
type indexWorld struct {
	current  map[string]bool
	built    []string
	buildErr error
	recorded bool // whether a build leaves the cache a record to vouch by
}

func (w *indexWorld) probe(idxs []projectIndex) map[indexRef]bool {
	out := map[indexRef]bool{}
	for _, idx := range idxs {
		out[idx.ref()] = w.current[indexName(idx.ref())]
	}
	return out
}

func (w *indexWorld) build(idx projectIndex) error {
	w.built = append(w.built, indexName(idx.ref()))
	if w.buildErr != nil {
		return w.buildErr
	}
	w.current[indexName(idx.ref())] = w.recorded
	return nil
}

func freshenProjects() []projectIndex {
	return []projectIndex{
		{project: &types.Project{Path: ".", Dir: "/ws"}, op: "scip", language: "go"},
		{project: &types.Project{Path: "web", Dir: "/ws/web"}, op: "scip", language: "typescript"},
	}
}

// A stale index is rebuilt through its own target before the review reads it, and a current
// one is left alone.
func TestFreshenIndexesRebuildsOnlyTheStale(t *testing.T) {
	idxs := freshenProjects()
	w := &indexWorld{current: map[string]bool{"web:scip": true}, recorded: true}

	require.NoError(t, freshenIndexes(idxs, w.probe, w.build, true))

	assert.Equal(t, []string{".:scip"}, w.built)
	assert.True(t, w.current[".:scip"], "the review then reads a current index")
}

func TestFreshenIndexesFailsWithACodeWhenTheIndexerCannotRun(t *testing.T) {
	idxs := freshenProjects()
	w := &indexWorld{current: map[string]bool{}, buildErr: errors.New(`exec: "scip-go": executable file not found`)}

	err := freshenIndexes(idxs, w.probe, w.build, true)

	require.ErrorIs(t, err, types.SymbolIndexNotCurrent)
	assert.Contains(t, err.Error(), "scip-go")
	assert.Contains(t, err.Error(), symbols.InstallHint("go"), "the cause comes with its fix")
}

// Another magus holding the project's lock refuses the refresh at once; the review says so,
// with the holder named, rather than reporting a missing indexer or reading the stale index.
func TestFreshenIndexesNamesTheLockHolderWhenTheRefreshIsRefused(t *testing.T) {
	idxs := freshenProjects()
	held := fmt.Errorf("run .:scip: %w", &lockContendedError{Project: ".", Owner: "pid 4242, `magus run test .`"})
	w := &indexWorld{current: map[string]bool{"web:scip": true}, buildErr: held}

	err := freshenIndexes(idxs, w.probe, w.build, true)

	require.ErrorIs(t, err, types.SymbolIndexNotCurrent)
	assert.Contains(t, err.Error(), "locked by another magus process")
	assert.Contains(t, err.Error(), "pid 4242")
	assert.NotContains(t, err.Error(), symbols.InstallHint("go"), "the indexer never ran, so it is not missing")
}

// A read-only cache runs the indexer and records nothing, so the fresh index is one nothing can
// vouch for: that is the error, never a stale index read as current.
func TestFreshenIndexesFailsWhenTheCacheRecordsNothing(t *testing.T) {
	idxs := freshenProjects()
	w := &indexWorld{current: map[string]bool{"web:scip": true}, recorded: false}

	err := freshenIndexes(idxs, w.probe, w.build, false)

	require.ErrorIs(t, err, types.SymbolIndexNotCurrent)
	assert.Contains(t, err.Error(), "cache writes are off")
}

// A missing scip-buzz must not cost the root project its review: while the Go index's
// indexer is installed the Buzz index is left out, and a project with no indexer installed
// keeps every index, so its review still fails naming what to install.
func TestInstalledIndexesDropsAMissingSecondIndexer(t *testing.T) {
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "zzz-scip-go"), []byte("#!/bin/sh\n"), 0o755))
	t.Setenv("PATH", bin)
	root, docs := &types.Project{Path: "."}, &types.Project{Path: "docs"}
	idxs := []projectIndex{
		{project: root, op: "scip", language: "go", bin: "zzz-scip-go"},
		{project: root, op: "scip-buzz", language: "buzz", bin: "zzz-scip-buzz"},
		{project: docs, op: "scip-buzz", language: "buzz", bin: "zzz-scip-buzz"},
	}

	got := installedIndexes(t.Context(), idxs)

	assert.Equal(t, []projectIndex{idxs[0], idxs[2]}, got)
}

// A touched project with no indexer is named, so "found nothing" is only ever said about
// projects that were checked. A regenerated file is not a change of its own.
func TestUncoveredProjectsAreTheOnesTheChecksCannotSee(t *testing.T) {
	files := []types.DiffFile{
		{Path: "a.go", Project: "."},
		{Path: "docs/x.md", Project: "docs"},
		{Path: "docs/y.md", Project: "docs"},
		{Path: "web/gen/z.ts", Project: "web", Role: types.DiffRoleOutput},
		{Path: "README", Project: ""},
	}

	assert.Equal(t, []types.DiffUncovered{{Project: "docs", Reason: types.DiffUncoveredNoIndexer}},
		uncoveredProjects(files, []string{"."}))
}

func TestDiagnosticOfKeepsTheCode(t *testing.T) {
	d := toDiagnostic(types.DiagnosticErrorf(types.SymbolIndexNotCurrent, "stale"))
	assert.Equal(t, types.Diagnostic{Code: "MGS7003", Message: "stale", URL: d.URL}, d)
	assert.NotEmpty(t, d.URL)
	assert.Equal(t, types.Diagnostic{Message: "plain"}, toDiagnostic(errors.New("plain")))
}

// The index lives in the cache dir, where no replay restores it, so an entry for sources
// the tree went back to must not replay over an index built from the edit: `magus refs`
// then answered with a line the source no longer held, and status called it up to date.
func TestScipReplayNeverKeepsAnIndexOfOtherSources(t *testing.T) {
	const spellName = "zzz-scip-replay-test-spell"
	runs := 0
	spell := spells.NewSpell(spellName,
		spells.WithTargets(spells.DefaultSymbolIndexOp),
		spells.WithSymbolIndexer(&spells.SymbolIndexer{Format: spells.SymbolFormatSCIP}),
		spells.WithSources("**/*.go"),
		spells.WithInvoker(func(ctx context.Context, req spells.InvokeRequest) (any, error) {
			runs++
			body, err := os.ReadFile(filepath.Join(req.Dir, "main.go"))
			if err != nil {
				return nil, err
			}
			index := symbols.IndexPath(cache.FromContext(ctx).Dir(), req.Dir, req.Target)
			if err := os.MkdirAll(filepath.Dir(index), 0o755); err != nil {
				return nil, err
			}
			return nil, os.WriteFile(index, body, 0o644)
		}),
	)
	project.DefaultSpellRegistry().RegisterSpell(spell)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))
	src := filepath.Join(root, "main.go")
	original := []byte("package main\n\nfunc Tokenize() {}\n")
	edited := []byte("package main\n\n// shifts Tokenize down a line\nfunc Tokenize() {}\n")
	require.NoError(t, os.WriteFile(src, original, 0o644))

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	m, err := Open(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })
	index := symbols.IndexPath(resolveCacheDir(m.Root(), m.cfg), m.Root(), spells.DefaultSymbolIndexOp)

	scip := func(source []byte) {
		t.Helper()
		require.NoError(t, os.WriteFile(src, source, 0o644))
		require.NoError(t, m.Run(context.Background(), []types.Target{{Path: ".", Name: spells.DefaultSymbolIndexOp}}), "scip run")
		got, err := os.ReadFile(index)
		require.NoError(t, err)
		require.Equal(t, string(source), string(got), "the index must be built from the sources in the tree")
		require.Equal(t, types.SymbolIndexFresh, freshness(t, m))
	}

	scip(original)
	scip(original)
	assert.Equal(t, 1, runs, "an index the entry recorded replays without rerunning the indexer")

	scip(edited)
	scip(original)
	assert.Equal(t, 3, runs, "reverting the edit reruns the indexer, since the entry for the original sources no longer describes the index")
}
