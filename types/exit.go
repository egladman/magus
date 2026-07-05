package types

import (
	"context"
	"fmt"
)

// ExitError aborts the current magus run with a specific process exit code,
// raised by os.exit(code) from a magusfile and propagated up like any other
// target error.
//
// It deliberately does NOT call os.Exit: a target can run inside a long-lived
// daemon serving multiple workspaces (see internal/proc), where os.Exit would
// kill unrelated in-flight work. Instead the CLI maps this error to its process
// exit status, and the daemon to the per-run reply code.
type ExitError struct {
	Code int
}

// Error reports the exit code. The message is incidental: the CLI/daemon
// recover ExitError via errors.As and use Code, not the string.
func (e ExitError) Error() string { return fmt.Sprintf("exit %d", e.Code) }

// exitCapture captures an exit code requested during a run.
type exitCapture struct {
	code int
	set  bool
}

type exitCaptureKey struct{}

// WithExitCapture returns a context that captures an exit code requested via
// CaptureExit during execution, plus a reader for it. The interpreter wraps each
// target run with this so an os.exit / magus.fatal code survives even when a VM
// stringifies the ExitError on the way out: an engine that raises host errors as
// plain strings drops the Go type, so reading the code out-of-band here is what
// makes os.exit's code engine-independent.
func WithExitCapture(ctx context.Context) (context.Context, func() (int, bool)) {
	r := &exitCapture{}
	return context.WithValue(ctx, exitCaptureKey{}, r), func() (int, bool) { return r.code, r.set }
}

// CaptureExit stores code on ctx's exit capture if one is present (set by the
// interpreter). It is a no-op outside a captured run. Called by os.exit and
// magus.fatal alongside returning/raising an ExitError.
func CaptureExit(ctx context.Context, code int) {
	if r, ok := ctx.Value(exitCaptureKey{}).(*exitCapture); ok {
		r.code = code
		r.set = true
	}
}
