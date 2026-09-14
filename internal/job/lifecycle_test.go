package job

import (
	"context"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	status, err := Wait(t.Context(), NewStore(loc), result.Job, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, types.JobStatus{Job: result.Job, Verified: true, Risks: []string{}, Command: result.Validation.Command}, status)

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

	status, err := Wait(t.Context(), NewStore(loc), row.ID, nil, nil)
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
	})
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
	_, err := Wait(t.Context(), boundStore(loc, row.ID), row.ID, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not verify its own work")
}
