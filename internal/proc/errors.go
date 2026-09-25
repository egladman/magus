package proc

import (
	"errors"
	"fmt"
	"strings"
)

// This file is the proc package's error vocabulary: every sentinel a forward can
// surface, the not-adopted classification carried on some of them, and the wire decode
// that rebuilds the typed value on the client side.

var (
	// ErrAlreadyAdopted is returned by New when [SocketEnv] is already set.
	ErrAlreadyAdopted = errors.New("proc: already running under a parent magus")

	// ErrCycleDetected is set in runReply.Err when the same (target, project) pair is already in-flight.
	ErrCycleDetected = errors.New("proc: cycle detected in nested magus invocation")

	// ErrTokenRefused answers a /proc/ request that did not carry the server's token.
	ErrTokenRefused = errors.New("proc: request refused: it does not carry this server's token")
)

// notAdoptedError is a proc sentinel for a forwarded call the server did not adopt:
// the server is alive and answered, but will not take this call: its subcommand does
// not adopt a server, or the client's build is incompatible with the
// server's. The caller runs the command locally, quietly, rather than warning. The
// classification lives ON the error (a NotAdopted() bool method, in the spirit of
// net.Error's Temporary()/Timeout() and Temporal's application errors), so callers ask
// NotAdopted(err) instead of enumerating sentinels.
type notAdoptedError struct{ msg string }

func (e *notAdoptedError) Error() string    { return e.msg }
func (e *notAdoptedError) NotAdopted() bool { return true }

// The not-adopted server sentinels. Their MESSAGE strings are the wire contract: the
// server serializes them and the client string-matches to rebuild the typed value
// (decodeWireError), so keep the messages stable across identifier renames.
var (
	// ErrNotAdoptable: the server cannot service this subcommand (only run and
	// affected adopt a server); the client runs it locally.
	ErrNotAdoptable error = &notAdoptedError{"proc: subcommand not adoptable"}

	// ErrVersionMismatch: the client's build version differs from the server's.
	ErrVersionMismatch error = &notAdoptedError{"proc: version mismatch between parent and child magus"}
)

// NotAdopted reports whether err (or any error it wraps) is a call the server did
// not adopt (a non-adoptable subcommand, or a build mismatch on an otherwise
// adoptable one): the server answered but will not take the call, so a caller runs it
// locally and quietly instead of treating it as a failure. Prefer this over matching
// the individual sentinels: it stays correct as reasons are added and sees through
// wrapping. Errors that do not implement NotAdopted() (e.g. a transport failure)
// report false; treat those as genuine forward failures.
func NotAdopted(err error) bool {
	var e interface{ NotAdopted() bool }
	return errors.As(err, &e) && e.NotAdopted()
}

// AlreadyReported reports whether err (or any error it wraps) says its failure has
// already been explained to the user, so its own text carries no information worth
// showing. A dispatch that printed a diagnostic and then returned a bare sentinel is the
// case: the sentinel's message describes magus's control flow, not the failure.
//
// It exists because that fact has to cross a process boundary. The server sends the
// error's text in runReply.Err and the client prints it; without a way to ask, every
// adopted failure would report itself with whatever placeholder the sentinel carries.
// Same shape as NotAdopted above, for the same reason: the classification belongs on the
// error, not in a list of strings to match.
func AlreadyReported(err error) bool {
	var e interface{ AlreadyReported() bool }
	return errors.As(err, &e) && e.AlreadyReported()
}

// ExitCode reports the process status err (or any error it wraps) asks for, and
// whether it asked at all. Same shape as NotAdopted and AlreadyReported above, and for
// the same reason: the classification has to cross a process boundary that erases the
// Go type. The CLI's own error types (a usage misuse exits 2, never 1) are in another
// package proc must not import, so the server asks the error rather than naming them.
func ExitCode(err error) (int, bool) {
	var e interface{ ExitCode() int }
	if !errors.As(err, &e) {
		return 0, false
	}
	return e.ExitCode(), true
}

// decodeWireError rebuilds a typed proc error from the message string a server sent
// over the wire. The error crossed the server->client process boundary as plain text,
// losing its Go type; matching that text back to the known sentinel restores errors.Is
// and NotAdopted on the client. It is a decode, not a wrap: only ErrNotAdoptable
// carries trailing context, so that one case wraps the sentinel to keep it; an
// unrecognized message becomes a plain error.
func decodeWireError(msg string) error {
	switch msg {
	case ErrVersionMismatch.Error():
		return ErrVersionMismatch
	case ErrCycleDetected.Error():
		return ErrCycleDetected
	case ErrTokenRefused.Error():
		return ErrTokenRefused
	}
	if strings.HasPrefix(msg, ErrNotAdoptable.Error()+":") {
		return fmt.Errorf("%w%s", ErrNotAdoptable, strings.TrimPrefix(msg, ErrNotAdoptable.Error()))
	}
	return errors.New(msg)
}
