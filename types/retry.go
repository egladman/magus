package types

import (
	"context"
	"errors"
)

// RetryChange names what must change before repeating a failed action can help.
type RetryChange string

const (
	// RetryAsIs is a failure that repeating unchanged may clear.
	RetryAsIs RetryChange = ""
	// RetryAfterConfig needs a declaration or configuration edit first.
	RetryAfterConfig RetryChange = "config"
	// RetryAfterWidening needs the job's write paths widened first (`magus job apply`).
	RetryAfterWidening RetryChange = "widening"
	// RetryAfterPerson needs a person to decide or act first.
	RetryAfterPerson RetryChange = "person"
	// RetryAfterUpgrade needs a different magus or tool version first.
	RetryAfterUpgrade RetryChange = "upgrade"
)

// NonRetryableError marks Err as one that repeating unchanged cannot clear. Its text is
// Err's, so wrapping changes what a caller may do and never what a person reads.
type NonRetryableError struct {
	Err error
	// Change is what must change first; empty reads as [RetryAfterPerson].
	Change RetryChange
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
func NonRetryable(err error, change RetryChange, reason string) error {
	if err == nil {
		return nil
	}
	return &NonRetryableError{Err: err, Change: change, Reason: reason}
}

// RetryVerdict is [ClassifyRetry]'s answer. Change and Reason are set only when Retryable
// is false; Code names the diagnostic whose entry in the non-retryable table decided it.
type RetryVerdict struct {
	Retryable bool           `json:"retryable"        yaml:"retryable"`
	Change    RetryChange    `json:"change,omitempty" yaml:"change,omitempty"`
	Reason    string         `json:"reason,omitempty" yaml:"reason,omitempty"`
	Code      DiagnosticCode `json:"code,omitempty"   yaml:"code,omitempty"`
}

// ClassifyRetry says whether repeating the action that failed with err could succeed, on
// Temporal's rule: retryable unless typed otherwise. It is pure: no I/O, no clock.
//
// In order: nil is retryable; context.Canceled is not, since a person stopped it; the
// outermost error in the chain with a Retryable() bool method decides, so a wrap overrides
// what it wraps; then the first diagnostic in the chain whose code the non-retryable table
// names; anything else is retryable. A context.DeadlineExceeded is retryable, as a timeout
// is in Temporal.
func ClassifyRetry(err error) RetryVerdict {
	if err == nil {
		return RetryVerdict{Retryable: true}
	}
	if errors.Is(err, context.Canceled) {
		return RetryVerdict{Change: RetryAfterPerson, Reason: "it was canceled"}
	}
	var typed interface{ Retryable() bool }
	if errors.As(err, &typed) {
		if typed.Retryable() {
			return RetryVerdict{Retryable: true}
		}
		v := RetryVerdict{Change: RetryAfterPerson, Reason: err.Error()}
		if nr, ok := typed.(*NonRetryableError); ok {
			if nr.Change != RetryAsIs {
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
			return RetryVerdict{Change: change, Reason: d.Msg, Code: d.Code}
		}
		if next = d.Unwrap(); next == nil {
			break
		}
	}
	return RetryVerdict{Retryable: true}
}
