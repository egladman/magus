package magus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
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

// verifyReadOnly gates a FailOnDrift target, so the distinction that matters is
// "no VCS to check against" (a legitimate pass) versus "the check could not run"
// (which must not be reported as clean). These pin that split; collapsing them is
// how the gate previously failed open.
func TestVerifyReadOnlySkipsWhenVCSDisabled(t *testing.T) {
	t.Setenv("MAGUS_VCS_ENABLED", "false")

	ran := false
	err := verifyReadOnly(t.Context(), t.TempDir(), "generate", func() error { ran = true; return nil })

	require.NoError(t, err, "no VCS means nothing to diff against, so the check does not apply")
	require.True(t, ran, "the target still runs; only the drift check is skipped")
}

func TestVerifyReadOnlyErrorsOnUnresolvableVCS(t *testing.T) {
	t.Setenv("MAGUS_VCS_NAME", "nosuchvcs")

	err := verifyReadOnly(t.Context(), t.TempDir(), "generate", func() error { return nil })

	require.Error(t, err, "an unknown MAGUS_VCS_NAME is misconfiguration, not an absent VCS")
	require.Contains(t, err.Error(), "FailOnDrift",
		"the message must name the guarantee that could not be honored, not just the VCS failure")
}

func TestVerifyReadOnlyPropagatesTargetError(t *testing.T) {
	t.Setenv("MAGUS_VCS_ENABLED", "false")

	want := errors.New("target blew up")
	err := verifyReadOnly(t.Context(), t.TempDir(), "generate", func() error { return want })

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
