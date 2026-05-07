//go:build !windows

package run

import (
	"os/exec"
	"syscall"
)

// setCancel starts c in its own process group so SIGTERM reaches the whole subtree.
func setCancel(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setpgid = true
	c.Cancel = func() error {
		return syscall.Kill(-c.Process.Pid, syscall.SIGTERM)
	}
}

// killGroup SIGKILLs the entire process group to reap grandchildren that ignored SIGTERM.
// ESRCH (group already gone) is expected and ignored.
func killGroup(c *exec.Cmd) {
	if c.Process == nil {
		return
	}
	_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
}
