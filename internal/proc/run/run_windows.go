//go:build windows

package run

import (
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

// SetupProcessGroup puts c in a new process group so a console control event can be
// scoped to the child subtree. Call it before c.Start.
func SetupProcessGroup(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
}

// TerminateGroup sends CTRL_BREAK_EVENT to c's process group for a graceful
// shutdown (SIGTERM is not deliverable on Windows). It is a no-op before the
// process starts, and requires c to have been configured with [SetupProcessGroup].
func TerminateGroup(c *exec.Cmd) error {
	if c.Process == nil {
		return nil
	}
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(c.Process.Pid))
}

// KillGroup terminates the child tree via taskkill /F /T; failures are ignored.
func KillGroup(c *exec.Cmd) {
	if c.Process == nil {
		return
	}
	_ = exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(c.Process.Pid)).Run()
}

// setCancel configures c to use CTRL_BREAK_EVENT (in its own process group) on
// context cancellation of a CommandContext.
func setCancel(c *exec.Cmd) {
	SetupProcessGroup(c)
	c.Cancel = func() error {
		return TerminateGroup(c)
	}
}
