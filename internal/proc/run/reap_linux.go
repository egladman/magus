package run

import (
	"errors"

	"golang.org/x/sys/unix"
)

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
