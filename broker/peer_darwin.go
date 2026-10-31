//go:build darwin

package broker

import "golang.org/x/sys/unix"

func peerPID(fd int) (int, error) {
	return unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID)
}
