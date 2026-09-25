//go:build !windows && !wasm && !(linux || darwin || dragonfly || freebsd || netbsd || openbsd)

package run

// wait reaps the child. Here its exit cannot be seen without reaping it, and a group
// killed after the reap could be another's, so what outlives the child is left running.
func (g *procGroup) wait() error {
	err := g.c.Wait()
	g.mu.Lock()
	g.reaped = true
	g.mu.Unlock()
	return err
}
