package main

import (
	"errors"
	"log/slog"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/types"
)

// diagnosticArgs is err's rationale as an [attr.Why], plus an origin mark
// on a workspace code.
func diagnosticArgs(err error) []any {
	var args []any
	if why := types.DiagnosticRationale(err); why != "" {
		args = append(args, attr.Why(why))
	}
	if ev, ok := types.ErrorDiagnosticEvent(err, ""); ok && types.CodeOrigin(ev.Code) == types.DiagnosticOriginWorkspace {
		args = append(args, slog.String("origin", string(types.DiagnosticOriginWorkspace)))
	}
	return args
}

// mapExitCode maps a dispatch error to an exit code and logs it unless it
// was already reported.
func mapExitCode(err error) int {
	code := exit.Code(err)
	if code != exit.OK && !exit.AlreadyReported(err) && !saidWhereRaised(err) {
		slog.Error(err.Error(), diagnosticArgs(err)...) //nolint:errmsg // the final error line has no verdict beyond the error itself; an attr.Error with an empty message would print a different line and log an empty message to non-display handlers
	}
	return code
}

// saidWhereRaised reports an ExitError its raiser already logged: a bare one
// (os.exit), or a diagnostic logged at its site, like the stall watchdog's.
// A plain cause or an unsaid cache refusal has no site, so it prints here.
func saidWhereRaised(err error) bool {
	var exitErr types.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	var diagnostic *types.DiagnosticError
	return exitErr.Err == nil || (errors.As(exitErr.Err, &diagnostic) && !cache.Unsaid(err))
}
