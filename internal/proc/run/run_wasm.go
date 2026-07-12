//go:build wasm

package run

import "os/exec"

// On wasm (the browser playground) there is no OS to run processes on: the playground
// dry-runs targets to their graph and never executes anything. These process-group
// controls are stubbed to no-ops so the package compiles for wasm - TinyGo's js/wasm
// syscall has neither Setpgid nor Kill. See run_unix.go / run_windows.go for the real
// implementations.
func SetupProcessGroup(c *exec.Cmd)    {}
func TerminateGroup(c *exec.Cmd) error { return nil }
func KillGroup(c *exec.Cmd)            {}
func setCancel(c *exec.Cmd)            {}
