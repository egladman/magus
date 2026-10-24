package magus

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/egladman/magus/buzzengine" // Open and Inspect fail without the engine linked
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/config"
	configgen "github.com/egladman/magus/internal/config/gen"
	"github.com/egladman/magus/internal/gen/typestest"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/observability/otlp"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/project/impact"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// TestMain keeps the variables helper processes are instructed with: LOCKTEST_* for
// helperHold's holder and PIPETEST_* for pipeStage's stages.
func TestMain(m *testing.M) {
	// A sandboxed run's children start as this binary re-run as their launcher.
	MaybeLaunchSandbox()
	testkit.Main(m, "LOCKTEST_*", "PIPETEST_*")
}

// TestMakeHandlerConventionalTargets asserts that targetHandler returns a
// non-nil handler for the six conventional target names and that invoking
// it on an empty project (no packs) returns nil without panic.
func TestTargetHandlerConventionalTargets(t *testing.T) {
	m := &Magus{}
	p := &types.Project{} // no packs → every handler must return nil cleanly
	ctx := context.Background()
	for _, name := range []string{"build", "test", "lint", "format", "clean", "generate"} {
		h := m.targetHandler(name)
		if !assert.NotNilf(t, h, "targetHandler(%q) = nil; expected non-nil handler", name) {
			continue
		}
		require.NoErrorf(t, h(ctx, p), "targetHandler(%q)(ctx, emptyProject)", name)
	}
	// Arbitrary non-conventional names also get a handler.
	h := m.targetHandler("go-build")
	if assert.NotNil(t, h, "targetHandler(\"go-build\") = nil; any target name should produce a handler") {
		require.NoError(t, h(ctx, p), "targetHandler(\"go-build\")(ctx, emptyProject)")
	}
}

// TestSpellTargetSources asserts that TargetSources returns the correct globs for a
// known target and nil for unknown targets.
func TestSpellTargetSources(t *testing.T) {
	t.Parallel()
	vs := map[string][]string{
		"lint": {".golangci.yml", ".golangci.yaml", ".golangci.toml", ".golangci.json"},
	}
	s := spells.NewSpell("testpack", spells.WithTargetSources(vs))

	assert.Equal(t, []string{".golangci.yml", ".golangci.yaml", ".golangci.toml", ".golangci.json"},
		s.TargetSources()["lint"], "TargetSources(lint)")
	assert.Nil(t, s.TargetSources()["build"], "TargetSources(build)")
	assert.Nil(t, s.TargetSources()[""], "TargetSources(\"\")")
}

// TestSpellTargetSources_nilMap asserts that TargetSources is nil-safe on a
// Spell constructed without any verb_sources (the common case for non-Go spells).
func TestSpellTargetSources_nilMap(t *testing.T) {
	t.Parallel()
	s := spells.NewSpell("bare")
	assert.Nil(t, s.TargetSources()["lint"], "TargetSources on nil-map spell")
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

	newSpell := func(name string) *spells.Spell {
		return spells.NewSpell(name, spells.WithInvoker(func(_ context.Context, _ spells.InvokeRequest) (any, error) {
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
		ResolvedSpells: []*spells.Spell{
			newSpell("a"), newSpell("b"), newSpell("c"), newSpell("d"),
		},
	}

	require.NoError(t, forEachSpell(ctx, p, "build", func(ctx context.Context, s *spells.Spell) error {
		_, err := s.Invoke(ctx, spells.InvokeRequest{Target: "build"})
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

	newSpell := func(name string) *spells.Spell {
		return spells.NewSpell(name, spells.WithInvoker(func(_ context.Context, _ spells.InvokeRequest) (any, error) {
			mu.Lock()
			ran = append(ran, name)
			mu.Unlock()
			return nil, nil
		}))
	}

	p := &types.Project{
		Path: "test-no-budget",
		ResolvedSpells: []*spells.Spell{
			newSpell("x"), newSpell("y"), newSpell("z"),
		},
	}

	require.NoError(t, forEachSpell(context.Background(), p, "build", func(ctx context.Context, s *spells.Spell) error {
		_, err := s.Invoke(ctx, spells.InvokeRequest{Target: "build"})
		return err
	}), "forEachSpell")

	assert.Len(t, ran, 3)
}

// TestCacheOps_InspectReturnErrNoCache asserts the cache-operation methods
// report ErrNoCache on an Inspect-constructed (cache-free) workspace rather
// than panicking on a nil cache.
func TestCacheOps_InspectReturnErrNoCache(t *testing.T) {
	t.Parallel()
	root := makeWorkspaceRoot(t, "magusfile.buzz")
	m, err := inspect(context.Background(), root, WithSpawnRule(TierCeiling))
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
	require.NoError(t, m.Close(), "Close on fresh Magus")
	// Trigger lazy init of poolReg, then Close again.
	_ = m.buzzPoolRegistry()
	require.NoError(t, m.Close(), "Close after PoolRegistry")
	require.NoError(t, m.Close(), "second Close")
}

// TestWithObserverRidesTheRun: a run's observer sees the graph builds made under the run's
// context, and nothing made outside it, so two runs on one workspace never share one.
func TestWithObserverRidesTheRun(t *testing.T) {
	t.Parallel()
	root := makeWorkspaceRoot(t, "magusfile.buzz")
	m, err := inspect(context.Background(), root, WithSpawnRule(TierCeiling))
	require.NoError(t, err)

	var builds int
	obs := typestest.NewMockObserver(t)
	obs.EXPECT().Observe(mock.Anything).Run(func(e types.GraphEvent) {
		if e.Op == types.GraphOpBuild {
			builds++
		}
	}).Return().Maybe()
	ctx := applyRunOpts([]RunOption{WithObserver(obs)}).observed(context.Background())
	_, err = m.AffectedFromPaths(ctx, []string{"magusfile.buzz"})
	require.NoError(t, err)
	assert.NotZero(t, builds, "WithObserver: no build observed under the run's context")

	before := builds
	_, err = m.AffectedFromPaths(context.Background(), []string{"magusfile.buzz"})
	require.NoError(t, err)
	assert.Equal(t, before, builds, "a build outside the run's context reached its observer")

	_, err = m.Graph()
	require.NoError(t, err)
	assert.Equal(t, before, builds, "the workspace holds no observer of its own")
}

func TestStepFor_RootProject(t *testing.T) {
	t.Parallel()
	root := makeWorkspaceRoot(t, "magusfile.buzz")
	m, err := inspect(context.Background(), root, WithSpawnRule(TierCeiling))
	require.NoError(t, err)
	p := &types.Project{
		Path:    ".",
		Sources: types.MustParseGlobs("**/*.go"),
		Outputs: types.MustParseGlobs("bin/app"),
	}
	spec := m.baseStep(p)
	// Root project: declared glob passes through unchanged; magusfile globs are
	// also appended (see magusfileGlobs). Use Contains rather than exact-count.
	assert.Contains(t, spec.Sources, types.Glob{Pattern: "**/*.go"}, "Sources must contain declared glob")
	assert.Contains(t, spec.Sources, types.Glob{Pattern: "magusfile.buzz"}, "Sources must contain root magusfile glob")
	assert.Equal(t, cache.Step{
		ProjectPath:     ".",
		Sources:         spec.Sources, // checked by Contains above
		Outputs:         types.MustParseGlobs("bin/app"),
		WorkspaceRoot:   m.Root(),
		SpellDefVersion: spec.SpellDefVersion, // the builtins' hash, not this test's to pin
		Label:           types.ProjectDisplayName(p.Path, p.Name, p.Dir),
	}, spec)
}

// TestStepFor_NoSpellsHasNoIgnoreDirs pins that a project with no resolved spells
// carries no ignore dirs into its Step: the walk falls back to the core set only.
func TestStepFor_NoSpellsHasNoIgnoreDirs(t *testing.T) {
	t.Parallel()
	root := makeWorkspaceRoot(t, "magusfile.buzz")
	m, err := inspect(context.Background(), root, WithSpawnRule(TierCeiling))
	require.NoError(t, err)
	p := &types.Project{Path: ".", Sources: types.MustParseGlobs("**/*.go")}
	assert.Empty(t, m.baseStep(p).IgnoreDirs, "no resolved spells: Step.IgnoreDirs must be empty")
}

// TestStepFor_UnionsSpellIgnoreDirs verifies baseStep unions the IgnoreDirs of ALL a
// project's resolved spells (not just one) and dedups overlaps, preserving first-seen
// order: the polyglot case where two spells both claim a dir. This exercises the whole
// spell-declared path end to end: WithIgnoreDirs -> Spell.IgnoreDirs() -> Step.IgnoreDirs.
func TestStepFor_UnionsSpellIgnoreDirs(t *testing.T) {
	t.Parallel()
	root := makeWorkspaceRoot(t, "magusfile.buzz")
	m, err := inspect(context.Background(), root, WithSpawnRule(TierCeiling))
	require.NoError(t, err)
	p := &types.Project{
		Path:    ".",
		Sources: types.MustParseGlobs("**/*.go"),
		ResolvedSpells: []*spells.Spell{
			spells.NewSpell("go", spells.WithIgnoreDirs("vendor")),
			spells.NewSpell("ts", spells.WithIgnoreDirs("node_modules", "vendor")), // vendor overlaps -> deduped
		},
	}
	assert.Equal(t, []string{"vendor", "node_modules"}, m.baseStep(p).IgnoreDirs,
		"baseStep must union all resolved spells' ignore dirs and dedup overlaps in first-seen order")
}

func TestStepFor_NestedProject(t *testing.T) {
	t.Parallel()
	root := makeWorkspaceRoot(t, "magusfile.buzz", "api/magusfile.buzz")
	m, err := inspect(context.Background(), root, WithSpawnRule(TierCeiling))
	require.NoError(t, err)
	p := &types.Project{
		Path:    "api",
		Sources: types.MustParseGlobs("**/*.go"),
		Outputs: types.MustParseGlobs("bin/server"),
	}
	spec := m.baseStep(p)
	// Declared glob is prefixed with the project path.
	assert.Contains(t, spec.Sources, types.Glob{Pattern: "api/**/*.go"}, "Sources must contain project-prefixed glob")
	// Project-local magusfile glob is included.
	assert.Contains(t, spec.Sources, types.Glob{Pattern: "api/magusfile.buzz"}, "Sources must contain project-local magusfile glob")
	// Root magusfile glob is always included for non-root projects.
	assert.Contains(t, spec.Sources, types.Glob{Pattern: "magusfile.buzz"}, "Sources must contain root magusfile glob")
	assert.Equal(t, types.MustParseGlobs("api/bin/server"), spec.Outputs, "Outputs")
}

// TestMagusfileGlobs verifies the workspace-relative glob sets returned
// for root and non-root project paths.
func TestMagusfileGlobs(t *testing.T) {
	t.Parallel()

	t.Run("root project", func(t *testing.T) {
		t.Parallel()
		want := types.MustParseGlobs(
			"magusfile.buzz",
			"magusfiles/**/*.buzz",
		)
		assert.Equal(t, want, magusfileGlobs("."))
	})

	t.Run("non-root project", func(t *testing.T) {
		t.Parallel()
		want := types.MustParseGlobs(
			"extensions/drape/magusfile.buzz",
			"extensions/drape/magusfiles/**/*.buzz",
		)
		assert.Equal(t, want, magusfileGlobs("extensions/drape"))
	})

	t.Run("single-segment path", func(t *testing.T) {
		t.Parallel()
		got := magusfileGlobs("api")
		require.NotEmpty(t, got, "expected non-empty globs for single-segment path")
		for _, g := range got {
			assert.Truef(t, strings.HasPrefix(g.Pattern, "api/"), "glob %q should start with \"api/\"", g.Pattern)
		}
	})
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

// newWorkspace lays down a minimal multi-language workspace and
// returns it as a types.WorkspaceRepository rooted at it.
// Uses Inspect (no cache, no Teal preload) so tests don't need a
// runtime backend registered.
func newWorkspace(t *testing.T) types.WorkspaceRepository {
	t.Helper()
	w := testkit.NewWorkspace(t)
	w.WriteTree(map[string]string{
		"magusfile.buzz":                    "", // root project "."
		"api/magusfile.buzz":                "", // "api"
		"web/studio/magusfile.buzz":         "", // "web/studio"
		"extensions/drape/magusfile.buzz":   "", // "extensions/drape"
		"extensions/lattice/magusfile.buzz": "", // "extensions/lattice"
	})
	ws, err := Inspect(context.Background(), w.Root(), WithSpawnRule(TierCeiling))
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
	assert.Equal(t, types.Target{Path: "api", Name: "test"}, targets[0])
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
			assert.Equalf(t, types.Target{Name: wantName, Charms: wantCharms}, got, "ParseTarget(%q)", input)
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
// public API so internal refactors do not silently change behaviour.
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

// TestInspectWiresVCSOptionsFromConfig locks down that magus.yaml's vcs.name,
// vcs.base_ref, and vcs.enabled reach every vcs.Resolve call through
// m.ws.VCSOptions. This wiring was previously missing: the fields parsed and
// validated but inspect() never copied them onto the discovered workspace, so
// only the MAGUS_VCS_* env vars (read directly by package vcs) had any effect.
func TestInspectWiresVCSOptionsFromConfig(t *testing.T) {
	t.Parallel()
	w := testkit.NewWorkspace(t)
	w.Magusfile("")
	root := w.Root()

	cfg := config.Defaults()
	cfg.VCS.Name = "git"
	cfg.VCS.BaseRef = "origin/main"
	disabled := false
	cfg.VCS.Enabled = &disabled

	ws, err := Inspect(context.Background(), root, workspace.WithLoadedConfig(cfg), WithSpawnRule(TierCeiling))
	require.NoError(t, err, "Inspect")

	got := ws.VCSOptions()
	assert.Equal(t, types.VCSOptions{Name: "git", BaseRef: "origin/main", Enabled: &disabled}, got, "VCSOptions")
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
	_, err := remoteCacheSigningOpts(nil, false, "", "")
	assert.Error(t, err, "empty trust set was accepted; a remote cache must require trusted_keys")
	_, err = remoteCacheSigningOpts([]string{}, false, "", "")
	assert.Error(t, err, "empty trust-set slice was accepted")
}

// TestRemoteCacheInsecureSkipsTrustSet: the explicit opt-out accepts a wired
// backend with no trust set and no signing key, yielding the insecure option.
func TestRemoteCacheInsecureSkipsTrustSet(t *testing.T) {
	opts, err := remoteCacheSigningOpts(nil, true, "single-repo CI cache; nothing outside this repo can write it", "")
	require.NoError(t, err, "insecure mode rejected empty trust set")
	assert.Len(t, opts, 1, "insecure: want 1 opt (WithInsecureRemote)")
}

// TestRemoteCacheInsecureNeedsAReason pins the articulated-decision rule at the config
// site: a bare `insecure: true` is refused, and the refusal names the sibling key that
// satisfies it, because an existing config it breaks has to be repairable from the
// message alone.
func TestRemoteCacheInsecureNeedsAReason(t *testing.T) {
	_, err := remoteCacheSigningOpts(nil, true, "", "")
	require.Error(t, err, "a bare cache.remote.insecure switched off signature verification with nothing recorded")
	assert.Contains(t, err.Error(), "cache.remote.insecure_reason", "the refusal must name the exact key that satisfies it")

	_, err = remoteCacheSigningOpts(nil, true, "   ", "")
	assert.Error(t, err, "whitespace is not prose")
}

// TestRemoteCacheTrustSetDecodes: a valid trust set yields verification options;
// adding a signing key yields a signing option too.
func TestRemoteCacheTrustSetDecodes(t *testing.T) {
	pub, seed := b64Pub(t)

	opts, err := remoteCacheSigningOpts([]string{pub}, false, "", "")
	require.NoError(t, err, "valid trust set rejected")
	assert.Len(t, opts, 1, "verify-only: want 1 opt (trusted keys only)")

	opts, err = remoteCacheSigningOpts([]string{pub}, false, "", seed)
	require.NoError(t, err, "valid trust set + signing key rejected")
	assert.Len(t, opts, 2, "signing: want 2 opts (trusted keys + signing key)")
}

// TestRemoteCacheRejectsMalformedKeys: bad base64 in either the trust set or the
// signing key is a clear configuration error, not a silent fallback.
func TestRemoteCacheRejectsMalformedKeys(t *testing.T) {
	_, err := remoteCacheSigningOpts([]string{"not!base64!"}, false, "", "")
	assert.Error(t, err, "malformed trusted key was accepted")
	pub, _ := b64Pub(t)
	_, err = remoteCacheSigningOpts([]string{pub}, false, "", "not!base64!")
	assert.Error(t, err, "malformed signing key was accepted")
}

// TestOpenRefusesARequiredRemoteWriteWithNoBackend: cache.remote.write.enabled true in a
// workspace whose magusfile wires no backend fails Open instead of running local-only.
func TestOpenRefusesARequiredRemoteWriteWithNoBackend(t *testing.T) {
	testkit.Isolate(t)
	w := testkit.NewWorkspace(t)
	w.Magusfile("")
	root := w.Root()
	cfg := config.Defaults()
	cfg.Cache.Dir = filepath.Join(t.TempDir(), ".magus")
	required := true
	cfg.Cache.Remote.Write.Enabled = &required

	m, err := Open(t.Context(), root, workspace.WithLoadedConfig(cfg), WithSpawnRule(TierCeiling))
	if m != nil {
		_ = m.Close()
	}
	assert.ErrorContains(t, err, "remote writes are required but no remote backend is wired")
}

// TestOpenRemoteBackendThatDidNotStart: no magusfile here imports the github spell, so the
// wired backend fails to start. A required write surfaces that failure as the error; an
// optional one names it in the header the run prints.
func TestOpenRemoteBackendThatDidNotStart(t *testing.T) {
	opt := remoteBackendOption(t.Context(), "github", nil)
	require.NotNil(t, opt)

	_, err := cache.Open(t.Context(), filepath.Join(t.TempDir(), ".magus"), opt, cache.WithRemoteWrite(true))
	assert.ErrorContains(t, err, `run local-only: remote github unavailable: spell "github" is not registered`)

	c, err := cache.Open(t.Context(), filepath.Join(t.TempDir(), ".magus"), opt)
	require.NoError(t, err)
	tier, mode, _ := (&Magus{cache: c}).CacheDescription()
	assert.True(t, strings.HasPrefix(tier, `local (remote github unavailable: spell "github" is not registered`), tier)
	assert.Equal(t, "read+write", mode)
}

// TestSharedProviderVisibleAcrossMagus proves the /dashboard data-flow invariant: when two
// Magus instances are opened with ONE shared observability provider (WithProvider), a metric
// recorded through one is visible via the other's MetricsCollector. This is exactly what lets
// the server's bridge Magus read the counters that separate per-workspace registry builds
// record. Without a shared provider each Magus has its own ManualReader and the bridge
// collector reads zeros: the bug this feature fixes.
func TestSharedProviderVisibleAcrossMagus(t *testing.T) {
	ctx := context.Background()

	// One provider, LocalCollect on (as the server builds it), shared by both workspaces.
	tel, err := otlp.New(ctx, observability.Config{LocalCollect: true})
	require.NoError(t, err)

	mkWS := func() string {
		w := testkit.NewWorkspace(t)
		w.Magusfile("import \"magus\";\nexport fun build(ctx: magus\\Context, args: [str]) > void {}\n")
		return w.Root()
	}

	mBuild, err := Open(ctx, mkWS(), workspace.WithTelemetryProvider(tel), WithSpawnRule(TierCeiling))
	require.NoError(t, err)
	defer func() { _ = mBuild.Close() }()

	mBridge, err := Open(ctx, mkWS(), workspace.WithTelemetryProvider(tel), WithSpawnRule(TierCeiling))
	require.NoError(t, err)
	defer func() { _ = mBridge.Close() }()

	// Both workspaces adopted the SAME provider instance rather than building their own.
	assert.True(t, tel == mBuild.tel, "build workspace did not adopt the injected provider")
	assert.True(t, tel == mBridge.tel, "bridge workspace did not adopt the injected provider")

	// Record a target run through the "build" workspace's provider (as a per-workspace
	// registry build would)...
	mBuild.tel.RecordTargetRun(ctx, 0.5,
		observability.Attr{Key: "magus.target", Value: "build"},
		observability.Attr{Key: "outcome", Value: string(observability.OutcomeOK)},
	)

	// ...and read it back through the "bridge" workspace's collector (as the dashboard does).
	coll, ok := mBridge.MetricsCollector()
	require.True(t, ok, "shared local-collect provider must yield a collector")

	rm, err := coll.Collect(ctx)
	require.NoError(t, err)

	var targetRuns uint64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "magus.target.duration" {
				continue
			}
			hist, isHist := m.Data.(metricdata.Histogram[float64])
			require.True(t, isHist, "magus.target.duration should be a float64 histogram")
			for _, dp := range hist.DataPoints {
				targetRuns += dp.Count
			}
		}
	}
	assert.Equal(t, uint64(1), targetRuns,
		"a target run recorded via one Magus must be visible through the other's shared-provider collector")
}

// TestCrossFileInputs is the end-to-end payoff of the unified per-target inputs: a target
// declaring ctx.readsFiles(<sibling>.file("go.mod"), "app/**") stores BOTH a cross-project
// and a same-project input in ONE TargetInputs list, each carrying its owning project.
// The cross input folds into the cache key WORKSPACE-relative (lib/go.mod, NOT
// consumer/lib/go.mod) and unions its owning project into DependsOn; the same-project
// input folds relative to the consumer (consumer/app/**) and adds NO self-dependency. A
// change to either file marks the consumer affected: the cross one via the DependsOn
// reverse-closure, the same-project one via directory containment. All recovered
// statically at Open, no runtime dispatch.
func TestCrossFileInputs(t *testing.T) {
	w := testkit.NewWorkspace(t)
	write := func(rel, content string) { w.Write(rel, content) }

	// Workspace root marker.
	write("magusfile.buzz", "")
	// The owning project and the file the consumer reaches for.
	write("lib/magusfile.buzz", "import \"magus\";\nexport fun compile(ctx: magus\\Context, args: [str]) > void {}\n")
	write("lib/go.mod", "module lib\n")
	// The consumer declares a cross-project AND a same-project input on the same target.
	write("consumer/app/main.go", "package app\n")
	write("consumer/magusfile.buzz", `import "magus";
import "project/../lib";
export fun build(ctx: magus\Context, args: [str]) > void {
    ctx.readsFiles(lib.file("go.mod"), "app/**");
}
`)

	m, err := Open(context.Background(), w.Root(), WithSpawnRule(TierCeiling))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	consumer := m.Get("consumer")
	require.NotNil(t, consumer, "consumer project must be discovered")

	// ONE list holds both inputs, each with its resolved owning project: the same-project
	// literal owned by "consumer" (folded relative to it), the cross input owned by "lib"
	// (folded workspace-relative, never re-anchored onto the consumer's path).
	assert.Equal(t, []types.InputRef{
		{Project: "consumer", Glob: "app/**"},
		{Project: "lib", Glob: "go.mod"},
	}, consumer.TargetInputs["build"],
		"same-project and cross-project inputs share one TargetInputs list, each owning-project-tagged")

	// The cross input's owning project is unioned into DependsOn, required for
	// affected-tracking (a DependsOn-reverse-closure). The same-project input adds no
	// self-edge (the depgraph rejects self-loops; it seeds by directory containment).
	assert.Contains(t, consumer.DependsOn, "lib",
		"the cross-input owning project must be a project-level dependency of the consumer")
	assert.NotContains(t, consumer.DependsOn, "consumer",
		"a same-project input must not add a self-dependency")

	// The buildStep hashes each input at its workspace-relative path.
	step := m.buildStep(consumer, "build")
	assert.Contains(t, step.Sources, types.Glob{Pattern: "lib/go.mod"},
		"the cross-input enters the cache key workspace-relative")
	assert.Contains(t, step.Sources, types.Glob{Pattern: "consumer/app/**"},
		"the same-project input enters the cache key relative to the consumer")

	// The payoff, both ways: editing lib/go.mod marks the consumer affected via DependsOn.
	res, err := m.AffectedFromPaths(context.Background(), []string{"lib/go.mod"})
	require.NoError(t, err, "AffectedFromPaths (cross)")
	assert.Contains(t, res.Affected, "consumer",
		"a change to a declared cross-input file must mark the consumer affected")

	// And editing the same-project input file marks the consumer affected via containment.
	res, err = m.AffectedFromPaths(context.Background(), []string{"consumer/app/main.go"})
	require.NoError(t, err, "AffectedFromPaths (same-project)")
	assert.Contains(t, res.Affected, "consumer",
		"a change to a same-project input file must mark the consumer affected")
}

// collapseOnSuccess decides whether a passing target's own output is withheld.
// The property worth pinning is that it keys on Stream and NOT on Level: raising
// verbosity asks for more records, not for every project's stdout at once. Those
// were the same switch until -v and -vv needed to differ.
func TestCollapseOnSuccessIgnoresLevel(t *testing.T) {
	t.Parallel()

	yes := true
	cases := []struct {
		name string
		log  config.Log
		want bool
	}{
		{"default", config.Log{Format: "pretty"}, true},
		{"debug level alone does not stream", config.Log{Format: "pretty", Level: "debug"}, true},
		{"trace level alone does not stream", config.Log{Format: "pretty", Level: "trace"}, true},
		{"stream disables collapse", config.Log{Format: "pretty", Stream: &yes}, false},
		{"silent disables collapse", config.Log{Format: "pretty", Silent: &yes}, false},
		{"machine format cannot collapse", config.Log{Format: "json"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, collapseOnSuccess(tc.log))
		})
	}
}

// TestForEachSpell_MagusfileShadowsSpellOp pins the precedence rule. This repo exports
// go_build from its magusfile while the go spell provides an op that normalizes to the
// same name, and before the rule BOTH ran: the bare op compiled the module with no -o,
// no ldflags and no trimpath, then the magusfile did the real build. The waste was
// invisible because neither run failed.
func TestForEachSpell_MagusfileShadowsSpellOp(t *testing.T) {
	t.Parallel()

	var ran []string
	newSpell := func(name string, targets ...string) *spells.Spell {
		return spells.NewSpell(name,
			spells.WithTargets(targets...),
			spells.WithInvoker(func(_ context.Context, _ spells.InvokeRequest) (any, error) {
				ran = append(ran, name)
				return nil, nil
			}))
	}

	p := &types.Project{
		Path:             ".",
		MagusfileTargets: []string{"go-build"},
	}
	p.ResolvedSpells = []*spells.Spell{
		newSpell("go", "go-build"),
		newSpell(types.MagusfileSpellName),
	}

	require.NoError(t, forEachSpell(context.Background(), p, "go-build",
		func(ctx context.Context, s *spells.Spell) error {
			_, err := s.Invoke(ctx, spells.InvokeRequest{Dir: ".", Target: "go-build"})
			return err
		}))
	assert.Equal(t, []string{types.MagusfileSpellName}, ran,
		"the magusfile target must run alone; the shadowed spell op must not also run")
}

// recordingProvider is a telemetry provider that records whether Shutdown ran (and can be
// told to error); every other method is observability.Nop's.
type recordingProvider struct {
	observability.Nop
	shutdownCalled bool
	shutdownErr    error
}

func (p *recordingProvider) Enabled() bool { return true }
func (p *recordingProvider) Shutdown(context.Context) error {
	p.shutdownCalled = true
	return p.shutdownErr
}

var _ observability.Provider = (*recordingProvider)(nil)

// TestClose_ShutsDownOwnedProvider is P1-A's regression test: before the fix, Close
// only closed the buzz pool registry, so a provider built by Open (spans/metrics)
// was silently dropped on exit rather than flushed. Pre-fix this failed with
// "shutdownCalled: Expected true, but got false" because nothing called Shutdown.
func TestClose_ShutsDownOwnedProvider(t *testing.T) {
	p := &recordingProvider{}
	m := &Magus{tel: p}

	err := m.Close()

	require.NoError(t, err)
	assert.True(t, p.shutdownCalled, "Close must shut down a provider it owns")
}

// TestClose_JoinsProviderShutdownError verifies a shutdown error is surfaced (not
// swallowed) and that Close does not bail out early, required so a later resource
// (the pool registry) still gets a chance to close even when telemetry shutdown fails.
func TestClose_JoinsProviderShutdownError(t *testing.T) {
	wantErr := errors.New("otlp: flush failed")
	p := &recordingProvider{shutdownErr: wantErr}
	m := &Magus{tel: p}

	err := m.Close()

	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

// TestClose_LeavesInjectedProviderRunning covers the server case (WithProvider):
// several workspaces plus the bridge Magus share ONE provider so metrics survive
// workspace eviction (see cmd/magus/registry.go's wsRegistry, which Closes an idle
// workspace while the shared provider keeps serving the others). Close must not
// shut down a provider it does not own.
func TestClose_LeavesInjectedProviderRunning(t *testing.T) {
	p := &recordingProvider{}
	m := &Magus{tel: p, injectedTel: p}

	err := m.Close()

	require.NoError(t, err)
	assert.False(t, p.shutdownCalled, "Close must not shut down a shared/injected provider")
}

// floorWorkspace writes a minimal workspace whose magus.yaml declares constraint.
func floorWorkspace(t *testing.T, constraint string) string {
	t.Helper()
	w := testkit.NewWorkspace(t)
	yaml := ""
	if constraint != "" {
		yaml = fmt.Sprintf("required_version: \"%s\"\n", constraint)
	}
	w.Write("magus.yaml", yaml)
	w.Magusfile("import \"magus\";\nexport fun ci(ctx: magus\\Context, args: [str]) > void {}\n")
	return w.Root()
}

// A load joins one error per failing file; each is its own diagnostic. A coded type error
// needs the magusfile bindings, which cmd/magus links; its registry test covers that case.
func TestWorkspaceLoadFailureLocatesEachJoinedFile(t *testing.T) {
	err := fmt.Errorf("magus: repo: %w", errors.Join(
		&interp.ExecError{Path: "/repo/a/magusfile.buzz", Err: errors.New("buzz: line 2:1: expected identifier")},
		&interp.ExecError{Path: "/elsewhere/spell.buzz", Err: errors.New("buzz: line 5:4: unexpected }")},
		// The shape a failed spell import takes: the file is on the wrapper, and each
		// import's error is a branch of the join beneath it.
		&interp.ImportError{Path: "/repo/b/magusfile.buzz", Err: errors.Join(
			errors.New("load spell: buzz: line 3:7: expected expression"),
		)},
	))
	assert.Equal(t, &types.WorkspaceFailure{
		Message: err.Error(),
		Diagnostics: []types.SourceDiagnostic{
			{File: "a/magusfile.buzz", Line: 2, Column: 1, Message: "expected identifier"},
			{File: "/elsewhere/spell.buzz", Line: 5, Column: 4, Message: "unexpected }"},
			{File: "b/magusfile.buzz", Line: 3, Column: 7, Message: "expected expression"},
		},
	}, WorkspaceLoadFailure("/repo", err))
	assert.Contains(t, err.Error(), "magusfile: exec /repo/a/magusfile.buzz: buzz:",
		"an error built without a relative name falls back to the absolute path")
	assert.Contains(t, err.Error(), "magusfile: /repo/b/magusfile.buzz: load spell")
}

func TestWorkspaceLoadFailureWithoutAPosition(t *testing.T) {
	err := errors.New("load config /repo: magus.yaml: unknown key")
	assert.Equal(t, &types.WorkspaceFailure{Message: err.Error()}, WorkspaceLoadFailure("/repo", err))
}

// The wiring, not the comparison: internal/ward covers the semver logic, and this
// covers that magus.yaml's required_version actually reaches it. That path is easy to
// break invisibly: the field carries `cli:"-"`, so it is absent from the generated
// flag/env/schema tables, and a loader that read those instead of the struct tags
// would silently see an empty floor and admit every binary.
//
// It also cannot be exercised by a dev build: an unstamped binary reports "unknown"
// and the ward exempts it by design, so `go run ./cmd/magus` against a floored
// workspace always passes. A version has to be injected to test this at all.
func TestRequiredVersion_TooOldBinaryIsRefused(t *testing.T) {
	root := floorWorkspace(t, ">= 99.0.0")

	_, err := Inspect(context.Background(), root, WithVersion("v0.3.0"), WithSpawnRule(TierCeiling))

	require.Error(t, err, "a binary below the declared floor must not open the workspace")
	assert.ErrorIs(t, err, types.WorkspaceNeedsNewerMagus)
	assert.Contains(t, err.Error(), ">= 99.0.0")
	assert.Contains(t, err.Error(), "v0.3.0")
}

func TestRequiredVersion_SatisfiedBinaryOpens(t *testing.T) {
	root := floorWorkspace(t, ">= 0.3.0")

	_, err := Inspect(context.Background(), root, WithVersion("v0.4.0"), WithSpawnRule(TierCeiling))

	require.NoError(t, err)
}

// No floor declared is the overwhelmingly common case and must stay free.
func TestRequiredVersion_NoFloorOpens(t *testing.T) {
	root := floorWorkspace(t, "")

	_, err := Inspect(context.Background(), root, WithVersion("v0.1.0"), WithSpawnRule(TierCeiling))

	require.NoError(t, err)
}

// A library caller that never supplied a version has no version to be too old.
func TestRequiredVersion_NoVersionSuppliedOpens(t *testing.T) {
	root := floorWorkspace(t, ">= 99.0.0")

	_, err := Inspect(context.Background(), root, WithSpawnRule(TierCeiling))

	require.NoError(t, err, "magus.Open/Inspect without WithVersion must not be gated")
}

func TestStatusLinePath(t *testing.T) {
	cases := map[string]string{
		"?? cmd/magus/review.go":  "cmd/magus/review.go", // git porcelain, untracked
		" M magus.go":             "magus.go",            // git porcelain, modified (leading space)
		"A  internal/new.go":      "internal/new.go",     // staged add
		"R  old.go -> new.go":     "new.go",              // a rename keeps the name on disk
		"? scratch.txt":           "scratch.txt",         // hg
		"bare/path/with/no/state": "bare/path/with/no/state",
		"":                        "",
		"   ":                     "",
	}
	for line, want := range cases {
		assert.Equal(t, want, statusLinePath(line), "line %q", line)
	}
}

// A brand-new file is the thing a reviewer most wants to see, and a tree-against-index diff
// misses it entirely. This is the regression test for that gap.
func TestWorkingDiffIncludesUntrackedFiles(t *testing.T) {
	repo := testkit.NewRepo(t, map[string]string{"tracked.txt": "one\ntwo\n"})

	// One modified tracked file and one brand-new untracked file.
	repo.Write("tracked.txt", "one\nCHANGED\n")
	repo.Write("fresh.txt", "hello\nworld\n")

	m, err := Open(context.Background(), repo.Root(), WithSpawnRule(TierCeiling))
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	patch, err := m.WorkingDiff(context.Background(), nil)
	require.NoError(t, err)

	assert.Contains(t, patch, "tracked.txt", "the tracked change must still be there")
	assert.Contains(t, patch, "diff --git a/fresh.txt b/fresh.txt",
		"an untracked file must appear, or a review cannot show a new file at all")
	assert.Contains(t, patch, "new file mode")
	assert.Contains(t, patch, "+hello")
	assert.Contains(t, patch, "+world")
}

func TestWorkingDiffMarksUntrackedBinaryRatherThanDumpingIt(t *testing.T) {
	repo := testkit.NewRepo(t, map[string]string{"seed.txt": "x\n"})

	repo.Write("blob.bin", string([]byte{0x00, 0x01, 0x02, 0x00}))

	m, err := Open(context.Background(), repo.Root(), WithSpawnRule(TierCeiling))
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	patch, err := m.WorkingDiff(context.Background(), nil)
	require.NoError(t, err)
	assert.Contains(t, patch, "Binary files /dev/null and b/blob.bin differ",
		"a binary file must be marked, not rendered as a wall of mojibake")
}

// The two halves are CONCATENATED, and a tracked diff whose last line is not
// newline-terminated would otherwise glue the first synthesized header onto it, so that
// header stops starting a line, every reader misses it, and the first untracked file vanishes
// from the review while the rest appear normally. It cost one real file here and reported
// nothing, which is what makes it worth a test rather than a comment.
func TestWorkingDiffSeparatesTheTrackedAndUntrackedHalves(t *testing.T) {
	// Seed a file with NO trailing newline, then change it: git renders "\ No newline at end
	// of file" and the diff's own last line is unterminated.
	repo := testkit.NewRepo(t, map[string]string{"nonewline.txt": "one"})
	repo.Write("nonewline.txt", "two")
	repo.Write("brand-new.txt", "fresh\n")

	m, err := Open(context.Background(), repo.Root(), WithSpawnRule(TierCeiling))
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	patch, err := m.WorkingDiff(context.Background(), nil)
	require.NoError(t, err)

	// The header must START a line. Asserting Contains would pass even when glued.
	var headers []string
	for _, l := range strings.Split(patch, "\n") {
		if strings.HasPrefix(l, "diff --git ") {
			headers = append(headers, l)
		}
	}
	assert.Contains(t, headers, "diff --git a/brand-new.txt b/brand-new.txt",
		"the first untracked file's header must begin a line, not be appended to the tracked half")
}

// A clean tree has nothing to review, and must not synthesize a patch out of ignored files.
func TestWorkingDiffOnACleanTreeIsEmpty(t *testing.T) {
	repo := testkit.NewRepo(t, map[string]string{"a.txt": "x\n"})

	m, err := Open(context.Background(), repo.Root(), WithSpawnRule(TierCeiling))
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	patch, err := m.WorkingDiff(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(patch))
}

// ApplyEnv is generated (internal/config/gen/env.go) from the Config struct. Its
// env-to-Config behavior is tested here, in a consuming package, rather than inside the
// generated tree.
func TestApplyEnv_VolatilityEnabledTrue(t *testing.T) {
	t.Setenv("MAGUS_VOLATILITY_ENABLED", "true")
	cfg := config.Defaults()
	require.NoError(t, configgen.ApplyEnv(&cfg, os.Getenv))
	assert.True(t, cfg.Volatility.Enabled, "MAGUS_VOLATILITY_ENABLED=true: Volatility.Enabled should be true")
}

func TestApplyEnv_VolatilityEnabledFalse(t *testing.T) {
	t.Setenv("MAGUS_VOLATILITY_ENABLED", "false")
	cfg := config.Defaults()
	require.NoError(t, configgen.ApplyEnv(&cfg, os.Getenv))
	assert.False(t, cfg.Volatility.Enabled, "MAGUS_VOLATILITY_ENABLED=false: Volatility.Enabled should be false")
}

func TestApplyEnvToConfig(t *testing.T) {
	t.Setenv("MAGUS_CACHE_WRITE_ENABLED", "false")
	t.Setenv("MAGUS_CONCURRENCY", "6")
	t.Setenv("MAGUS_DRY_RUN", "1")

	cfg := config.Defaults()
	require.NoError(t, configgen.ApplyEnv(&cfg, os.Getenv))

	assert.False(t, cfg.Cache.WriteEnabled())
	assert.Equal(t, 6, cfg.Concurrency)
	assert.True(t, cfg.DryRun, "DryRun should be true")
}

// An unrecognized boolean is refused for both bool kinds, and the field keeps its value.
func TestApplyEnv_UnrecognizedBooleanIsAnError(t *testing.T) {
	env := map[string]string{
		"MAGUS_DRY_RUN":                    "ture",
		"MAGUS_CACHE_REMOTE_WRITE_ENABLED": "off",
	}
	cfg := config.Defaults()
	err := configgen.ApplyEnv(&cfg, func(k string) string { return env[k] })
	require.Error(t, err)
	assert.ErrorContains(t, err, `MAGUS_DRY_RUN: "ture" is not a boolean`)
	assert.ErrorContains(t, err, `MAGUS_CACHE_REMOTE_WRITE_ENABLED: "off" is not a boolean`)
	assert.False(t, cfg.DryRun)
	assert.Nil(t, cfg.Cache.Remote.Write.Enabled)
}

func TestApplyEnvSandboxMode(t *testing.T) {
	cfg := config.Defaults()
	require.NoError(t, configgen.ApplyEnv(&cfg, func(k string) string {
		return map[string]string{"MAGUS_SANDBOX": "required"}[k]
	}))
	assert.Equal(t, types.SandboxModeRequired, cfg.Sandbox.Mode)

	cfg = config.Defaults()
	err := configgen.ApplyEnv(&cfg, func(k string) string {
		return map[string]string{"MAGUS_SANDBOX": "on"}[k]
	})
	assert.ErrorContains(t, err, `MAGUS_SANDBOX: unknown sandbox mode "on"`)

	// The retired name is an error naming its replacement, never a sandbox quietly off.
	err = configgen.ApplyEnv(&cfg, func(k string) string {
		return map[string]string{"MAGUS_SANDBOX_ENABLED": "1"}[k]
	})
	assert.ErrorContains(t, err, "MAGUS_SANDBOX_ENABLED was renamed to MAGUS_SANDBOX")
}

// MAGUS_SANDBOX is the mode a sandboxed parent hands its children, and a floor: a
// nested workspace that declares a stronger mode runs under its own, and one that
// declares a weaker mode runs under its parent's.
func TestApplyEnvSandboxModeIsAFloor(t *testing.T) {
	parent := func(k string) string { return map[string]string{"MAGUS_SANDBOX": "best-effort"}[k] }
	for declared, want := range map[types.SandboxMode]types.SandboxMode{
		types.SandboxModeRequired:   types.SandboxModeRequired,
		types.SandboxModeOff:        types.SandboxModeBestEffort,
		"":                          types.SandboxModeBestEffort,
		types.SandboxModeBestEffort: types.SandboxModeBestEffort,
	} {
		cfg := config.Defaults()
		cfg.Sandbox.Mode = declared
		require.NoError(t, configgen.ApplyEnv(&cfg, parent))
		assert.Equal(t, want, cfg.Sandbox.Mode, "workspace declares %q", declared)
	}
}

// The environment overwrites fields after the yaml is validated, so an SDK load has to
// validate again: an unknown profile must be a config error, never a value that reaches
// the limiter.
func TestLoadConfigValidatesTheEnvironment(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Write("magus.yaml", "concurrency_profile: balanced\n")

	_, err := loadConfig(w.Root(), WithEnv([]string{"MAGUS_CONCURRENCY_PROFILE=turbo"}))
	require.ErrorContains(t, err, `MAGUS_CONCURRENCY_PROFILE: unknown concurrency profile "turbo"`)
}

// TestLoadConfigReadsOnlyTheEnvironmentItIsGiven: a library workspace given no
// environment is configured by its magus.yaml alone, whatever the process environment
// says, and one given an environment reads that and not the process's.
func TestLoadConfigReadsOnlyTheEnvironmentItIsGiven(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Write("magus.yaml", "concurrency: 3\n")
	t.Setenv("MAGUS_CONCURRENCY", "5")

	cfg, err := loadConfig(w.Root())
	require.NoError(t, err)
	assert.Equal(t, 3, cfg.Concurrency, "no WithEnv: the process environment is not read")

	cfg, err = loadConfig(w.Root(), WithEnv([]string{"MAGUS_CONCURRENCY=7"}))
	require.NoError(t, err)
	assert.Equal(t, 7, cfg.Concurrency, "WithEnv: the given environment overrides magus.yaml")
}

// TestWorkspaceProcessOptionsDefaultToNothing: without WithStderr notices are
// discarded and without WithCommandLine no command line is recorded; with them, the
// workspace keeps what it was given.
func TestWorkspaceProcessOptionsDefaultToNothing(t *testing.T) {
	root := writeWorkspace(t, map[string]string{"magusfile.buzz": ""})
	ws, err := Inspect(t.Context(), root, WithSpawnRule(TierCeiling))
	require.NoError(t, err)
	bare := ws.(*Magus)
	assert.Equal(t, io.Discard, bare.noticeWriter())
	assert.Empty(t, bare.commandLine)
	assert.Empty(t, bare.env("PATH"), "no WithEnv: no environment variable reads as set")

	var notices bytes.Buffer
	ws, err = Inspect(t.Context(), root, WithStderr(&notices), WithCommandLine([]string{"magus", "run", "x"}), WithEnv([]string{"A=1", "A=2"}), WithSpawnRule(TierCeiling))

	require.NoError(t, err)
	given := ws.(*Magus)
	assert.Same(t, &notices, given.noticeWriter())
	assert.Equal(t, []string{"magus", "run", "x"}, given.commandLine)
	assert.Equal(t, "2", given.env("A"), "a key set twice reads its last value, as os.Getenv does")
}

// TestFindRootRefusesAnEmptyDir: the library has no working directory to fall back to.
func TestFindRootRefusesAnEmptyDir(t *testing.T) {
	_, err := FindRoot("")
	require.ErrorContains(t, err, "needs a directory")
}

// yaml reaches the same door as env and flags, so a misspelled profile stops the load.
func TestLoadConfigRefusesAnUnknownProfile(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Write("magus.yaml", "concurrency_profile: turbo\n")

	_, err := config.LoadFile(w.Path("magus.yaml"), false)
	require.ErrorContains(t, err, `unknown concurrency profile "turbo"`)
}

// TestTargetLabel renders the scope header a run prints. The empty case is the one
// worth pinning: a scope that selected nothing says so, rather than rendering as
// "0 projects" among the plural forms.
func TestTargetLabel(t *testing.T) {
	one := []types.Target{{Path: "api", Name: "build"}}
	several := []types.Target{{Path: "api"}, {Path: "web"}, {Path: "."}}

	assert.Equal(t, "no projects", TargetLabel(nil, ""))
	assert.Equal(t, "no projects (affected)", TargetLabel(nil, "affected"))
	assert.Equal(t, "api", TargetLabel(one, ""))
	assert.Equal(t, "api (affected)", TargetLabel(one, "affected"))
	assert.Equal(t, "3 projects", TargetLabel(several, ""))
	assert.Equal(t, "3 projects (stdin paths)", TargetLabel(several, "stdin paths"))
}

func TestReviewOriginReadsRefWithoutMetadata(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	d := typestest.NewMockVCSDriver(t)
	d.EXPECT().Ref(mock.Anything, root).Return("feat/x", nil).Once()
	d.EXPECT().RemoteURL(mock.Anything, root, "").Return("https://example.com/o/r.git", nil).Once()
	m := &Magus{ws: &types.Workspace{Root: root}}

	got := m.resolveReviewOrigin(t.Context(), d)

	assert.Equal(t, types.ReviewOrigin{Branch: "feat/x", Remote: "https://example.com/o/r.git"}, got)
	d.AssertNotCalled(t, "Metadata", mock.Anything, mock.Anything)
}

func TestReviewOriginRefErrorLeavesBranchEmpty(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	d := typestest.NewMockVCSDriver(t)
	d.EXPECT().Ref(mock.Anything, root).Return("", errors.New("unborn branch")).Once()
	d.EXPECT().RemoteURL(mock.Anything, root, "").Return("", errors.New("no remote")).Once()
	m := &Magus{ws: &types.Workspace{Root: root}}

	assert.Equal(t, types.ReviewOrigin{}, m.resolveReviewOrigin(t.Context(), d))
	d.AssertNotCalled(t, "Metadata", mock.Anything, mock.Anything)
}

func TestLastPassedBaseReadsRefWithoutMetadata(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	d := typestest.NewMockVCSDriver(t)
	d.EXPECT().Ref(mock.Anything, root).Return("feat/x", nil).Once()
	d.EXPECT().ParentRef().Return("HEAD~1").Once()
	m := &Magus{ws: &types.Workspace{Root: root}}
	m.cfg.CI.RecordRuns = true
	m.cfg.HistoryPath = filepath.Join(t.TempDir(), "history.json")
	res := types.VCSResolution{Name: "git", Source: types.VCSSourceAuto, Base: BaseLastPassed, VCS: d}

	got, err := m.lastPassedBase(t.Context(), res)

	require.NoError(t, err)
	assert.Equal(t, "HEAD~1", got)
	d.AssertNotCalled(t, "Metadata", mock.Anything, mock.Anything)
}

func TestLastPassedBaseRefErrorKeepsItsText(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cause := errors.New("unborn branch")
	d := typestest.NewMockVCSDriver(t)
	d.EXPECT().Ref(mock.Anything, root).Return("", cause).Once()
	m := &Magus{ws: &types.Workspace{Root: root}}
	m.cfg.CI.RecordRuns = true
	res := types.VCSResolution{Name: "git", Source: types.VCSSourceAuto, Base: BaseLastPassed, VCS: d}

	_, err := m.lastPassedBase(t.Context(), res)

	require.ErrorIs(t, err, cause)
	assert.EqualError(t, err, `base "last-passed": read git metadata: unborn branch`)
	d.AssertNotCalled(t, "Metadata", mock.Anything, mock.Anything)
}

// rangedSymbol is an indexed symbol with its definition's extent and SCIP kind.
func rangedSymbol(id, label, source string, end int, kind string) types.KnowledgeNode {
	n := symbolNode(id, label, source, fmt.Sprintf("func %s()", label), "")
	n.Attrs[attrDefEndLine] = fmt.Sprint(end)
	n.Attrs[attrSymbolKind] = kind
	return n
}

func TestTouchedSymbolsAreTheOnesThePatchChanged(t *testing.T) {
	patch := `diff --git a/api.go b/api.go
--- a/api.go
+++ b/api.go
@@ -1,9 +1,13 @@
 package api

 func Open() {
-	a()
+	b()
 }

-func Close() {
+func Close(force bool) {
 	c()
 }
+
+func Fresh() {
+	d()
+}
`
	head := newGraph(
		rangedSymbol(goSymbol("api", ""), "api", "api.go:1", 0, "Package"),
		rangedSymbol(goSymbol("api", "Open()."), "Open", "api.go:3", 5, "Function"),
		rangedSymbol(goSymbol("api", "Close()."), "Close", "api.go:7", 9, "Function"),
		rangedSymbol(goSymbol("api", "Fresh()."), "Fresh", "api.go:11", 13, "Function"),
		rangedSymbol(goSymbol("api", "Elsewhere()."), "Elsewhere", "other.go:4", 6, "Function"),
	)
	got := touchedSymbols(head, patch, map[string]*types.DiffFile{"api.go": {Path: "api.go"}})
	require.Equal(t, map[string]touchedSymbol{
		goSymbol("api", "Open()."):  {change: types.DiffChangeBody, signature: "func Open()", kind: "Function", place: hunkPlace{hunks: []int{0}, start: 3, extent: 3}},
		goSymbol("api", "Close()."): {change: types.DiffChangeSignature, signature: "func Close()", kind: "Function", place: hunkPlace{hunks: []int{0}, start: 7, extent: 3}},
		goSymbol("api", "Fresh()."): {change: types.DiffChangeAdded, signature: "func Fresh()", kind: "Function", place: hunkPlace{hunks: []int{0}, start: 11, extent: 3}},
	}, got)
}

const twoHunkPatch = `diff --git a/api.go b/api.go
--- a/api.go
+++ b/api.go
@@ -2,4 +2,4 @@ package api

 func Open() {
-	a()
+	b()
 }
@@ -20,3 +20,3 @@ func Close() {
 func Close() {
-	c()
+	d()
 }
`

// A file's hunks each carry the digest the parser gave them and only the symbols whose lines
// that hunk changed.
func TestDiffHunksSplitSymbolsAcrossHunks(t *testing.T) {
	open, closeID := goSymbol("api", "Open()."), goSymbol("api", "Close().")
	head := newGraph(
		rangedSymbol(open, "Open", "api.go:3", 5, "Function"),
		rangedSymbol(closeID, "Close", "api.go:20", 22, "Function"),
	)
	files := []types.DiffFile{{Path: "api.go", Symbols: []types.DiffSymbol{{ID: closeID}, {ID: open}}}}
	touched := touchedSymbols(head, twoHunkPatch, map[string]*types.DiffFile{"api.go": &files[0]})
	places := map[string]map[string]hunkPlace{"api.go": {}}
	for id, ts := range touched {
		places["api.go"][id] = ts.place
	}

	attachHunks(files, twoHunkPatch, places)

	parsed := changeset.Parse(twoHunkPatch)
	require.Len(t, parsed, 1)
	require.Len(t, parsed[0].Hunks, 2)
	h0, h1 := parsed[0].Hunks[0], parsed[0].Hunks[1]
	assert.Equal(t, []types.DiffHunk{
		{Index: 0, Digest: h0.Digest, OldStart: 2, OldCount: 4, NewStart: 2, NewCount: 4, Declaration: "package api", Symbols: []string{open}},
		{Index: 1, Digest: h1.Digest, OldStart: 20, OldCount: 3, NewStart: 20, NewCount: 3, Declaration: "func Close() {", Symbols: []string{closeID}},
	}, files[0].Hunks)
	assert.NotEmpty(t, h0.Digest)
	assert.NotEqual(t, h0.Digest, h1.Digest)
}

// A hunk no listed symbol sits in is still a hunk, and a file the patch has no hunks for
// gets none.
func TestDiffHunksIncludeHunksWithoutSymbols(t *testing.T) {
	files := []types.DiffFile{{Path: "api.go"}, {Path: "other.go"}}

	attachHunks(files, twoHunkPatch, nil)

	require.Len(t, files[0].Hunks, 2)
	assert.Empty(t, files[0].Hunks[0].Symbols)
	assert.Empty(t, files[0].Hunks[1].Symbols)
	assert.Nil(t, files[1].Hunks)
}

// Symbols a hunk shares come innermost first: the smaller definition, then the later start.
func TestDiffHunkSymbolsAreInnermostFirst(t *testing.T) {
	files := []types.DiffFile{{Path: "api.go", Symbols: []types.DiffSymbol{{ID: "outer"}, {ID: "inner"}, {ID: "sibling"}}}}
	places := map[string]map[string]hunkPlace{"api.go": {
		"outer":   {hunks: []int{0}, start: 3, extent: 10},
		"inner":   {hunks: []int{0}, start: 5, extent: 2},
		"sibling": {hunks: []int{0}, start: 8, extent: 2},
	}}

	attachHunks(files, twoHunkPatch, places)

	assert.Equal(t, []string{"sibling", "inner", "outer"}, files[0].Hunks[0].Symbols)
}

// A changed test function has no referents and leaves no module, and is still the
// declaration a reviewer and a split plan need named. An object-literal property SCIP
// indexes as a symbol is not.
func TestListedDiffSymbolsKeepTouchedDeclarations(t *testing.T) {
	unreferenced := types.DiffSymbol{ID: goSymbol("api", "TestOpen()."), Label: "TestOpen"}
	assert.True(t, listedDiffSymbol(unreferenced, "Function"), "a touched test function")
	assert.True(t, listedDiffSymbol(unreferenced, "Method"), "a touched method")
	assert.False(t, listedDiffSymbol(unreferenced, ""), "untouched and unreferenced")
	assert.False(t, listedDiffSymbol(unreferenced, "UnspecifiedKind"), "a TypeScript property")
	assert.False(t, listedDiffSymbol(unreferenced, "Parameter"))
	assert.True(t, listedDiffSymbol(types.DiffSymbol{RefCount: 2, FileCount: 1}, ""), "referenced")
	assert.True(t, listedDiffSymbol(types.DiffSymbol{PublicBeyondWorkspace: true}, ""), "exported")
}

func TestTouchedSymbolsNeverIncludeANamespace(t *testing.T) {
	assert.True(t, namespaceSymbol(goSymbol("api", ""), ""))
	assert.True(t, namespaceSymbol("symbol:npm pkg 1.0 src/`mod.ts`/", "Namespace"))
	assert.False(t, namespaceSymbol(goSymbol("api", "Open()."), "Function"))
}

func TestPublicThroughCrossesPackagesAndProjects(t *testing.T) {
	ns := func(pkg string) map[string]string { return map[string]string{attrNamespace: pkg} }
	sym := func(pkg, desc, label, source string) types.KnowledgeNode {
		return types.KnowledgeNode{ID: goSymbol(pkg, desc), Kind: types.KindSymbol, Label: label, Source: source, Attrs: ns(goSymbol(pkg, ""))}
	}
	helper, public, exported := goSymbol("api", "helper()."), goSymbol("api", "Public()."), goSymbol("api", "Exported().")
	entry, tool := goSymbol("cli", "main()."), goSymbol("tools", "run().")
	defines := func(file, id string) types.KnowledgeEdge {
		return types.KnowledgeEdge{Source: "file:" + file, Target: id, Relation: types.RelationDefines}
	}
	references := func(file, id string) types.KnowledgeEdge {
		return types.KnowledgeEdge{Source: "file:" + file, Target: id, Relation: types.RelationReferences, Provenance: "scip count=1 lines=3"}
	}
	calls := func(from, to string) types.KnowledgeEdge {
		return types.KnowledgeEdge{Source: from, Target: to, Relation: types.RelationCalls}
	}
	g := knowledge.NewGraph()
	g.Merge([]types.KnowledgeNode{
		sym("api", "helper().", "helper", "api/a.go:3"),
		sym("api", "Public().", "Public", "api/a.go:10"),
		sym("api", "Exported().", "Exported", "api/a.go:20"),
		sym("cli", "main().", "main", "cli/main.go:5"),
		sym("tools", "run().", "run", "tools/t.go:5"),
		{ID: "file:api/a.go", Kind: types.KindFile},
		{ID: "file:cli/main.go", Kind: types.KindFile},
		{ID: "file:tools/t.go", Kind: types.KindFile},
	}, []types.KnowledgeEdge{
		defines("api/a.go", helper), defines("api/a.go", public), defines("api/a.go", exported),
		defines("cli/main.go", entry), defines("tools/t.go", tool),
		references("api/a.go", helper), references("cli/main.go", public), references("tools/t.go", exported),
		calls(public, helper), calls(exported, helper), calls(entry, public),
	})
	owner := func(path string) string {
		if strings.HasPrefix(path, "tools/") {
			return "tools"
		}
		return "."
	}
	cg := newCallGraph(g, owner)

	assert.Empty(t, cg.Boundary(helper))
	assert.Equal(t, types.DiffBoundaryPackage, cg.Boundary(public))
	assert.Equal(t, types.DiffBoundaryProject, cg.Boundary(exported))
	assert.Equal(t, []types.DiffPublicPath{
		{ID: exported, Qualified: "Exported", Boundary: types.DiffBoundaryProject},
		{ID: public, Qualified: "Public", Boundary: types.DiffBoundaryPackage},
	}, impact.PublicThrough(cg, helper))
}

// exampleTB lets an example build a testkit workspace: an Example has no *testing.T, and
// testkit takes a testkit.TB. It panics on the methods no example calls, so a new use
// fails loudly. TestMain already isolates the environment, so nothing calls Setenv.
type exampleTB struct {
	cleanups []func()
}

func (*exampleTB) Skip(...any)        { panic("exampleTB: Skip") }
func (*exampleTB) Setenv(_, _ string) { panic("exampleTB: Setenv") }
func (*exampleTB) Helper()            {}

func (e *exampleTB) Cleanup(f func()) { e.cleanups = append(e.cleanups, f) }

func (e *exampleTB) TempDir() string {
	dir, err := os.MkdirTemp("", "magus-example-*")
	if err != nil {
		panic(err)
	}
	e.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func (*exampleTB) Fatalf(format string, args ...any) { panic(fmt.Sprintf(format, args...)) }

// close runs the cleanups last in, first out, as testing.T does.
func (e *exampleTB) close() {
	for i := len(e.cleanups) - 1; i >= 0; i-- {
		e.cleanups[i]()
	}
}

// newExample returns a workspace holding files, keyed by workspace-relative path, and the
// exampleTB that owns it. The caller defers close.
func newExample(files map[string]string) (*exampleTB, *testkit.Workspace) {
	tb := &exampleTB{}
	ws := testkit.NewWorkspace(tb)
	ws.WriteTree(files)
	return tb, ws
}

// monorepo is a workspace laid out the way a monorepo is.
func monorepo() (*exampleTB, *testkit.Workspace) {
	return newExample(map[string]string{
		"magusfile.buzz":     "",
		"api/magusfile.buzz": "",
		"web/magusfile.buzz": "",
	})
}

// ExampleInspect shows how to discover projects in a workspace without
// opening the cache. Inspect is the right entry point for read-only
// commands (list, graph, describe) where cache overhead is unnecessary.
func ExampleInspect() {
	// A directory is a project if it contains a magusfile.buzz.
	tb, w := newExample(map[string]string{"myapp/magusfile.buzz": ""})
	defer tb.close()

	ws, err := Inspect(context.Background(), w.Root(), WithSpawnRule(TierCeiling))
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

// ExampleOpen opens a workspace, which also opens its cache, and lists its projects. A
// program that only reads the workspace calls Inspect instead.
func ExampleOpen() {
	tb, ws := monorepo()
	defer tb.close()

	m, err := Open(context.Background(), ws.Root(), WithSpawnRule(TierCeiling))
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	for _, p := range m.All() {
		fmt.Println(p.Path)
	}
	// Output:
	// .
	// api
	// web
}

// ExampleMagus_ExpandAffected expands a target over the projects a changeset touches. A
// workspace that is not under version control cannot tell which projects changed, so it
// falls back to every project and says so in the third result.
func ExampleMagus_ExpandAffected() {
	tb, ws := monorepo()
	defer tb.close()

	m, err := Open(context.Background(), ws.Root(), WithSpawnRule(TierCeiling))
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	targets, _, fellBack, err := m.ExpandAffected(context.Background(), "ci", "")
	if err != nil {
		fmt.Println("expand:", err)
		return
	}
	fmt.Println("fell back to every project:", fellBack)
	for _, t := range targets {
		fmt.Println(fmt.Sprintf("%s:%s", t.Path, t.Name))
	}
	// Output:
	// fell back to every project: true
	// .:ci
	// api:ci
	// web:ci
}
