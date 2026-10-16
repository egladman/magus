package job

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// worktreeStore is a job store over a fresh git repository, and that repository.
func worktreeStore(t *testing.T) (*Store, Location, *testkit.Repo) {
	t.Helper()
	repo := execRepo(t)
	loc := Location{StateBase: t.TempDir(), CacheDir: t.TempDir(), Root: repo.Root()}
	return NewStore(loc), loc, repo
}

// forkInto makes job id's checkout in a fresh directory, declares the job and records the
// checkout on it, the way `magus job fork --checkout` does.
func forkInto(t *testing.T, s *Store, id, binary string) types.Job {
	t.Helper()
	wt, made, err := s.ForkWorktree(t.Context(), id, filepath.Join(t.TempDir(), id), "", binary)
	require.NoError(t, err)
	require.True(t, made)
	seed(t, s, types.Job{ID: id, State: types.StateDeclared, WritePaths: []string{"a.txt"}})
	row, err := s.RecordWorktree(t.Context(), id, wt)
	require.NoError(t, err)
	return row
}

func TestForkWorktreeMakesTheJobsCheckoutAtTheForkersRevision(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _, repo := worktreeStore(t)

	row := forkInto(t, s, "w1", "")
	wt := *row.Worktree
	assert.Equal(t, repo.Head(), wt.Base)
	assert.FileExists(t, filepath.Join(wt.Root, "a.txt"), "the checkout holds the revision it was made at")
	assert.Empty(t, wt.Binary)
	assert.NoFileExists(t, filepath.Join(wt.Root, "magus"), "a forker running magus from PATH places none")

	again, made, err := s.ForkWorktree(ctx, "w1", wt.Root, "", "")
	require.NoError(t, err)
	assert.False(t, made, "forking the job again into its own checkout makes nothing")
	assert.Equal(t, wt, again)

	_, _, err = s.ForkWorktree(ctx, "w1", filepath.Join(t.TempDir(), "elsewhere"), "", "")
	require.ErrorContains(t, err, "already owns the checkout "+wt.Root)
}

func TestForkWorktreeRefusesACheckpointWithUncommittedChanges(t *testing.T) {
	t.Parallel()
	s, _, repo := worktreeStore(t)
	dir := filepath.Join(t.TempDir(), "w1")

	_, _, err := s.ForkWorktree(t.Context(), "w1", dir, repo.Head()+"+00112233445566778899aabbccddeeff", "")
	require.ErrorContains(t, err, "uncommitted changes")
	assert.NoDirExists(t, dir)
}

func TestMirroredBinaryIsOnlyTheForkingCheckoutsOwnMagus(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	local := filepath.Join(root, "magus")
	require.NoError(t, os.WriteFile(local, []byte("local\n"), 0o755))
	onPath := filepath.Join(t.TempDir(), "magus")
	require.NoError(t, os.WriteFile(onPath, []byte("local\n"), 0o755))

	assert.Equal(t, local, MirroredBinary(local, root))
	assert.Empty(t, MirroredBinary(onPath, root), "a magus on PATH is not the checkout's own, however alike")
	assert.Empty(t, MirroredBinary(onPath, t.TempDir()), "a checkout holding no ./magus has none to mirror")
}

func TestForkWorktreePlacesTheForkersBinary(t *testing.T) {
	t.Parallel()
	s, _, _ := worktreeStore(t)
	bin := filepath.Join(t.TempDir(), "magus")
	require.NoError(t, os.WriteFile(bin, []byte("build 1\n"), 0o755))

	row := forkInto(t, s, "w1", bin)
	assert.Equal(t, filepath.Join(row.Worktree.Root, "magus"), row.Worktree.Binary)
	body, err := os.ReadFile(row.Worktree.Binary)
	require.NoError(t, err)
	assert.Equal(t, "build 1\n", string(body))
	info, err := os.Stat(row.Worktree.Binary)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&0o111, "the placed binary runs")
}

func TestExecTakesAJobInItsOwnCheckoutAndNowhereElse(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, loc, _ := worktreeStore(t)
	row := forkInto(t, s, "w1", "")

	_, err := s.Exec(ctx, "w1")
	require.ErrorIs(t, err, ErrHeldElsewhere)
	assert.ErrorContains(t, err, row.Worktree.Root)

	taken, err := NewStore(Location{StateBase: loc.StateBase, CacheDir: loc.CacheDir, Root: row.Worktree.Root}).Exec(ctx, "w1")
	require.NoError(t, err)
	assert.Equal(t, row.Worktree.Root, taken.CheckoutRoot)
	assert.Equal(t, types.StateRunning, taken.State)
}

func TestEndWorktreesRemovesACleanCheckoutOnceItsJobEnds(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _, _ := worktreeStore(t)
	row := forkInto(t, s, "w1", "")

	changed, err := s.EndWorktrees(ctx)
	require.NoError(t, err)
	assert.Empty(t, changed, "a live job keeps its checkout")
	assert.DirExists(t, row.Worktree.Root)

	_, err = Exit(ctx, s, "w1", nil, nil)
	require.NoError(t, err)
	changed, err = s.EndWorktrees(ctx)
	require.NoError(t, err)
	require.Len(t, changed, 1)
	assert.NotZero(t, changed[0].Worktree.Removed)
	assert.Empty(t, changed[0].Worktree.Kept)
	assert.NoDirExists(t, row.Worktree.Root)

	changed, err = s.EndWorktrees(ctx)
	require.NoError(t, err)
	assert.Empty(t, changed, "a removed checkout is not removed twice")
}

// The holder exits from inside its checkout, which stays and says how it goes; the next call
// from elsewhere removes it.
func TestEndWorktreesKeepsTheCheckoutTheCallRunsInAndSaysSo(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, loc, _ := worktreeStore(t)
	row := forkInto(t, s, "w1", "")
	inside := NewStore(Location{StateBase: loc.StateBase, CacheDir: loc.CacheDir, Root: row.Worktree.Root})
	_, err := Exit(ctx, inside, "w1", nil, nil)
	require.NoError(t, err)

	changed, err := inside.EndWorktrees(ctx)
	require.NoError(t, err)
	require.Len(t, changed, 1)
	assert.Contains(t, changed[0].Worktree.Kept, "this call runs in it; `magus job wait w1` from another checkout removes it")
	assert.DirExists(t, row.Worktree.Root)

	changed, err = s.EndWorktrees(ctx)
	require.NoError(t, err)
	require.Len(t, changed, 1)
	assert.NotZero(t, changed[0].Worktree.Removed)
	assert.NoDirExists(t, row.Worktree.Root)
}

func TestEndWorktreesKeepsACheckoutHoldingWorkAndSaysWhy(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _, repo := worktreeStore(t)
	dirty := forkInto(t, s, "dirty", "")
	require.NoError(t, os.WriteFile(filepath.Join(dirty.Worktree.Root, "b.txt"), []byte("b\n"), 0o644))
	stranded := forkInto(t, s, "stranded", "")
	repo.Git("-C", stranded.Worktree.Root, "commit", "-q", "--allow-empty", "-m", "local")
	for _, id := range []string{"dirty", "stranded"} {
		_, err := Exit(ctx, s, id, nil, nil)
		require.NoError(t, err)
	}

	changed, err := s.EndWorktrees(ctx)
	require.NoError(t, err)
	require.Len(t, changed, 2)
	kept := map[string]types.JobWorktree{}
	for _, row := range changed {
		kept[row.ID] = *row.Worktree
	}
	assert.Contains(t, kept["dirty"].Kept, "b.txt")
	assert.Contains(t, kept["stranded"].Kept, "on no branch, bookmark, remote or its base")
	for id, wt := range kept {
		assert.Zero(t, wt.Removed, id)
		assert.DirExists(t, wt.Root, id)
	}

	changed, err = s.EndWorktrees(ctx)
	require.NoError(t, err)
	assert.Empty(t, changed, "a reason that has not changed is not recorded again")
}
