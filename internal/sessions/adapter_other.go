//go:build !unix

package sessions

import "os/exec"

// killGroup is a no-op where there are no POSIX process groups to kill: Windows, plan9,
// and the wasm targets, none of which carry Setpgid on syscall.SysProcAttr.
//
// The runaway case adapter.go describes is therefore still reachable here, and WaitDelay
// is what bounds it: Wait stops reading the inherited pipe after the deadline instead of
// blocking on it, so the caller returns even when a grandchild outlives its parent. The
// grandchild itself is left running, which is the honest limit of what this can do
// without a job object.
func killGroup(*exec.Cmd) {}
