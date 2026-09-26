//go:build unix

package broker

import (
	"errors"
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// adoptListener takes over fd, which must be a listening unix stream socket.
func adoptListener(fd int) (net.Listener, error) {
	typ, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_TYPE)
	if err != nil {
		return nil, fmt.Errorf("broker: socket activation: descriptor %d is not a socket: %w", fd, err)
	}
	if typ != unix.SOCK_STREAM {
		return nil, fmt.Errorf("broker: socket activation: descriptor %d is not a stream socket", fd)
	}
	// Darwin does not answer SO_ACCEPTCONN; there a socket that is not listening fails
	// at the first Accept instead of here.
	listening, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_ACCEPTCONN)
	if (err != nil && !errors.Is(err, unix.ENOPROTOOPT)) || (err == nil && listening == 0) {
		return nil, fmt.Errorf("broker: socket activation: descriptor %d is not listening", fd)
	}
	f := os.NewFile(uintptr(fd), "activation")
	// FileListener duplicates the descriptor with close-on-exec set, so no service the
	// broker starts inherits either copy once f is closed.
	ln, err := net.FileListener(f)
	_ = f.Close()
	if err != nil {
		return nil, fmt.Errorf("broker: socket activation: descriptor %d: %w", fd, err)
	}
	if _, ok := ln.(*net.UnixListener); !ok {
		_ = ln.Close()
		return nil, fmt.Errorf("broker: socket activation: descriptor %d is a %s socket, not a unix one", fd, ln.Addr().Network())
	}
	return ln, nil
}
