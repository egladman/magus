package job

import (
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

// A session that ends takes the job it was editing with it, tells the root once, and a
// session resuming the work in the same checkout takes the row back with its next exec.
func TestEndSessionEndsTheTakenJobAndExecTakesItBack(t *testing.T) {
	repo := execRepo(t)
	root := repo.Root()
	loc := tmpLoc(t, root)
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

func TestEndSessionLeavesWorkItDidNotTake(t *testing.T) {
	root := t.TempDir()
	s := tmpStore(t, root)
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
	s := tmpStore(t, execRepo(t).Root())
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
	loc := tmpLoc(t, execRepo(t).Root())
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
	s := tmpStore(t, t.TempDir())
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
