//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package queue

import (
	"errors"
	"syscall"

	"golang.org/x/sys/unix"
)

// awaitExit blocks until the child pid has exited, leaving it unreaped. wait4 ignores
// WNOWAIT here (it reaps on darwin), so the exit is read from kqueue instead.
func awaitExit(pid int) error {
	kq, err := unix.Kqueue()
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(kq) }()
	var change unix.Kevent_t
	unix.SetKevent(&change, pid, unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ONESHOT)
	change.Fflags = unix.NOTE_EXIT
	for {
		got := make([]unix.Kevent_t, 1)
		n, err := unix.Kevent(kq, []unix.Kevent_t{change}, got, nil)
		switch {
		case errors.Is(err, unix.EINTR):
			continue
		// An unreaped child that already exited is a zombie, which kqueue does not watch.
		case errors.Is(err, unix.ESRCH):
			return nil
		case err != nil:
			return err
		case n == 1 && got[0].Flags&unix.EV_ERROR != 0:
			if errno := syscall.Errno(got[0].Data); errno != unix.ESRCH {
				return errno
			}
		}
		return nil
	}
}
