package job

import (
	"errors"
	"testing"

	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyAttempt(t *testing.T) {
	t.Parallel()

	unmet := func(ids ...string) []types.GateStatus {
		out := make([]types.GateStatus, len(ids))
		for i, id := range ids {
			out[i] = types.GateStatus{ID: id}
		}
		return out
	}
	for _, tc := range []struct {
		name    string
		p       types.PriorAttempt
		earlier []types.PriorAttempt
		want    types.RetryVerdict
	}{
		{
			name: "a boundary denial needs a widening",
			p:    types.PriorAttempt{N: 1, Denials: []types.JobDenial{{Rule: "raw-tool"}, {Rule: "lease-write", Reason: "a.go is lease b's\nmore"}}},
			want: types.RetryVerdict{Change: types.RetryAfterWidening, Reason: "the guard denied its holder under lease-write: a.go is lease b's"},
		},
		{
			name: "a changed path outside the write paths needs a widening",
			p:    types.PriorAttempt{N: 1, Violations: []string{`changed path "b.go" is outside the job's write paths (a.go)`}},
			want: types.RetryVerdict{Change: types.RetryAfterWidening, Reason: `changed path "b.go" is outside the job's write paths (a.go)`},
		},
		{
			name:    "a goal unmet twice running needs a person",
			p:       types.PriorAttempt{N: 2, Unmet: unmet("lint", "check")},
			earlier: []types.PriorAttempt{{N: 1, Unmet: unmet("check")}},
			want:    types.RetryVerdict{Change: types.RetryAfterPerson, Reason: "goal check was unmet in attempts 1 and 2"},
		},
		{
			name:    "a goal unmet only in an older attempt is retryable",
			p:       types.PriorAttempt{N: 3, Unmet: unmet("check")},
			earlier: []types.PriorAttempt{{N: 1, Unmet: unmet("check")}, {N: 2, Unmet: unmet("lint")}},
			want:    types.RetryVerdict{Retryable: true},
		},
		{
			name: "an end the sweep wrote is retryable as is",
			p:    types.PriorAttempt{N: 1, State: types.StateNoReturn, EndReason: "its checkout is gone"},
			want: types.RetryVerdict{Retryable: true},
		},
		{
			name: "a first unmet goal is retryable",
			p:    types.PriorAttempt{N: 1, Unmet: unmet("check"), Denials: []types.JobDenial{{Rule: "raw-tool"}}},
			want: types.RetryVerdict{Retryable: true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, classifyAttempt(tc.p, tc.earlier))
		})
	}
}

// internal/job cannot import the guard, so its list of boundary rules is held here to the
// rules the guard catalogs: a renamed rule would otherwise classify nothing.
func TestWideningRulesAreRulesTheGuardCatalogs(t *testing.T) {
	t.Parallel()

	known := builtin.Defaults()
	for _, rule := range wideningRules {
		assert.Contains(t, known, rule)
	}
}

// The violation classifyAttempt reads as a widening is the one VerifyGates words.
func TestVerifyGatesWordsAnOutsidePathAsClassifyAttemptReadsIt(t *testing.T) {
	t.Parallel()

	status := VerifyGates(types.Job{ID: "a", WritePaths: []string{"a.go"}}, types.JobResult{ChangedPaths: []string{"b.go"}},
		types.JobAttempt{}, nil, nil, Observed{})
	got := classifyAttempt(types.PriorAttempt{N: 1, Violations: status.Violations}, nil)
	assert.Equal(t, types.RetryAfterWidening, got.Change)
}

func TestFoldAttemptKeepsWhatTheNextHolderNeeds(t *testing.T) {
	t.Parallel()

	prev := types.Job{ID: "a", State: types.StateFail, Generation: 2, Attempts: 1, Checkpoint: "abc", ReportedBase: "abd"}
	prev.Updated, prev.ReworkRounds, prev.Widenings = 40, 2, 1
	prev.ModelTier = &types.JobModelTier{Name: "economy"}
	prev.Result = &types.JobResult{UnresolvedRisks: []string{"one", "two"}}
	prev.Attempt = &types.JobAttempt{Ref: "out1", Failed: true}
	prev.GateAttempts = []types.JobGateAttempt{
		{GateID: "lint", Attempt: types.JobAttempt{Ref: "out2"}},
		{GateID: "unit", Attempt: types.JobAttempt{Ref: "out3", Failed: true}},
	}
	prev.Rejection = &types.JobRejection{Unmet: []types.GateStatus{{ID: "check", Violations: []string{"failed"}}}, Violations: []string{"v"}}
	denials := make([]types.JobDenial, 10)
	for i := range denials {
		denials[i] = types.JobDenial{Rule: "raw-tool", Ts: int64(i)}
	}

	got := foldAttempt(prev, denials, true, 99)

	want := types.PriorAttempt{N: 1, State: types.StateFail, ObservedGeneration: 2, ModelTier: "economy", Checkpoint: "abc", ReportedBase: "abd"}
	want.Ended, want.ReworkRounds, want.Widenings = 40, 2, 1
	want.Unmet = []types.GateStatus{{ID: "check", Violations: []string{"failed"}}}
	want.Violations = []string{"v"}
	want.UnresolvedRisks = []string{"one", "two"}
	want.FailingRefs = []string{"out1", "out3"}
	want.Denials, want.DenialsKnown = denials[2:], true
	want.Retry = types.RetryVerdict{Retryable: true}
	assert.Equal(t, want, got, "the eight newest denials are kept")

	prev.Rejection.Unmet[0].Violations[0] = "edited"
	assert.Equal(t, "failed", got.Unmet[0].Violations[0], "the fold shares nothing with the row")

	prev.Updated = 0
	assert.Equal(t, int64(99), foldAttempt(prev, nil, false, 99).Ended, "a row never stamped ends now")
}

func TestEvidenceAtSetsAsideRunsOfAnEarlierSpec(t *testing.T) {
	t.Parallel()

	row := types.Job{ID: "a", Generation: 3}
	row.Result = &types.JobResult{ChangedPaths: []string{"a.go"}, ObservedGeneration: 2}
	row.Attempt = &types.JobAttempt{Ref: "out1", ObservedGeneration: 2}
	row.GateAttempts = []types.JobGateAttempt{
		{GateID: "lint", Attempt: types.JobAttempt{Ref: "out2", ObservedGeneration: 3}},
		{GateID: "unit", Attempt: types.JobAttempt{Ref: "out3", ObservedGeneration: 1}},
		{GateID: "vet", Attempt: types.JobAttempt{Ref: "out4"}},
	}

	ev, prior := evidenceAt(row)

	assert.Equal(t, 1, prior, "the oldest generation set aside")
	assert.Equal(t, []string{"a.go"}, ev.result.ChangedPaths, "a result of an earlier spec still grades")
	assert.Equal(t, types.JobAttempt{}, ev.attempt, "a run of an earlier spec does not")
	require.Len(t, ev.gateAttempts, 2)
	assert.Equal(t, "lint", ev.gateAttempts[0].GateID)
	assert.Equal(t, "vet", ev.gateAttempts[1].GateID, "an unstamped run is unknown, never prior")

	current := types.Job{ID: "a", Generation: 1, Result: &types.JobResult{ObservedGeneration: 1}}
	_, prior = evidenceAt(current)
	assert.Zero(t, prior)
}

func TestRefuseAttemptPastTheCap(t *testing.T) {
	t.Parallel()

	prev := types.Job{Attempts: 2}
	require.NoError(t, refuseAttempt("a", prev, types.Job{}), "no cap")
	require.NoError(t, refuseAttempt("a", prev, types.Job{MaxAttempts: 3}))

	err := refuseAttempt("a", prev, types.Job{MaxAttempts: 2})
	require.ErrorIs(t, err, ErrMaxAttempts)
	assert.Contains(t, err.Error(), "a used 2 of its 2 attempt(s)")
	var nonRetryable *types.NonRetryableError
	require.True(t, errors.As(err, &nonRetryable))
	assert.Equal(t, types.RetryAfterPerson, types.ClassifyRetry(err).Change)
}
