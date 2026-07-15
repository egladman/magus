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
	// ErrAlreadyAdopted is returned by New when MAGUS_DAEMON_SOCKET is already set.
	ErrAlreadyAdopted = errors.New("proc: already running under a parent magus")

	// ErrCycleDetected is set in RunReply.Err when the same (target, project) pair is already in-flight.
	ErrCycleDetected = errors.New("proc: cycle detected in nested magus invocation")
)

// notAdoptedError is a proc sentinel for a forwarded call the daemon did not adopt:
// the daemon is alive and answered, but will not take this call - its subcommand does
// not adopt a daemon, or the client's build/protocol is incompatible with the
// daemon's. The caller runs the command locally, quietly, rather than warning. The
// classification lives ON the error (a NotAdopted() bool method, in the spirit of
// net.Error's Temporary()/Timeout() and Temporal's application errors), so callers ask
// NotAdopted(err) instead of enumerating sentinels.
type notAdoptedError struct{ msg string }

func (e *notAdoptedError) Error() string    { return e.msg }
func (e *notAdoptedError) NotAdopted() bool { return true }

// The not-adopted daemon sentinels. Their MESSAGE strings are the wire contract: the
// daemon serializes them and the client string-matches to rebuild the typed value
// (decodeWireError), so keep the messages stable across identifier renames.
var (
	// ErrNotAdoptable: the daemon cannot service this subcommand (only run and
	// affected adopt a daemon); the client runs it locally.
	ErrNotAdoptable error = &notAdoptedError{"proc: subcommand not adoptable"}

	// ErrVersionMismatch: the client's build version differs from the daemon's.
	ErrVersionMismatch error = &notAdoptedError{"proc: version mismatch between parent and child magus"}

	// ErrProtocolMismatch: the client sent an unrecognised non-empty Protocol value.
	ErrProtocolMismatch error = &notAdoptedError{"proc: protocol version mismatch"}
)

// NotAdopted reports whether err - or any error it wraps - is a call the daemon did
// not adopt (a non-adoptable subcommand, or a build/protocol mismatch on an otherwise
// adoptable one): the daemon answered but will not take the call, so a caller runs it
// locally and quietly instead of treating it as a failure. Prefer this over matching
// the individual sentinels: it stays correct as reasons are added and sees through
// wrapping. Errors that do not implement NotAdopted() (e.g. a transport failure)
// report false - treat those as genuine forward failures.
func NotAdopted(err error) bool {
	var e interface{ NotAdopted() bool }
	return errors.As(err, &e) && e.NotAdopted()
}

// decodeWireError rebuilds a typed proc error from the message string a server sent
// over the wire. The error crossed the daemon->client process boundary as plain text,
// losing its Go type; matching that text back to the known sentinel restores errors.Is
// and NotAdopted on the client. It is a decode, not a wrap - only ErrNotAdoptable
// carries trailing context, so that one case wraps the sentinel to keep it; an
// unrecognised message becomes a plain error.
func decodeWireError(msg string) error {
	switch msg {
	case ErrProtocolMismatch.Error():
		return ErrProtocolMismatch
	case ErrVersionMismatch.Error():
		return ErrVersionMismatch
	case ErrCycleDetected.Error():
		return ErrCycleDetected
	}
	if strings.HasPrefix(msg, ErrNotAdoptable.Error()+":") {
		return fmt.Errorf("%w%s", ErrNotAdoptable, strings.TrimPrefix(msg, ErrNotAdoptable.Error()))
	}
	return errors.New(msg)
}
