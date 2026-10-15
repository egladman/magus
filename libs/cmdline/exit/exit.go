package exit

import (
	"errors"
	"flag"
	"fmt"
)

// The exit statuses a command line returns:
//
//	0   the command did what was asked (an explicit -h/--help included)
//	1   the command was invoked correctly and the work failed
//	2   the invocation itself was wrong; nothing was attempted (flag.ExitOnError's code)
//	75  nothing ran and the same command succeeds later (EX_TEMPFAIL, sysexits.h)
const (
	OK       = 0
	Failure  = 1
	Usage    = 2
	TempFail = 75
)

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }
func (usageError) ExitCode() int   { return Usage }

// Usagef returns a command-line misuse that exits Usage. Its message is what the
// person fixes their invocation by; any usage text is the command's to print.
func Usagef(format string, a ...any) error {
	return usageError{msg: fmt.Sprintf(format, a...)}
}

type reported struct{ code int }

func (reported) Error() string         { return "silent exit" }
func (e reported) ExitCode() int       { return e.code }
func (reported) AlreadyReported() bool { return true }

// Reported returns a failure the command has already explained, exiting code. Its text
// says nothing about the failure, so AlreadyReported tells a caller not to print it.
func Reported(code int) error { return reported{code: code} }

// Code returns the status err asks for: OK for nil and flag.ErrHelp, a Reported
// failure's code (a failure already explained keeps its status over any other in the
// chain), else the code of the first error with an ExitCode() int method, else
// Failure. It asks the method rather than matching types, so an error that crossed a
// process boundary can carry its status.
func Code(err error) int {
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return OK
	}
	var r reported
	if errors.As(err, &r) {
		return r.code
	}
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return Failure
}

// AlreadyReported reports whether err, or an error it wraps, says its failure was
// already explained, so its own text is not worth showing.
func AlreadyReported(err error) bool {
	var e interface{ AlreadyReported() bool }
	return errors.As(err, &e) && e.AlreadyReported()
}
