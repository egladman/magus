//go:build unix

package mergequeue

import (
	"os/exec"
	"syscall"
)

// isolate starts cmd in a process group of its own, so a signal reaches every process a
// hook started and not only the shell.
func isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func interrupt(cmd *exec.Cmd) error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGINT) }

func kill(cmd *exec.Cmd) { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
