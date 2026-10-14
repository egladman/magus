//go:build unix

package broker

import (
	"errors"
	"os/exec"
	"syscall"
)

// InOwnGroup makes cmd start as the leader of a new process group, so signalling the
// group reaches everything it starts that does not start a session of its own.
func InOwnGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// SignalGroup asks every process in group pgid to stop, or with kill ends them.
func SignalGroup(pgid int, kill bool) error {
	if pgid <= 1 {
		return errors.New("broker: no process group to signal")
	}
	sig := syscall.SIGTERM
	if kill {
		sig = syscall.SIGKILL
	}
	return syscall.Kill(-pgid, sig)
}

// groupAlive reports whether any process is left in group pgid.
func groupAlive(pgid int) bool {
	return pgid > 1 && syscall.Kill(-pgid, 0) == nil
}
