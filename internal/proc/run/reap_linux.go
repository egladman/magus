package run

import (
	"errors"
	"syscall"

	"golang.org/x/sys/unix"
)

// killGroup kills pgid's group with one signal. copy_process records a group signal
// sent while a fork is in flight and aborts the fork on a fatal one (kernel commit
// c3ad2c3b02e9), so no child forked during the kill escapes it. A descendant that left
// the group through setsid or setpgid is not reached.
func killGroup(pgid int) { _ = syscall.Kill(-pgid, syscall.SIGKILL) }

// awaitExit blocks until the child pid has exited, leaving it unreaped (WNOWAIT).
func awaitExit(pid int) error {
	var info unix.Siginfo
	for {
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}
