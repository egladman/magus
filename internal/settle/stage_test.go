package settle

import (
	"context"
	"os"
	"testing"

	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/trail"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFilterStageable pins the fix for a data-loss bug. Staging hands a project's
// declared outputs to one `git add`; if one path no longer exists on disk (a renamed
// directory with a stale declaration), git fails that pathspec with "did not match any
// files" and aborts the WHOLE invocation, staging nothing. You think you staged and did
// not.
//
// So: keep every path that exists; keep a path gone from disk but still tracked, since
// that is how a deletion or rename gets recorded; drop only what is neither.
func TestFilterStageable(t *testing.T) {
	repo := testkit.NewRepository(t, nil)
	dir := repo.Root()

	// tracked.txt is committed, then deleted from disk without `git rm`, the "old half
	// of a rename" case: gone from disk, still tracked.
	trackedPath := repo.Write("tracked.txt", "x")
	repo.Commit("seed", nil)
	require.NoError(t, os.Remove(trackedPath))

	// present.txt exists on disk and was never committed: the ordinary new-file case.
	repo.Write("present.txt", "y")

	// stale.txt is a declared output that never existed on disk and was never tracked,
	// e.g. libs/diag/MAGUS.md after libs/diag was renamed to libs/diagnostics. This is
	// the path that must be dropped, not fed to `git add`.
	paths := []string{"tracked.txt", "present.txt", "stale.txt"}

	stageable, dropped, err := filterStageable(context.Background(), dir, resolveGitDriver(t, dir), paths)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"tracked.txt", "present.txt"}, stageable)
	assert.Equal(t, []string{"stale.txt"}, dropped)
}

// TestFilterStageableNonGit pins that the probe is skipped for backends without git's
// abort-the-whole-invocation behavior, rather than shelling out to a missing command.
func TestFilterStageableNonGit(t *testing.T) {
	dir := t.TempDir()
	res, err := vcs.Resolve(context.Background(), dir, "", types.VCSOptions{Name: "hg"})
	require.NoError(t, err)
	stageable, dropped, err := filterStageable(context.Background(), dir, res.VCS, []string{"gone.txt"})
	require.NoError(t, err)
	assert.Equal(t, []string{"gone.txt"}, stageable)
	assert.Empty(t, dropped)
}

// TestStagePathsSurvivesStalePath proves the end-to-end fix: one nonexistent, untracked
// path in the batch must not abort the rest. The field failure:
//
//	fatal: pathspec 'libs/diag/MAGUS.md' did not match any files
//	[error] vcs resolve: stage: exit status 128
//
// where every other path in the batch was silently left unstaged.
func TestStagePathsSurvivesStalePath(t *testing.T) {
	repo := testkit.NewRepository(t, nil)
	dir := repo.Root()
	repo.Write("present.txt", "y")

	staged, dropped, err := stagePaths(context.Background(), dir, resolveGitDriver(t, dir), []string{"present.txt", "stale.txt"})
	require.NoError(t, err)
	assert.Equal(t, []string{"present.txt"}, staged)
	assert.Equal(t, []string{"stale.txt"}, dropped, "the stale path is reported, not silently discarded")

	assert.Equal(t, "present.txt", repo.Git("diff", "--cached", "--name-only"),
		"present.txt must be staged even though stale.txt was handed to the same call")
}

// TestOtherLeasesPaths: a whole-tree stage swept a sibling lease's uncommitted sources out
// of a shared checkout into the caller's commit (2026-10-14).
func TestOtherLeasesPaths(t *testing.T) {
	root := t.TempDir()
	rows := []jobtypes.Job{
		{ID: "mine", State: jobtypes.StateRunning, CheckoutRoot: root, Paths: jobtypes.Paths{Write: []string{"api/**"}}},
		{ID: "sibling", State: jobtypes.StateRunning, CheckoutRoot: root, Paths: jobtypes.Paths{Write: []string{"web/**"}}},
		{ID: "elsewhere", State: jobtypes.StateRunning, CheckoutRoot: t.TempDir(), Paths: jobtypes.Paths{Write: []string{"docs/**"}}},
		{ID: "ended", State: jobtypes.StatePass, CheckoutRoot: root, Paths: jobtypes.Paths{Write: []string{"cli/**"}}},
	}
	paths := []string{"api/a.go", "web/b.ts", "docs/c.md", "cli/d.go", "README.md"}

	assert.Equal(t, map[string]string{"web/b.ts": "sibling"}, otherLeasesPaths(rows, root, "mine", paths),
		"only a live lease writing in this checkout holds a path back from its sibling")
	assert.Equal(t, map[string]string{"api/a.go": "mine", "web/b.ts": "sibling"}, otherLeasesPaths(rows, root, "", paths),
		"a caller with no lease stages no lease's work")
}

// The CLI leaves the context unstamped, so the caller's lease is the one its BAGGAGE claims.
func TestHeldForOtherLeasesReadsTheCallersEnvironmentLease(t *testing.T) {
	testkit.Isolate(t)
	root := testkit.NewRepository(t, nil).Root()
	loc := job.Location{StateBase: fixture.StateBase(t), CacheDir: t.TempDir(), Root: root}
	store := job.NewStore(loc)
	for _, row := range []jobtypes.Job{
		{ID: "mine", State: jobtypes.StateRunning, CheckoutRoot: root, Paths: jobtypes.Paths{Write: []string{"api/**"}}},
		{ID: "sibling", State: jobtypes.StateRunning, CheckoutRoot: root, Paths: jobtypes.Paths{Write: []string{"web/**"}}},
	} {
		_, err := store.Update(t.Context(), row.ID, func(cur *jobtypes.Job) { *cur = row })
		require.NoError(t, err)
	}
	t.Setenv(trail.EnvBaggage, trail.BaggageLease+"=mine")

	held, err := heldForOtherLeases(context.Background(), func(string) (job.Location, error) { return loc, nil }, root, []string{"api/a.go", "web/b.ts"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"web/b.ts": "sibling"}, held)
}
