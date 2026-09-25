package confinement

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/internal/sandbox/env"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fromConfig is FromConfig for a test that expects it to succeed.
func fromConfig(t *testing.T, root, cacheDir string, cfg config.SandboxConfig) *sandbox.Policy {
	t.Helper()
	p, err := FromConfig(root, cacheDir, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(p.TempDir) })
	return p
}

// A sandbox.allow entry changes the policy's fingerprint, and so its kernel ruleset,
// and grants exactly the mode it spells.
func TestFromConfigFlowsAllowIntoPolicy(t *testing.T) {
	t.Parallel()
	root, cacheDir := t.TempDir(), t.TempDir()
	extra := filesystem.ResolveRulePath(t.TempDir())

	base := fromConfig(t, root, cacheDir, config.SandboxConfig{})
	withAllow := fromConfig(t, root, cacheDir, config.SandboxConfig{
		Allow: []config.SandboxAllowPath{{Path: extra, Mode: "rw"}},
	})
	assert.NotEqual(t, base.Fingerprint(), withAllow.Fingerprint())
	assert.Equal(t, base.Fingerprint(), fromConfig(t, root, cacheDir, config.SandboxConfig{}).Fingerprint(),
		"one workspace's policy is stable, or a server's second run trips MGS2010")
	assert.NoError(t, withAllow.CheckWrite(t.Context(), filepath.Join(extra, "out")))
	assert.Error(t, withAllow.CheckExec(t.Context(), filepath.Join(extra, "tool")), "rw grants no exec")
}

func TestFromConfigFingerprintDiffersByRoot(t *testing.T) {
	t.Parallel()
	a := fromConfig(t, t.TempDir(), "", config.SandboxConfig{})
	b := fromConfig(t, t.TempDir(), "", config.SandboxConfig{})
	assert.NotEqual(t, a.Fingerprint(), b.Fingerprint())
}

// Misconfiguration is an error naming every bad entry, never an entry skipped with a
// warning: skipped, a typo grants less than was written, or more.
func TestFromConfigRefusesABadAllowOrPassthrough(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MAGUS_TEST_UNSET_VAR", "")
	for name, cfg := range map[string]config.SandboxConfig{
		"mode typo":     {Allow: []config.SandboxAllowPath{{Path: "/opt/x", Mode: "RW"}}},
		"unset var":     {Allow: []config.SandboxAllowPath{{Path: "$MAGUS_TEST_UNSET_VAR/", Mode: "rw"}}},
		"relative path": {Allow: []config.SandboxAllowPath{{Path: "build", Mode: "ro"}}},
		"short prefix":  {Env: config.SandboxEnv{Passthrough: []string{"GO*"}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := FromConfig(root, "", cfg)
			assert.ErrorIs(t, err, types.AllowlistUnresolved)
		})
	}
}

// The private temp dir exists outside the workspace, is the children's TMPDIR, and
// is the same dir for every build of one workspace's policy.
func TestFromConfigGivesChildrenAPrivateTempDir(t *testing.T) {
	root, cacheDir := t.TempDir(), t.TempDir()
	p := fromConfig(t, root, cacheDir, config.SandboxConfig{})

	info, err := os.Stat(p.TempDir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	assert.False(t, filesystem.Under(filesystem.ResolveRulePath(p.TempDir), filesystem.ResolveRulePath(root)))
	assert.Contains(t, p.BaseEnv, "TMPDIR="+p.TempDir)
	assert.NoError(t, p.CheckExec(t.Context(), filepath.Join(p.TempDir, "go-build1", "a.test")))
	assert.Equal(t, p.TempDir, fromConfig(t, root, cacheDir, config.SandboxConfig{}).TempDir)
	assert.NotEqual(t, p.TempDir, fromConfig(t, t.TempDir(), cacheDir, config.SandboxConfig{}).TempDir)
}

// A temp dir inside the checkout changes what tools see: a repository a test creates
// there nests in the workspace's own, and a VCS command run in it rewrites the
// workspace's history. So a TMPDIR inside the workspace is refused, not used.
func TestFromConfigNeverPutsTheTempDirInsideTheWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", filepath.Join(root, ".magus", "tmp"))
	require.NoError(t, os.MkdirAll(os.Getenv("TMPDIR"), 0o700))

	_, err := FromConfig(root, filepath.Join(root, ".magus"), config.SandboxConfig{})
	assert.ErrorContains(t, err, "inside the workspace")
}

// The shared temp dir is world-writable, so a name another account created first is
// refused rather than used.
func TestPrivateTempDirRefusesAPlantedDirectory(t *testing.T) {
	base := t.TempDir()
	dir, err := privateTempDir(base, "/ws")
	require.NoError(t, err)
	require.NoError(t, os.Chmod(dir, 0o755))
	_, err = privateTempDir(base, "/ws")
	assert.ErrorContains(t, err, "not a private directory")

	require.NoError(t, os.RemoveAll(dir))
	require.NoError(t, os.Symlink(t.TempDir(), dir))
	_, err = privateTempDir(base, "/ws")
	assert.ErrorContains(t, err, "not a private directory")
}

// A linked worktree keeps its git directories outside the checkout, and a subdirectory
// workspace has its .git above it.
func TestGitDirs(t *testing.T) {
	repo := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".git", "worktrees", "wt"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "sub"), 0o755))
	gitDir, common := gitDirs(filepath.Join(repo, "sub"))
	assert.Equal(t, filepath.Join(repo, ".git"), gitDir)
	assert.Equal(t, filepath.Join(repo, ".git"), common)

	wt := t.TempDir()
	linked := filepath.Join(repo, ".git", "worktrees", "wt")
	require.NoError(t, os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+linked+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(linked, "commondir"), []byte("../..\n"), 0o644))
	gitDir, common = gitDirs(wt)
	assert.Equal(t, linked, gitDir)
	assert.Equal(t, filepath.Join(repo, ".git"), common)

	gitDir, common = gitDirs(filepath.VolumeName(wt) + string(filepath.Separator))
	assert.Empty(t, gitDir+common, "no checkout, no grant")
}

// TestApplyFingerprintMismatch exercises the MGS2010 guard: once a policy has been
// applied to the process, a later Apply with a different fingerprint is rejected
// (landlock is immutable once set). The package's apply state is process-global and
// can only be driven once per process, so the whole sequence lives in this one test,
// which must run before any test calling MarkAppliedExternally.
//
// Skipped where landlock is supported: the first Apply would restrict the test runner.
func TestApplyFingerprintMismatch(t *testing.T) {
	if sandbox.Supported() {
		t.Skip("landlock is supported here; the first Apply would restrict the test process")
	}

	ctx := context.Background()
	rootA, rootB := t.TempDir(), t.TempDir()
	policyA := fromConfig(t, rootA, "", config.SandboxConfig{})
	policyB := fromConfig(t, rootB, "", config.SandboxConfig{})

	ctxA, err := Apply(ctx, policyA, rootA, types.SandboxModeBestEffort)
	require.NoError(t, err, "best-effort swallows ErrUnsupported as its fallback")
	assert.Same(t, policyA, sandbox.PolicyFromContext(ctxA))

	gotCtx, err := Apply(ctx, policyB, rootB, types.SandboxModeBestEffort)
	require.ErrorIs(t, err, types.SandboxPolicyMismatch)
	assert.ErrorContains(t, err, rootB, "names the offending workspace")
	assert.Equal(t, ctx, gotCtx, "a rejected Apply returns the input ctx")
	assert.Nil(t, sandbox.PolicyFromContext(gotCtx))

	ctxA2, err := Apply(ctx, policyA, rootA, types.SandboxModeBestEffort)
	require.NoError(t, err, "re-applying the same fingerprint is idempotent")
	assert.Same(t, policyA, sandbox.PolicyFromContext(ctxA2))

	// The process fell back to binding checks, so a caller requiring the kernel's
	// enforcement is refused and nothing is attached for it to run under.
	gotCtx, err = Apply(ctx, policyA, rootA, types.SandboxModeRequired)
	require.ErrorIs(t, err, types.SandboxRequired)
	assert.ErrorContains(t, err, rootA)
	assert.Nil(t, sandbox.PolicyFromContext(gotCtx))
}

// TestConcurrentMarkAppliedExternallyAndApply races MarkAppliedExternally's writes to
// the package globals against Apply's reads; `go test -race` flags an unguarded one.
// Skipped where landlock is supported: a losing goroutine could call the real apply.
func TestConcurrentMarkAppliedExternallyAndApply(t *testing.T) {
	if sandbox.Supported() {
		t.Skip("landlock is supported here; a losing goroutine could call the real kernel apply")
	}

	root := t.TempDir()
	policy := fromConfig(t, root, "", config.SandboxConfig{})
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			MarkAppliedExternally(fmt.Sprintf("fp-%d", i), false)
		}(i)
		go func() {
			defer wg.Done()
			_, _ = Apply(ctx, policy, root, types.SandboxModeBestEffort)
		}()
	}
	wg.Wait()
}

// leaseWorkspace lays out a workspace with pkg/a, pkg/a/gen and pkg/b, writes row into
// its job store, and returns the resolved root and the cache directory.
func leaseWorkspace(t *testing.T, row types.Job) (root, cacheDir string) {
	t.Helper()
	root = filesystem.ResolveRulePath(t.TempDir())
	cacheDir = t.TempDir()
	// The job store lives in the per-repository state dir, and NarrowToLease resolves it
	// through the environment, so without this the fixture's rows land in the
	// developer's own store and the store under test is the real one.
	testkit.Isolate(t)
	for _, dir := range []string{"pkg/a/gen", "pkg/a/keep", "pkg/b"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755))
	}
	if row.ID != "" {
		_, err := job.NewStore(job.Location{CacheDir: cacheDir, Root: root}).
			Update(t.Context(), row.ID, func(cur *types.Job) { *cur = row })
		require.NoError(t, err)
	}
	return root, cacheDir
}

// narrowed builds the workspace policy for root and narrows it to leaseID's row.
func narrowed(t *testing.T, root, cacheDir, leaseID string) *sandbox.Policy {
	t.Helper()
	base := fromConfig(t, root, cacheDir, config.SandboxConfig{})
	return NarrowToLease(t.Context(), base, job.Location{CacheDir: cacheDir, Root: root}, leaseID, types.LeaseSourceMarker)
}

// TestNarrowToLeaseGrantsOnlyTheWritePaths is the boundary the whole tier rests on: a
// worker's write grant is the job row's write paths and nothing else in the checkout.
// Reads stay wide, because the row declares a write boundary only.
func TestNarrowToLeaseGrantsOnlyTheWritePaths(t *testing.T) {
	root, cacheDir := leaseWorkspace(t, types.Job{
		ID: "fleet/w1", Parent: "fleet/root", State: types.StateRunning,
		WritePaths: []string{"pkg/a/**"},
	})
	p := narrowed(t, root, cacheDir, "fleet/w1")
	ctx := t.Context()

	assert.Equal(t, "fleet/w1", p.Lease)
	assert.Equal(t, types.LeaseSourceMarker, p.LeaseFrom, "the narrowed policy carries where its lease came from")
	assert.NoError(t, p.CheckWrite(ctx, filepath.Join(root, "pkg", "a", "x.txt")))
	assert.Error(t, p.CheckWrite(ctx, filepath.Join(root, "pkg", "b", "x.txt")))
	assert.Error(t, p.CheckWrite(ctx, filepath.Join(root, "x.txt")))
	assert.NoError(t, p.CheckRead(ctx, filepath.Join(root, "pkg", "b", "x.txt")),
		"a worker has to read the tree it is changing")
	assert.NoError(t, p.CheckWrite(ctx, filepath.Join(cacheDir, "out")),
		"a target that cannot write the cache dir produces nothing")
	assert.NoError(t, p.CheckWrite(ctx, filepath.Join(p.TempDir, "scratch")))
	assert.NoError(t, p.CheckWrite(ctx, "/dev/null"), "grants outside the checkout keep their writes")
}

// TestNarrowToLeaseRefusesAForbiddenPathInsideAnOwnedOne pins the allowlist's consequence.
// The forbidden subtree is refused and the siblings keep their grants; what is lost with
// it is the directory HOLDING the forbidden entry, because granting that would grant the
// entry too.
func TestNarrowToLeaseRefusesAForbiddenPathInsideAnOwnedOne(t *testing.T) {
	root, cacheDir := leaseWorkspace(t, types.Job{
		ID: "fleet/w1", Parent: "fleet/root", State: types.StateRunning,
		WritePaths: []string{"pkg/**"}, DenyPaths: []string{"pkg/a/gen"},
	})
	p := narrowed(t, root, cacheDir, "fleet/w1")
	ctx := t.Context()

	assert.Error(t, p.CheckWrite(ctx, filepath.Join(root, "pkg", "a", "gen", "x.txt")))
	assert.NoError(t, p.CheckWrite(ctx, filepath.Join(root, "pkg", "a", "keep", "x.txt")))
	assert.NoError(t, p.CheckWrite(ctx, filepath.Join(root, "pkg", "b", "x.txt")))
	assert.Error(t, p.CheckWrite(ctx, filepath.Join(root, "pkg", "a", "new.txt")),
		"pkg/a cannot be granted without granting the forbidden pkg/a/gen with it")
}

// TestNarrowToLeaseGrantsNothingForAGlobThatMatchesNothing keeps a typo from reading as a
// grant: an owned path is a claim about files that exist.
func TestNarrowToLeaseGrantsNothingForAGlobThatMatchesNothing(t *testing.T) {
	root, cacheDir := leaseWorkspace(t, types.Job{
		ID: "fleet/w1", Parent: "fleet/root", State: types.StateRunning,
		WritePaths: []string{"pkg/nowhere/**"},
	})
	p := narrowed(t, root, cacheDir, "fleet/w1")

	assert.Equal(t, "fleet/w1", p.Lease)
	assert.Error(t, p.CheckWrite(t.Context(), filepath.Join(root, "pkg", "a", "x.txt")))
	assert.Error(t, p.CheckWrite(t.Context(), filepath.Join(root, "x.txt")))
}

// TestNarrowToLeaseGrantsTheDirectoryOfALiteralPathToCreate is the create case: a lease
// that owns a file not yet on disk can write it, because its directory is granted. A
// glob keeps the existing-only rule, so the two cases sit side by side.
func TestNarrowToLeaseGrantsTheDirectoryOfALiteralPathToCreate(t *testing.T) {
	root, cacheDir := leaseWorkspace(t, types.Job{
		ID: "fleet/w1", Parent: "fleet/root", State: types.StateRunning,
		WritePaths: []string{"pkg/a/new.go", "pkg/nowhere/**"},
	})
	p := narrowed(t, root, cacheDir, "fleet/w1")
	ctx := t.Context()

	assert.NoError(t, p.CheckWrite(ctx, filepath.Join(root, "pkg", "a", "new.go")))
	assert.NoError(t, p.CheckWrite(ctx, filepath.Join(root, "pkg", "a", "sibling.go")),
		"the grant is the directory the file lands in, which is what landlock can express")
	assert.Error(t, p.CheckWrite(ctx, filepath.Join(root, "pkg", "b", "x.txt")))
	assert.Error(t, p.CheckWrite(ctx, filepath.Join(root, "pkg", "nowhere", "x.txt")), "a glob that matches nothing still grants nothing")
}

// TestNarrowToLeaseNeverGrantsOutsideTheCheckout is the containment rule: an owned path
// that escapes the root through `..`, through a symlink, or by having no existing ancestor
// below the root grants nothing, because the alternative is a grant on the checkout or on
// whatever the link points at.
func TestNarrowToLeaseNeverGrantsOutsideTheCheckout(t *testing.T) {
	outside := filesystem.ResolveRulePath(t.TempDir())
	root, cacheDir := leaseWorkspace(t, types.Job{
		ID: "fleet/w1", Parent: "fleet/root", State: types.StateRunning,
		WritePaths: []string{"../" + filepath.Base(outside) + "/x.txt", "link/x.txt", "nowhere/deeper/new.go", "."},
	})
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link")))
	p := narrowed(t, root, cacheDir, "fleet/w1")
	ctx := t.Context()

	assert.Equal(t, "fleet/w1", p.Lease)
	assert.Error(t, p.CheckWrite(ctx, filepath.Join(outside, "x.txt")), "a `..` path is not a grant on the sibling")
	assert.Error(t, p.CheckWrite(ctx, filepath.Join(root, "link", "x.txt")), "a symlink out of the checkout is not a grant on its target")
	assert.Error(t, p.CheckWrite(ctx, filepath.Join(root, "x.txt")), "a path with no existing ancestor below the root grants nothing, not the root")
	assert.Error(t, p.CheckWrite(ctx, filepath.Join(root, "pkg", "b", "x.txt")))
}

// A read-only declaration is a boundary at every tier: a root row that declares it is
// not the orchestrator's full grant, and a worker keeps only the cache and temp dirs.
func TestNarrowToLeaseGrantsAReadOnlyRowNoWrites(t *testing.T) {
	for _, row := range []types.Job{
		{ID: "fleet/root", State: types.StateRunning, ReadOnly: true},
		{ID: "fleet/w1", Parent: "fleet/root", State: types.StateRunning, ReadOnly: true},
	} {
		t.Run(row.ID, func(t *testing.T) {
			root, cacheDir := leaseWorkspace(t, row)
			p := narrowed(t, root, cacheDir, row.ID)
			ctx := t.Context()

			assert.Equal(t, row.ID, p.Lease)
			assert.Error(t, p.CheckWrite(ctx, filepath.Join(root, "pkg", "a", "x.txt")))
			assert.NoError(t, p.CheckRead(ctx, filepath.Join(root, "pkg", "a", "x.txt")))
			assert.NoError(t, p.CheckWrite(ctx, filepath.Join(cacheDir, "out")))
		})
	}
}

// TestNarrowToLeaseLeavesEveryUnnarrowableCaseAlone covers the rows that state no boundary
// narrower than the workspace. A root lease is the orchestrator and owns the checkout; the
// rest are rows the sandbox has nothing to derive from.
func TestNarrowToLeaseLeavesEveryUnnarrowableCaseAlone(t *testing.T) {
	for _, tc := range []struct {
		name  string
		row   types.Job
		acted string
	}{
		{"root lease", types.Job{ID: "fleet/root", State: types.StateRunning, WritePaths: []string{"pkg/a/**"}}, "fleet/root"},
		{"no row", types.Job{}, "fleet/w1"},
		{"no lease claimed", types.Job{ID: "fleet/w1", Parent: "fleet/root", State: types.StateRunning, WritePaths: []string{"pkg/a/**"}}, ""},
		{"empty write paths", types.Job{ID: "fleet/w1", Parent: "fleet/root", State: types.StateRunning}, "fleet/w1"},
		{"terminal row", types.Job{ID: "fleet/w1", Parent: "fleet/root", State: types.StatePass, WritePaths: []string{"pkg/a/**"}}, "fleet/w1"},
		{"no state", types.Job{ID: "fleet/w1", Parent: "fleet/root", WritePaths: []string{"pkg/a/**"}}, "fleet/w1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, cacheDir := leaseWorkspace(t, tc.row)
			base := fromConfig(t, root, cacheDir, config.SandboxConfig{})

			p := NarrowToLease(t.Context(), base, job.Location{CacheDir: cacheDir, Root: root}, tc.acted, types.LeaseSourceMarker)

			assert.Same(t, base, p)
			assert.NoError(t, p.CheckWrite(t.Context(), filepath.Join(root, "pkg", "b", "x.txt")))
		})
	}
}

// TestApplyAttachesANarrowedPolicy runs the narrowed policy through the process-wide apply
// path in ATTACH-ONLY mode. MarkAppliedExternally first is not a shortcut: a real
// landlock_restrict_self would confine the test binary itself, permanently.
func TestApplyAttachesANarrowedPolicy(t *testing.T) {
	root, cacheDir := leaseWorkspace(t, types.Job{
		ID: "fleet/w1", Parent: "fleet/root", State: types.StateRunning,
		WritePaths: []string{"pkg/a/**"},
	})
	p := narrowed(t, root, cacheDir, "fleet/w1")
	MarkAppliedExternally(p.Fingerprint(), false)

	ctx, err := Apply(t.Context(), p, root, types.SandboxModeBestEffort)
	require.NoError(t, err)
	require.Same(t, p, sandbox.PolicyFromContext(ctx))
	assert.Error(t, p.CheckWrite(ctx, filepath.Join(root, "pkg", "b", "x.txt")))
}

// Attach-only, a required sandbox holds only when the ruleset the server applied is
// kernel-enforced.
func TestAttachingARequiredSandboxNeedsTheKernelsEnforcement(t *testing.T) {
	root := t.TempDir()
	p := fromConfig(t, root, "", config.SandboxConfig{})

	MarkAppliedExternally(p.Fingerprint(), false)
	ctx, err := Apply(t.Context(), p, root, types.SandboxModeRequired)
	require.ErrorIs(t, err, types.SandboxRequired)
	assert.Nil(t, sandbox.PolicyFromContext(ctx))

	MarkAppliedExternally(p.Fingerprint(), true)
	ctx, err = Apply(t.Context(), p, root, types.SandboxModeRequired)
	require.NoError(t, err)
	assert.Same(t, p, sandbox.PolicyFromContext(ctx))
}

// sanity: the sentinel-based match used above behaves as errors.Is expects, so a
// regression in DiagnosticError.Is would surface here rather than as a confusing
// failure in the mismatch test.
func TestDiagnosticMismatchSentinel(t *testing.T) {
	t.Parallel()
	err := types.DiagnosticErrorf(types.SandboxPolicyMismatch, "x")
	assert.True(t, errors.Is(err, types.SandboxPolicyMismatch))
	assert.False(t, errors.Is(err, types.SandboxUnsupported))
}

// applyRecorder captures the apply-family metric calls. It embeds observability.Provider
// (left nil) to satisfy the full interface; only the two methods RecordApply exercises are
// overridden, so no other method is ever called.
type applyRecorder struct {
	observability.Provider
	applies []applyCall
	rules   []rulesCall
}

type applyCall struct {
	secs           float64
	outcome, scope string
}

type rulesCall struct {
	read, write, exec, envExact, envGlob int64
	scope                                string
}

func (r *applyRecorder) RecordSandboxApply(_ context.Context, secs float64, outcome, scope string) {
	r.applies = append(r.applies, applyCall{secs, outcome, scope})
}

func (r *applyRecorder) RecordSandboxRules(_ context.Context, sr observability.SandboxRules) {
	r.rules = append(r.rules, rulesCall{sr.Read, sr.Write, sr.Exec, sr.EnvExact, sr.EnvGlob, sr.Scope})
}

func TestRecordApplyDurationAndRules(t *testing.T) {
	rec := &applyRecorder{}
	ctx := observability.WithProvider(context.Background(), rec)

	policy := &sandbox.Policy{
		FS: filesystem.Ruleset{Rules: []filesystem.Rule{
			{Read: true, Write: true, Exec: true},
			{Read: true, Exec: true},
			{Read: true},
		}},
		Env: env.Allowlist{Names: []string{"HOME", "PATH"}, Prefixes: []string{"LC_*"}},
	}

	RecordApply(ctx, 0.25, "applied", "workspace", policy)

	assert.Equal(t, []applyCall{{0.25, "applied", "workspace"}}, rec.applies)
	assert.Equal(t, []rulesCall{{read: 3, write: 1, exec: 2, envExact: 2, envGlob: 1, scope: "workspace"}}, rec.rules)
}

func TestRecordApplyMismatchSkipsRules(t *testing.T) {
	rec := &applyRecorder{}
	ctx := observability.WithProvider(context.Background(), rec)

	// A mismatch installs no ruleset, so the caller passes a nil policy: outcome only.
	RecordApply(ctx, 0, "mismatch", "workspace", nil)

	assert.Equal(t, []applyCall{{0, "mismatch", "workspace"}}, rec.applies)
	assert.Empty(t, rec.rules)
}

func TestRecordApplyNoProviderIsNoop(t *testing.T) {
	RecordApply(context.Background(), 1, "applied", "workspace", &sandbox.Policy{})
}
