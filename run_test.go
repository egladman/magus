package magus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/project"
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

func TestMakeHandler_PreflightGenerateFireOnVariantSpellings(t *testing.T) {
	// makeHandler special-cases the exact strings "preflight"/"generate"
	// (run.go). types.ParseTarget normalizes the CLI's raw spelling before it
	// ever reaches makeHandler, so a variant invocation still resolves to the
	// canonical name the special-casing checks against.
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
	h := m.makeHandler("generate")
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
	spell := types.NewSpell(spellName,
		types.WithTargets("build"),
		types.WithInvoker(func(context.Context, types.InvokeRequest) (any, error) {
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
	spell := types.NewSpell(spellName,
		types.WithTargets("build"),
		types.WithInvoker(func(context.Context, types.InvokeRequest) (any, error) {
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
	spell := types.NewSpell(spellName,
		types.WithTargets("build"),
		types.WithInvoker(func(context.Context, types.InvokeRequest) (any, error) {
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
	const mf = `export fun build(ctx: magus\Context, args: [str]) > void {
    ctx.inputs("src/**", "tsconfig.json");
    ctx.outputs("dist/**");
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
	assert.Contains(t, buildStep.Outputs, "dist/**",
		"build's declared output must be in its snapshot/replay set")

	testStep := m.buildStep(p, "test")
	assert.NotContains(t, testStep.Sources, "src/**",
		"a sibling target must not inherit build's per-target inputs")
	assert.NotContains(t, testStep.Outputs, "dist/**",
		"a sibling target must not inherit build's per-target outputs")
}

// TestInputsDynamicArgIsLoadError guards the loud-rejection contract: a
// magus.inputs/outputs call with a non-literal (computed) argument is a hard load
// error, because a computed footprint is invisible to the static cache read.
func TestInputsDynamicArgIsLoadError(t *testing.T) {
	root := t.TempDir()
	const mf = `export fun build(ctx: magus\Context, args: [str]) > void {
    final extra = "gen/**";
    ctx.inputs(extra);
}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(mf), 0o644))

	_, err := Open(context.Background(), root)
	require.Error(t, err, "Open must reject a computed magus.inputs argument")
	assert.Contains(t, err.Error(), "string-literal", "error should explain the literal requirement")
	assert.Contains(t, err.Error(), "build", "error should name the offending target")
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

	ctx, cancel := m.withTargetDeadline(t.Context())
	defer cancel()

	_, ok := ctx.Deadline()
	assert.False(t, ok, "no timeout configured means no deadline")
	assert.NoError(t, ctx.Err())
}

func TestWithTargetDeadlineBoundsATarget(t *testing.T) {
	t.Parallel()
	m := &Magus{cfg: config.Config{TargetTimeout: 50 * time.Millisecond}}

	ctx, cancel := m.withTargetDeadline(t.Context())
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok, "a configured timeout must set a deadline")
	assert.WithinDuration(t, time.Now().Add(50*time.Millisecond), deadline, 20*time.Millisecond)

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
		_, cancel := m.withTargetDeadline(t.Context())
		require.NotNil(t, cancel)
		assert.NotPanics(t, func() { cancel() })
		assert.NotPanics(t, func() { cancel() }, "cancel is idempotent")
	}
}
