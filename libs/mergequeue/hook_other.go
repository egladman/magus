//go:build !unix

package mergequeue

import (
	"os"
	"os/exec"
)

// isolate is a no-op where process groups are not a signal target; interrupt and kill
// then reach the shell alone.
func isolate(*exec.Cmd) {}

func interrupt(cmd *exec.Cmd) error { return cmd.Process.Signal(os.Interrupt) }

func kill(cmd *exec.Cmd) { _ = cmd.Process.Kill() }
