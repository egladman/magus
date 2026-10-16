package job

import (
	"context"
	"go/format"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// endRequests are the open requests an end filed for the repository at root.
func endRequests(t *testing.T, root string) []sessions.AttentionRequest {
	t.Helper()
	return slices.DeleteFunc(attentionRows(t, root), func(r sessions.AttentionRequest) bool { return r.Source != endSource })
}

// dispose closes request id as the person the guard lets do it.
func dispose(t *testing.T, root, id string) {
	t.Helper()
	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	_, err = sessions.DisposeRequest(dir, id, "confirmed", sessions.InvocationStart{})
	require.NoError(t, err)
}

func storedRows(t *testing.T, s *Store) map[string]types.Job {
	t.Helper()
	rows, err := s.List()
	require.NoError(t, err)
	out := map[string]types.Job{}
	for _, r := range rows {
		out[r.ID] = r
	}
	return out
}

func TestExitWithoutAResultRefusesALiveDescendantAndNamesIt(t *testing.T) {
	t.Parallel()
	loc := declared(t,
		types.Job{ID: "plan", State: types.StateRunning, ReadOnly: true},
		types.Job{ID: "plan/a", Parent: "plan", State: types.StateDeclared, ReadOnly: true},
		types.Job{ID: "plan/a/x", Parent: "plan/a", State: types.StateExited, ReadOnly: true},
		types.Job{ID: "plan/b", Parent: "plan", State: types.StateNoReturn, ReadOnly: true},
	)
	s := NewStore(loc)

	_, err := Exit(t.Context(), s, "plan", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ending plan would end 2 live job(s) below it: plan/a (declared, session holder, never taken); plan/a/x (exited")
	assert.Contains(t, err.Error(), "`magus job exit plan plan/a plan/a/x`", "an ended descendant needs no restating")
	assert.Equal(t, types.StateRunning, storedRows(t, s)["plan"].State)
}

func TestAbandonEndsATreeOnlyWhenEveryLiveDescendantIsNamed(t *testing.T) {
	t.Parallel()
	loc := declared(t,
		types.Job{ID: "plan", State: types.StateRunning, ReadOnly: true},
		types.Job{ID: "plan/a", Parent: "plan", State: types.StateDeclared, ReadOnly: true},
		types.Job{ID: "plan/b", Parent: "plan", State: types.StateDeclared, ReadOnly: true},
	)
	s := NewStore(loc)

	ended, refused := Abandon(t.Context(), s, []string{"plan", "plan/a"})
	require.Len(t, refused, 1)
	assert.Contains(t, refused[0].Error(), "ending plan would end 1 live job(s) below it: plan/b")
	require.Len(t, ended, 1, "a named job with nothing live below it still ends")
	assert.Equal(t, "plan/a", ended[0].ID)
	assert.Equal(t, types.StateRunning, storedRows(t, s)["plan"].State)

	ended, refused = Abandon(t.Context(), s, []string{"plan", "plan/b", "plan"})
	require.Empty(t, refused)
	ids := make([]string, len(ended))
	for i, r := range ended {
		ids[i] = r.ID
	}
	assert.Equal(t, []string{"plan/b", "plan"}, ids, "descendants end before their ancestors")
	for id, r := range storedRows(t, s) {
		assert.Equal(t, types.StateNoReturn, r.State, id)
	}
}

func TestExitWithAResultRefusesADescendantStillWorking(t *testing.T) {
	t.Parallel()
	row := acceptRow()
	loc := declared(t, row, types.Job{ID: "harness/child", Parent: row.ID, State: types.StateRunning, ReadOnly: true})
	result := passingResult()
	resolve := func(context.Context, string) (types.JobAttempt, error) { return passingRun, nil }

	_, err := Exit(t.Context(), NewStore(loc), row.ID, &result, resolve)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1 job(s) below it are still working: harness/child (running")

	seed(t, NewStore(loc), types.Job{ID: "harness/child", Parent: row.ID, State: types.StateExited, ReadOnly: true})
	stored, err := Exit(t.Context(), NewStore(loc), row.ID, &result, resolve)
	require.NoError(t, err, "an exited descendant keeps its result, and the parent's stays live beside it")
	assert.Equal(t, types.StateExited, stored.State)
}

// A descendant holding uncommitted work ends only once a person disposes the request the
// refusal files; every attempt lands on that one request.
func TestAbandonWaitsOnAPersonWhenARestatedDescendantHoldsWork(t *testing.T) {
	t.Parallel()
	repo := execRepo(t)
	loc := tmpLoc(t, repo.Root())
	s := NewStore(loc)
	seed(t, s, types.Job{ID: "plan", State: types.StateRunning, ReadOnly: true})
	seed(t, s, types.Job{ID: "plan/w", Parent: "plan", State: types.StateRunning, WritePaths: []string{"a.txt"}, CheckoutRoot: repo.Root()})
	require.NoError(t, os.WriteFile(filepath.Join(repo.Root(), "a.txt"), []byte("half done\n"), 0o644))

	for range 2 {
		ended, refused := Abandon(t.Context(), s, []string{"plan", "plan/w"})
		assert.Empty(t, ended)
		require.Len(t, refused, 1)
		assert.Contains(t, refused[0].Error(), "plan/w (running, session holder, held in "+repo.Root()+"): 1 uncommitted path(s) in its write paths, first a.txt")
		assert.Contains(t, refused[0].Error(), "which an agent may not")
	}
	requests := endRequests(t, repo.Root())
	require.Len(t, requests, 1)
	assert.Equal(t, "plan plan/w", requests[0].Where)
	assert.Equal(t, []types.FileRef{{Value: filepath.Join(repo.Root(), "a.txt")}}, requests[0].Files)
	for id, r := range storedRows(t, s) {
		assert.Equal(t, types.StateRunning, r.State, id)
	}

	dispose(t, repo.Root(), requests[0].ID)
	ended, refused := Abandon(t.Context(), s, []string{"plan", "plan/w"})
	require.Empty(t, refused)
	require.Len(t, ended, 2)
	assert.Empty(t, endRequests(t, repo.Root()))
}

func TestAbandonNeedsNoPersonForADescendantHoldingNoWork(t *testing.T) {
	t.Parallel()
	repo := execRepo(t)
	s := NewStore(tmpLoc(t, repo.Root()))
	seed(t, s, types.Job{ID: "plan", State: types.StateRunning, ReadOnly: true})
	seed(t, s, types.Job{ID: "plan/w", Parent: "plan", State: types.StateRunning, WritePaths: []string{"a.txt"}, CheckoutRoot: repo.Root()})
	require.NoError(t, os.WriteFile(filepath.Join(repo.Root(), "other.txt"), []byte("not its paths\n"), 0o644))

	ended, refused := Abandon(t.Context(), s, []string{"plan", "plan/w"})
	require.Empty(t, refused)
	assert.Len(t, ended, 2)
	assert.Empty(t, endRequests(t, repo.Root()))
}

func TestAbandonWaitsOnAPersonWhenADescendantsCheckIsRunning(t *testing.T) {
	t.Parallel()
	loc := declared(t,
		types.Job{ID: "plan", State: types.StateRunning, ReadOnly: true},
		types.Job{ID: "plan/w", Parent: "plan", State: types.StateRunning, ReadOnly: true, LastRun: &types.JobRun{Invocation: "inv-1"}},
	)
	ended, refused := Abandon(t.Context(), NewStore(loc), []string{"plan", "plan/w"})
	assert.Empty(t, ended)
	require.Len(t, refused, 1)
	assert.Contains(t, refused[0].Error(), "its check is still running as invocation inv-1")
}

// gitRepo is a git checkout holding files, for a job store to fork checkouts of.
func gitRepo(t *testing.T, files map[string]string) (*Store, *testkit.Repo) {
	t.Helper()
	repo := testkit.NewRepo(t, files)
	return NewStore(Location{StateBase: t.TempDir(), CacheDir: t.TempDir(), Root: repo.Root()}), repo
}

func endedFork(t *testing.T, s *Store, id string) types.Job {
	t.Helper()
	row := forkInto(t, s, id, "")
	_, err := Exit(t.Context(), s, id, nil, nil)
	require.NoError(t, err)
	return row
}

// What magus wrote in a checkout is restored and never keeps it; what it did not write does.
func TestEndCheckoutsRestoresWhatMagusWroteAndKeepsTheRest(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := gitRepo(t, map[string]string{"a.txt": "a\n", "MAGUS.md": "routing\n"})
	row := endedFork(t, s, "w1")
	root := row.Worktree.Root
	write := func(rel, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644))
	}
	write("MAGUS.md", "regenerated\n")
	write("gen.json", "{}\n")
	write("b.txt", "real work\n")
	var asked []string
	written := func(_ context.Context, at string, paths []string) ([]string, error) {
		assert.Equal(t, root, at)
		asked = paths
		return []string{"MAGUS.md", "gen.json"}, nil
	}

	changed, err := s.EndCheckouts(ctx, written)
	require.NoError(t, err)
	require.Len(t, changed, 1)
	assert.ElementsMatch(t, []string{"MAGUS.md", "gen.json", "b.txt"}, asked)
	assert.Equal(t, "1 path(s) in it hold uncommitted work (b.txt): commit them on a branch or hand them back, and `magus job wait w1` removes it", changed[0].Worktree.Kept)
	body, err := os.ReadFile(filepath.Join(root, "MAGUS.md"))
	require.NoError(t, err)
	assert.Equal(t, "routing\n", string(body), "a tracked file goes back to its committed content")
	assert.NoFileExists(t, filepath.Join(root, "gen.json"), "an untracked one goes")

	require.NoError(t, os.Remove(filepath.Join(root, "b.txt")))
	changed, err = s.EndCheckouts(ctx, written)
	require.NoError(t, err)
	require.Len(t, changed, 1)
	assert.NotZero(t, changed[0].Worktree.Removed)
	assert.NoDirExists(t, root)
}

func TestEndCheckoutsRestoresAGoFileOnlyGofmtChanged(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	const committed = "package x\nvar  y = 1\n"
	formatted, err := format.Source([]byte(committed))
	require.NoError(t, err)
	none := func(context.Context, string, []string) ([]string, error) { return nil, nil }

	s, _ := gitRepo(t, map[string]string{"a.txt": "a\n", "x.go": committed})
	edited := endedFork(t, s, "edited")
	require.NoError(t, os.WriteFile(filepath.Join(edited.Worktree.Root, "x.go"), []byte("package x\n\nvar y = 2\n"), 0o644))
	gofmt := endedFork(t, s, "gofmt")
	require.NoError(t, os.WriteFile(filepath.Join(gofmt.Worktree.Root, "x.go"), formatted, 0o644))

	changed, err := s.EndCheckouts(ctx, none)
	require.NoError(t, err)
	byID := map[string]types.JobWorktree{}
	for _, r := range changed {
		byID[r.ID] = *r.Worktree
	}
	assert.NotZero(t, byID["gofmt"].Removed, byID["gofmt"].Kept)
	assert.NoDirExists(t, gofmt.Worktree.Root)
	assert.Contains(t, byID["edited"].Kept, "uncommitted work (x.go)", "a change gofmt did not make is work")
	assert.DirExists(t, edited.Worktree.Root)
}

func TestEndCheckoutsKeepsACheckoutALiveJobIsHeldIn(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := gitRepo(t, map[string]string{"a.txt": "a\n"})
	row := endedFork(t, s, "w1")
	seed(t, s, types.Job{ID: "w2", State: types.StateRunning, ReadOnly: true, CheckoutRoot: row.Worktree.Root})

	changed, err := s.EndCheckouts(ctx, nil)
	require.NoError(t, err)
	require.Len(t, changed, 1)
	assert.Equal(t, "job w2 (running) is held in it", changed[0].Worktree.Kept)
	assert.DirExists(t, row.Worktree.Root)
}

// A job taken in a linked checkout its fork did not make: ending it by name tries that
// checkout too, once nothing in it is work, and says what became of it without recording
// a worktree on the row.
func TestEndCheckoutsTriesTheLinkedCheckoutAnEndedJobWasTakenIn(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, repo := gitRepo(t, map[string]string{"a.txt": "a\n"})
	linked := filepath.Join(t.TempDir(), "linked")
	repo.Git("worktree", "add", "-q", "--detach", linked)
	linked, err := filepath.EvalSymlinks(linked)
	require.NoError(t, err)
	seed(t, s, types.Job{ID: "w1", State: types.StateRunning, ReadOnly: true, CheckoutRoot: linked})
	seed(t, s, types.Job{ID: "main", State: types.StateRunning, ReadOnly: true, CheckoutRoot: repo.Root()})
	for _, id := range []string{"w1", "main"} {
		_, err := Exit(ctx, s, id, nil, nil)
		require.NoError(t, err)
	}
	require.NoError(t, os.WriteFile(filepath.Join(linked, "MAGUS.md"), []byte("regenerated\n"), 0o644))
	written := func(context.Context, string, []string) ([]string, error) { return []string{"MAGUS.md"}, nil }

	changed, err := s.EndCheckouts(ctx, written)
	require.NoError(t, err)
	assert.Empty(t, changed, "a job this call did not end leaves its checkout alone")

	changed, err = s.EndCheckouts(ctx, written, "w1", "main")
	require.NoError(t, err)
	require.Len(t, changed, 1, "the primary checkout is never a job's to remove")
	assert.Equal(t, "w1", changed[0].ID)
	assert.Equal(t, linked, changed[0].Worktree.Root)
	assert.NoFileExists(t, filepath.Join(linked, "MAGUS.md"), "what magus wrote is restored first")
	assert.Contains(t, changed[0].Worktree.Kept, "removing it failed", "the VCS removes only a checkout `magus job fork --checkout` made")
	assert.Nil(t, storedRows(t, s)["w1"].Worktree, "no worktree is recorded for a checkout no fork made")
}

// A prune that ends a descendant only because its ancestor ends is refused until the
// descendant is restated, and waits on a person when it holds work.
func TestJobPruneWaitsOnAPersonForARestatedDescendantHoldingWork(t *testing.T) {
	t.Parallel()
	const now = int64(100_000)
	repo := execRepo(t)
	s := tmpStore(t, repo.Root())
	staleAfter := 2 * time.Hour
	s.clock = func() time.Time { return time.Unix(now, 0) }
	s.staleAfter = &staleAfter
	s.landed = func(context.Context, types.Job) string { return "" }
	old := now - int64((3 * time.Hour).Seconds())
	plant(t, s,
		types.Job{ID: "done", State: types.StateExited, Registered: old, Updated: old},
		types.Job{ID: "done/w", Parent: "done", State: types.StateRunning, WritePaths: []string{"a.txt"}, CheckoutRoot: repo.Root(), Updated: now - 60},
	)
	require.NoError(t, os.WriteFile(filepath.Join(repo.Root(), "a.txt"), []byte("half done\n"), 0o644))

	_, err := s.Prune(t.Context(), PruneOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only because an ancestor it ends does: done/w (running")
	assert.Contains(t, err.Error(), "`magus job prune done/w`")

	_, err = s.Prune(t.Context(), PruneOptions{}, "done", "done/w")
	require.ErrorContains(t, err, "prune does not end done with an ancestor")

	_, err = s.Prune(t.Context(), PruneOptions{}, "done/w")
	require.ErrorContains(t, err, "which an agent may not")
	requests := endRequests(t, repo.Root())
	require.Len(t, requests, 1)
	for id, r := range storedRows(t, s) {
		assert.True(t, r.State.Live(), id)
	}

	dispose(t, repo.Root(), requests[0].ID)
	got, err := s.Prune(t.Context(), PruneOptions{}, "done/w")
	require.NoError(t, err)
	assert.Len(t, got, 2)
}
