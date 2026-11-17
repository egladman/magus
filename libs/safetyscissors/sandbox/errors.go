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

// UnconfinedError is a Required policy's refusal to start a child on a host that
// cannot confine it. Either Err says why there is no confinement at all, or Have is
// the host's level and Need the lowest one Required accepts.
type UnconfinedError struct {
	Have, Need int
	Err        error
}

func (e *UnconfinedError) Error() string {
	const optOut = "; WithMode(BestEffort) runs the child unconfined instead"
	if e.Err != nil {
		return fmt.Sprintf("sandbox: required mode cannot confine the child: %v%s", e.Err, optOut)
	}
	return fmt.Sprintf("sandbox: required mode cannot confine the child: the host's sandbox level (on Linux, its landlock ABI) is %d, below the %d it needs%s", e.Have, e.Need, optOut)
}

func (e *UnconfinedError) Unwrap() error { return e.Err }

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
