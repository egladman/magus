//go:build wasm

package spawn

import "os/exec"

// On wasm (the browser playground) there is no OS to run processes on.
// The playground dry-runs targets to their graph and never executes anything.
// These process-group controls are no-ops so the package compiles for wasm.
// TinyGo's js/wasm syscall has neither Setpgid nor Kill. The real versions
// are in run_unix.go and run_windows.go.
func SetupProcessGroup(c *exec.Cmd)    {}
func TerminateGroup(c *exec.Cmd) error { return nil }
func KillGroup(c *exec.Cmd)            {}

type procGroup struct{ c *exec.Cmd }

func setCancel(c *exec.Cmd) *procGroup        { return &procGroup{c: c} }
func (g *procGroup) watch(groupWatch, string) {}
func (g *procGroup) wait() error              { return g.c.Wait() }
