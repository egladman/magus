package types

import (
	"context"
	"fmt"
)

// ExitError aborts the current magus run with a specific process exit code. It
// is raised by os.exit(code) from a magusfile — typically after logging an
// error — and propagates up like any other target error.
//
// It deliberately does NOT call os.Exit. A magusfile target can execute inside
// a long-lived daemon process that serves multiple workspaces (see internal/proc),
// where os.Exit would terminate unrelated in-flight work. Instead the CLI maps
// this error to its process exit status, and the daemon maps it to the per-run
// reply code.
type ExitError struct {
	Code int
}

// Error reports the exit code; the message is incidental since the CLI/daemon
// recover ExitError via errors.As and use Code, not the string.
func (e ExitError) Error() string { return fmt.Sprintf("exit %d", e.Code) }

// exitRecorder captures an exit code requested during a run.
type exitRecorder struct {
	code int
	set  bool
}

type exitRecorderKey struct{}

// WithExitRecorder returns a context that captures an exit code requested via
// RecordExit during execution, plus a reader for it. The interpreter wraps each
// target run with this so an os.exit / magus.fatal code survives even when a VM
// stringifies the ExitError on the way out — the Lua engines raise host errors
// as Lua strings, dropping the Go type, so reading the code out-of-band here is
// what makes os.exit's code engine-independent.
func WithExitRecorder(ctx context.Context) (context.Context, func() (int, bool)) {
	r := &exitRecorder{}
	return context.WithValue(ctx, exitRecorderKey{}, r), func() (int, bool) { return r.code, r.set }
}

// RecordExit records code on ctx's exit recorder if one is present (set by the
// interpreter). It is a no-op outside a recorded run. Called by os.exit and
// magus.fatal alongside returning/raising an ExitError.
func RecordExit(ctx context.Context, code int) {
	if r, ok := ctx.Value(exitRecorderKey{}).(*exitRecorder); ok {
		r.code = code
		r.set = true
	}
}
