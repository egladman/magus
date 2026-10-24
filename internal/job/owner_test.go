package job

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func attentionRows(t *testing.T, root string) []sessions.AttentionRequest {
	t.Helper()
	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	fold, err := sessions.ReadAll(dir)
	require.NoError(t, err)
	return sessions.AttentionQueue(fold)
}

// A knock on a holder's tree root is one attention row on that root, however many callers
// meet the same held path.
func TestNoteHeldFilesOneRowForTheTreeRoot(t *testing.T) {
	root := t.TempDir()
	NoteHeld(t.Context(), root, "orchestrator", "worker needs coverage/buzz/x.lcov, held by job mover")
	NoteHeld(t.Context(), root, "orchestrator", "worker needs coverage/buzz/x.lcov, held by job mover")

	queue := attentionRows(t, root)
	require.Len(t, queue, 1)
	got := queue[0]
	assert.Contains(t, got.Message, "held by job mover")
	// The id, the invocation and the clock are the raise's own; the message is checked above.
	got.ID, got.Invocation, got.OpenedMs, got.Message = "", "", 0, ""
	assert.Equal(t, sessions.AttentionRequest{Outcome: "failed", Severity: "warning", Lease: "orchestrator", Source: heldSource}, got)
}

// A session that ends takes the job it was editing with it, tells the root once, and a
// session resuming the work in the same checkout takes the row back with its next exec.
func TestEndSessionEndsTheTakenJobAndExecTakesItBack(t *testing.T) {
	repo := execRepo(t)
	root := repo.Root()
	loc := tempLocation(t, root)
	s := NewStore(loc)
	seed(t, s, types.Job{ID: "w", Checkpoint: repo.Head(), State: types.StateDeclared})
	_, err := s.Exec(t.Context(), "w")
	require.NoError(t, err)
	worker := Caller{Host: "claude-code", Session: "s1", Agent: "a1"}
	require.NoError(t, s.Bind(worker, "w"))

	ended, ok, err := s.EndSession(t.Context(), worker)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, types.StateNoReturn, ended.State)
	assert.True(t, ended.EndedWithSession(), ended.EndReason)
	assert.Contains(t, ended.EndReason, "magus job exec w")
	assert.True(t, s.Binding(worker).Gone, "the record is a tombstone: nothing is graded as that caller until a session binds again")

	queue := attentionRows(t, root)
	require.Len(t, queue, 1, "the root hears of it once")
	assert.Equal(t, "w", queue[0].Lease)
	assert.Contains(t, queue[0].Message, types.EndReasonSessionEnded)

	_, again, err := s.EndSession(t.Context(), worker)
	require.NoError(t, err)
	assert.False(t, again, "a tombstone ends nothing more")
	assert.Len(t, attentionRows(t, root), 1)

	resumed, err := boundStore(loc, "w").Exec(t.Context(), "w")
	require.NoError(t, err)
	assert.Equal(t, types.StateRunning, resumed.State)
	assert.Empty(t, resumed.EndReason)
}

// A read-only row ends with its session as a writing one does: it holds no paths, and
// left running it would sit in the stale list until somebody pruned it.
func TestEndSessionEndsATakenReadOnlyJob(t *testing.T) {
	repo := execRepo(t)
	loc := tempLocation(t, repo.Root())
	s := NewStore(loc)
	seed(t, s, types.Job{ID: "scout", Checkpoint: repo.Head(), State: types.StateDeclared, ReadOnly: true})
	_, err := s.Exec(t.Context(), "scout")
	require.NoError(t, err)
	worker := Caller{Host: "claude-code", Session: "s1", Agent: "a1"}
	require.NoError(t, s.Bind(worker, "scout"))

	ended, ok, err := s.EndSession(t.Context(), worker)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, types.StateNoReturn, ended.State)
	assert.True(t, ended.EndedWithSession(), ended.EndReason)
}

func TestEndSessionLeavesWorkItDidNotTake(t *testing.T) {
	root := t.TempDir()
	s := tempStore(t, root)
	seed(t, s, types.Job{ID: "untaken", State: types.StateDeclared})
	seed(t, s, types.Job{ID: "returned", State: types.StateExited})
	callers := map[string]Caller{
		"untaken":  {Host: "codex", Session: "s1", Agent: "a1"},
		"returned": {Host: "codex", Session: "s1", Agent: "a2"},
	}
	for id, c := range callers {
		require.NoError(t, s.Bind(c, id))
		_, ok, err := s.EndSession(t.Context(), c)
		require.NoError(t, err)
		assert.False(t, ok, id)
	}
	_, ok, err := s.EndSession(t.Context(), Caller{})
	require.NoError(t, err)
	assert.False(t, ok, "an identity-less caller shares the checkout's record, so its end ends nothing")

	rows, err := s.List()
	require.NoError(t, err)
	for _, r := range rows {
		assert.NotEqual(t, types.StateNoReturn, r.State, r.ID)
	}
	assert.Empty(t, attentionRows(t, root))
}

func TestExecDoesNotTakeBackARowEndedForAnotherReason(t *testing.T) {
	s := tempStore(t, execRepo(t).Root())
	seed(t, s, types.Job{ID: "w", Checkpoint: baseA, State: types.StateNoReturn})
	require.NoError(t, s.update(t.Context(), func(f *jobsFile) error {
		f.Jobs[0].EndReason = "overdue: its deadline passed 1m0s ago"
		return nil
	}))
	_, err := s.Exec(t.Context(), "w")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already ended")
}

func TestExecTakesBackASessionEndedRowOnlyInItsOwnCheckout(t *testing.T) {
	loc := tempLocation(t, execRepo(t).Root())
	elsewhere := t.TempDir()
	seed(t, NewStore(loc), types.Job{ID: "w", Checkpoint: baseA, State: types.StateNoReturn})
	require.NoError(t, NewStore(loc).update(t.Context(), func(f *jobsFile) error {
		f.Jobs[0].EndReason = types.EndReasonSessionEnded + " (claude-code)"
		f.Jobs[0].CheckoutRoot = elsewhere
		return nil
	}))
	_, err := NewStore(loc).Exec(t.Context(), "w")
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "already ended"), err.Error())
}

func TestTreeRootIsTheTopmostAncestor(t *testing.T) {
	s := tempStore(t, t.TempDir())
	plant(t, s,
		types.Job{ID: "plan", State: types.StateRunning},
		types.Job{ID: "plan/a", Parent: "plan", State: types.StateRunning},
		types.Job{ID: "plan/a/b", Parent: "plan/a", State: types.StateRunning},
	)
	for id, want := range map[string]string{"plan/a/b": "plan", "plan": "plan", "undeclared": "undeclared"} {
		got, err := s.TreeRoot(id)
		require.NoError(t, err)
		assert.Equal(t, want, got, id)
	}
}

func TestNoteEndedFilesOneRowPerEnding(t *testing.T) {
	root := t.TempDir()
	NoteEnded(t.Context(), root, "w", "magus stopped `rg x` at its 5m deadline")
	NoteEnded(t.Context(), root, "w", "magus stopped `rg x` at its 5m deadline")
	NoteEnded(t.Context(), "", "w", "no repository, no queue")
	queue := attentionRows(t, root)
	require.Len(t, queue, 1, "two processes noting one ending raise it once")
	assert.Equal(t, string(types.OutcomeFailed), queue[0].Outcome)
}

// A rejecting wait sends an exited row back to rework, holding its write paths again under
// the rule a claim meets; a path another job took meanwhile refuses the re-claim, naming
// that job, and the row stays exited holding nothing.
func TestWaitReclaimsTheWritePathsForRework(t *testing.T) {
	t.Parallel()

	result := passingResult()
	attempt := passingRun
	attempt.Ref, attempt.TimestampMs = result.Validation.OutputRef, 9_999_999_999_999
	resolve := func(context.Context, string) (types.JobAttempt, error) { return attempt, nil }
	empty := result
	empty.ChangedPaths = nil
	exited := func(t *testing.T, others ...types.Job) Location {
		t.Helper()
		loc := declared(t, append([]types.Job{acceptRow()}, others...)...)
		_, err := Exit(t.Context(), boundStore(loc, result.Job), result.Job, &result, resolve)
		require.NoError(t, err)
		return loc
	}
	state := func(t *testing.T, loc Location) types.JobState {
		t.Helper()
		rows, err := NewStore(loc).List()
		require.NoError(t, err)
		return rows[slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == result.Job })].State
	}

	loc := exited(t)
	status, err := Wait(t.Context(), NewStore(loc), result.Job, &empty, resolve, nil, nil)
	require.NoError(t, err)
	require.False(t, status.Verified)
	assert.Equal(t, types.StateRunning, state(t, loc))
	assert.Contains(t, strings.Join(status.Notes, "\n"), "holds its write paths again")

	taker := types.Job{ID: "taker", Criteria: "c", WritePaths: []string{"cmd/magus/ledger.go"}, State: types.StateRunning}
	loc = exited(t, taker)
	status, err = Wait(t.Context(), NewStore(loc), result.Job, &empty, resolve, nil, nil)
	require.NoError(t, err)
	require.False(t, status.Verified)
	assert.Equal(t, types.StateExited, state(t, loc))
	assert.Contains(t, strings.Join(status.Notes, "\n"), "cmd/magus/ledger.go is held by live job taker")
}
