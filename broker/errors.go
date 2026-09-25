package broker

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/types"
)

// WaitError is a claim Acquire gave up on: other invocations held the host's capacity
// past the WithWait bound (at once, without one), or ctx ended the wait. It unwraps to
// the reason, so errors.Is(err, context.DeadlineExceeded) holds for an expired bound
// and errors.Is(err, context.Canceled) for a cancelled ctx.
type WaitError struct {
	Claim types.MachineClaim
	// Holders are the claims of other invocations that kept it out when it gave up.
	Holders []types.MachineClaimant
	Waited  time.Duration
	Err     error
}

func (e *WaitError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "broker: %s %s was not seated", e.Claim.Project, e.Claim.Target)
	if e.Waited > 0 {
		fmt.Fprintf(&b, " after waiting %s", e.Waited.Round(time.Millisecond))
	}
	for i, h := range e.Holders {
		sep := "; held by "
		if i > 0 {
			sep = ", "
		}
		fmt.Fprintf(&b, "%spid %d (%s %s)", sep, h.PID, h.Project, h.Target)
	}
	return b.String()
}

func (e *WaitError) Unwrap() error { return e.Err }

// ExitCode is 75, EX_TEMPFAIL: the same claim is seated once the host frees.
func (e *WaitError) ExitCode() int { return cache.ExitCodeMachineBusy }

// DoesNotFitError is a claim larger than the host's whole capacity; no wait seats it.
type DoesNotFitError struct {
	Claim   types.MachineClaim
	Verdict types.MachineVerdict
}

func (e *DoesNotFitError) Error() string {
	return fmt.Sprintf("broker: %s %s asks for %d slots and %d MiB, more than this host's whole capacity of %d slots and %d MiB",
		e.Claim.Project, e.Claim.Target, max(e.Claim.Slots, 1), e.Claim.MemoryMB, e.Verdict.BudgetSlots, e.Verdict.BudgetMB)
}

// ExitCode is 78, EX_CONFIG: the declaration is what has to change.
func (e *DoesNotFitError) ExitCode() int { return cache.ExitCodeMachineDeclaration }

// ErrUnavailable is returned, wrapped, when no broker answers: nothing is listening,
// the connection closed under a request, or the broker refused the hello. Under
// `broker: required` a step refuses with it; under best-effort the step runs
// unarbitrated.
var ErrUnavailable = errors.New("broker: unavailable")

// Error is an error frame a broker answered with, or a reply the client could not read
// (CodeProtocol). Code, not Message, is what to act on: match it with errors.Is against
// the sentinel for its code, or read Code after errors.As.
type Error struct {
	Code    ErrorCode
	Message string
}

func (e *Error) Error() string { return e.Message }

// Is matches any *Error with the same code, so errors.Is(err, ErrNoServices) holds
// whatever the message says.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// One sentinel per error code, for errors.Is.
var (
	ErrProtocol    = &Error{Code: CodeProtocol, Message: "broker: protocol mismatch"}
	ErrMalformed   = &Error{Code: CodeMalformed, Message: "broker: malformed frame"}
	ErrUnknownType = &Error{Code: CodeUnknownType, Message: "broker: unknown frame type"}
	ErrUnsupported = &Error{Code: CodeUnsupported, Message: "broker: unsupported request"}
	ErrNoServices  = &Error{Code: CodeNoServices, Message: "broker: this broker hosts no services"}
	ErrService     = &Error{Code: CodeService, Message: "broker: service failed"}
)
