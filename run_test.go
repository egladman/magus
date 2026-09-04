package magus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/internal/secret"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiagEventFromError(t *testing.T) {
	// A coded DiagnosticError yields an event tagged with the target identity.
	de := types.DiagnosticErrorf(types.ExecDenied, "exec denied: /bin/x")
	ev, ok := diagEventFromError("pkg/foo", "build", de)
	assert.True(t, ok)
	assert.Equal(t, types.ExecDenied, ev.Code)
	assert.Equal(t, "pkg/foo:build", ev.Unit)

	// A wrapped diagnostic error is still recognized (errors.As unwraps).
	wrapped := fmt.Errorf("run failed: %w", de)
	ev, ok = diagEventFromError("pkg/foo", "", wrapped)
	assert.True(t, ok)
	assert.Equal(t, "pkg/foo", ev.Unit, "no target -> project-scoped unit")

	// A nil or plain error is not a diagnostic event.
	_, ok = diagEventFromError("pkg/foo", "build", nil)
	assert.False(t, ok)
	_, ok = diagEventFromError("pkg/foo", "build", errors.New("boom"))
	assert.False(t, ok)
}

func TestTargetHandler_NormalizesVariantSpellings(t *testing.T) {
	// The drift gate no longer keys off the target NAME - every target is eligible, and
	// the policy plus its declared outputs decide. Normalization still matters: a policy
	// is looked up by canonical name, so a variant invocation has to resolve to it or the
	// target silently runs ungated.
	for _, in := range []string{"preflight", "Preflight", "PREFLIGHT"} {
		parsed, err := types.ParseTarget(in)
		assert.NoErrorf(t, err, "ParseTarget(%q)", in)
		assert.Equalf(t, "preflight", parsed.Name, "ParseTarget(%q).Name", in)
	}
	for _, in := range []string{"generate", "Generate", "GENERATE"} {
		parsed, err := types.ParseTarget(in)
		assert.NoErrorf(t, err, "ParseTarget(%q)", in)
		assert.Equalf(t, "generate", parsed.Name, "ParseTarget(%q).Name", in)
	}

	var m *Magus
	h := m.targetHandler("generate")
	assert.NotNil(t, h)
}

func TestRaceForcesNoCache(t *testing.T) {
	assert.False(t, raceForcesNoCache(run{}), "neither Race nor RaceReplay set")
	assert.True(t, raceForcesNoCache(run{Race: true}), "Race alone")
	assert.True(t, raceForcesNoCache(run{RaceReplay: true}), "RaceReplay alone")
	assert.True(t, raceForcesNoCache(run{Race: true, RaceReplay: true}), "both set")
}

// TestRun_RaceReexecutesCachedTarget guards the A2 fix end to end: a target
// that's already a cache hit must still genuinely re-execute under --race
// (magus.WithRace), not replay - otherwise the race detector observes nothing.
func TestRun_RaceReexecutesCachedTarget(t *testing.T) {
	const spellName = "zzz-race-test-spell"
	var calls atomic.Int32
	spell := spells.NewSpell(spellName,
		spells.WithTargets("build"),
		spells.WithInvoker(func(context.Context, spells.InvokeRequest) (any, error) {
			calls.Add(1)
			return nil, nil
		}),
	)
	project.DefaultSpellRegistry().RegisterSpell(spell)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	m, err := Open(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	ctx := context.Background()
	targets := []types.Target{{Path: ".", Name: "build"}}

	require.NoError(t, m.Run(ctx, targets), "first run")
	assert.Equal(t, int32(1), calls.Load(), "first run: expected one real execution")

	require.NoError(t, m.Run(ctx, targets), "second run (should hit cache)")
	assert.Equal(t, int32(1), calls.Load(), "second run: cache hit must not re-execute")

	require.NoError(t, m.Run(ctx, targets, WithRace()), "third run (--race)")
	assert.Equal(t, int32(2), calls.Load(), "--race run: a cached target must still genuinely re-execute")
}

// TestRun_NoCacheReexecutesAndRefreshesEntry guards the A7 fix: magus run
// --no-cache (WithNoCache) forces a cached target to re-execute, and - unlike
// --race - the rebuild refreshes the entry, so a subsequent ordinary run hits
// the refreshed result instead of missing or replaying something stale.
func TestRun_NoCacheReexecutesAndRefreshesEntry(t *testing.T) {
	const spellName = "zzz-no-cache-test-spell"
	var calls atomic.Int32
	spell := spells.NewSpell(spellName,
		spells.WithTargets("build"),
		spells.WithInvoker(func(context.Context, spells.InvokeRequest) (any, error) {
			calls.Add(1)
			return nil, nil
		}),
	)
	project.DefaultSpellRegistry().RegisterSpell(spell)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	m, err := Open(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	ctx := context.Background()
	targets := []types.Target{{Path: ".", Name: "build"}}

	require.NoError(t, m.Run(ctx, targets), "first run")
	assert.Equal(t, int32(1), calls.Load(), "first run: expected one real execution")

	require.NoError(t, m.Run(ctx, targets, WithNoCache()), "second run (--no-cache)")
	assert.Equal(t, int32(2), calls.Load(), "--no-cache run: a cached target must still genuinely re-execute")

	require.NoError(t, m.Run(ctx, targets), "third run (ordinary)")
	assert.Equal(t, int32(2), calls.Load(), "ordinary run must hit the entry --no-cache refreshed, not re-execute")
}

// TestRunAffected_NoCacheReexecutes guards magus affected --no-cache
// specifically: RunAffected (not just Run) must also honor WithNoCache. There
// is no VCS in this temp workspace, so ExpandAffected falls back to "all
// projects" (types.ErrAffectedFallback) rather than erroring - the same
// documented safety net a real no-VCS or disabled-VCS workspace gets.
func TestRunAffected_NoCacheReexecutes(t *testing.T) {
	const spellName = "zzz-affected-no-cache-test-spell"
	var calls atomic.Int32
	spell := spells.NewSpell(spellName,
		spells.WithTargets("build"),
		spells.WithInvoker(func(context.Context, spells.InvokeRequest) (any, error) {
			calls.Add(1)
			return nil, nil
		}),
	)
	project.DefaultSpellRegistry().RegisterSpell(spell)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	m, err := Open(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	ctx := context.Background()

	require.NoError(t, m.RunAffected(ctx, "build"), "first affected run")
	assert.Equal(t, int32(1), calls.Load(), "first run: expected one real execution")

	require.NoError(t, m.RunAffected(ctx, "build"), "second affected run (should hit cache)")
	assert.Equal(t, int32(1), calls.Load(), "second run: cache hit must not re-execute")

	require.NoError(t, m.RunAffected(ctx, "build", WithNoCache()), "third affected run (--no-cache)")
	assert.Equal(t, int32(2), calls.Load(), "affected --no-cache must still genuinely re-execute a cached target")

	require.NoError(t, m.RunAffected(ctx, "build"), "fourth affected run (ordinary)")
	assert.Equal(t, int32(2), calls.Load(), "ordinary run must hit the entry --no-cache refreshed, not re-execute")
}

// TestInputsOutputsColocation guards F1 end to end: magus.inputs/outputs declared
// in a target body populate that target's per-target cache footprint (step.Sources /
// step.Outputs), joined to the project path, without leaking to a sibling target.
func TestInputsOutputsColocation(t *testing.T) {
	root := t.TempDir()
	const mf = `magus\project({"outputs": ["legacy/**"]});
export fun build(ctx: magus\Context, args: [str]) > void {
    ctx.readsFiles("src/**", "tsconfig.json");
    ctx.writesFiles("dist/**");
}
export fun test(ctx: magus\Context, args: [str]) > void {}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(mf), 0o644))

	m, err := Open(context.Background(), root)
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	p := m.Get(".")
	require.NotNil(t, p, "root project")

	buildStep := m.buildStep(p, "build")
	assert.Subset(t, buildStep.Sources, []string{"src/**", "tsconfig.json"},
		"build's declared inputs must be in its cache-key sources")
	assert.NotContains(t, buildStep.Sources, "**/*.go",
		"explicit inputs narrow the project-wide source baseline")
	assert.Contains(t, buildStep.Outputs, "dist/**",
		"build's declared output must be in its snapshot/replay set")
	assert.NotContains(t, buildStep.Outputs, "legacy/**",
		"explicit outputs narrow the project-wide replay baseline")

	testStep := m.buildStep(p, "test")
	assert.NotContains(t, testStep.Sources, "src/**",
		"a sibling target must not inherit build's per-target inputs")
	assert.NotContains(t, testStep.Outputs, "dist/**",
		"a sibling target must not inherit build's per-target outputs")
}

// A composer's key must move when the artifact of a composed skip_cache target
// does; see types.ChainSkipCacheOutputs for why.
func TestComposerKeysOnAComposedSkipCacheTargetsOutput(t *testing.T) {
	root := t.TempDir()
	const mf = `export fun index_generate(ctx: magus\Context, args: [str]) > void {
    ctx.writesFiles("MAGUS.md");
}
export fun generate(ctx: magus\Context, args: [str]) > void {
    ctx.needs(index_generate);
}
export fun lint(ctx: magus\Context, args: [str]) > void {
    ctx.needs(generate);
}
export fun test(ctx: magus\Context, args: [str]) > void {}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(mf), 0o644))

	m, err := Open(context.Background(), root)
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	p := m.Get(".")
	require.NotNil(t, p, "root project")
	// This package's tests do not link the Buzz interpreter, so a magus.project()
	// policy table in the fixture would never evaluate (see interp.Available in Open).
	// The chain and the writesFiles footprint are static, and come from the magusfile.
	p.TargetPolicies = map[string]types.Target{"index-generate": {SkipCache: true}}

	assert.Contains(t, m.buildStep(p, "lint").Sources, "MAGUS.md",
		"lint composes index-generate two hops down; a stale MAGUS.md must move its key")
	assert.NotContains(t, m.buildStep(p, "index-generate").Sources, "MAGUS.md",
		"the skip_cache target's own step never replays, so keying it on its own output says nothing")
	assert.NotContains(t, m.buildStep(p, "test").Sources, "MAGUS.md",
		"a target composing nothing must not inherit another target's artifact")
}

// A composed target's ctx.modifiesExistingFiles must reach the composer's
// Step.Updates, or MGS4007 blames the composer for the constituent's declared
// write: `generate` accused of modifying the CHANGELOG.md that changelog-generate
// declares and maintains.
func TestComposerInheritsAComposedTargetsUpdates(t *testing.T) {
	root := t.TempDir()
	const mf = `export fun changelog_generate(ctx: magus\Context, args: [str]) > void {
    ctx.modifiesExistingFiles("CHANGELOG.md");
}
export fun generate(ctx: magus\Context, args: [str]) > void {
    ctx.needs(ctx.glob("*-generate"));
}
export fun test(ctx: magus\Context, args: [str]) > void {}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(mf), 0o644))

	m, err := Open(context.Background(), root)
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	p := m.Get(".")
	require.NotNil(t, p, "root project")

	assert.Contains(t, m.buildStep(p, "generate").Updates, "CHANGELOG.md",
		"the composer runs the edit inside its own window, so the declaration covers it")
	assert.Contains(t, m.buildStep(p, "changelog-generate").Updates, "CHANGELOG.md",
		"the declaring target keeps its own declaration")
	assert.NotContains(t, m.buildStep(p, "test").Updates, "CHANGELOG.md",
		"a target composing nothing must not inherit another target's declared write")
}

// Cross-project rooting and cycle termination, which the same-project fixture
// above cannot reach.
func TestChainSkipCacheOutputsCrossProjectAndCycle(t *testing.T) {
	lib := &types.Project{
		Path:           "libs/gb",
		TargetPolicies: map[string]types.Target{"index-generate": {SkipCache: true}},
		TargetOutputs:  map[string][]types.OutputRef{"index-generate": {{Glob: "MAGUS.md"}}},
	}
	root := &types.Project{
		Path:         ".",
		TargetChains: map[string][]types.ChainStep{"ci": {{Project: "libs/gb", Target: "index-generate"}}},
	}
	lookup := func(path string) *types.Project {
		if path == lib.Path {
			return lib
		}
		return nil
	}
	assert.Equal(t, []string{"libs/gb/MAGUS.md"}, types.ChainSkipCacheOutputs(root, "ci", lookup),
		"a cross-project step's output is rooted at the project that declares it")
	assert.Equal(t, []types.ChainStep{{Project: "libs/gb", Target: "index-generate"}},
		types.ChainSkipCacheSteps(root, "ci", lookup),
		"a gate the caller has to run carries the project that owns it")

	looped := &types.Project{
		Path:           ".",
		TargetPolicies: map[string]types.Target{"b": {SkipCache: true}},
		TargetOutputs:  map[string][]types.OutputRef{"b": {{Glob: "out.txt"}}},
		TargetChains:   map[string][]types.ChainStep{"a": {{Target: "b"}}, "b": {{Target: "a"}}},
	}
	assert.Equal(t, []string{"out.txt"}, types.ChainSkipCacheOutputs(looped, "a", nil))
	assert.Equal(t, []types.ChainStep{{Project: ".", Target: "b"}},
		types.ChainSkipCacheSteps(looped, "a", nil))
}

// The two walks split where they treat a nested skip_cache target differently.
// Running `generate` runs the `index-generate` inside it, so only the covering one is
// a gate to invoke, while the key needs the inner one's artifact because that is where
// the bytes a stale replay would miss actually live.
func TestChainSkipCacheStepsDropsAGateItsCallerAlreadyCovers(t *testing.T) {
	p := &types.Project{
		Path: ".",
		TargetPolicies: map[string]types.Target{
			"generate":       {SkipCache: true},
			"index-generate": {SkipCache: true},
		},
		TargetOutputs: map[string][]types.OutputRef{"index-generate": {{Glob: "MAGUS.md"}}},
		TargetChains: map[string][]types.ChainStep{
			"lint":     {{Target: "generate"}},
			"generate": {{Target: "index-generate"}},
		},
	}
	assert.Equal(t, []types.ChainStep{{Project: ".", Target: "generate"}},
		types.ChainSkipCacheSteps(p, "lint", nil),
		"descending past generate would run index-generate a second time")
	assert.Equal(t, []string{"MAGUS.md"}, types.ChainSkipCacheOutputs(p, "lint", nil),
		"the artifact lives on the inner target, so keying stops at neither")
}

// Root `ci` reaches `generate` through `lint` and again through `security`, and
// `security` is a gate in its own right. Emitting both runs `generate` once on its own
// and once more inside security's body.
func TestChainSkipCacheStepsDropsAGateUnderAnotherGate(t *testing.T) {
	policies := map[string]types.Target{"generate": {SkipCache: true}, "security": {SkipCache: true}}
	outputs := map[string][]types.OutputRef{"generate": {{Glob: "MAGUS.md"}}}

	p := &types.Project{
		Path:           ".",
		TargetPolicies: policies,
		TargetOutputs:  outputs,
		TargetChains: map[string][]types.ChainStep{
			"ci":       {{Target: "lint"}, {Target: "security"}},
			"lint":     {{Target: "generate"}},
			"security": {{Target: "generate"}},
		},
	}
	assert.Equal(t, []types.ChainStep{{Project: ".", Target: "security"}},
		types.ChainSkipCacheSteps(p, "ci", nil),
		"security runs generate itself, so running both runs generate twice")

	noSecurity := &types.Project{
		Path:           ".",
		TargetPolicies: policies,
		TargetOutputs:  outputs,
		TargetChains: map[string][]types.ChainStep{
			"ci":   {{Target: "lint"}},
			"lint": {{Target: "generate"}},
		},
	}
	assert.Equal(t, []types.ChainStep{{Project: ".", Target: "generate"}},
		types.ChainSkipCacheSteps(noSecurity, "ci", nil),
		"with nothing above it, generate is the gate to run")
}

// A skip_cache target that maintains nothing is not a gate. image-build opts out
// because it pushes a signed digest per invocation, and a composer that replays has
// no artifact of its to validate, so running it would buy a side effect and an eight
// minute docker build for no verdict.
func TestChainSkipCacheStepsSkipsATargetThatMaintainsNoArtifact(t *testing.T) {
	p := &types.Project{
		Path: ".",
		TargetPolicies: map[string]types.Target{
			"image-build":    {SkipCache: true},
			"index-generate": {SkipCache: true},
		},
		TargetOutputs: map[string][]types.OutputRef{"index-generate": {{Glob: "MAGUS.md"}}},
		TargetChains:  map[string][]types.ChainStep{"ci": {{Target: "image-build"}, {Target: "index-generate"}}},
	}
	assert.Equal(t, []types.ChainStep{{Project: ".", Target: "index-generate"}},
		types.ChainSkipCacheSteps(p, "ci", nil))
}

// The acceptance behavior skip_cache promises: a target the magusfile says always
// runs must run even when the target that composes it replays. Counted through a
// spell invoker, following TestRun_RaceReexecutesCachedTarget.
func TestRun_CachedComposerStillRunsItsSkipCacheGate(t *testing.T) {
	const spellName = "zzz-gate-test-spell"
	var composer, gate atomic.Int32
	spell := spells.NewSpell(spellName,
		spells.WithTargets("composer", "gate"),
		spells.WithInvoker(func(_ context.Context, req spells.InvokeRequest) (any, error) {
			switch req.Target {
			case "composer":
				composer.Add(1)
			case "gate":
				gate.Add(1)
			}
			return nil, nil
		}),
	)
	project.DefaultSpellRegistry().RegisterSpell(spell)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	m, err := Open(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	p := m.Get(".")
	require.NotNil(t, p, "root project")
	// This package's tests do not link the Buzz interpreter, so a magusfile stating
	// the policy and the chain would never evaluate (see interp.Available in Open).
	p.TargetPolicies = map[string]types.Target{"gate": {SkipCache: true}}
	p.TargetChains = map[string][]types.ChainStep{"composer": {{Target: "gate"}}}
	// The artifact is what makes it a gate rather than a side effect to leave alone.
	p.TargetOutputs = map[string][]types.OutputRef{"gate": {{Glob: "GATE.md"}}}

	ctx := context.Background()
	targets := []types.Target{{Path: ".", Name: "composer"}}

	require.NoError(t, m.Run(ctx, targets), "first run")
	assert.Equal(t, int32(1), composer.Load(), "first run: the composer executes")
	assert.Equal(t, int32(0), gate.Load(),
		"a miss runs the composer's body, which is where the chain already runs")

	require.NoError(t, m.Run(ctx, targets), "second run")
	assert.Equal(t, int32(1), composer.Load(), "second run: the composer replays")
	assert.Equal(t, int32(1), gate.Load(), "a replayed composer must still run its gate")

	require.NoError(t, m.Run(ctx, targets, WithNoCache()), "third run (--no-cache)")
	assert.Equal(t, int32(2), composer.Load(), "--no-cache re-executes the composer")
	assert.Equal(t, int32(1), gate.Load(),
		"--no-cache runs the body, so pre-running the gate would execute it twice")
}

// TestInputsDynamicArgIsLoadError guards the loud-rejection contract: a
// magus.inputs/outputs call with a non-literal (computed) argument is a hard load
// error, because a computed footprint is invisible to the static cache read.
func TestInputsDynamicArgIsLoadError(t *testing.T) {
	root := t.TempDir()
	const mf = `export fun build(ctx: magus\Context, args: [str]) > void {
    final extra = "gen/**";
    ctx.readsFiles(extra);
}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(mf), 0o644))

	_, err := Open(context.Background(), root)
	require.Error(t, err, "Open must reject a computed magus.inputs argument")
	assert.Contains(t, err.Error(), "literal arguments", "error should explain the literal requirement")
	assert.Contains(t, err.Error(), "build", "error should name the offending target")
}

// TestComputedExecOverrideLoadsForALibraryCaller is the negative control for the test
// above, and the regression guard for a bug only a library caller saw: the same
// rejection scoped on a per-target skip_cache policy passes on the CLI path (where
// magus.project() has been evaluated and policies are loaded) and fails everywhere else,
// because an unevaluated project reads as cacheable. A computed ctx.withEnv is the shape
// that tripped it - it must load, with or without the interpreter linked in.
func TestComputedExecOverrideLoadsForALibraryCaller(t *testing.T) {
	root := t.TempDir()
	const mf = `export fun build(ctx: magus\Context, args: [str]) > void {
    final env = mut {"GOOS": "linux"};
    go["go-build"](ctx.withEnv(env), {});
}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(mf), 0o644))

	_, err := Open(context.Background(), root)
	assert.NoError(t, err, "a computed ctx.withEnv must not be a load error")
}

func TestDiagCollectorCollects(t *testing.T) {
	d := &diagCollector{} // nil report writer: Record must still collect
	d.Record(types.DiagnosticEvent{Unit: "a:build", Code: types.ExecDenied})
	d.Record(types.DiagnosticEvent{Unit: "b:test", Code: types.RaceDetected})

	snap := d.snapshot()
	assert.Len(t, snap, 2)
	// snapshot is a copy: mutating it must not affect the collector.
	snap[0].Unit = "mutated"
	assert.Equal(t, "a:build", d.snapshot()[0].Unit)
}

// TestWithTargetDeadlineIsOffByDefault pins the default: a zero timeout must
// not wrap the context at all. A deadline set near a legitimate target's
// runtime fails builds that were fine, so opting in is the user's call.
func TestWithTargetDeadlineIsOffByDefault(t *testing.T) {
	t.Parallel()
	m := &Magus{}

	ctx, cancel := m.withTargetDeadline(t.Context(), types.Target{})
	defer cancel()

	_, ok := ctx.Deadline()
	assert.False(t, ok, "no timeout configured means no deadline")
	assert.NoError(t, ctx.Err())
}

// assertDeadlineBudget asserts ctx's deadline encodes exactly want, given instants
// taken either side of the call that produced it. The deadline is stamped now+want
// for a now somewhere in [before, after], so the pair brackets it whatever the
// scheduler does; comparing against a single time.Now() would instead be asserting
// how promptly this goroutine got scheduled.
func assertDeadlineBudget(t *testing.T, ctx context.Context, before, after time.Time, want time.Duration) {
	t.Helper()
	deadline, ok := ctx.Deadline()
	require.True(t, ok, "a configured timeout must set a deadline")
	assert.WithinRange(t, deadline, before.Add(want), after.Add(want))
}

func TestWithTargetDeadlineBoundsATarget(t *testing.T) {
	t.Parallel()
	m := &Magus{cfg: config.Config{TargetTimeout: 50 * time.Millisecond}}

	before := time.Now()
	ctx, cancel := m.withTargetDeadline(t.Context(), types.Target{})
	after := time.Now()
	defer cancel()

	assertDeadlineBudget(t, ctx, before, after, 50*time.Millisecond)

	<-ctx.Done()
	assert.ErrorIs(t, ctx.Err(), context.DeadlineExceeded,
		"a target that outruns its budget is cancelled, which the VM samples on loop back edges")
}

// TestWithTargetDeadlineCancelReleasesTheTimer guards the leak: every handler
// defers the returned cancel, and the no-timeout path must return a callable
// one rather than nil.
func TestWithTargetDeadlineCancelReleasesTheTimer(t *testing.T) {
	t.Parallel()
	for _, timeout := range []time.Duration{0, time.Minute} {
		m := &Magus{cfg: config.Config{TargetTimeout: timeout}}
		_, cancel := m.withTargetDeadline(t.Context(), types.Target{})
		require.NotNil(t, cancel)
		assert.NotPanics(t, func() { cancel() })
		assert.NotPanics(t, func() { cancel() }, "cancel is idempotent")
	}
}

// A target's own ceiling bounds it with no workspace-wide timeout configured, which
// is the ordinary case: target_timeout is off by default and a per-target ceiling is
// opted into one target at a time.
func TestWithTargetDeadlineHonorsTheTargetsOwnCeiling(t *testing.T) {
	t.Parallel()
	m := &Magus{}

	before := time.Now()
	ctx, cancel := m.withTargetDeadline(t.Context(), types.Target{Timeout: "50ms"})
	after := time.Now()
	defer cancel()

	assertDeadlineBudget(t, ctx, before, after, 50*time.Millisecond)
}

// The TIGHTER of the two wins in both directions. A workspace-wide runaway guard and
// a target that declares its own budget are answering different questions, and
// letting either override the other would silently widen one of them.
func TestWithTargetDeadlineTakesTheTighterOfTheTwo(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		workspace time.Duration
		declared  string
		want      time.Duration
	}{
		{"target is tighter", time.Hour, "50ms", 50 * time.Millisecond},
		{"workspace is tighter", 50 * time.Millisecond, "1h", 50 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Magus{cfg: config.Config{TargetTimeout: tc.workspace}}
			before := time.Now()
			ctx, cancel := m.withTargetDeadline(t.Context(), types.Target{Timeout: tc.declared})
			after := time.Now()
			defer cancel()

			assertDeadlineBudget(t, ctx, before, after, tc.want)
		})
	}
}

// gateDrift answers for a drift-gated target, so the distinction that matters is
// "no VCS to check against" (a legitimate pass) versus "the check could not run"
// (which must not be reported as clean). These pin that split; collapsing them is
// how the gate previously failed open.
// gateDriftFixture builds the minimum Magus the gate needs: it resolves the VCS from
// the WORKSPACE ROOT (not the project dir), so the root is what a test has to set.
func gateDriftFixture(t *testing.T) (*Magus, *types.Project) {
	t.Helper()
	dir := t.TempDir()
	return &Magus{ws: &types.Workspace{Root: dir}}, &types.Project{Path: ".", Dir: dir}
}

// driftingFixture is gateDriftFixture plus a declared output the target actually moves.
// The VCS is only consulted once something drifted - there is nothing to attribute
// otherwise - so a test about the VCS half has to produce drift first or it never gets
// there.
func driftingFixture(t *testing.T) (*Magus, *types.Project, func() error) {
	t.Helper()
	m, p := gateDriftFixture(t)
	p.Outputs = []string{"out.txt"}
	path := filepath.Join(p.Dir, "out.txt")
	require.NoError(t, os.WriteFile(path, []byte("before"), 0o644))
	return m, p, func() error { return os.WriteFile(path, []byte("after"), 0o644) }
}

func TestGateDriftSkipsWhenVCSDisabled(t *testing.T) {
	t.Setenv("MAGUS_VCS_ENABLED", "false")
	m, p := gateDriftFixture(t)

	ran := false
	err := m.gateDrift(t.Context(), p, "generate", types.DriftFail, func() error { ran = true; return nil })

	require.NoError(t, err, "no VCS means nothing to diff against, so the check does not apply")
	require.True(t, ran, "the target still runs; only the drift check is skipped")
}

// An unversioned tree is the "container build, extracted tarball" case the gate promises to
// no-op on. It is NOT reached through a nil driver: Resolve falls back to git and reports
// VCSSourceDefault, so testing res.VCS alone leaves the promised no-op unreachable and the
// gate hard-fails with "git could not report working-tree status" on a directory that was
// never a repository.
func TestGateDriftSkipsWhenNothingClaimsTheRoot(t *testing.T) {
	t.Setenv("MAGUS_VCS_ENABLED", "")
	t.Setenv("MAGUS_VCS_NAME", "")
	m, p := gateDriftFixture(t)

	ran := false
	err := m.gateDrift(t.Context(), p, "generate", types.DriftFail, func() error { ran = true; return nil })

	require.NoError(t, err, "an unversioned tree has nothing to diff against; the gate must not fail")
	require.True(t, ran)
}

func TestGateDriftErrorsOnUnresolvableVCS(t *testing.T) {
	t.Setenv("MAGUS_VCS_NAME", "nosuchvcs")
	m, p, drift := driftingFixture(t)

	err := m.gateDrift(t.Context(), p, "generate", types.DriftFail, drift)

	require.Error(t, err, "an unknown MAGUS_VCS_NAME is misconfiguration, not an absent VCS")
	require.Contains(t, err.Error(), "drift-gated",
		"the message must name the guarantee that could not be honored, not just the VCS failure")
}

// An UNTRACKED output cannot be stale: a bundle, a dist/ tree, anything the build rewrites
// every run has no committed form to disagree with. This was the gate's first false
// positive when it became default-on - `console:ci` failed on its own gen/sw.js, which is
// gitignored and rewritten by design.
//
// The temp dir here is not a repository, so no backend claims it and the gate returns
// before the tracked-file question. That is the same no-op an unversioned tree gets, which
// is what makes an artifact-only project safe by construction rather than by policy.
func TestGateDriftIgnoresUntrackedOutput(t *testing.T) {
	t.Setenv("MAGUS_VCS_ENABLED", "")
	t.Setenv("MAGUS_VCS_NAME", "")
	m, p, drift := driftingFixture(t)

	require.NoError(t, m.gateDrift(t.Context(), p, "build", types.DriftFail, drift),
		"an output with no committed form behind it is a build artifact, not drift")
}

// Nothing moved, so there is nothing to attribute and no reason to consult the VCS at all.
// This is the half hashing bought: detection no longer depends on the VCS, so a broken one
// cannot fail a run whose outputs are all exactly where they were.
func TestGateDriftIgnoresBrokenVCSWhenNothingMoved(t *testing.T) {
	t.Setenv("MAGUS_VCS_NAME", "nosuchvcs")
	m, p := gateDriftFixture(t)
	p.Outputs = []string{"out.txt"}
	require.NoError(t, os.WriteFile(filepath.Join(p.Dir, "out.txt"), []byte("stable"), 0o644))

	err := m.gateDrift(t.Context(), p, "generate", types.DriftFail, func() error { return nil })

	require.NoError(t, err, "unmoved bytes are not drift, whatever the VCS is doing")
}

func TestGateDriftPropagatesTargetError(t *testing.T) {
	t.Setenv("MAGUS_VCS_ENABLED", "false")
	m, p := gateDriftFixture(t)

	want := errors.New("target blew up")
	err := m.gateDrift(t.Context(), p, "generate", types.DriftFail, func() error { return want })

	require.ErrorIs(t, err, want, "the target's own failure is returned before any drift check")
}

// TestCurrentRevisionNoVCS pins CurrentRevision's no-VCS behavior directly: a disabled
// VCS yields ("", false), never an error - the caller (executeStages) always has a
// value to stamp onto every step, and "unknown" is the correct answer, not a failure.
func TestCurrentRevisionNoVCS(t *testing.T) {
	t.Setenv("MAGUS_VCS_ENABLED", "false")

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))
	m, err := Open(context.Background(), root)
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	_, revision, dirty := m.CurrentRevision(t.Context())
	assert.Empty(t, revision)
	assert.False(t, dirty)
}

// TestCurrentRevisionWithVCS pins the success path CurrentRevisionNoVCS does not
// reach: a real git repo yields meta.ID/meta.IsDirty verbatim as (revision, dirty).
// Reuses gitRun/writeCommit/gitHeadFull from knowledge_test.go rather than
// reimplementing a git fixture helper.
func TestCurrentRevisionWithVCS(t *testing.T) {
	root := t.TempDir()
	gitRun(t, root, "init", "-q")
	writeCommit(t, root, "magusfile.buzz", "")
	// Open creates the .magus cache dir as a side effect; untracked, it would
	// otherwise read as an uncommitted change unrelated to anything this test
	// does. Ignore it before the "clean tree" assertion below.
	writeCommit(t, root, ".gitignore", ".magus/\n")
	head := gitHeadFull(t, root)

	m, err := Open(context.Background(), root)
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	_, revision, dirty := m.CurrentRevision(t.Context())
	assert.Equal(t, head, revision, "CurrentRevision must pass meta.ID through verbatim")
	assert.False(t, dirty, "a freshly committed tree is clean")

	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("x"), 0o644))
	_, revision, dirty = m.CurrentRevision(t.Context())
	assert.Equal(t, head, revision, "an uncommitted edit does not move HEAD")
	assert.True(t, dirty, "an uncommitted edit must report dirty")
}

// TestRun_NoVCSLeavesRevisionEmpty drives the real Run path (executeStages -> recordOutput)
// in a workspace with VCS disabled: the persisted descriptor's Revision must be empty and
// Dirty false, and recording it must raise nothing - a missing revision is "unknown", the
// same silent no-op verifyReadOnly's VCS resolution already treats it as.
func TestRun_NoVCSLeavesRevisionEmpty(t *testing.T) {
	t.Setenv("MAGUS_VCS_ENABLED", "false")

	const spellName = "zzz-no-vcs-revision-test-spell"
	spell := spells.NewSpell(spellName,
		spells.WithTargets("build"),
		spells.WithInvoker(func(context.Context, spells.InvokeRequest) (any, error) {
			return nil, nil
		}),
	)
	project.DefaultSpellRegistry().RegisterSpell(spell)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	m, err := Open(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	ctx := context.Background()
	targets := []types.Target{{Path: ".", Name: "build"}}
	require.NoError(t, m.Run(ctx, targets))

	key, _, err := m.ComputeTargetKey(ctx, ".", "build", nil)
	require.NoError(t, err, "ComputeTargetKey")
	desc, err := m.OutputDescriptorByRef(cache.PortableRef(key))
	require.NoError(t, err, "OutputDescriptorByRef")
	assert.Empty(t, desc.Revision, "no VCS: revision is unknown, not an error")
	assert.False(t, desc.Dirty)
}

// The affected fallback selects EVERY project when the VCS cannot produce a changed-file
// set. That is the safe direction, but it silently turns an incremental run into a full
// build, so RunAffected announces it. ExpandAffected has always returned the signal;
// every one of its five callers discarded it, which is the gap this pins.
func TestExpandAffectedSignalsFallbackWithoutVCS(t *testing.T) {
	root := mkGlobalWS(t, "build")

	m, err := Open(t.Context(), root)
	require.NoError(t, err)

	targets, source, fellBack, err := m.ExpandAffected(t.Context(), "build", "")

	require.NoError(t, err, "an uncomputable diff is a fallback, not a failure")
	require.True(t, fellBack, "a workspace with no VCS cannot diff, so every project is selected")
	require.NotEmpty(t, targets, "the fallback selects everything rather than nothing")
	require.NotEmpty(t, source, "source carries the reason, which is what the warning shows the user")
}

func TestWithReportWriter(t *testing.T) {
	var buf bytes.Buffer
	var r run
	WithReportWriter(&buf)(&r)
	assert.Same(t, &buf, r.ReportWriter, "WithReportWriter: run.ReportWriter not set to provided writer")
}

func TestRunOptions(t *testing.T) {
	var r run
	WithDryRun()(&r)
	assert.True(t, r.DryRun, "WithDryRun: DryRun = false, want true")
	WithCharms("write", "debug")(&r)
	assert.Equal(t, []string{"write", "debug"}, r.Charms)
	WithBaseRef("main")(&r)
	assert.Equal(t, "main", r.BaseRef)
	WithSpellFilter("go")(&r)
	assert.Equal(t, "go", r.Spell)
	WithNoVolatilityRetry()(&r)
	assert.True(t, r.NoVolatilityRetry, "WithNoVolatilityRetry: NoVolatilityRetry = false, want true")
}

func TestWithWrite_SetsWriteCharm(t *testing.T) {
	var r run
	WithWrite()(&r)
	assert.Equal(t, []string{"rw"}, r.Charms)
}

// TestOutputWatchDirsSpansOwnerRoots pins the reason outputWatchDirs returns directories
// instead of globs: one target's outputs can now land in two different project roots, so
// a cross-project glob must resolve against the OWNER's dir. Resolving it against the
// writer's would watch a path that does not exist, and the previous behavior - dropping
// it - left the one file two projects can both write as the only output the race,
// overlap, replay, and missing-dependency checks never looked at.
func TestOutputWatchDirsSpansOwnerRoots(t *testing.T) {
	m, _ := writeCrossOutputWorkspace(t)
	producer := m.Get("producer")
	require.NotNil(t, producer)

	dirs := outputWatchDirs(m.ws, producer, "build")

	assert.Equal(t, []string{filepath.Join(m.Root(), "site")}, dirs,
		"the watched dir is the OWNER's tree, not the declaring project's")
}

// TestRedactError masks a secret in a run error's message while keeping the chain
// intact. The CLI's last line on any failure is `slog.Error(err.Error())` with no
// context, so the log handler's redaction cannot reach it - a magusfile that throws an
// interpolated credential would print it verbatim as the final thing a user sees. This
// is the one seam where a run error stops being magus's and becomes the caller's text.
func TestRedactError(t *testing.T) {
	m := &Magus{resolver: secret.New()}
	ctx := secret.ContextWithResolver(t.Context(), m.resolver)
	t.Setenv("MAGUS_TEST_RUNERR_TOKEN", "ghp_thrown_from_a_magusfile")
	_, err := m.resolver.Read(ctx, "MAGUS_TEST_RUNERR_TOKEN")
	require.NoError(t, err)

	t.Run("masks the message", func(t *testing.T) {
		got := m.redactError(errors.New("auth failed: ghp_thrown_from_a_magusfile"))
		assert.NotContains(t, got.Error(), "ghp_thrown_from_a_magusfile")
		assert.Contains(t, got.Error(), "***")
	})

	t.Run("preserves the chain so errors.As still classifies it", func(t *testing.T) {
		// exitCodeOf relies on errors.As to recognise ExitError; a redaction that broke
		// unwrapping would silently turn every exit code into 1.
		wrapped := fmt.Errorf("target failed with ghp_thrown_from_a_magusfile: %w",
			types.ExitError{Code: 42})
		got := m.redactError(wrapped)
		assert.NotContains(t, got.Error(), "ghp_thrown_from_a_magusfile")

		var exitErr types.ExitError
		require.True(t, errors.As(got, &exitErr), "the chain must survive redaction")
		assert.Equal(t, 42, exitErr.Code)
	})

	t.Run("returns the original error untouched when nothing matched", func(t *testing.T) {
		orig := errors.New("ordinary failure")
		assert.Same(t, orig, m.redactError(orig))
	})

	t.Run("nil in, nil out", func(t *testing.T) {
		assert.NoError(t, m.redactError(nil))
	})
}

// TestRunCIAnchorIgnoresSpellCIOpOnMagusfileProject pins the existing rule as
// unchanged: for a magusfile project the magusfile is the definition, so a bound
// spell's ci op must NOT satisfy the anchor - only counting it for provided
// projects, which have no magusfile to shadow.
func TestRunCIAnchorIgnoresSpellCIOpOnMagusfileProject(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz")
	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell("ci-capable"))

	ctx := context.Background()
	m, err := Open(ctx, root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	defer func() { _ = m.Close() }()

	err = m.RunCI(ctx, []types.Target{{Path: ".", Name: "ci"}})
	assert.True(t, errors.Is(err, types.NoCITarget),
		"a spell ci op must not satisfy the anchor for a magusfile project, got: %v", err)
}

// recordedOutputOverlap decodes one report.OutputOverlapDetected JSONL line. The
// wire format splices schema/type and the event's own fields into one flat object
// (see envelope.writeJSONL), not a nested "body".
type recordedOutputOverlap struct {
	Type        string   `json:"type"`
	ProjectA    string   `json:"project_a"`
	ProjectB    string   `json:"project_b"`
	Target      string   `json:"target"`
	Overlapping []string `json:"overlapping"`
}

func recordOutputOverlapEvents(t *testing.T, steps []cache.Step) []recordedOutputOverlap {
	t.Helper()
	var buf bytes.Buffer
	w := report.NewWriter(&buf)
	checkOutputOverlap(steps, w)
	require.NoError(t, w.Close())

	var out []recordedOutputOverlap
	dec := json.NewDecoder(&buf)
	for {
		var ev recordedOutputOverlap
		err := dec.Decode(&ev)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		if ev.Type == report.TypeOutputOverlapDetected {
			out = append(out, ev)
		}
	}
	return out
}

// TestCheckOutputOverlap_UsesStepTargetNotScopeLabel is P1-B's regression test for
// checkOutputOverlap: the function used to take the whole invocation's scope label
// (e.g. "3 projects") as its "target" parameter and stamp that into MGS4002 reports,
// even though every cache.Step already carries its own real Target. Pre-fix, calling
// checkOutputOverlap(steps, "3 projects", w) recorded Target: "3 projects" - never
// "build", which is what this test now asserts.
func TestCheckOutputOverlap_UsesStepTargetNotScopeLabel(t *testing.T) {
	steps := []cache.Step{
		{ProjectPath: "a", Target: "build", Outputs: []string{"dist/**"}},
		{ProjectPath: "b", Target: "build", Outputs: []string{"dist/**"}},
	}

	evs := recordOutputOverlapEvents(t, steps)

	require.Len(t, evs, 1)
	assert.Equal(t, "a", evs[0].ProjectA)
	assert.Equal(t, "b", evs[0].ProjectB)
	assert.Equal(t, "build", evs[0].Target, "must carry the steps' real target, not an invocation-wide scope label")
	assert.Equal(t, []string{"dist/**"}, evs[0].Overlapping)
}

// TestCheckOutputOverlap_DifferingTargetsReportsBoth covers the case that made a
// single blanket "target" parameter actively wrong: one executeStages call can cover
// several target stages (runResolved groups a multi-target request into one call), so
// two overlapping steps can legitimately belong to different targets. Neither target
// alone is "the" target - both must be visible in the report.
func TestCheckOutputOverlap_DifferingTargetsReportsBoth(t *testing.T) {
	steps := []cache.Step{
		{ProjectPath: "a", Target: "build", Outputs: []string{"dist/**"}},
		{ProjectPath: "b", Target: "test", Outputs: []string{"dist/**"}},
	}

	evs := recordOutputOverlapEvents(t, steps)

	require.Len(t, evs, 1)
	assert.Equal(t, "build,test", evs[0].Target)
}

// recordedMissingDependency decodes one report.MissingDependency JSONL line (see the
// flat-envelope note on recordedOutputOverlap).
type recordedMissingDependency struct {
	Type     string `json:"type"`
	Consumer string `json:"consumer"`
	Producer string `json:"producer"`
	Path     string `json:"path"`
	Target   string `json:"target"`
}

// TestCheckMissingDependencies_ReportsScopeLabelAsTarget covers the other half of
// P1-B: unlike checkOutputOverlap, no per-write target exists here (written comes from
// race.Runtime.WrittenPaths, which is keyed by project only), so the fix renamed the
// parameter from "target" to "scope" and documented why, rather than fabricating a
// target value. This pins that the report's Target field still carries the scope
// label - the value genuinely available - so a caller reading it is not left with an
// empty field.
func TestCheckMissingDependencies_ReportsScopeLabelAsTarget(t *testing.T) {
	consumer := &types.Project{Path: "consumer", Dir: "/ws/consumer", Sources: []string{"**/*.go"}}
	written := map[string][]string{"producer": {"/ws/consumer/generated.go"}}

	var buf bytes.Buffer
	w := report.NewWriter(&buf)
	checkMissingDependencies([]*types.Project{consumer}, map[string]*types.Project{}, written, "3 projects", w)
	require.NoError(t, w.Close())

	var evs []recordedMissingDependency
	dec := json.NewDecoder(&buf)
	for {
		var ev recordedMissingDependency
		err := dec.Decode(&ev)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		if ev.Type == report.TypeMissingDependency {
			evs = append(evs, ev)
		}
	}

	require.Len(t, evs, 1)
	assert.Equal(t, "consumer", evs[0].Consumer)
	assert.Equal(t, "producer", evs[0].Producer)
	assert.Equal(t, "3 projects", evs[0].Target, "scope label is the best available identifier and must still reach the report")
}

// key mirrors how probeTools records a version, so the tests exercise the real lookup
// rather than a convenient stand-in.
func key(project, spell, tool string) string { return project + "\x00" + spell + "\x00" + tool }

func projectWith(path string, bounds map[string]spells.VersionBounds, sp *spells.Spell) *types.Project {
	return &types.Project{Path: path, Dir: "/tmp/" + path, ToolBounds: bounds, ResolvedSpells: []*spells.Spell{sp}}
}

func tsSpell(tool string, supported spells.VersionBounds) *spells.Spell {
	return spells.NewSpell("typescript", spells.WithTools(map[string]spells.Tool{
		tool: {Probe: spells.Command{Bin: tool, Args: []string{"--version"}}, Supported: supported},
	}))
}

// The gate has to fire for a project whose targets never dispatch a spell op, which is
// the whole reason it moved out of op dispatch: every TypeScript project here runs its
// own pnpm scripts, so the op-level check could never reach them.
func TestToolWindowFailsWithoutAnyOpDispatch(t *testing.T) {
	p := projectWith("libs/textsearch", map[string]spells.VersionBounds{"node": {Min: "22", Below: "25"}}, tsSpell("node", spells.VersionBounds{}))
	err := checkToolWindows([]*types.Project{p}, map[string]string{key("libs/textsearch", "typescript", "node"): "v26.5.0"})
	require.Error(t, err)
	assert.ErrorIs(t, err, types.ToolTooNew)
	assert.Contains(t, err.Error(), "node v26.5.0 is newer than the supported range (below 25)")
}

// The spell's requirement and the project's policy intersect, narrower winning, so a
// version inside one but outside the other still fails.
func TestToolWindowIntersectsSpellAndProject(t *testing.T) {
	p := projectWith(".", map[string]spells.VersionBounds{"go": {Min: "1.26"}}, spells.NewSpell("go", spells.WithTools(map[string]spells.Tool{
		"go": {Probe: spells.Command{Bin: "go"}, Supported: spells.VersionBounds{Min: "1.21"}},
	})))
	// 1.24 satisfies the spell's 1.21 floor but not the project's 1.26 one.
	err := checkToolWindows([]*types.Project{p}, map[string]string{key(".", "go", "go"): "v1.24.0"})
	require.Error(t, err)
	assert.ErrorIs(t, err, types.ToolTooOld)

	assert.NoError(t, checkToolWindows([]*types.Project{p}, map[string]string{key(".", "go", "go"): "v1.26.5"}))
}

// Every violation is reported, not just the first: a toolchain mismatch usually has more
// than one tool to fix, and one-per-rebuild is the experience this replaces.
func TestToolWindowReportsEveryViolation(t *testing.T) {
	a := projectWith("console", map[string]spells.VersionBounds{"node": {Below: "25"}}, tsSpell("node", spells.VersionBounds{}))
	b := projectWith("docs", map[string]spells.VersionBounds{"node": {Below: "25"}}, tsSpell("node", spells.VersionBounds{}))
	err := checkToolWindows([]*types.Project{a, b}, map[string]string{
		key("console", "typescript", "node"): "v26.5.0",
		key("docs", "typescript", "node"):    "v26.5.0",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "console:")
	assert.Contains(t, err.Error(), "docs:")
}

// A batch with one violation of each kind must not let the project NAMES decide the code.
// The code was previously recovered by sniffing the sorted messages for "older than", so
// it turned on which project label sorted first - an alphabetical accident.
func TestToolWindowCodeDoesNotTurnOnProjectOrder(t *testing.T) {
	old := projectWith("zz", map[string]spells.VersionBounds{"node": {Min: "22"}}, tsSpell("node", spells.VersionBounds{}))
	recent := projectWith("aa", map[string]spells.VersionBounds{"node": {Below: "25"}}, tsSpell("node", spells.VersionBounds{}))
	versions := map[string]string{
		key("zz", "typescript", "node"): "v18.0.0",
		key("aa", "typescript", "node"): "v26.5.0",
	}

	// "aa" sorts first and is the too-NEW one, so a code read off the sorted messages
	// would say MGS3006 while the too-old violation sits in the same payload.
	err := checkToolWindows([]*types.Project{old, recent}, versions)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "older than the supported range (min 22)")
	assert.Contains(t, err.Error(), "newer than the supported range (below 25)")
	assert.ErrorIs(t, err, types.ToolTooOld, "one error carries one code, and too-old wins a mixed batch")

	// Reversing the projects must change nothing. One error, one code, and neither the
	// iteration order nor the alphabet gets a say in which.
	flipped := checkToolWindows([]*types.Project{recent, old}, versions)
	require.Error(t, flipped)
	assert.Equal(t, err.Error(), flipped.Error())
}

// An unread version is never a violation. That covers an absent binary, output carrying
// no version, and probing switched off entirely - magus must not fail on a comparison it
// could not make.
func TestToolWindowSkipsUnreadVersions(t *testing.T) {
	p := projectWith("console", map[string]spells.VersionBounds{"node": {Min: "99"}}, tsSpell("node", spells.VersionBounds{}))
	assert.NoError(t, checkToolWindows([]*types.Project{p}, map[string]string{}))
}

// A tool nobody constrained is never probed against anything.
func TestToolWindowIgnoresUnconstrainedTools(t *testing.T) {
	p := projectWith("console", nil, tsSpell("node", spells.VersionBounds{}))
	assert.NoError(t, checkToolWindows([]*types.Project{p}, map[string]string{key("console", "typescript", "node"): "v26.5.0"}))
}

// probedSpell is tsSpell with the version prober injected, so these tests exercise the
// real probeTools path without forking anything. The counter is what proves memoization,
// which is the only reason probeTools threads two outputs out of one pass.
func probedSpell(name, tool string, key spells.VersionKey, out func(dir string) (string, error), calls *int) *spells.Spell {
	return spells.NewSpell(name,
		spells.WithTools(map[string]spells.Tool{
			tool: {Probe: spells.Command{Bin: tool, Args: []string{"--version"}}, Key: key},
		}),
		spells.WithVersionProber(func(_ context.Context, _ spells.Command, dir string) (string, error) {
			*calls++
			return out(dir)
		}),
	)
}

// The cache key wants a narrowed token and the window gate wants the whole version. Both
// come out of one probe, and the two must not disagree about what was read.
func TestProbeToolsRecordsTheKeyTokenAndTheFullVersion(t *testing.T) {
	calls := 0
	sp := probedSpell("typescript", "node", spells.VersionKey{}, func(string) (string, error) { return "v22.14.0", nil }, &calls)
	p := &types.Project{Path: "console", Dir: "/tmp/console", ResolvedSpells: []*spells.Spell{sp}}

	full := map[string]string{}
	got := (&Magus{}).probeTools(t.Context(), []*types.Project{p}, full)

	assert.Equal(t, 1, calls)
	assert.Equal(t, map[string]string{key("console", "typescript", "node"): "v22.14.0"}, full)
	require.Contains(t, got, "console")
	assert.Equal(t, []string{"typescript:node:v22.14.0"}, got["console"], "the default key narrows nothing; the gate reads the same probe")
}

// A probe that fails records UNPROBED for the cache key and NOTHING for the gate. Those
// have to differ: a key must change so a later successful probe misses, while the gate
// must not invent a version and fail a build over a comparison it could not make.
func TestProbeToolsLeavesAFailedProbeOutOfTheGate(t *testing.T) {
	calls := 0
	sp := probedSpell("typescript", "node", spells.VersionKey{}, func(string) (string, error) {
		return "", errors.New("exec: node: not found")
	}, &calls)
	p := &types.Project{
		Path: "console", Dir: "/tmp/console",
		ToolBounds:     map[string]spells.VersionBounds{"node": {Min: "22"}},
		ResolvedSpells: []*spells.Spell{sp},
	}

	full := map[string]string{}
	got := (&Magus{}).probeTools(t.Context(), []*types.Project{p}, full)

	assert.Equal(t, []string{"typescript:node:UNPROBED"}, got["console"])
	assert.Empty(t, full)
	assert.NoError(t, checkToolWindows([]*types.Project{p}, full), "an absent tool is not a version violation")
}

// Output carrying no version is the same contract as a failed probe for the gate's
// purposes: nothing to compare, so nothing to fail. It differs for the cache key, which
// still has to record what it saw.
func TestProbeToolsLeavesUnparsableOutputOutOfTheGate(t *testing.T) {
	calls := 0
	sp := probedSpell("typescript", "node", spells.VersionKey{}, func(string) (string, error) { return "nightly", nil }, &calls)
	p := &types.Project{Path: "console", Dir: "/tmp/console", ResolvedSpells: []*spells.Spell{sp}}

	full := map[string]string{}
	(&Magus{}).probeTools(t.Context(), []*types.Project{p}, full)
	assert.Empty(t, full)
}

// Memoization is on (spell, dir, tool), so two projects in one directory cost one spawn.
// Without it the gate would multiply forks by the number of projects sharing a tree.
func TestProbeToolsMemoizesAcrossProjectsSharingADir(t *testing.T) {
	calls := 0
	sp := probedSpell("go", "go", spells.VersionKey{}, func(string) (string, error) { return "go1.26.5", nil }, &calls)
	a := &types.Project{Path: "a", Dir: "/tmp/same", ResolvedSpells: []*spells.Spell{sp}}
	b := &types.Project{Path: "b", Dir: "/tmp/same", ResolvedSpells: []*spells.Spell{sp}}

	full := map[string]string{}
	(&Magus{}).probeTools(t.Context(), []*types.Project{a, b}, full)

	assert.Equal(t, 1, calls, "one spawn for the shared (spell, dir, tool)")
	// Both projects still get their own gate entry, or the second would go unchecked.
	assert.Equal(t, "v1.26.5", full[key("a", "go", "go")])
	assert.Equal(t, "v1.26.5", full[key("b", "go", "go")])
}

// A declared constant token is the documented way to key a tool without spawning. It must
// stay out of the gate: the author typed it to invalidate a cache, and it is not a reading
// of anything installed.
func TestProbeToolsDoesNotGateOnADeclaredConstant(t *testing.T) {
	sp := spells.NewSpell("typescript", spells.WithTools(map[string]spells.Tool{
		"node": {Key: spells.VersionKey{Const: "pinned-1"}},
	}))
	p := &types.Project{Path: "console", Dir: "/tmp/console", ResolvedSpells: []*spells.Spell{sp}}
	full := map[string]string{}
	got := (&Magus{}).probeTools(t.Context(), []*types.Project{p}, full)
	assert.Equal(t, []string{"typescript:node:pinned-1"}, got["console"], "the constant still keys the cache")
	assert.Empty(t, full, "but it is not a reading of anything installed, so the gate never sees it")
}

// MAGUS_CACHE_TOOL_VERSION=off short-circuits the probe pass, and the window gate reads
// what that pass records - so switching off tool-version CACHE KEYING also switches off
// the version gate. Pinned because it is not obvious from either name, and because a gate
// that can be disabled by a cache knob should at least be disabled on purpose.
func TestProbeToolsOffAlsoDisablesTheWindowGate(t *testing.T) {
	t.Setenv("MAGUS_CACHE_TOOL_VERSION", "off")
	calls := 0
	sp := probedSpell("typescript", "node", spells.VersionKey{}, func(string) (string, error) { return "v26.5.0", nil }, &calls)
	p := &types.Project{
		Path: "console", Dir: "/tmp/console",
		ToolBounds:     map[string]spells.VersionBounds{"node": {Below: "25"}},
		ResolvedSpells: []*spells.Spell{sp},
	}

	full := map[string]string{}
	assert.Nil(t, (&Magus{}).probeTools(t.Context(), []*types.Project{p}, full))
	assert.Equal(t, 0, calls)
	assert.NoError(t, checkToolWindows([]*types.Project{p}, full))
}

// mode=workspace probes once at the root instead of per project dir. The gate still keys
// by project path, so a per-project window is enforced against the root's reading.
func TestProbeToolsWorkspaceModeProbesTheRootDir(t *testing.T) {
	t.Setenv("MAGUS_CACHE_TOOL_VERSION", "workspace")
	var seen []string
	calls := 0
	sp := probedSpell("typescript", "node", spells.VersionKey{}, func(dir string) (string, error) {
		seen = append(seen, dir)
		return "v26.5.0", nil
	}, &calls)
	p := &types.Project{
		Path: "console", Dir: "/tmp/console",
		ToolBounds:     map[string]spells.VersionBounds{"node": {Below: "25"}},
		ResolvedSpells: []*spells.Spell{sp},
	}

	m := &Magus{ws: &types.Workspace{Root: "/tmp/root"}}
	full := map[string]string{}
	m.probeTools(t.Context(), []*types.Project{p}, full)

	assert.Equal(t, []string{"/tmp/root"}, seen)
	assert.ErrorIs(t, checkToolWindows([]*types.Project{p}, full), types.ToolTooNew)
}

// toolVersionsByProject is probeTools with the gate output discarded. A nil map must not
// panic on the write path, which is the only thing separating the two.
func TestToolVersionsByProjectTakesTheNilGateMap(t *testing.T) {
	calls := 0
	sp := probedSpell("typescript", "node", spells.VersionKey{}, func(string) (string, error) { return "v22.14.0", nil }, &calls)
	p := &types.Project{Path: "console", Dir: "/tmp/console", ResolvedSpells: []*spells.Spell{sp}}
	assert.Equal(t, []string{"typescript:node:v22.14.0"}, (&Magus{}).toolVersionsByProject(t.Context(), []*types.Project{p})["console"])
}

// A workspace target runs another project's generate, so that project's output is written inside
// this step's window. MGS4007 reports a target that rewrote its own SOURCES; a file some project
// declares as an output is generated by definition and is not that.
//
// The case that found this: the root declares **/*.md through the markdown spell, which sweeps in
// every nested project's generated MAGUS.md. Root ci ran a nested generate, the nested project's
// own output changed, and the root reported it as an undeclared source mutation.
func TestOwnedOutputsSpanEveryProjectNotJustTheRunningOne(t *testing.T) {
	root := t.TempDir()
	const rootMF = `magus\project({"sources": ["**/*.md"]});
export fun ci(ctx: magus\Context, args: [str]) > void {}
`
	const leafMF = `export fun generate(ctx: magus\Context, args: [str]) > void {
    ctx.writesFiles("INDEX.md");
}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(rootMF), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "leaf"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "leaf", "magusfile.buzz"), []byte(leafMF), 0o644))

	m, err := Open(context.Background(), root)
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	rootProject := m.Get(".")
	require.NotNil(t, rootProject, "root project")

	step := m.buildStep(rootProject, "ci")

	assert.Contains(t, step.OwnedOutputs, "leaf/INDEX.md",
		"the root step must exempt a nested project's declared output, or running its generate reports MGS4007")
}

// TestRun_RetryOnVolatileIsPerTargetNotPerRun is the regression this policy was
// promoted to fix, and the reason it needed fixing before it could be declared.
//
// The run used to collapse every selected target's policy to one bool - the first
// project that declared ANY policy for a target won - and store it on the volatility
// Runtime, which then short-circuited on it without ever asking which pair it was
// deciding for. Opting `flaky` in therefore made `steady` retryable too, so a real
// regression in a sibling got a second attempt nobody asked to give it and the run
// came back green. Both targets fail here; exactly one may run twice.
func TestRun_RetryOnVolatileIsPerTargetNotPerRun(t *testing.T) {
	const spellName = "zzz-retry-granularity-spell"
	var flaky, steady atomic.Int32
	spell := spells.NewSpell(spellName,
		spells.WithTargets("flaky", "steady"),
		spells.WithInvoker(func(_ context.Context, req spells.InvokeRequest) (any, error) {
			switch req.Target {
			case "flaky":
				flaky.Add(1)
			case "steady":
				steady.Add(1)
			}
			return nil, errors.New("boom")
		}),
	)
	project.DefaultSpellRegistry().RegisterSpell(spell)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".",
		WithSpell(spellName),
		WithTarget("flaky", RetryOnVolatile("a fixture that races its own teardown")),
	)
	// An empty history means every eligible failure retries on the bootstrap rule,
	// so the Wilson score plays no part and the only thing under test is the gate.
	cfg := config.Defaults()
	cfg.HistoryPath = filepath.Join(root, "history.json")
	m, err := Open(context.Background(), root, WithWorkspaceRegistry(reg), WithLoadedConfig(cfg))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	err = m.Run(context.Background(), []types.Target{
		{Path: ".", Name: "flaky"},
		{Path: ".", Name: "steady"},
	})
	require.Error(t, err, "both targets fail, so the run must fail whatever the retries did")

	assert.Equal(t, int32(2), flaky.Load(), "the opted-in target retries once")
	assert.Equal(t, int32(1), steady.Load(),
		"a sibling that never opted in must not inherit the retry from a target that did")
}

// syncBuffer collects log output from the worker goroutines a run fans out over.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestRun_RetryIsAudibleOffCI pins the retry saying so on every host.
//
// Its only two reports used to be a CI annotation, which needs a live annotator and is
// Nop everywhere else, and a report record, which needs --report. So locally a target
// failed, silently reran, passed, and the run went green with the first attempt's output
// collapsed - the unnoticed volatile target the annotation exists to prevent, unnoticed.
func TestRun_RetryIsAudibleOffCI(t *testing.T) {
	const spellName = "zzz-retry-audible-spell"
	var calls atomic.Int32
	spell := spells.NewSpell(spellName,
		spells.WithTargets("flaky"),
		spells.WithInvoker(func(context.Context, spells.InvokeRequest) (any, error) {
			if calls.Add(1) == 1 {
				return nil, errors.New("boom")
			}
			return nil, nil
		}),
	)
	project.DefaultSpellRegistry().RegisterSpell(spell)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	var logged syncBuffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".",
		WithSpell(spellName),
		WithTarget("flaky", RetryOnVolatile("a fixture that races its own teardown")),
	)
	cfg := config.Defaults()
	cfg.HistoryPath = filepath.Join(root, "history.json")
	m, err := Open(context.Background(), root, WithWorkspaceRegistry(reg), WithLoadedConfig(cfg))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	require.NoError(t, m.Run(context.Background(), []types.Target{{Path: ".", Name: "flaky"}}),
		"the retry passes, so the run is green - which is exactly why it has to be said out loud")
	require.Equal(t, int32(2), calls.Load(), "the target ran twice")

	got := logged.String()
	assert.Contains(t, got, "volatile target retried")
	assert.Contains(t, got, "target="+spellName+"/flaky", "the line must name the pair, not just the project")
	assert.Contains(t, got, "status=retried_volatile")
	assert.Contains(t, got, "reason=bootstrap")
}
