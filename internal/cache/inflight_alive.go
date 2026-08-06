//go:build !windows

package cache

import (
	"errors"
	"os"
	"syscall"
)

// processAlive reports whether pid is still running. Signal 0 performs the permission
// and existence checks without delivering anything, which is the portable-POSIX way to
// ask. A process owned by another user answers EPERM, and that still proves it exists,
// so only ESRCH ("no such process") counts as dead.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid) // never fails on unix
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || !isDead(err)
}

// isDead distinguishes "no such process" from "exists but not yours". Two shapes mean
// dead, and missing the second is a silent bug: Go returns os.ErrProcessDone rather than
// a bare ESRCH for a process it knows has finished, so an errno-only check reads every
// corpse as alive and the post-mortem never fires.
func isDead(err error) bool {
	if errors.Is(err, os.ErrProcessDone) {
		return true
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	// EPERM proves existence: the process is there, just owned by someone else.
	return errno == syscall.ESRCH
}
