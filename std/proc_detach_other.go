//go:build unix

package std

import "syscall"

// detachedProcAttr makes the child the leader of a new session: out of the caller's
// process group and off its controlling terminal.
func detachedProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
