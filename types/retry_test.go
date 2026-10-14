package types

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
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
		want RetryVerdict
	}{
		"no error":              {nil, RetryVerdict{Retryable: true}},
		"an untyped error":      {boom, RetryVerdict{Retryable: true}},
		"a deadline":            {fmt.Errorf("run: %w", context.DeadlineExceeded), RetryVerdict{Retryable: true}},
		"a cancel":              {fmt.Errorf("run: %w", context.Canceled), RetryVerdict{Change: RetryAfterPerson, Reason: "it was canceled"}},
		"typed non-retryable":   {NonRetryable(boom, RetryAfterConfig, "the hook is not a command"), RetryVerdict{Change: RetryAfterConfig, Reason: "the hook is not a command"}},
		"typed with no change":  {NonRetryable(boom, RetryAsIs, ""), RetryVerdict{Change: RetryAfterPerson, Reason: "boom"}},
		"a foreign typed error": {retryableError{boom, false}, RetryVerdict{Change: RetryAfterPerson, Reason: "boom"}},
		"a typed error wrapped": {fmt.Errorf("fetch: %w", NonRetryable(boom, RetryAfterUpgrade, "404")), RetryVerdict{Change: RetryAfterUpgrade, Reason: "404"}},
		"an outer wrap overrides an inner one": {
			retryableError{NonRetryable(boom, RetryAfterConfig, "inner"), true},
			RetryVerdict{Retryable: true},
		},
		"a typed error overrides the table": {
			retryableError{tooOld, true},
			RetryVerdict{Retryable: true},
		},
		"a code in the table": {
			fmt.Errorf("probe: %w", tooOld),
			RetryVerdict{Change: RetryAfterUpgrade, Reason: "go 1.20 is below the declared 1.26", Code: ToolTooOld},
		},
		"a code in the table under one that is not": {
			WrapDiagnostic(WorkspaceLoadFailed, DiagnosticErrorf(UnknownConfigKey, "jobs.retries"), "load"),
			RetryVerdict{Change: RetryAfterConfig, Reason: "jobs.retries", Code: UnknownConfigKey},
		},
		"a code that clears on its own": {
			DiagnosticErrorf(MachineBudgetExhausted, "held by another run"),
			RetryVerdict{Retryable: true},
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

	assert.NoError(t, NonRetryable(nil, RetryAfterConfig, "unused"))
	boom := errors.New("boom")
	err := NonRetryable(boom, RetryAfterConfig, "why")
	assert.EqualError(t, err, "boom")
	assert.ErrorIs(t, err, boom)
}

func TestRetryTableNamesRegisteredCodes(t *testing.T) {
	t.Parallel()

	changes := []RetryChange{RetryAfterConfig, RetryAfterWidening, RetryAfterPerson, RetryAfterUpgrade}
	for code, change := range retryTable {
		assert.Contains(t, allDiagnosticCodes, code)
		assert.Contains(t, changes, change, "%s names %q, which is no change", code, change)
	}
}
