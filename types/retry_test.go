package types

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type retryableError struct {
	error
	retryable bool
}

func (e retryableError) Retryable() bool { return e.retryable }
func (e retryableError) Unwrap() error   { return e.error }

func TestClassifyRetry(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	tooOld := DiagnosticErrorf(ToolTooOld, "go 1.20 is below the declared 1.26")
	for name, tc := range map[string]struct {
		err  error
		want job.RetryVerdict
	}{
		"no error":              {nil, job.RetryVerdict{Retryable: true}},
		"an untyped error":      {boom, job.RetryVerdict{Retryable: true}},
		"a deadline":            {fmt.Errorf("run: %w", context.DeadlineExceeded), job.RetryVerdict{Retryable: true}},
		"a cancel":              {fmt.Errorf("run: %w", context.Canceled), job.RetryVerdict{Change: job.RetryAfterPerson, Reason: "it was canceled"}},
		"typed non-retryable":   {NonRetryable(boom, job.RetryAfterConfig, "the hook is not a command"), job.RetryVerdict{Change: job.RetryAfterConfig, Reason: "the hook is not a command"}},
		"typed with no change":  {NonRetryable(boom, job.RetryAsIs, ""), job.RetryVerdict{Change: job.RetryAfterPerson, Reason: "boom"}},
		"a foreign typed error": {retryableError{boom, false}, job.RetryVerdict{Change: job.RetryAfterPerson, Reason: "boom"}},
		"a typed error wrapped": {fmt.Errorf("fetch: %w", NonRetryable(boom, job.RetryAfterUpgrade, "404")), job.RetryVerdict{Change: job.RetryAfterUpgrade, Reason: "404"}},
		"an outer wrap overrides an inner one": {
			retryableError{NonRetryable(boom, job.RetryAfterConfig, "inner"), true},
			job.RetryVerdict{Retryable: true},
		},
		"a typed error overrides the table": {
			retryableError{tooOld, true},
			job.RetryVerdict{Retryable: true},
		},
		"a code in the table": {
			fmt.Errorf("probe: %w", tooOld),
			job.RetryVerdict{Change: job.RetryAfterUpgrade, Reason: "go 1.20 is below the declared 1.26", Code: ToolTooOld},
		},
		"a code in the table under one that is not": {
			WrapDiagnostic(WorkspaceLoadFailed, DiagnosticErrorf(UnknownConfigKey, "jobs.retries"), "load"),
			job.RetryVerdict{Change: job.RetryAfterConfig, Reason: "jobs.retries", Code: UnknownConfigKey},
		},
		"a code that clears on its own": {
			DiagnosticErrorf(MachineBudgetExhausted, "held by another run"),
			job.RetryVerdict{Retryable: true},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ClassifyRetry(tc.err))
		})
	}
}

// Wrapping changes what a caller may do with an error, never what it says or matches.
func TestNonRetryableKeepsTheErrorItWraps(t *testing.T) {
	t.Parallel()

	require.NoError(t, NonRetryable(nil, job.RetryAfterConfig, "unused"))
	boom := errors.New("boom")
	err := NonRetryable(boom, job.RetryAfterConfig, "why")
	assert.EqualError(t, err, "boom")
	assert.ErrorIs(t, err, boom)
}

func TestRetryTableNamesRegisteredCodes(t *testing.T) {
	t.Parallel()

	changes := []job.RetryChange{job.RetryAfterConfig, job.RetryAfterWidening, job.RetryAfterPerson, job.RetryAfterUpgrade}
	for code, change := range retryTable {
		assert.Contains(t, allDiagnosticCodes, code)
		assert.Contains(t, changes, change, "%s names %q, which is no change", code, change)
	}
}
