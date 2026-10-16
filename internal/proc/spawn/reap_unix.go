//go:build linux || darwin || dragonfly || freebsd || netbsd || openbsd

package spawn

import "fmt"

// watch hands w the started group. wait releases it.
func (g *procGroup) watch(w groupWatch, command string) {
	g.release = w(g.c.Process.Pid, command)
}

// wait waits for the child to exit, kills whatever of its group outlives it while the
// child's unreaped exit still holds the group's id, releases the group's watch, and only
// then reaps it. A kill or a watcher's stop after the reap could reach another group
// that took the id. Without the kill a background process the child started runs on past
// the result it fed, and, holding the child's output, keeps Wait from returning until
// WaitDelay.
func (g *procGroup) wait() error {
	exitErr := awaitExit(g.c.Process.Pid)
	g.mu.Lock()
	if exitErr == nil {
		killGroup(g.c.Process.Pid)
	}
	g.reaped = true
	g.mu.Unlock()
	if g.release != nil {
		g.release()
	}
	err := g.c.Wait()
	if exitErr != nil {
		return fmt.Errorf("wait for %s to exit, which left the processes it started running: %w", g.c.Args[0], exitErr)
	}
	return err
}
