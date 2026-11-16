package types

import (
	"context"
	"errors"

	"github.com/egladman/magus/libs/safetyscissors/job"
)

// NonRetryableError marks Err as one that repeating unchanged cannot clear. Its text is
// Err's, so wrapping changes what a caller may do and never what a person reads.
type NonRetryableError struct {
	Err error
	// Change is what must change first; empty reads as [job.RetryAfterPerson].
	Change job.RetryChange
	// Reason says why, for a person deciding what to change.
	Reason string
}

func (e *NonRetryableError) Error() string {
	if e.Err == nil {
		return e.Reason
	}
	return e.Err.Error()
}

func (e *NonRetryableError) Unwrap() error { return e.Err }

// Retryable is the classification [ClassifyRetry] asks of any error in a chain.
func (e *NonRetryableError) Retryable() bool { return false }

// NonRetryable wraps err as not retryable until change is made. A nil err stays nil, so a
// call site can wrap a result unconditionally.
func NonRetryable(err error, change job.RetryChange, reason string) error {
	if err == nil {
		return nil
	}
	return &NonRetryableError{Err: err, Change: change, Reason: reason}
}

// ClassifyRetry says whether repeating the action that failed with err could succeed, on
// Temporal's rule: retryable unless typed otherwise. It is pure: no I/O, no clock.
//
// In order: nil is retryable; context.Canceled is not, since a person stopped it; the
// outermost error in the chain with a Retryable() bool method decides, so a wrap overrides
// what it wraps; then the first diagnostic in the chain whose code the non-retryable table
// names; anything else is retryable. A context.DeadlineExceeded is retryable, as a timeout
// is in Temporal.
func ClassifyRetry(err error) job.RetryVerdict {
	if err == nil {
		return job.RetryVerdict{Retryable: true}
	}
	if errors.Is(err, context.Canceled) {
		return job.RetryVerdict{Change: job.RetryAfterPerson, Reason: "it was canceled"}
	}
	var typed interface{ Retryable() bool }
	if errors.As(err, &typed) {
		if typed.Retryable() {
			return job.RetryVerdict{Retryable: true}
		}
		v := job.RetryVerdict{Change: job.RetryAfterPerson, Reason: err.Error()}
		if nr, ok := typed.(*NonRetryableError); ok {
			if nr.Change != job.RetryAsIs {
				v.Change = nr.Change
			}
			if nr.Reason != "" {
				v.Reason = nr.Reason
			}
		}
		return v
	}
	for next := err; ; {
		var d *DiagnosticError
		if !errors.As(next, &d) {
			break
		}
		if change, ok := retryTable[d.Code]; ok {
			return job.RetryVerdict{Change: change, Reason: d.Msg, Code: d.Code}
		}
		if next = d.Unwrap(); next == nil {
			break
		}
	}
	return job.RetryVerdict{Retryable: true}
}
