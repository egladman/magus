//go:build darwin

package run

import (
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ptsname returns the slave device path for a pty master.
//
// Darwin ships no ptsname(3) wrapper in x/sys/unix, so this is the raw ioctl:
// TIOCPTYGNAME fills a caller-supplied 128-byte buffer with the /dev/ttysNNN path.
// The grant and unlock ioctls before it are Darwin's grantpt(3) and unlockpt(3);
// both must run before the slave can be opened. Their errors are ignored because a
// master opened from /dev/ptmx is already granted on current systems, and a real
// failure surfaces immediately as an open error on the returned path.
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
