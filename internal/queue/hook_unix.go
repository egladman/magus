//go:build linux || darwin || dragonfly || freebsd || netbsd || openbsd

package queue

import (
	"fmt"
	"os/exec"
	"syscall"
)

// isolate starts cmd in a process group of its own, so a signal reaches every process a
// hook started and not only the shell.
func isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func signalGroup(cmd *exec.Cmd, kill bool) error {
	sig := syscall.SIGINT
	if kill {
		sig = syscall.SIGKILL
	}
	return syscall.Kill(-cmd.Process.Pid, sig)
}

// wait waits for the leader to exit, kills what is left of its group while the
// leader's unreaped exit still holds the group's id, and only then reaps it. A kill
// after the reap could reach another hook's group that took the id.
func (g *group) wait() error {
	exitErr := awaitExit(g.cmd.Process.Pid)
	g.mu.Lock()
	if exitErr == nil {
		_ = signalGroup(g.cmd, true)
	}
	g.reaped = true
	g.mu.Unlock()
	err := g.cmd.Wait()
	if exitErr != nil {
		return fmt.Errorf("wait for the hook's exit, which left the processes it started running: %w", exitErr)
	}
	return err
}
