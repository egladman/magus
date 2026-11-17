//go:build windows

package proc

import "syscall"

// DetachedSysProcAttr is the Windows counterpart to the unix Setsid detach. Windows has no
// session concept; CREATE_NEW_PROCESS_GROUP detaches the child from the parent console's
// Ctrl+C/Ctrl+Break group so a shell closing does not take the process down with it.
func DetachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x00000200} // CREATE_NEW_PROCESS_GROUP
}
