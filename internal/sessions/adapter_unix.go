// `unix` and not `!windows`: the constraint is POSIX process groups, and js/wasm,
// wasip1 and plan9 lack them too, so negating one platform compiled this everywhere
// except the one platform that was named.
//go:build unix

package sessions

import (
	"os/exec"
	"syscall"
)

// killGroup makes cmd killable as a whole process TREE rather than as one process.
//
// Setpgid puts the child in a group of its own, and Cancel then signals the negative pid,
// which is that whole group. Without both, the deadline reaches the direct child only and
// anything it backgrounded survives holding the output pipe, which is what kept Wait
// blocked forever; adapter.go's runAdapter has the full account.
func killGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
