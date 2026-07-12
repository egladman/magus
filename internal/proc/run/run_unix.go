//go:build !windows && !wasm

package run

import (
	"os/exec"
	"syscall"
)

// SetupProcessGroup starts c in its own process group so a later signal reaches
// the whole subtree (grandchildren included), not just the direct child. Call it
// before c.Start.
func SetupProcessGroup(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setpgid = true
}

// TerminateGroup sends SIGTERM to c's process group for a graceful shutdown. It is a
// no-op before the process starts, and requires c to have been configured with
// [SetupProcessGroup].
func TerminateGroup(c *exec.Cmd) error {
	if c.Process == nil {
		return nil
	}
	return syscall.Kill(-c.Process.Pid, syscall.SIGTERM)
}

// KillGroup SIGKILLs c's entire process group to reap grandchildren that ignored the
// graceful signal. ESRCH (group already gone) is expected and ignored.
func KillGroup(c *exec.Cmd) {
	if c.Process == nil {
		return
	}
	_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
}

// setCancel starts c in its own process group and, on context cancellation of a
// CommandContext, SIGTERMs the whole group.
func setCancel(c *exec.Cmd) {
	SetupProcessGroup(c)
	c.Cancel = func() error {
		return TerminateGroup(c)
	}
}
