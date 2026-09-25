package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

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
func narrowed(t *testing.T, root, cacheDir, leaseID string) *Policy {
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
