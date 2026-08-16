//go:build linux

package main

import (
	"os"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// ptsname returns the slave device path for a pty master.
//
// Linux numbers its slaves rather than naming them, so TIOCGPTN answers with an
// index and the path is derived from it. TIOCSPTLCK with a zero value is
// unlockpt(3); it must run before the slave can be opened, and a failure is
// ignored for the same reason the Darwin grant ioctls are - a master opened from
// /dev/ptmx is already unlocked on current systems.
func ptsname(ptmx *os.File) (string, error) {
	fd := int(ptmx.Fd())
	_ = unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0)

	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		return "", err
	}
	return "/dev/pts/" + strconv.Itoa(n), nil
}

// sysProcAttr puts the child in its own session with the pty slave as its
// controlling terminal. Without Setctty the child has a tty on its file
// descriptors but no controlling terminal, and job control in a recorded shell
// misbehaves.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true, Setctty: true}
}
