//go:build windows

package std

import "syscall"

// detachedProcess is DETACHED_PROCESS, a Win32 creation flag the syscall package does not
// name: the child gets no console of the caller's.
const detachedProcess = 0x00000008

// detachedProcAttr starts the child in a new process group with no console, so a Ctrl-C to
// the caller's console does not reach it.
func detachedProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess}
}
