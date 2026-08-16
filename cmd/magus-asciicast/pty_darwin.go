//go:build darwin

package main

import (
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ptsname returns the slave device path for a pty master.
//
// Darwin has no ptsname(3) wrapper in x/sys/unix, so this is the raw ioctl:
// TIOCPTYGNAME fills a caller-supplied 128-byte buffer with the /dev/ttysNNN
// path. The grant and unlock ioctls that precede it are Darwin's equivalents of
// grantpt(3) and unlockpt(3); both must run before the slave can be opened, and
// both are no-ops to ignore failures on because a master opened from /dev/ptmx is
// already granted on current systems.
func ptsname(ptmx *os.File) (string, error) {
	fd := ptmx.Fd()
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(unix.TIOCPTYGRANT), 0)
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(unix.TIOCPTYUNLK), 0)

	buf := make([]byte, 128)
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd,
		uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&buf[0]))); errno != 0 {
		return "", errno
	}
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i]), nil
		}
	}
	return string(buf), nil
}

// sysProcAttr puts the child in its own session with the pty slave as its
// controlling terminal. Without Setctty the child has a tty on its file
// descriptors but no controlling terminal, and job control in a recorded shell
// misbehaves.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true, Setctty: true}
}
