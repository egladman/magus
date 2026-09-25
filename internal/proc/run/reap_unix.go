//go:build linux || darwin || dragonfly || freebsd || netbsd || openbsd

package run

import (
	"fmt"
	"syscall"
)

// wait waits for the child to exit, kills whatever of its group outlives it while the
// child's unreaped exit still holds the group's id, and only then reaps it. A kill
// after the reap could reach another group that took the id. Without it a background
// process the child started runs on past the result it fed, and, holding the child's
// output, keeps Wait from returning until WaitDelay.
func (g *procGroup) wait() error {
	exitErr := awaitExit(g.c.Process.Pid)
	g.mu.Lock()
	if exitErr == nil {
		_ = syscall.Kill(-g.c.Process.Pid, syscall.SIGKILL)
	}
	g.reaped = true
	g.mu.Unlock()
	err := g.c.Wait()
	if exitErr != nil {
		return fmt.Errorf("wait for %s to exit, which left the processes it started running: %w", g.c.Args[0], exitErr)
	}
	return err
}
