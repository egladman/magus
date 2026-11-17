package sandbox

import (
	"fmt"
	"runtime"

	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/sandbox/filesystem"
)

// DeniedError is an access a Policy refused. It wraps filesystem.ErrDenied.
type DeniedError struct {
	Access filesystem.Access
	Path   string // as the caller asked for it, before resolution
	// Lease and LeaseFrom name the job lease whose boundary refused it, empty on a
	// policy no lease narrowed.
	Lease     string
	LeaseFrom job.LeaseSource
	// Err is the ruleset's refusal, naming the resolved path.
	Err error
}

func (e *DeniedError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("%v: %s %s", filesystem.ErrDenied, e.Access, e.Path)
	}
	return e.Err.Error()
}

func (e *DeniedError) Unwrap() error {
	if e.Err == nil {
		return filesystem.ErrDenied
	}
	return e.Err
}

// UnconfinedError is a host that cannot confine a child: a Required policy's refusal to
// start it, or what a BestEffort policy reports when it starts it unconfined. Either Err
// says why there is no confinement at all, or Have is the host's level and Need the
// lowest one Mode accepts.
type UnconfinedError struct {
	Mode       Mode
	Have, Need int
	Err        error
}

func (e *UnconfinedError) Error() string {
	head, tail := "sandbox: required mode cannot confine the child", "; WithMode(BestEffort) runs the child unconfined instead"
	if e.Mode == BestEffort {
		head, tail = "sandbox: best-effort mode runs the child unconfined", ""
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %v%s", head, e.Err, tail)
	}
	return fmt.Sprintf("%s: the host's sandbox level (on Linux, its landlock ABI) is %d, below the %d it needs%s", head, e.Have, e.Need, tail)
}

func (e *UnconfinedError) Unwrap() error { return e.Err }

// OptionError is [New]'s or [Relaunch]'s refusal of an option or a combination of them.
type OptionError struct {
	Option, Problem string
}

func (e *OptionError) Error() string {
	return fmt.Sprintf("sandbox: option %s: %s", e.Option, e.Problem)
}

// UnsupportedError is a host with no kernel sandbox: Windows, a kernel without
// landlock, or a macOS without /usr/bin/sandbox-exec. Err is the host's answer, nil
// where the platform has no sandbox to ask.
type UnsupportedError struct {
	Err error
}

func (e *UnsupportedError) Error() string {
	if e.Err == nil {
		return "sandbox: kernel sandbox unsupported on " + runtime.GOOS
	}
	return "sandbox: kernel sandbox unsupported on this host: " + e.Err.Error()
}

func (e *UnsupportedError) Unwrap() error { return e.Err }
