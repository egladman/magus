//go:build !(linux || darwin || dragonfly || freebsd || netbsd || openbsd)

package queue

import (
	"os"
	"os/exec"
)

// isolate is a no-op where the queue cannot see a hook's exit without reaping it: a
// process group killed after the reap could be another hook's. Signals then reach the
// hook's own process alone.
func isolate(*exec.Cmd) {}

func signalGroup(cmd *exec.Cmd, kill bool) error {
	if kill {
		return cmd.Process.Kill()
	}
	return cmd.Process.Signal(os.Interrupt)
}

func (g *group) wait() error {
	err := g.cmd.Wait()
	g.mu.Lock()
	g.reaped = true
	g.mu.Unlock()
	return err
}
