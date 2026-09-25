package job

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// observeClaim is an observer whose diff shows exactly what rep claims.
func observeClaim(rep types.JobResult) Observer {
	return func(context.Context, types.Job) (Observed, error) { return claimed(rep), nil }
}

// TestExitFilesEvidenceForAWaitInAnotherCheckout keeps the lifecycle at the Store
// boundary. The resolver is the worker checkout's output cache; the waiter deliberately
// has no resolver, proving Exit copied the portable attempt onto the repository-wide row.
func TestExitFilesEvidenceForAWaitInAnotherCheckout(t *testing.T) {
	t.Parallel()

	loc := declared(t, acceptRow())
	result := passingResult()
	attempt := passingRun
	attempt.Ref = result.Validation.OutputRef
	attempt.TimestampMs = 9_999_999_999_999
	resolver := func(_ context.Context, ref string) (types.JobAttempt, error) {
		assert.Equal(t, result.Validation.OutputRef, ref)
		return attempt, nil
	}

	exited, err := Exit(t.Context(), boundStore(loc, result.Job), result.Job, &result, resolver)
	require.NoError(t, err)
	assert.Equal(t, types.StateExited, exited.State)
	require.NotNil(t, exited.Result)
	require.NotNil(t, exited.Attempt)
	assert.Equal(t, attempt, *exited.Attempt)

	status, err := Wait(t.Context(), NewStore(loc), result.Job, nil, nil, observeClaim(result))
	require.NoError(t, err)
	// The primary check is LISTED, like any other gate: it is one, and reporting it only
	// when other gates existed is what let `describe job --gates` say a check-only job had
	// none.
	assert.Equal(t, types.JobStatus{
		Job:      result.Job,
		Verified: true,
		Risks:    []string{},
		Command:  result.Validation.Command,
		Gates:    []types.GateStatus{{ID: types.PrimaryCompletionGateID, OutputRef: result.Validation.OutputRef, Verified: true}},
	}, status)

	rows, err := NewStore(loc).List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, types.StatePass, rows[0].State)
}

func TestExitFilesEveryCompletionGateForAWaitInAnotherCheckout(t *testing.T) {
	t.Parallel()

	row := completionGateRow()
	loc := declared(t, row)
	result := completionGateResult()
	attempts := map[string]types.JobAttempt{}
	for _, snapshot := range completionGateAttempts() {
		snapshot.Attempt.TimestampMs = 9_999_999_999_999
		attempts[snapshot.Attempt.Ref] = snapshot.Attempt
	}
	resolver := func(_ context.Context, ref string) (types.JobAttempt, error) { return attempts[ref], nil }

	exited, err := Exit(t.Context(), boundStore(loc, row.ID), row.ID, &result, resolver)
	require.NoError(t, err)
	require.Len(t, exited.GateAttempts, 2)
	assert.Nil(t, exited.Attempt, "a gate-only job has no synthetic primary attempt")

	status, err := Wait(t.Context(), NewStore(loc), row.ID, nil, nil, observeClaim(result))
	require.NoError(t, err)
	assert.True(t, status.Verified, status.Violations)
	require.Len(t, status.Gates, 2)
}

func TestExitWithoutAResultRecordsNoReturn(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	loc := declared(t, row)
	stored, err := Exit(t.Context(), boundStore(loc, row.ID), row.ID, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, types.StateNoReturn, stored.State)
	assert.Nil(t, stored.Result)
	assert.Nil(t, stored.Attempt)
}

func TestWaitKeepsARejectedJobOpen(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	loc := declared(t, row)
	result := passingResult()
	result.ChangedPaths = nil // a writing job cannot be accepted on an empty claimed change set

	status, err := Wait(t.Context(), NewStore(loc), row.ID, &result, func(_ context.Context, _ string) (types.JobAttempt, error) {
		return passingRun, nil
	}, nil)
	require.NoError(t, err)
	assert.False(t, status.Verified)
	assert.Contains(t, status.Violations, "job harness/ledger-accept is not read-only and the result claims no changed paths at all")

	rows, err := NewStore(loc).List()
	require.NoError(t, err)
	assert.Equal(t, types.StateRunning, rows[0].State, "a rejected result is evidence to repair, not a terminal verdict")
}

func TestWaitRefusesTheBoundHolder(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	loc := declared(t, row)
	_, err := Wait(t.Context(), boundStore(loc, row.ID), row.ID, nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not verify its own work")
}

// plant writes rows exactly as given, timestamps included, which no write door allows.
func plant(t *testing.T, s *Store, rows ...types.Job) {
	t.Helper()
	path, err := s.Path()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, s.write(jobsFile{Jobs: rows}, nil))
}

// sweepStore is a temp store whose sweep runs at now with a fixed jobs.stale_after, its
// stderr lines captured.
func sweepStore(t *testing.T, now int64, staleAfter time.Duration) (*Store, *strings.Builder) {
	t.Helper()
	s := tmpStore(t, t.TempDir())
	var notices strings.Builder
	s.clock = func() time.Time { return time.Unix(now, 0) }
	s.staleAfter = &staleAfter
	s.notices = &notices
	return s, &notices
}

func states(t *testing.T, s *Store) map[string]types.Job {
	t.Helper()
	rows, err := s.List()
	require.NoError(t, err)
	out := map[string]types.Job{}
	for _, r := range rows {
		out[r.ID] = r
	}
	return out
}

func TestListEndsProvablyDeadJobs(t *testing.T) {
	const now = int64(100_000)
	fresh := now - 60
	old := now - int64((3 * time.Hour).Seconds())
	gone := filepath.Join(t.TempDir(), "removed-worktree")
	here := t.TempDir()
	notDir := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(notDir, nil, 0o644))

	tests := []struct {
		name       string
		staleAfter time.Duration
		rows       []types.Job
		ended      map[string]string
	}{
		{
			name: "a child of an ended parent ends with it",
			rows: []types.Job{
				{ID: "root", State: types.StatePass, Updated: fresh},
				{ID: "root/a", Parent: "root", State: types.StateRunning, Registered: fresh, Updated: fresh},
			},
			ended: map[string]string{"root/a": "parent root ended as pass, and its tree ends with it"},
		},
		{
			name: "the whole tree under an ended root ends, grandchildren included",
			rows: []types.Job{
				{ID: "root", State: types.StateNoReturn, Updated: fresh},
				{ID: "root/a", Parent: "root", State: types.StateRunning, Updated: fresh},
				{ID: "root/a/b", Parent: "root/a", State: types.StateExited, Updated: fresh},
			},
			ended: map[string]string{
				"root/a":   "parent root ended as no_return, and its tree ends with it",
				"root/a/b": "ancestor root ended as no_return, and its tree ends with it",
			},
		},
		{
			name: "a job taken in a checkout that no longer exists ends, and its children with it",
			rows: []types.Job{
				{ID: "w", State: types.StateDeclared, Registered: fresh, CheckoutRoot: gone, Updated: fresh},
				{ID: "w/c", Parent: "w", State: types.StateDeclared, Updated: fresh},
			},
			ended: map[string]string{
				"w":   "taken in " + gone + ", which no longer exists",
				"w/c": "parent w ended as no_return, and its tree ends with it",
			},
		},
		{
			name: "a checkout that is still there, one that cannot be read, and an exited holder all stay live",
			rows: []types.Job{
				{ID: "present", State: types.StateRunning, Registered: fresh, CheckoutRoot: here, Updated: old},
				{ID: "undecidable", State: types.StateRunning, Registered: fresh, CheckoutRoot: filepath.Join(notDir, "sub"), Updated: old},
				{ID: "returned", State: types.StateExited, Registered: fresh, CheckoutRoot: gone, Updated: old},
			},
		},
		{
			name:       "a declared job nobody took ends once it is older than stale_after",
			staleAfter: 2 * time.Hour,
			rows: []types.Job{
				{ID: "untaken", State: types.StateDeclared, Updated: old},
				{ID: "recent", State: types.StateDeclared, Updated: fresh},
				{ID: "taken", State: types.StateDeclared, Registered: old, Updated: old},
				{ID: "running", State: types.StateRunning, Updated: old},
				{ID: "maintenance", Holder: types.HolderServer, State: types.StateDeclared, Updated: old},
			},
			ended: map[string]string{"untaken": "declared and never taken, untouched for 3h0m0s (jobs.stale_after is 2h0m0s)"},
		},
		{
			name: "a zero stale_after ends nothing for age",
			rows: []types.Job{{ID: "untaken", State: types.StateDeclared, Updated: 1}},
		},
		{
			name:       "an untaken root outlives its working children and ends after the last untaken one",
			staleAfter: 2 * time.Hour,
			rows: []types.Job{
				{ID: "busy", State: types.StateDeclared, Updated: old},
				{ID: "busy/w", Parent: "busy", State: types.StateRunning, Registered: fresh, Updated: fresh},
				{ID: "idle", State: types.StateDeclared, Updated: old},
				{ID: "idle/w", Parent: "idle", State: types.StateDeclared, Updated: old},
			},
			ended: map[string]string{
				"idle/w": "declared and never taken, untouched for 3h0m0s (jobs.stale_after is 2h0m0s)",
				"idle":   "declared and never taken, untouched for 3h0m0s (jobs.stale_after is 2h0m0s)",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, notices := sweepStore(t, now, tt.staleAfter)
			plant(t, s, tt.rows...)

			got := states(t, s)
			var lines []string
			for _, want := range tt.rows {
				row := got[want.ID]
				reason, ended := tt.ended[want.ID]
				if !ended {
					assert.Equal(t, want.State, row.State, "%s stays as it was", want.ID)
					assert.Empty(t, row.EndReason, want.ID)
					assert.Equal(t, want.Updated, row.Updated, want.ID)
					continue
				}
				assert.Equal(t, types.StateNoReturn, row.State, want.ID)
				assert.Equal(t, reason, row.EndReason, want.ID)
				assert.Equal(t, now, row.Updated, "ending a row is its own transition")
				lines = append(lines, "ended "+want.ID+": "+reason)
			}
			gotLines := strings.Split(strings.TrimSuffix(notices.String(), "\n"), "\n")
			if len(lines) == 0 {
				gotLines = nil
			}
			assert.ElementsMatch(t, lines, gotLines, "one stderr line per ended job")
		})
	}
}

// A sweep with nothing left to end writes nothing and prints nothing, so every read after
// the first is the lock-free read it always was.
func TestListSweepIsIdempotent(t *testing.T) {
	s, notices := sweepStore(t, 100_000, time.Hour)
	plant(t, s,
		types.Job{ID: "root", State: types.StateFail, Updated: 99_000},
		types.Job{ID: "root/a", Parent: "root", State: types.StateDeclared, Updated: 99_000},
	)
	first := states(t, s)
	require.Equal(t, types.StateNoReturn, first["root/a"].State)
	path, err := s.Path()
	require.NoError(t, err)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	notices.Reset()

	assert.Equal(t, first, states(t, s))
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
	assert.Empty(t, notices.String())
}

// Exec records where the job was taken, so removing that worktree ends the job.
func TestExecRecordsTheCheckoutAndItsRemovalEndsTheJob(t *testing.T) {
	checkout := t.TempDir()
	s := tmpStore(t, checkout)
	var notices strings.Builder
	s.notices = &notices
	seed(t, s, types.Job{ID: "w", State: types.StateDeclared, Checkpoint: "abc"})

	taken, err := s.Exec(t.Context(), "w", "abc")
	require.NoError(t, err)
	abs, err := filepath.Abs(checkout)
	require.NoError(t, err)
	assert.Equal(t, abs, taken.CheckoutRoot)

	seed(t, s, types.Job{ID: "w", State: types.StateDeclared, Checkpoint: "abc", Criteria: "rewritten"})
	require.NoError(t, os.RemoveAll(checkout))

	rows := states(t, s)
	assert.Equal(t, abs, rows["w"].CheckoutRoot, "a declaration carries the store's record forward")
	assert.Equal(t, types.StateNoReturn, rows["w"].State)
	assert.Equal(t, "ended w: taken in "+abs+", which no longer exists\n", notices.String())
}

// The guard appends to unattributed every time somebody else writes inside a job's paths.
// That is not the job doing anything, and moving updated for it made dead rows look alive.
func TestUnattributedWritesLeaveUpdatedAlone(t *testing.T) {
	root := t.TempDir()
	s := tmpStore(t, root)
	plant(t, s, types.Job{ID: "w", State: types.StateRunning, WritePaths: []string{"a.go"}, Created: 50, Updated: 50})

	require.NoError(t, s.RecordUnattributedWrite(t.Context(), "w", "a.go"))

	row := states(t, s)["w"]
	require.Len(t, row.Unattributed, 1)
	assert.Equal(t, int64(50), row.Updated)
}

// The sweep reads jobs.stale_after from the workspace's own magus.yaml, where a written
// zero means never and an absent key is the 2h default.
func TestSweepReadsStaleAfterFromTheWorkspace(t *testing.T) {
	old := time.Now().Add(-3 * time.Hour).Unix()
	for _, tc := range []struct {
		name, yaml string
		ended      bool
	}{
		{name: "the default ends a row three hours old", ended: true},
		{name: "a written zero is never", yaml: "jobs:\n  stale_after: 0s\n"},
		{name: "a longer window keeps it", yaml: "jobs:\n  stale_after: 4h\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.yaml != "" {
				require.NoError(t, os.WriteFile(filepath.Join(root, config.Filename), []byte(tc.yaml), 0o644))
			}
			s := tmpStore(t, root)
			s.notices = io.Discard
			plant(t, s, types.Job{ID: "untaken", State: types.StateDeclared, Updated: old})

			want := types.StateDeclared
			if tc.ended {
				want = types.StateNoReturn
			}
			assert.Equal(t, want, states(t, s)["untaken"].State)
		})
	}
}

// A magus.yaml the sweep cannot read is an error on the read, never a silent default.
func TestSweepRefusesAnUnreadableWorkspaceConfig(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, config.Filename), []byte("jobs:\n  stale_aftr: 1h\n"), 0o644))
	s := tmpStore(t, root)
	plant(t, s, types.Job{ID: "untaken", State: types.StateDeclared, Updated: 1})

	_, err := s.List()
	assert.ErrorContains(t, err, "jobs.stale_after")
}
