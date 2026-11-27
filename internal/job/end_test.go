package job

import (
	"context"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
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
	_, err = sessions.DisposeRequest(t.Context(), dir, id, "confirmed", session.InvocationStart{})
	require.NoError(t, err)
}

func storedRows(t *testing.T, s *Store) map[string]job.Job {
	t.Helper()
	rows, err := s.List(t.Context())
	require.NoError(t, err)
	out := map[string]job.Job{}
	for _, r := range rows {
		out[r.ID] = r
	}
	return out
}

func TestExitWithoutAResultRefusesALiveDescendantAndNamesIt(t *testing.T) {
	t.Parallel()
	loc := declared(t,
		job.Job{ID: "plan", State: job.StateRunning, ReadOnly: true},
		job.Job{ID: "plan/a", Parent: "plan", State: job.StateDeclared, ReadOnly: true},
		job.Job{ID: "plan/a/x", Parent: "plan/a", State: job.StateExited, ReadOnly: true},
		job.Job{ID: "plan/b", Parent: "plan", State: job.StateNoReturn, ReadOnly: true},
	)
	s := NewStore(loc)

	_, err := Exit(t.Context(), s, "plan", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ending plan would end 2 live job(s) below it: plan/a (declared, session holder, never taken); plan/a/x (exited")
	assert.Contains(t, err.Error(), "`magus job exit plan plan/a plan/a/x`", "an ended descendant needs no restating")
	assert.Equal(t, job.StateRunning, storedRows(t, s)["plan"].State)
}

func TestAbandonEndsATreeOnlyWhenEveryLiveDescendantIsNamed(t *testing.T) {
	t.Parallel()
	loc := declared(t,
		job.Job{ID: "plan", State: job.StateRunning, ReadOnly: true},
		job.Job{ID: "plan/a", Parent: "plan", State: job.StateDeclared, ReadOnly: true},
		job.Job{ID: "plan/b", Parent: "plan", State: job.StateDeclared, ReadOnly: true},
	)
	s := NewStore(loc)

	ended, already, refused := Abandon(t.Context(), s, []string{"plan", "plan/a"})
	assert.Empty(t, already)
	require.Len(t, refused, 1)
	assert.Contains(t, refused[0].Error(), "ending plan would end 1 live job(s) below it: plan/b")
	require.Len(t, ended, 1, "a named job with nothing live below it still ends")
	assert.Equal(t, "plan/a", ended[0].ID)
	assert.Equal(t, job.StateRunning, storedRows(t, s)["plan"].State)

	ended, already, refused = Abandon(t.Context(), s, []string{"plan", "plan/b", "plan"})
	assert.Empty(t, already)
	require.Empty(t, refused)
	ids := make([]string, len(ended))
	for i, r := range ended {
		ids[i] = r.ID
	}
	assert.Equal(t, []string{"plan/b", "plan"}, ids, "descendants end before their ancestors")
	rows := storedRows(t, s)
	for id, r := range rows {
		assert.Equal(t, job.StateNoReturn, r.State, id)
	}

	ended, already, refused = Abandon(t.Context(), s, []string{"plan/a"})
	assert.Empty(t, ended)
	assert.Empty(t, refused, "ending a row again asks for what is already true")
	assert.Equal(t, []error{&alreadyEndedError{Row: rows["plan/a"]}}, already)
}

// A dry run ends a tree in what it returns, descendants first as a real end does, and
// writes no row.
func TestAbandonUnderADryRunWritesNothing(t *testing.T) {
	t.Parallel()
	loc := declared(t,
		job.Job{ID: "plan", State: job.StateRunning, ReadOnly: true},
		job.Job{ID: "plan/a", Parent: "plan", State: job.StateDeclared, ReadOnly: true},
	)
	before := storedRows(t, NewStore(loc))
	dry := loc
	dry.DryRun = true

	ended, already, refused := Abandon(t.Context(), NewStore(dry), []string{"plan", "plan/a"})
	assert.Empty(t, already)
	require.Empty(t, refused)
	ids := make([]string, len(ended))
	for i, r := range ended {
		ids[i] = r.ID
		assert.Equal(t, job.StateNoReturn, r.State, r.ID)
	}
	assert.Equal(t, []string{"plan/a", "plan"}, ids)
	assert.Equal(t, before, storedRows(t, NewStore(loc)))
}

// Ending one job restores and removes its checkout alone; another ended row's checkout
// waits for the call that ends every one.
func TestEndNamedCheckoutsLeavesOtherEndedRowsCheckouts(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := gitRepository(t, map[string]string{"a.txt": "a\n"})
	first := endedFork(t, s, "w1")
	second := endedFork(t, s, "w2")

	changed, err := s.EndNamedCheckouts(ctx, nil, "w1")
	require.NoError(t, err)
	require.Len(t, changed, 1)
	assert.Equal(t, "w1", changed[0].ID)
	assert.NoDirExists(t, first.Worktree.Root)
	assert.DirExists(t, second.Worktree.Root)

	changed, err = s.EndCheckouts(ctx, nil)
	require.NoError(t, err)
	require.Len(t, changed, 1)
	assert.Equal(t, "w2", changed[0].ID)
	assert.NoDirExists(t, second.Worktree.Root)
}

// A row taken from a plain shell binds the checkout; ending the row
// must not leave the checkout bound to it.
func TestEndCheckoutsReleasesTheCheckoutBindingOfAnEndedRow(t *testing.T) {
	t.Parallel()
	s, _ := gitRepository(t, map[string]string{"a.txt": "a\n"})
	endedFork(t, s, "w1")
	session := Caller{Host: "claude-code", Session: "s1"}
	require.NoError(t, s.Bind(Caller{}, "w1"))
	require.NoError(t, s.Bind(session, "w1"))

	_, err := s.EndNamedCheckouts(t.Context(), nil, "w1")
	require.NoError(t, err)
	assert.Empty(t, s.Bound(Caller{}), "the checkout's binding goes with the row")
	assert.Empty(t, s.Bound(session), "so does a session's binding")
}

func TestExitWithAResultRefusesADescendantStillWorking(t *testing.T) {
	t.Parallel()
	row := acceptRow()
	loc := declared(t, row, job.Job{ID: "harness/child", Parent: row.ID, State: job.StateRunning, ReadOnly: true})
	result := passingResult()
	resolve := func(context.Context, string) (job.CapturedRun, error) { return passingRun, nil }

	_, err := Exit(t.Context(), NewStore(loc), row.ID, &result, resolve)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1 job(s) below it are still working: harness/child (running")

	seed(t, NewStore(loc), job.Job{ID: "harness/child", Parent: row.ID, State: job.StateExited, ReadOnly: true})
	stored, err := Exit(t.Context(), NewStore(loc), row.ID, &result, resolve)
	require.NoError(t, err, "an exited descendant keeps its result, and the parent's stays live beside it")
	assert.Equal(t, job.StateExited, stored.State)
}

// A descendant holding uncommitted work ends only once a person disposes the request the
// refusal files; every attempt lands on that one request.
func TestAbandonWaitsOnAPersonWhenARestatedDescendantHoldsWork(t *testing.T) {
	t.Parallel()
	repo := execRepository(t)
	loc := tempLocation(t, repo.Root())
	s := NewStore(loc)
	seed(t, s, job.Job{ID: "plan", State: job.StateRunning, ReadOnly: true})
	seed(t, s, job.Job{ID: "plan/w", Parent: "plan", State: job.StateRunning, Paths: job.Paths{Write: []string{"a.txt"}}, CheckoutRoot: repo.Root()})
	require.NoError(t, os.WriteFile(filepath.Join(repo.Root(), "a.txt"), []byte("half done\n"), 0o644))

	for range 2 {
		ended, _, refused := Abandon(t.Context(), s, []string{"plan", "plan/w"})
		assert.Empty(t, ended)
		require.Len(t, refused, 1)
		assert.Contains(t, refused[0].Error(), fmt.Sprintf("plan/w (running, session holder, held in %s): 1 uncommitted path(s) in its write paths, first a.txt", repo.Root()))
		assert.Contains(t, refused[0].Error(), "which an agent may not")
	}
	requests := endRequests(t, repo.Root())
	require.Len(t, requests, 1)
	req := requests[0]
	assert.Equal(t, sessions.AttentionRequest{
		ID: req.ID, Invocation: req.Invocation, OpenedMs: req.OpenedMs,
		Outcome: "permission", Severity: "warning", Source: "job/end",
		Where: "plan plan/w", Files: []types.FileRef{{Value: filepath.Join(repo.Root(), "a.txt")}}, Message: req.Message,
	}, req)
	for id, r := range storedRows(t, s) {
		assert.Equal(t, job.StateRunning, r.State, id)
	}

	dispose(t, repo.Root(), requests[0].ID)
	ended, _, refused := Abandon(t.Context(), s, []string{"plan", "plan/w"})
	require.Empty(t, refused)
	require.Len(t, ended, 2)
	assert.Empty(t, endRequests(t, repo.Root()))
}

func TestAbandonNeedsNoPersonForADescendantHoldingNoWork(t *testing.T) {
	t.Parallel()
	repo := execRepository(t)
	s := NewStore(tempLocation(t, repo.Root()))
	seed(t, s, job.Job{ID: "plan", State: job.StateRunning, ReadOnly: true})
	seed(t, s, job.Job{ID: "plan/w", Parent: "plan", State: job.StateRunning, Paths: job.Paths{Write: []string{"a.txt"}}, CheckoutRoot: repo.Root()})
	require.NoError(t, os.WriteFile(filepath.Join(repo.Root(), "other.txt"), []byte("not its paths\n"), 0o644))

	ended, _, refused := Abandon(t.Context(), s, []string{"plan", "plan/w"})
	require.Empty(t, refused)
	assert.Len(t, ended, 2)
	assert.Empty(t, endRequests(t, repo.Root()))
}

func TestAbandonWaitsOnAPersonWhenADescendantsCheckIsRunning(t *testing.T) {
	t.Parallel()
	loc := declared(t,
		job.Job{ID: "plan", State: job.StateRunning, ReadOnly: true},
		job.Job{ID: "plan/w", Parent: "plan", State: job.StateRunning, ReadOnly: true, LastRun: &job.Run{Invocation: "inv-1"}},
	)
	ended, _, refused := Abandon(t.Context(), NewStore(loc), []string{"plan", "plan/w"})
	assert.Empty(t, ended)
	require.Len(t, refused, 1)
	assert.Contains(t, refused[0].Error(), "its check is still running as invocation inv-1")
}

// gitRepository is a git checkout holding files, for a job store to fork checkouts of.
func gitRepository(t *testing.T, files map[string]string) (*Store, *testkit.Repo) {
	t.Helper()
	repo := testkit.NewRepository(t, files)
	return NewStore(Location{StateBase: t.TempDir(), CacheDir: t.TempDir(), Root: repo.Root()}), repo
}

func endedFork(t *testing.T, s *Store, id string) job.Job {
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
	s, _ := gitRepository(t, map[string]string{"a.txt": "a\n", "MAGUS.md": "routing\n"})
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

	s, _ := gitRepository(t, map[string]string{"a.txt": "a\n", "x.go": committed})
	edited := endedFork(t, s, "edited")
	require.NoError(t, os.WriteFile(filepath.Join(edited.Worktree.Root, "x.go"), []byte("package x\n\nvar y = 2\n"), 0o644))
	gofmt := endedFork(t, s, "gofmt")
	require.NoError(t, os.WriteFile(filepath.Join(gofmt.Worktree.Root, "x.go"), formatted, 0o644))

	changed, err := s.EndCheckouts(ctx, none)
	require.NoError(t, err)
	byID := map[string]job.Worktree{}
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
	s, _ := gitRepository(t, map[string]string{"a.txt": "a\n"})
	row := endedFork(t, s, "w1")
	seed(t, s, job.Job{ID: "w2", State: job.StateRunning, ReadOnly: true, CheckoutRoot: row.Worktree.Root})

	changed, err := s.EndCheckouts(ctx, nil)
	require.NoError(t, err)
	require.Len(t, changed, 1)
	assert.Equal(t, "job w2 (running) is held in it", changed[0].Worktree.Kept)
	assert.DirExists(t, row.Worktree.Root)
}

// A host-made checkout a job was only taken in, and one a row records with Made unset (adopted,
// or recorded before Made existed), stand after the job ends: ending, waiting on and deleting
// the job never remove a checkout its fork did not make.
func TestEndCheckoutsNeverRemovesACheckoutTheForkDidNotMake(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, repo := gitRepository(t, map[string]string{"a.txt": "a\n"})
	linked := filepath.Join(t.TempDir(), "linked")
	repo.Git("worktree", "add", "-q", "--detach", linked)
	linked, err := filepath.EvalSymlinks(linked)
	require.NoError(t, err)
	adopted := job.Worktree{Root: linked, Source: repo.Root(), Base: repo.Head(), Created: 1}
	seed(t, s, job.Job{ID: "taken", State: job.StateRunning, ReadOnly: true, CheckoutRoot: linked})
	for _, id := range []string{"adopted", "deleted"} {
		seed(t, s, job.Job{ID: id, State: job.StateDeclared, ReadOnly: true})
		_, err := s.RecordWorktree(ctx, id, adopted)
		require.NoError(t, err)
	}
	_, err = s.Update(ctx, "adopted", func(cur *job.Job) { cur.State = job.StateRunning })
	require.NoError(t, err)
	for _, id := range []string{"taken", "adopted"} {
		_, err := Exit(ctx, s, id, nil, nil)
		require.NoError(t, err)
	}

	changed, err := s.EndCheckouts(ctx, nil, "taken", "adopted")
	require.NoError(t, err)
	require.Len(t, changed, 1, "a row recording no checkout records none on ending")
	assert.Equal(t, "adopted", changed[0].ID)
	assert.Equal(t, adoptedKept, changed[0].Worktree.Kept)
	assert.Zero(t, changed[0].Worktree.Removed)

	changed, err = s.EndNamedCheckouts(ctx, nil, "taken", "adopted")
	require.NoError(t, err)
	assert.Empty(t, changed, "a reason that has not changed is not recorded again")

	dropped, err := s.Delete(ctx, "deleted", false)
	require.NoError(t, err)
	assert.Zero(t, dropped.Worktree.Removed, "deleting the row leaves the checkout")

	assert.DirExists(t, linked)
	assert.Nil(t, storedRows(t, s)["taken"].Worktree)
}

// A prune that ends a descendant only because its ancestor ends is refused until the
// descendant is named with it, and waits on a person when it holds work.
func TestJobPruneWaitsOnAPersonForARestatedDescendantHoldingWork(t *testing.T) {
	t.Parallel()
	const now = int64(100_000)
	repo := execRepository(t)
	s := tempStore(t, repo.Root())
	staleAfter := 2 * time.Hour
	s.clock = func() time.Time { return time.Unix(now, 0) }
	s.staleAfter = &staleAfter
	s.landed = func(context.Context, job.Job) string { return "" }
	old := now - int64((3 * time.Hour).Seconds())
	plant(t, s,
		job.Job{ID: "done", State: job.StateExited, Registered: old, Updated: old},
		job.Job{ID: "done/w", Parent: "done", State: job.StateRunning, Paths: job.Paths{Write: []string{"a.txt"}}, CheckoutRoot: repo.Root(), Updated: now - 60},
	)
	require.NoError(t, os.WriteFile(filepath.Join(repo.Root(), "a.txt"), []byte("half done\n"), 0o644))

	_, err := s.Prune(t.Context(), PruneOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only because an ancestor it ends does: done/w (running")
	assert.Contains(t, err.Error(), "`magus job prune done done/w`")

	_, err = s.Prune(t.Context(), PruneOptions{}, "done/w")
	require.ErrorContains(t, err, "prune would not end done/w", "without its ancestor named, the descendant has nothing to end with")

	_, err = s.Prune(t.Context(), PruneOptions{}, "done", "done/w")
	require.ErrorContains(t, err, "which an agent may not")
	requests := endRequests(t, repo.Root())
	require.Len(t, requests, 1)
	for id, r := range storedRows(t, s) {
		assert.True(t, r.State.Live(), id)
	}

	dispose(t, repo.Root(), requests[0].ID)
	got, err := s.Prune(t.Context(), PruneOptions{}, "done", "done/w")
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestExitWithoutAResultRecordsNoReturn(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	loc := declared(t, row)
	stored, err := Exit(t.Context(), boundStore(loc, row.ID), row.ID, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, job.StateNoReturn, stored.State)
	assert.Nil(t, stored.Attempt)
	tries, err := NewStore(loc).Attempts(t.Context())
	require.NoError(t, err)
	live := job.NewAttempts(tries).Live(row.ID)
	assert.Nil(t, live.Status.Result)
	assert.Equal(t, job.AttemptEndExit, live.Status.End, "the exit ends the try")
}

type exitCommittedSuite struct {
	suite.Suite
	repo *testkit.Repo
	row  job.Job
	loc  Location
}

func TestExitCommitted(t *testing.T) { suite.Run(t, new(exitCommittedSuite)) }

func (s *exitCommittedSuite) SetupTest() {
	s.repo = testkit.NewRepository(s.T(), map[string]string{"internal/ledger/a.go": "package ledger\n"})
	s.row = acceptRow()
	s.row.CheckoutRoot = s.repo.Root()
	s.loc = declared(s.T(), s.row)
	s.Require().NoError(os.WriteFile(filepath.Join(s.repo.Root(), "internal/ledger/a.go"), []byte("package ledger // edited\n"), 0o644))
}

func (s *exitCommittedSuite) exit() (job.Job, error) {
	result := passingResult()
	return Exit(s.T().Context(), boundStore(s.loc, s.row.ID), s.row.ID, &result, func(context.Context, string) (job.CapturedRun, error) {
		attempt := passingRun
		attempt.Ref, attempt.TimestampMs = result.Validation.OutputRef, 9_999_999_999_999
		return attempt, nil
	})
}

func (s *exitCommittedSuite) TestUncommittedEditIsRefusedNamingThePath() {
	_, err := s.exit()

	s.ErrorContains(err, "hold uncommitted work")
	s.ErrorContains(err, "internal/ledger/a.go")
	rows, listErr := NewStore(s.loc).List(s.T().Context())
	s.Require().NoError(listErr)
	s.Equal(job.StateRunning, rows[0].State)
}

func (s *exitCommittedSuite) TestCommittedEditIsAccepted() {
	s.repo.Commit("edit", map[string]string{"internal/ledger/a.go": "package ledger // edited\n"})

	exited, err := s.exit()

	s.Require().NoError(err)
	s.NotEqual(job.StateRunning, exited.State)
}

// A deleted branch must not take the work out of every count, so the row
// keeps the commit itself.
func (s *exitCommittedSuite) TestAnExitRecordsTheCheckoutsHead() {
	s.repo.Commit("edit", map[string]string{"internal/ledger/a.go": "package ledger // edited\n"})
	s.take()

	exited, err := s.exit()

	s.Require().NoError(err)
	s.Equal(s.repo.Head(), exited.Head)
	s.Equal(s.repo.Head(), storedRows(s.T(), NewStore(s.loc))[s.row.ID].Head)
}

func (s *exitCommittedSuite) TestAnAbandonmentRecordsTheCheckoutsHead() {
	s.take()

	ended, err := Exit(s.T().Context(), boundStore(s.loc, s.row.ID), s.row.ID, nil, nil)

	s.Require().NoError(err)
	s.Equal(job.StateNoReturn, ended.State)
	s.Equal(s.repo.Head(), ended.Head)
	s.Empty(ended.HeadUnread)
}

func (s *exitCommittedSuite) TestAnAbandonmentInAGoneCheckoutSaysWhyItHasNoHead() {
	s.take()
	s.Require().NoError(os.RemoveAll(s.repo.Root()))

	ended, err := Exit(s.T().Context(), boundStore(s.loc, s.row.ID), s.row.ID, nil, nil)

	s.Require().NoError(err)
	s.Empty(ended.Head)
	s.Contains(ended.HeadUnread, "does not read")
}

// take records a holder on the row, as `magus job exec` does.
func (s *exitCommittedSuite) take() {
	s.Require().NoError(NewStore(s.loc).Rewrite(s.T().Context(), func(f *job.Rows) error {
		i := slices.IndexFunc(f.Jobs, func(r job.Job) bool { return r.ID == s.row.ID })
		f.Jobs[i].Registered, f.Jobs[i].CheckoutRoot = 1, s.repo.Root()
		return nil
	}))
}

func TestExitAndWaitForDiscardedPrototype(t *testing.T) {
	t.Parallel()
	const file = "internal/ledger/a.go"
	const original = "package ledger\n"
	repo := testkit.NewRepository(t, map[string]string{file: original})
	row := acceptRow()
	row.Delivery = job.DeliveryFindings
	row.Checkpoint, row.CheckoutRoot = repo.Head(), repo.Root()
	loc := declared(t, row)
	result := passingResult()
	result.ChangedPaths = nil
	result.Findings = []string{"The prototype allocated twice per lookup."}
	resolve := func(context.Context, string) (job.CapturedRun, error) {
		run := passingRun
		run.Ref, run.TimestampMs = result.Validation.OutputRef, 9_999_999_999_999
		return run, nil
	}
	holder := boundStore(loc, row.ID)
	target := filepath.Join(repo.Root(), file)
	require.NoError(t, os.WriteFile(target, []byte("package ledger // prototype\n"), 0o644))
	_, err := Exit(t.Context(), holder, row.ID, &result, resolve)
	require.ErrorContains(t, err, "discard the prototype")
	require.ErrorContains(t, err, file)

	require.NoError(t, os.WriteFile(target, []byte(original), 0o644))
	exited, err := Exit(t.Context(), holder, row.ID, &result, resolve)
	require.NoError(t, err)
	require.Equal(t, job.StateExited, exited.State)
	waiter := NewStore(loc)
	status, err := Wait(t.Context(), waiter, row.ID, nil, nil, CheckpointObserver(repo.Root(), nil), nil)
	require.NoError(t, err)
	require.True(t, status.Verified, "%v", status.Violations)
	assert.Equal(t, job.StatePass, storedRows(t, waiter)[row.ID].State)
	assert.Equal(t, result.Findings, liveTry(t, waiter, row.ID).Status.Result.Findings)
	assert.Equal(t, row.Checkpoint, repo.Head())
}
