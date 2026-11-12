package job

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
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
	assert.Equal(t, sessions.AttentionRequest{
		Lease: "orchestrator", Source: heldSource, Outcome: string(types.OutcomeFailed), Severity: string(types.SeverityWarning),
	}, got)
}

// A session that ends takes the job it was editing with it, tells the root once, and a
// session resuming the work in the same checkout takes the row back with its next exec.
func TestEndSessionEndsTheTakenJobAndExecTakesItBack(t *testing.T) {
	repo := execRepository(t)
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
	repo := execRepository(t)
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

// An integration row outlives its holder's turn: the merges it holds sit in a standing
// checkout, so a session end leaves the row running and the binding live for the holder
// that resumes it.
func TestEndSessionLeavesAnIntegrationRowRunning(t *testing.T) {
	repo := execRepository(t)
	loc := tempLocation(t, repo.Root())
	s := NewStore(loc)
	seed(t, s, types.Job{ID: "integrate", Checkpoint: repo.Head(), State: types.StateDeclared, Integrates: []string{"feat-a"}})
	_, err := s.Exec(t.Context(), "integrate")
	require.NoError(t, err)
	holder := Caller{Host: "claude-code", Session: "s1", Agent: "a1"}
	require.NoError(t, s.Bind(holder, "integrate"))

	_, ok, err := s.EndSession(t.Context(), holder)
	require.NoError(t, err)
	assert.False(t, ok)
	binding := s.Binding(holder)
	assert.Equal(t, []any{"integrate", false}, []any{binding.Job, binding.Gone}, "the binding stays live, never a tombstone")
	rows, err := s.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, types.StateRunning, rows[0].State, rows[0].EndReason)
	assert.Empty(t, attentionRows(t, repo.Root()))
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
	s := tempStore(t, execRepository(t).Root())
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
	loc := tempLocation(t, execRepository(t).Root())
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

type HolderStoppedSuite struct {
	suite.Suite
	root   string
	loc    Location
	store  *Store
	worker Caller
}

func TestHolderStopped(t *testing.T) {
	suite.Run(t, new(HolderStoppedSuite))
}

func (s *HolderStoppedSuite) SetupTest() {
	repo := execRepository(s.T())
	s.root = repo.Root()
	s.loc = tempLocation(s.T(), s.root)
	s.store = NewStore(s.loc)
	seed(s.T(), s.store, types.Job{ID: "w", Checkpoint: repo.Head(), State: types.StateDeclared, WritePaths: []string{"a.go"}})
	_, err := s.store.Exec(s.T().Context(), "w")
	s.Require().NoError(err)
	s.worker = Caller{Host: "claude-code", Session: "s1", Agent: "a1"}
	s.Require().NoError(s.store.Bind(s.worker, "w"))
}

func (s *HolderStoppedSuite) row(id string) types.Job {
	rows, err := NewStore(s.loc).List()
	s.Require().NoError(err)
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id })
	s.Require().GreaterOrEqual(i, 0, id)
	return rows[i]
}

func (s *HolderStoppedSuite) at(unix int64) *Store {
	at := NewStore(s.loc)
	at.clock = func() time.Time { return time.Unix(unix, 0) }
	return at
}

func (s *HolderStoppedSuite) TestAStopStampsAndEndsNothing() {
	before := s.row("w")
	s.Require().NoError(s.at(before.Updated+60).StampHolderStopped(s.T().Context(), s.worker))

	after := s.row("w")
	s.Equal(before.Updated+60, after.HolderStopped)
	after.HolderStopped = 0
	s.Equal(before, after, "state, end reason, attempts and Updated all stay")
	s.False(s.store.Binding(s.worker).Gone, "no tombstone")
	s.Empty(attentionRows(s.T(), s.root), "no attention row")
	_, ended := s.store.SessionEnded("w")
	s.False(ended, "a stop is no session's end")
}

func (s *HolderStoppedSuite) TestTheHoldersNextCallClearsTheStamp() {
	before := s.row("w")
	s.Require().NoError(s.at(before.Updated+60).StampHolderStopped(s.T().Context(), s.worker))
	s.Require().NoError(s.at(before.Updated+90).ClearHolderStopped(s.T().Context(), s.worker))

	want := before.Clone()
	want.Updated = before.Updated + 90
	s.Equal(want, s.row("w"), "a dead-holder claim reads the new Updated as the holder being back")
}

func (s *HolderStoppedSuite) TestClearingAnUnstampedRowWritesNothing() {
	before := s.row("w")
	s.Require().NoError(s.at(before.Updated+90).ClearHolderStopped(s.T().Context(), s.worker))
	s.Equal(before, s.row("w"))
}

func (s *HolderStoppedSuite) TestAnotherCallersStopStampsNothing() {
	before := s.row("w")
	s.Require().NoError(s.store.StampHolderStopped(s.T().Context(), Caller{Host: "claude-code", Session: "s1", Agent: "a2"}))
	s.Require().NoError(s.store.StampHolderStopped(s.T().Context(), Caller{}))
	s.Equal(before, s.row("w"))
}

func (s *HolderStoppedSuite) TestARowNotBeingWorkedIsNotStamped() {
	seed(s.T(), s.store, types.Job{ID: "untaken", State: types.StateDeclared})
	seed(s.T(), s.store, types.Job{ID: "returned", State: types.StateExited})
	for _, id := range []string{"untaken", "returned"} {
		c := Caller{Host: "codex", Session: "s2", Agent: id}
		s.Require().NoError(s.store.Bind(c, id))
		s.Require().NoError(s.store.StampHolderStopped(s.T().Context(), c))
		s.Zero(s.row(id).HolderStopped, id)
	}
}

func (s *HolderStoppedSuite) TestASessionEndStillEndsAStampedRow() {
	s.Require().NoError(s.store.StampHolderStopped(s.T().Context(), s.worker))

	ended, ok, err := s.store.EndSession(s.T().Context(), s.worker)
	s.Require().NoError(err)
	s.True(ok)
	s.Equal(types.StateNoReturn, ended.State)
	s.True(ended.EndedWithSession(), ended.EndReason)
}

func (s *HolderStoppedSuite) TestOneCallEndsAStampedRow() {
	s.Require().NoError(s.store.StampHolderStopped(s.T().Context(), s.worker))

	ended, already, refused := Abandon(s.T().Context(), NewStore(s.loc), []string{"w"})
	s.Empty(refused)
	s.Empty(already)
	s.Require().Len(ended, 1)
	s.Equal(types.StateNoReturn, ended[0].State)
	s.False(Editing(s.row("w")), "its paths are free")
}

func (s *HolderStoppedSuite) TestTheDeadlineEndsAStampedRow() {
	s.Require().NoError(s.store.StampHolderStopped(s.T().Context(), s.worker))
	s.Require().NoError(s.store.update(s.T().Context(), func(f *jobsFile) error {
		f.Jobs[0].Deadline = f.Jobs[0].Updated + 60
		return nil
	}))

	rows, err := s.at(s.row("w").Deadline + 60).List()
	s.Require().NoError(err)
	s.Require().Len(rows, 1)
	s.Equal(types.StateNoReturn, rows[0].State, rows[0].EndReason)
	s.Contains(rows[0].EndReason, overdueReasonPrefix)
}

func (s *HolderStoppedSuite) TestPruneAllEndsAStampedRowPastStaleAfter() {
	stale, _ := sweepStore(s.T(), 100_000, 2*time.Hour)
	stale.landed = func(context.Context, types.Job) string { return "" }
	plant(s.T(), stale, types.Job{ID: "w", State: types.StateRunning, Registered: 1000, Updated: 1000, HolderStopped: 99_000})

	got, err := stale.Prune(s.T().Context(), PruneOptions{All: true})
	s.Require().NoError(err)
	s.Require().Len(got, 1)
	s.Contains(got[0].Reason, "taken, then untouched", "a stop moved no clock")
}

// #587 ended a row at a stop so a dead worker's paths would not strand.
func (s *HolderStoppedSuite) TestALaterJobTakesAStaleStampedHoldersPath() {
	loc, row, _, facts := deadHolderStore(s.T())
	holder := Caller{Host: "claude-code", Session: "s9", Agent: "dead"}
	store := NewStore(loc)
	s.Require().NoError(store.Bind(holder, "gone"))
	s.Require().NoError(store.StampHolderStopped(s.T().Context(), holder))
	rows, err := NewStore(loc).List()
	s.Require().NoError(err)
	gone := rows[slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == "gone" })]
	s.NotZero(gone.HolderStopped)
	facts = facts.WithDeadHolders([]DeadHolder{{Row: gone, Since: gone.HolderStopped, Why: "its holder stopped"}})

	_, err = boundStore(loc, row.ID).Claim(s.T().Context(), row.ID, []types.JobClaimRequest{{Path: "internal/free/free.go", Because: "dead:gone"}}, facts, config.Defaults().Jobs, false)
	s.Require().NoError(err)
	rows, err = NewStore(loc).List()
	s.Require().NoError(err)
	s.Empty(rows[slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == "gone" })].WritePaths)
}

func (s *HolderStoppedSuite) TestAHolderBackBeforeTheTakeKeepsItsPath() {
	loc, row, _, facts := deadHolderStore(s.T())
	holder := Caller{Host: "claude-code", Session: "s9", Agent: "dead"}
	store := NewStore(loc)
	s.Require().NoError(store.Bind(holder, "gone"))
	s.Require().NoError(store.StampHolderStopped(s.T().Context(), holder))
	rows, err := NewStore(loc).List()
	s.Require().NoError(err)
	gone := rows[slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == "gone" })]
	facts = facts.WithDeadHolders([]DeadHolder{{Row: gone, Since: gone.HolderStopped, Why: "its holder stopped"}})

	back := NewStore(loc)
	back.clock = func() time.Time { return time.Unix(gone.Updated+60, 0) }
	s.Require().NoError(back.ClearHolderStopped(s.T().Context(), holder))

	_, err = boundStore(loc, row.ID).Claim(s.T().Context(), row.ID, []types.JobClaimRequest{{Path: "internal/free/free.go", Because: "dead:gone"}}, facts, config.Defaults().Jobs, false)
	s.Require().ErrorContains(err, "its holder may be back")
}

func (s *HolderStoppedSuite) TestALaterForkOverlapsAStampedHolderUntilItEnds() {
	s.Require().NoError(s.store.StampHolderStopped(s.T().Context(), s.worker))
	later := types.Job{ID: "later", State: types.StateDeclared, WritePaths: []string{"a.go"}}

	s.Equal(types.WriteProofOverlapping, s.store.WriteProof([]types.Job{s.row("w"), later}, later), "recorded, never refused")

	_, _, refused := Abandon(s.T().Context(), NewStore(s.loc), []string{"w"})
	s.Empty(refused)
	s.Equal(types.WriteProofAlone, s.store.WriteProof([]types.Job{s.row("w"), later}, later))
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

	taker := types.Job{ID: "taker", Agent: agent.Instructed("c"), WritePaths: []string{"cmd/magus/ledger.go"}, State: types.StateRunning}
	loc = exited(t, taker)
	status, err = Wait(t.Context(), NewStore(loc), result.Job, &empty, resolve, nil, nil)
	require.NoError(t, err)
	require.False(t, status.Verified)
	assert.Equal(t, types.StateExited, state(t, loc))
	assert.Contains(t, strings.Join(status.Notes, "\n"), "cmd/magus/ledger.go is held by live job taker")
}
