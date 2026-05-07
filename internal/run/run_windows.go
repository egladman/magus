//go:build windows

package run

import (
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

// setCancel configures c to use CTRL_BREAK_EVENT (in its own process group) on cancellation.
// SIGTERM is not deliverable on Windows; CREATE_NEW_PROCESS_GROUP scopes the event to the child.
func setCancel(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
	c.Cancel = func() error {
		return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(c.Process.Pid))
	}
}

// killGroup terminates the child tree via taskkill /F /T; failures are ignored.
func killGroup(c *exec.Cmd) {
	if c.Process == nil {
		return
	}
	_ = exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(c.Process.Pid)).Run()
}
