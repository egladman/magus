package magus

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/observability/otlp"
	"github.com/egladman/magus/types"
)

// TestContainsAll covers the StreamAllSentinel detection used by the
// affected --stdin streaming flow.
func TestContainsAll(t *testing.T) {
	t.Parallel()
	assert.False(t, slices.Contains([]string(nil), StreamAllSentinel), "empty slice")
	assert.True(t, slices.Contains([]string{StreamAllSentinel}, StreamAllSentinel), "only sentinel")
	assert.True(t, slices.Contains([]string{"a", StreamAllSentinel, "b"}, StreamAllSentinel), "sentinel mid")
	assert.False(t, slices.Contains([]string{"a", "b"}, StreamAllSentinel), "no sentinel")
	assert.False(t, slices.Contains([]string{"--all-things"}, StreamAllSentinel), "sentinel-like prefix")
}

// TestMakeHandlerConventionalTargets asserts that makeHandler returns a
// non-nil handler for the seven conventional target names and that invoking
// it on an empty project (no packs) returns nil without panic.
func TestMakeHandlerConventionalTargets(t *testing.T) {
	m := &Magus{}
	p := &types.Project{} // no packs → every handler must return nil cleanly
	ctx := context.Background()
	for _, name := range []string{"preflight", "build", "test", "lint", "format", "clean", "generate"} {
		h := m.makeHandler(name)
		if !assert.NotNilf(t, h, "makeHandler(%q) = nil; expected non-nil handler", name) {
			continue
		}
		assert.NoErrorf(t, h(ctx, p), "makeHandler(%q)(ctx, emptyProject)", name)
	}
	// Arbitrary non-conventional names also get a handler.
	h := m.makeHandler("go-build")
	if assert.NotNil(t, h, "makeHandler(\"go-build\") = nil; any target name should produce a handler") {
		assert.NoError(t, h(ctx, p), "makeHandler(\"go-build\")(ctx, emptyProject)")
	}
}

// TestSpellTargetSources asserts that TargetSources returns the correct globs for a
// known target and nil for unknown targets, covering the generic enrichment loop in
// executeOnProjects.
func TestSpellTargetSources(t *testing.T) {
	t.Parallel()
	vs := map[string][]string{
		"lint": {".golangci.yml", ".golangci.yaml", ".golangci.toml", ".golangci.json"},
	}
	s := types.NewSpell("testpack", types.WithTargetSources(vs))

	assert.Equal(t, []string{".golangci.yml", ".golangci.yaml", ".golangci.toml", ".golangci.json"},
		s.TargetSources()["lint"], "TargetSources(lint)")
	assert.Nil(t, s.TargetSources()["build"], "TargetSources(build)")
	assert.Nil(t, s.TargetSources()[""], "TargetSources(\"\")")
}

// TestSpellTargetSources_nilMap asserts that TargetSources is nil-safe on a
// Spell constructed without any verb_sources (the common case for non-Go spells).
func TestSpellTargetSources_nilMap(t *testing.T) {
	t.Parallel()
	s := types.NewSpell("bare")
	assert.Nil(t, s.TargetSources()["lint"], "TargetSources on nil-map spell")
}

// TestMergePaths verifies dedup + lex ordering, the contract callers
// rely on for deterministic stream-driven affected output.
func TestMergePaths(t *testing.T) {
	t.Parallel()

	// merge normalizes a nil result to an empty slice so the comparison is
	// against a non-nil expected value (assert.Equal distinguishes nil/empty).
	merge := func(a, b []string) []string {
		got := mergePaths(a, b)
		if got == nil {
			got = []string{}
		}
		return got
	}

	assert.Equal(t, []string{}, merge(nil, nil), "both empty")
	assert.Equal(t, []string{"a", "x"}, merge(nil, []string{"x", "a"}), "a empty")
	assert.Equal(t, []string{"a", "x"}, merge([]string{"x", "a"}, nil), "b empty")
	assert.Equal(t, []string{"a", "b", "c"}, merge([]string{"a", "b"}, []string{"b", "c"}), "dedup across")
	assert.Equal(t, []string{"a"}, merge([]string{"a", "a"}, []string{"a"}), "all dupes")
}

// TestForEachSpell_BudgetHonored verifies that when the context carries a
// cache.Limiter (as set by cache.RunAll), forEachSpell does not exceed the
// configured concurrency cap.  Before the fix, spell goroutines acquired no
// limiter slot, so N projects × M spells could run N×M concurrent subprocesses
// against a cap of N.
func TestForEachSpell_BudgetHonored(t *testing.T) {
	t.Parallel()
	const cap = 2 // concurrency budget

	lim := cache.NewLimiter(cap)

	// Simulate a RunAll worker: the test goroutine has acquired one slot and
	// marked the context accordingly, exactly as cache.RunAll does at
	// cache.go:447-456.
	ctx := context.Background()
	require.NoError(t, lim.Acquire(ctx))
	defer lim.Release()
	ctx = cache.ContextWithLimiter(ctx, lim)
	ctx = cache.WithSlotHeld(ctx)

	var (
		mu       sync.Mutex
		inFlight int
		peak     int
	)

	newSpell := func(name string) *types.Spell {
		return types.NewSpell(name, types.WithInvoker(func(_ context.Context, _ types.InvokeRequest) (any, error) {
			mu.Lock()
			inFlight++
			if inFlight > peak {
				peak = inFlight
			}
			mu.Unlock()

			time.Sleep(20 * time.Millisecond) // hold long enough for overlap to be detectable

			mu.Lock()
			inFlight--
			mu.Unlock()
			return nil, nil
		}))
	}

	p := &types.Project{
		Path: "test-budget",
		ResolvedSpells: []*types.Spell{
			newSpell("a"), newSpell("b"), newSpell("c"), newSpell("d"),
		},
	}

	require.NoError(t, forEachSpell(ctx, p, "build", func(ctx context.Context, s *types.Spell) error {
		_, err := s.Invoke(ctx, types.InvokeRequest{Target: "build"})
		return err
	}), "forEachSpell")

	require.NotZero(t, peak, "no spells ran")
	assert.LessOrEqualf(t, peak, cap,
		"peak concurrent spells = %d; want ≤ %d (limiter cap = %d): spell fan-out escapes the concurrency budget",
		peak, cap, cap)
}

// TestForEachSpell_NoBudgetUnchanged verifies that when no limiter is in the
// context, forEachSpell runs all spells concurrently as before (no regression).
func TestForEachSpell_NoBudgetUnchanged(t *testing.T) {
	t.Parallel()

	var (
		mu  sync.Mutex
		ran []string
	)

	newSpell := func(name string) *types.Spell {
		return types.NewSpell(name, types.WithInvoker(func(_ context.Context, _ types.InvokeRequest) (any, error) {
			mu.Lock()
			ran = append(ran, name)
			mu.Unlock()
			return nil, nil
		}))
	}

	p := &types.Project{
		Path: "test-no-budget",
		ResolvedSpells: []*types.Spell{
			newSpell("x"), newSpell("y"), newSpell("z"),
		},
	}

	require.NoError(t, forEachSpell(context.Background(), p, "build", func(ctx context.Context, s *types.Spell) error {
		_, err := s.Invoke(ctx, types.InvokeRequest{Target: "build"})
		return err
	}), "forEachSpell")

	assert.Len(t, ran, 3)
}

// TestRunTarget_InjectsEffectiveClaims verifies that runTarget injects
// effective claims into the spell context for every target, not just "build".
func TestRunTarget_InjectsEffectiveClaims(t *testing.T) {
	t.Parallel()

	var gotClaims []string

	spell := types.NewSpell(
		"s",
		types.WithClaims("**/*.go"),
		types.WithInvoker(func(ctx context.Context, _ types.InvokeRequest) (any, error) {
			gotClaims = types.EffectiveClaimsFromContext(ctx)
			return nil, nil
		}),
	)

	p := &types.Project{
		Path:           "p",
		Spells:         []string{"s"},
		Bindings:       []*types.Binding{{Name: "s"}},
		ResolvedSpells: []*types.Spell{spell},
	}

	require.NoError(t, runTarget(context.Background(), p, "ci"))
	assert.NotEmpty(t, gotClaims, "runTarget did not inject effective claims into the spell context")
}

// TestCacheOps_InspectReturnErrNoCache asserts the cache-operation methods
// report ErrNoCache on an Inspect-constructed (cache-free) workspace rather
// than panicking on a nil cache.
func TestCacheOps_InspectReturnErrNoCache(t *testing.T) {
	t.Parallel()
	root := makeWorkspaceRoot(t, "magusfile.buzz")
	m, err := inspect(context.Background(), root)
	require.NoError(t, err)
	_, _, err = m.PruneCache(context.Background(), time.Now(), true)
	assert.ErrorIs(t, err, types.ErrNoCache, "PruneCache on Inspect")
	assert.ErrorIs(t, m.ExportCache(context.Background(), io.Discard), types.ErrNoCache, "ExportCache on Inspect")
	assert.ErrorIs(t, m.ImportCache(context.Background(), strings.NewReader("")), types.ErrNoCache, "ImportCache on Inspect")
	_, err = m.TailLog("anything", "")
	assert.ErrorIs(t, err, types.ErrNoCache, "TailLog on Inspect")
}

func TestLimiter_IdempotentNonNil(t *testing.T) {
	t.Parallel()
	m := &Magus{}
	l1 := m.limiter()
	require.NotNil(t, l1, "limiter() = nil, want non-nil")
	assert.Same(t, l1, m.limiter(), "limiter() not idempotent: second call returned different pointer")
}

func TestPoolRegistry_IdempotentNonNil(t *testing.T) {
	t.Parallel()
	m := &Magus{}
	r1 := m.buzzPoolRegistry()
	require.NotNil(t, r1, "buzzPoolRegistry() = nil, want non-nil")
	assert.Same(t, r1, m.buzzPoolRegistry(), "buzzPoolRegistry() not idempotent: second call returned different pointer")
}

func TestClose_Idempotent(t *testing.T) {
	t.Parallel()
	m := &Magus{}
	assert.NoError(t, m.Close(), "Close on fresh Magus")
	// Trigger lazy init of poolReg, then Close again.
	_ = m.buzzPoolRegistry()
	assert.NoError(t, m.Close(), "Close after PoolRegistry")
	assert.NoError(t, m.Close(), "second Close")
}

func TestSetGraphObserver_Invoked(t *testing.T) {
	t.Parallel()
	root := makeWorkspaceRoot(t, "magusfile.buzz")
	m, err := inspect(context.Background(), root)
	require.NoError(t, err)

	var builds int
	m.SetGraphObserver(&countingObserver{onBuild: func() { builds++ }})
	_, err = m.Graph()
	require.NoError(t, err)
	assert.NotZero(t, builds, "SetGraphObserver: OnBuild never called after Graph()")

	// Clear the observer; subsequent Graph() must not increment.
	before := builds
	m.SetGraphObserver(nil)
	_, err = m.Graph()
	require.NoError(t, err)
	assert.Equal(t, before, builds, "SetGraphObserver(nil): OnBuild called after clearing observer")
}

func TestStepFor_RootProject(t *testing.T) {
	t.Parallel()
	root := makeWorkspaceRoot(t, "magusfile.buzz")
	m, err := inspect(context.Background(), root)
	require.NoError(t, err)
	p := &types.Project{
		Path:    ".",
		Sources: []string{"**/*.go"},
		Outputs: []string{"bin/app"},
	}
	spec := m.baseStep(p)
	assert.Equal(t, ".", spec.ProjectPath, "ProjectPath")
	// Root project: declared glob passes through unchanged; magusfile globs are
	// also appended (see magusfileGlobs). Use Contains rather than exact-count.
	assert.Contains(t, spec.Sources, "**/*.go", "Sources must contain declared glob")
	assert.Contains(t, spec.Sources, "magusfile.buzz", "Sources must contain root magusfile glob")
	assert.Equal(t, []string{"bin/app"}, spec.Outputs, "Outputs")
	assert.Equal(t, m.Root(), spec.WorkspaceRoot, "WorkspaceRoot")
}

// TestStepFor_NoSpellsHasNoIgnoreDirs pins that a project with no resolved spells
// carries no ignore dirs into its Step - the walk falls back to the core set only.
func TestStepFor_NoSpellsHasNoIgnoreDirs(t *testing.T) {
	t.Parallel()
	root := makeWorkspaceRoot(t, "magusfile.buzz")
	m, err := inspect(context.Background(), root)
	require.NoError(t, err)
	p := &types.Project{Path: ".", Sources: []string{"**/*.go"}}
	assert.Empty(t, m.baseStep(p).IgnoreDirs, "no resolved spells: Step.IgnoreDirs must be empty")
}

// TestStepFor_UnionsSpellIgnoreDirs verifies baseStep unions the IgnoreDirs of ALL a
// project's resolved spells (not just one) and dedups overlaps, preserving first-seen
// order - the polyglot case where two spells both claim a dir. This exercises the whole
// spell-declared path end to end: WithIgnoreDirs -> Spell.IgnoreDirs() -> Step.IgnoreDirs.
func TestStepFor_UnionsSpellIgnoreDirs(t *testing.T) {
	t.Parallel()
	root := makeWorkspaceRoot(t, "magusfile.buzz")
	m, err := inspect(context.Background(), root)
	require.NoError(t, err)
	p := &types.Project{
		Path:    ".",
		Sources: []string{"**/*.go"},
		ResolvedSpells: []*types.Spell{
			types.NewSpell("go", types.WithIgnoreDirs("vendor")),
			types.NewSpell("ts", types.WithIgnoreDirs("node_modules", "vendor")), // vendor overlaps -> deduped
		},
	}
	assert.Equal(t, []string{"vendor", "node_modules"}, m.baseStep(p).IgnoreDirs,
		"baseStep must union all resolved spells' ignore dirs and dedup overlaps in first-seen order")
}

func TestStepFor_NestedProject(t *testing.T) {
	t.Parallel()
	root := makeWorkspaceRoot(t, "magusfile.buzz", "api/magusfile.buzz")
	m, err := inspect(context.Background(), root)
	require.NoError(t, err)
	p := &types.Project{
		Path:    "api",
		Sources: []string{"**/*.go"},
		Outputs: []string{"bin/server"},
	}
	spec := m.baseStep(p)
	// Declared glob is prefixed with the project path.
	assert.Contains(t, spec.Sources, "api/**/*.go", "Sources must contain project-prefixed glob")
	// Project-local magusfile glob is included.
	assert.Contains(t, spec.Sources, "api/magusfile.buzz", "Sources must contain project-local magusfile glob")
	// Root magusfile glob is always included for non-root projects.
	assert.Contains(t, spec.Sources, "magusfile.buzz", "Sources must contain root magusfile glob")
	assert.Equal(t, []string{"api/bin/server"}, spec.Outputs, "Outputs")
}

// countingObserver is a types.Observer stub that counts OnBuild calls.
type countingObserver struct{ onBuild func() }

func (c *countingObserver) OnBuild(types.BuildStats) {
	if c.onBuild != nil {
		c.onBuild()
	}
}
func (c *countingObserver) OnQuery(types.QueryEvent) {}
func (c *countingObserver) OnError(error)            {}

// TestMagusfileGlobs verifies the workspace-relative glob sets returned
// for root and non-root project paths.
func TestMagusfileGlobs(t *testing.T) {
	t.Parallel()

	t.Run("root project", func(t *testing.T) {
		t.Parallel()
		want := []string{
			"magusfile.buzz",
			"magusfiles/**/*.buzz",
		}
		assert.Equal(t, want, magusfileGlobs("."))
	})

	t.Run("non-root project", func(t *testing.T) {
		t.Parallel()
		want := []string{
			"extensions/drape/magusfile.buzz",
			"extensions/drape/magusfiles/**/*.buzz",
		}
		assert.Equal(t, want, magusfileGlobs("extensions/drape"))
	})

	t.Run("single-segment path", func(t *testing.T) {
		t.Parallel()
		got := magusfileGlobs("api")
		require.NotEmpty(t, got, "expected non-empty globs for single-segment path")
		for _, g := range got {
			assert.Truef(t, strings.HasPrefix(g, "api/"), "glob %q should start with \"api/\"", g)
		}
	})
}

// ExampleWorkspaceRegistry_withSpell shows the recommended way to attach a spell to a
// project using the string-name API. The registry is passed to Inspect or Open
// via WithWorkspaceRegistry.
func ExampleWorkspaceRegistry_withSpell() {
	reg := NewWorkspaceRegistry()
	reg.RegisterProject(
		"api",
		WithSpell("go"),
	)
	// pass reg to Inspect or Open:
	// Inspect(ctx, root, WithWorkspaceRegistry(reg))
}

// ExampleInspect shows how to discover projects in a workspace without
// opening the cache. Inspect is the right entry point for read-only
// commands (list, graph, describe) where cache overhead is unnecessary.
func ExampleInspect() {
	// Create a minimal workspace with one project for illustration.
	root, err := os.MkdirTemp("", "magus-example-*")
	if err != nil {
		fmt.Println("setup error:", err)
		return
	}
	defer os.RemoveAll(root)

	// A directory is a project if it contains a magusfile.buzz.
	projDir := filepath.Join(root, "myapp")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		fmt.Println("setup error:", err)
		return
	}
	if err := os.WriteFile(filepath.Join(projDir, "magusfile.buzz"), []byte(""), 0o644); err != nil {
		fmt.Println("setup error:", err)
		return
	}

	ws, err := Inspect(context.Background(), root)
	if err != nil {
		fmt.Println("inspect error:", err)
		return
	}

	for _, p := range ws.All() {
		fmt.Println(p.Path)
	}
	// Output:
	// myapp
}

// ExampleOpen shows the canonical entry point: open a Magus
// orchestrator rooted at "." and run a target across every project.
func ExampleOpen() {
	m, err := Open(context.Background(), ".")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	targets, err := m.ExpandPath(types.Target{Name: "build"})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	if err := m.Run(context.Background(), targets); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

// TestExpandAffectedFallbackDistinguishable verifies that when the VCS can't
// compute a definitive set (here: disabled), ExpandAffected selects all projects
// AND reports fellBack=true — a typed signal callers can distinguish from a
// deliberate full run, rather than parsing the free-text source string.
func TestExpandAffectedFallbackDistinguishable(t *testing.T) {
	// Not parallel: t.Setenv.
	t.Setenv("MAGUS_VCS_ENABLED", "false")
	ws := newWorkspaceCustom(t)

	targets, source, fellBack, err := ws.ExpandAffected(context.Background(), "ci", "")
	require.NoError(t, err, "ExpandAffected")
	assert.True(t, fellBack, "fellBack should be true when VCS is disabled")
	assert.NotEmpty(t, targets, "fallback should select all projects")
	assert.NotEmpty(t, source, "source should carry the fallback reason")
}

// ExampleMagus_ExpandAffected shows how to compute the VCS-diff
// affected project set, with automatic fallback to all projects when
// the VCS command is unavailable (shallow clone, missing binary, etc.).
func ExampleMagus_ExpandAffected() {
	m, err := Open(context.Background(), ".")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	targets, source, _, err := m.ExpandAffected(context.Background(), "test", "")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	fmt.Printf("[%s]\n", source)
	for _, t := range targets {
		fmt.Println(" ", t.Path)
	}
}

// newWorkspace lays down a minimal multi-language workspace and
// returns it as a types.WorkspaceRepository rooted at it.
// Uses Inspect (no cache, no Teal preload) so tests don't need a
// runtime backend registered.
func newWorkspace(t *testing.T) types.WorkspaceRepository {
	t.Helper()
	root := t.TempDir()
	for _, rel := range []string{
		"magusfile.buzz",                    // root project "."
		"api/magusfile.buzz",                // "api"
		"web/studio/magusfile.buzz",         // "web/studio"
		"extensions/drape/magusfile.buzz",   // "extensions/drape"
		"extensions/lattice/magusfile.buzz", // "extensions/lattice"
	} {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := Inspect(context.Background(), root)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	return ws
}

// TestExpand_AllNoPath fans out to every project when Path is empty.
func TestExpand_AllNoPath(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	targets, err := ws.ExpandPath(types.Target{Name: "build"})
	require.NoError(t, err)
	got := make([]string, len(targets))
	for i, tgt := range targets {
		got[i] = tgt.Path
	}
	slices.Sort(got)
	want := []string{".", "api", "extensions/drape", "extensions/lattice", "web/studio"}
	require.Equal(t, want, got, "Paths")
	for _, tgt := range targets {
		assert.Equal(t, "build", tgt.Name, "Target")
	}
}

// TestExpand_ExplicitPath returns exactly the requested project.
func TestExpand_ExplicitPath(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	targets, err := ws.ExpandPath(types.Target{Path: "api", Name: "test"})
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, "api", targets[0].Path)
	assert.Equal(t, "test", targets[0].Name)
}

// TestExpand_SlashAlias verifies "/" is treated as all-projects.
func TestExpand_SlashAlias(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	targets, err := ws.ExpandPath(types.Target{Path: "/", Name: "build"})
	require.NoError(t, err)
	got := make([]string, len(targets))
	for i, tgt := range targets {
		got[i] = tgt.Path
	}
	slices.Sort(got)
	want := []string{".", "api", "extensions/drape", "extensions/lattice", "web/studio"}
	assert.Equal(t, want, got, "Paths")
}

// TestExpand_WsPrefixRejected verifies "ws:foo" paths are rejected.
func TestExpand_WsPrefixRejected(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	_, err := ws.ExpandPath(types.Target{Path: "ws:api", Name: "build"})
	assert.Error(t, err, "expected error for ws:-prefixed path")
}

// TestExpand_UnknownPath returns ErrUnknownProject for unknown paths.
func TestExpand_UnknownPath(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	_, err := ws.ExpandPath(types.Target{Path: "does/not/exist", Name: "build"})
	assert.ErrorIs(t, err, types.ErrUnknownProject, "expected ErrUnknownProject for unknown project path")
}

// TestExpand_UnknownPath_Suggestion appends a did-you-mean when a typo'd path is
// close to a real project, matching the CLI's other nearest-match hints.
func TestExpand_UnknownPath_Suggestion(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	_, err := ws.ExpandPath(types.Target{Path: "aip", Name: "build"})
	require.ErrorIs(t, err, types.ErrUnknownProject)
	assert.Contains(t, err.Error(), `did you mean "api"`)
}

// TestParseTarget covers the canonical "target[:charm,...]" parsing cases.
func TestParseTarget(t *testing.T) {
	t.Parallel()

	parseOK := func(input, wantName string, wantCharms []string) {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			got, err := types.ParseTarget(input)
			require.NoErrorf(t, err, "ParseTarget(%q)", input)
			assert.Equalf(t, wantName, got.Name, "ParseTarget(%q).Name", input)
			assert.Equalf(t, wantCharms, got.Charms, "ParseTarget(%q).Charms", input)
		})
	}
	parseErr := func(name, input string) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := types.ParseTarget(input)
			assert.Errorf(t, err, "ParseTarget(%q): want error", input)
		})
	}

	parseOK("build", "build", nil) // bare target
	parseOK("lint:read", "lint", []string{"read"})
	parseOK("format:write", "format", []string{"write"})
	parseErr("empty", "")
	parseErr("empty charm", "lint:")               // empty charm
	parseErr("slash in target", "web/studio:test") // '/' not allowed in target
}

// TestTargetString verifies the canonical "path:target" form.
func TestTargetString(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "api:build", types.Target{Path: "api", Name: "build"}.String())
	assert.Equal(t, ":test", types.Target{Name: "test"}.String())
}

// TestMagus_proxies asserts that the methods *Magus exposes
// (Root, All, Get, Where, Affected, AffectedFromPaths, Graph,
// VCSOptions) return consistent values. The intent is to lock the
// public surface so internal refactors do not silently change behaviour.
func TestMagus_proxies(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)

	require.NotEmpty(t, ws.Root(), "Root: empty")

	all := ws.All()
	require.NotEmpty(t, all, "All: empty")

	for _, p := range all {
		got := ws.Get(p.Path)
		if assert.NotNilf(t, got, "Get(%q): got nil, want project", p.Path) {
			assert.Equalf(t, p.Path, got.Path, "Get(%q): wrong project", p.Path)
		}
	}

	assert.Nil(t, ws.Get("does/not/exist"), "Get(unknown): want nil")

	_, err := ws.Graph()
	require.NoError(t, err, "Graph")

	// Where: pick the first non-root project. The root project
	// ("." marker) is deliberately not matched at the workspace root by
	// the underlying implementation (callers fall through to Select()).
	var nonRoot string
	for _, p := range all {
		if p.Path != "." {
			nonRoot = p.Path
			break
		}
	}
	require.NotEmpty(t, nonRoot, "setup: no non-root project to test Where")
	abs := filepath.Join(ws.Root(), nonRoot)
	p, ok := ws.Where(abs)
	require.Truef(t, ok, "Where(%q): not found, want project at %q", abs, nonRoot)
	assert.Equalf(t, nonRoot, p.Path, "Where(%q): wrong project", abs)

	// VCSOptions is a value-type accessor; it must be safe to call
	// without panicking on a freshly-opened workspace.
	_ = ws.VCSOptions()

	// AffectedFromPaths accepts an empty slice and returns a result with
	// no affected projects. We do not exercise Affected() here because it
	// shells out to git, which is not always available in test
	// environments.
	r, err := ws.AffectedFromPaths(context.Background(), nil)
	require.NoError(t, err, "AffectedFromPaths(nil)")
	assert.Empty(t, r.Affected, "AffectedFromPaths(nil).Affected: want empty")
}

func b64Pub(t *testing.T) (pub string, seed string) {
	t.Helper()
	pk, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err, "generate key")
	return base64.StdEncoding.EncodeToString(pk), base64.StdEncoding.EncodeToString(priv.Seed())
}

// TestRemoteCacheRequiresTrustSet is the paranoid root: a wired remote backend
// with no declared trust set must be a hard error, not a silent unverified cache.
func TestRemoteCacheRequiresTrustSet(t *testing.T) {
	_, err := remoteCacheSigningOpts(nil, false)
	assert.Error(t, err, "empty trust set was accepted; a remote cache must require trusted_keys")
	_, err = remoteCacheSigningOpts([]string{}, false)
	assert.Error(t, err, "empty trust-set slice was accepted")
}

// TestRemoteCacheInsecureSkipsTrustSet: the explicit opt-out accepts a wired
// backend with no trust set and no signing key, yielding the insecure option.
func TestRemoteCacheInsecureSkipsTrustSet(t *testing.T) {
	opts, err := remoteCacheSigningOpts(nil, true)
	require.NoError(t, err, "insecure mode rejected empty trust set")
	assert.Len(t, opts, 1, "insecure: want 1 opt (WithInsecureRemote)")
}

// TestRemoteCacheTrustSetDecodes: a valid trust set yields verification options;
// adding a signing-key env var yields a signing option too.
func TestRemoteCacheTrustSetDecodes(t *testing.T) {
	pub, seed := b64Pub(t)

	opts, err := remoteCacheSigningOpts([]string{pub}, false)
	require.NoError(t, err, "valid trust set rejected")
	assert.Len(t, opts, 1, "verify-only: want 1 opt (trusted keys only)")

	t.Setenv(signingKeyEnv, seed)
	opts, err = remoteCacheSigningOpts([]string{pub}, false)
	require.NoError(t, err, "valid trust set + signing key rejected")
	assert.Len(t, opts, 2, "signing: want 2 opts (trusted keys + signing key)")
}

// TestRemoteCacheRejectsMalformedKeys: bad base64 in either the trust set or the
// signing-key env var is a clear configuration error, not a silent fallback.
func TestRemoteCacheRejectsMalformedKeys(t *testing.T) {
	_, err := remoteCacheSigningOpts([]string{"not!base64!"}, false)
	assert.Error(t, err, "malformed trusted key was accepted")
	pub, _ := b64Pub(t)
	t.Setenv(signingKeyEnv, "not!base64!")
	_, err = remoteCacheSigningOpts([]string{pub}, false)
	assert.Error(t, err, "malformed signing key was accepted")
}

// TestSharedProviderVisibleAcrossMagus proves the /dashboard data-flow invariant: when two
// Magus instances are opened with ONE shared observability provider (WithProvider), a metric
// recorded through one is visible via the other's MetricsCollector. This is exactly what lets
// the daemon's bridge Magus read the counters that separate per-workspace registry builds
// record. Without a shared provider each Magus has its own ManualReader and the bridge
// collector reads zeros - the bug this feature fixes.
func TestSharedProviderVisibleAcrossMagus(t *testing.T) {
	ctx := context.Background()

	// One provider, LocalCollect on (as the daemon builds it), shared by both workspaces.
	tel, err := otlp.New(ctx, observability.Config{LocalCollect: true})
	require.NoError(t, err)

	mkWS := func() string {
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"),
			[]byte("export fun build(ctx: magus\\Context, args: [str]) > void {}\n"), 0o644))
		return root
	}

	mBuild, err := Open(ctx, mkWS(), WithProvider(tel))
	require.NoError(t, err)
	defer func() { _ = mBuild.Close() }()

	mBridge, err := Open(ctx, mkWS(), WithProvider(tel))
	require.NoError(t, err)
	defer func() { _ = mBridge.Close() }()

	// Both workspaces adopted the SAME provider instance rather than building their own.
	assert.True(t, tel == mBuild.Telemetry(), "build workspace did not adopt the injected provider")
	assert.True(t, tel == mBridge.Telemetry(), "bridge workspace did not adopt the injected provider")

	// Record a target run through the "build" workspace's provider (as a per-workspace
	// registry build would)...
	mBuild.Telemetry().RecordTargetRun(ctx, 0.5,
		observability.Attr{Key: "magus.target", Value: "build"},
		observability.Attr{Key: "outcome", Value: "success"},
	)

	// ...and read it back through the "bridge" workspace's collector (as the dashboard does).
	coll, ok := mBridge.MetricsCollector()
	require.True(t, ok, "shared local-collect provider must yield a collector")

	rm, err := coll.Collect(ctx)
	require.NoError(t, err)

	var targetRuns int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "magus.target.runs" {
				continue
			}
			sum, isSum := m.Data.(metricdata.Sum[int64])
			require.True(t, isSum, "magus.target.runs should be an int64 sum")
			for _, dp := range sum.DataPoints {
				targetRuns += dp.Value
			}
		}
	}
	assert.Equal(t, int64(1), targetRuns,
		"a target run recorded via one Magus must be visible through the other's shared-provider collector")
}
