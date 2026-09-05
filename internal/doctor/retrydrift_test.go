package doctor

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/types"
)

// retryHistoryWith writes a history whose outcomes for one "<spell>/<target>" key carry
// the given results, each of the given attempt counts.
func retryHistoryWith(t *testing.T, project, key string, outcomes []forecast.Outcome) string {
	t.Helper()
	h := forecast.History{
		Version:  forecast.HistoryVersion,
		Projects: map[string]map[string]forecast.Stats{project: {key: {Samples: len(outcomes), RecentOutcomes: outcomes}}},
	}
	path := filepath.Join(t.TempDir(), "history.json")
	require.NoError(t, h.Save(context.Background(), path))
	return path
}

func failedRetries(n int) []forecast.Outcome {
	out := make([]forecast.Outcome, 0, n)
	for range n {
		out = append(out, forecast.Outcome{Result: forecast.OutcomeFail, Attempts: 2})
	}
	return out
}

// The finding this check exists for, and the only question Attempts can answer that
// Result cannot: these failures were retried and lost anyway, so the declaration has
// spent a second run every time and changed no verdict.
func TestRetryDeclarationsReportsRetriesThatRescueNothing(t *testing.T) {
	path := retryHistoryWith(t, ".", "go/test", failedRetries(4))
	got := runnerFor(path).checkRetryDeclarations([]*types.Project{
		projectWith(".", map[string]types.Target{
			"test": {RetryOnVolatile: true, RetryOnVolatileReason: "the suite shares a port with the dev server"},
		}),
	})

	assert.Equal(t, types.DoctorAdvice, got.Status)
	require.Len(t, got.Details, 1)
	assert.Contains(t, got.Details[0], "retried 4 recorded failure(s) and rescued none")
	assert.Contains(t, got.Details[0], "the suite shares a port with the dev server",
		"the reader is asked whether the reason they wrote still holds, so it is quoted back")
}

// One rescue is the declaration working. Reporting it would train the reader to ignore
// the check on exactly the targets it is right about.
func TestRetryDeclarationsIsQuietWhenARetryRescuedARun(t *testing.T) {
	outcomes := append(failedRetries(4), forecast.Outcome{Result: forecast.OutcomeVolatile, Attempts: 2})
	path := retryHistoryWith(t, ".", "go/test", outcomes)
	got := runnerFor(path).checkRetryDeclarations([]*types.Project{
		projectWith(".", map[string]types.Target{"test": {RetryOnVolatile: true}}),
	})

	assert.Equal(t, types.DoctorOK, got.Status)
	assert.Empty(t, got.Details)
}

// Below the sample floor a run of bad luck would read as a verdict.
func TestRetryDeclarationsWaitsForEnoughRetries(t *testing.T) {
	path := retryHistoryWith(t, ".", "go/test", failedRetries(judgeableRetries-1))
	got := runnerFor(path).checkRetryDeclarations([]*types.Project{
		projectWith(".", map[string]types.Target{"test": {RetryOnVolatile: true}}),
	})

	assert.Equal(t, types.DoctorOK, got.Status)
	assert.Empty(t, got.Details)
}

// A plain failure is not a retry. Without the Attempts field this check cannot tell the
// two apart, which is the whole reason the field is written.
func TestRetryDeclarationsIgnoresFailuresThatWereNeverRetried(t *testing.T) {
	path := retryHistoryWith(t, ".", "go/test", []forecast.Outcome{
		{Result: forecast.OutcomeFail, Attempts: 1},
		{Result: forecast.OutcomeFail, Attempts: 1},
		{Result: forecast.OutcomeFail, Attempts: 1},
		{Result: forecast.OutcomeFail, Attempts: 1},
	})
	got := runnerFor(path).checkRetryDeclarations([]*types.Project{
		projectWith(".", map[string]types.Target{"test": {RetryOnVolatile: true}}),
	})

	assert.Equal(t, types.DoctorOK, got.Status)
	assert.Empty(t, got.Details)
}

// An undeclared target is never retried, so there is nothing to keep honest.
func TestRetryDeclarationsIsQuietWithoutADeclaration(t *testing.T) {
	path := retryHistoryWith(t, ".", "go/test", failedRetries(4))
	got := runnerFor(path).checkRetryDeclarations([]*types.Project{
		projectWith(".", map[string]types.Target{"test": {}}),
	})

	assert.Equal(t, types.DoctorOK, got.Status)
	assert.Contains(t, got.Message, "no target declares retry_on_volatile")
}
