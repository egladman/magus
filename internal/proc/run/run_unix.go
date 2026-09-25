//go:build !windows && !wasm

package run

import (
	"os"
	"os/exec"
	"sync"
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

// procGroup is a started child's process group, whose id is the child's pid. Once the
// child is reaped another process can take that id, so nothing signals the group after.
type procGroup struct {
	c      *exec.Cmd
	mu     sync.Mutex
	reaped bool
}

// setCancel starts c in its own process group and, on context cancellation of a
// CommandContext, SIGTERMs the whole group. When c's WaitDelay runs out exec kills c,
// and wait then kills the rest of the group.
func setCancel(c *exec.Cmd) *procGroup {
	SetupProcessGroup(c)
	g := &procGroup{c: c}
	c.Cancel = func() error { return g.signal(syscall.SIGTERM) }
	return g
}

// signal sends sig to the group unless its leader is reaped.
func (g *procGroup) signal(sig syscall.Signal) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.reaped {
		return os.ErrProcessDone
	}
	return syscall.Kill(-g.c.Process.Pid, sig)
}
