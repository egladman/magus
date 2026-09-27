//go:build linux

package endpoint

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// maxSocketPath is the size of sockaddr_un's sun_path on linux, its NUL included.
const maxSocketPath = 108

// unixName is the name to bind or dial path by, and done releases what that name
// holds. A path that fits sun_path is its own name. A longer one is reached through
// its directory, held open: /proc/self/fd/<fd>/<base> resolves to that directory, so
// the socket is made at path itself, and removing or finding it by path works as
// usual. done must run once the bind or connect has returned.
func unixName(path string) (string, func(), error) {
	if len(path) < maxSocketPath {
		return path, func() {}, nil
	}
	dir, base := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	fd, err := unix.Open(dir, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", nil, fmt.Errorf("endpoint: open %s to reach the %d-byte socket path %s: %w", dir, len(path), path, err)
	}
	name := "/proc/self/fd/" + strconv.Itoa(fd) + "/" + base
	if len(name) >= maxSocketPath {
		_ = unix.Close(fd)
		return "", nil, fmt.Errorf("endpoint: unix socket name %q is %d bytes, and linux holds at most %d", base, len(base), maxSocketPath-1)
	}
	return name, func() { _ = unix.Close(fd) }, nil
}

func init() { boundAt = diagBoundAt }

// From linux/unix_diag.h, which x/sys/unix does not carry.
const (
	udiagShowVFS = 0x2
	unixDiagVFS  = 1
)

// diagBoundAt compares the file sock_diag says ln is bound to with the file at path.
func diagBoundAt(ln *net.UnixListener, path string) (bool, error) {
	var want unix.Stat_t
	if err := unix.Stat(path, &want); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return false, nil
		}
		return false, fmt.Errorf("endpoint: stat %s: %w", path, err)
	}
	rc, err := ln.SyscallConn()
	if err != nil {
		return false, fmt.Errorf("endpoint: %w", err)
	}
	var sock unix.Stat_t
	var statErr error
	if err := rc.Control(func(fd uintptr) { statErr = unix.Fstat(int(fd), &sock) }); err != nil {
		return false, fmt.Errorf("endpoint: %w", err)
	}
	if statErr != nil {
		return false, fmt.Errorf("endpoint: stat the socket offered for %s: %w", path, statErr)
	}
	dev, ino, bound, err := diagVFS(uint32(sock.Ino))
	if err != nil {
		return false, fmt.Errorf("endpoint: ask sock_diag which file the socket offered for %s is bound to: %w", path, err)
	}
	return bound && dev == uint64(want.Dev) && uint64(ino) == want.Ino, nil //nolint:unconvert // Dev is uint32 on linux/mips
}

// diagVFS is the device and inode of the file the unix socket with sockfs inode sock
// is bound to; bound is false for a socket bound to no file.
func diagVFS(sock uint32) (dev uint64, ino uint32, bound bool, err error) {
	s, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, unix.NETLINK_SOCK_DIAG)
	if err != nil {
		return 0, 0, false, err
	}
	defer func() { _ = unix.Close(s) }()

	ne := binary.NativeEndian
	// struct nlmsghdr, then struct unix_diag_req.
	req := make([]byte, unix.NLMSG_HDRLEN+24)
	ne.PutUint32(req[0:], uint32(len(req)))
	ne.PutUint16(req[4:], unix.SOCK_DIAG_BY_FAMILY)
	ne.PutUint16(req[6:], unix.NLM_F_REQUEST)
	r := req[unix.NLMSG_HDRLEN:]
	r[0] = unix.AF_UNIX
	ne.PutUint32(r[4:], ^uint32(0))
	ne.PutUint32(r[8:], sock)
	ne.PutUint32(r[12:], udiagShowVFS)
	// INET_DIAG_NOCOOKIE: match by inode alone.
	ne.PutUint32(r[16:], ^uint32(0))
	ne.PutUint32(r[20:], ^uint32(0))
	if err := unix.Sendto(s, req, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return 0, 0, false, err
	}
	buf := make([]byte, unix.Getpagesize())
	n, _, err := unix.Recvfrom(s, buf, 0)
	if err != nil {
		return 0, 0, false, err
	}
	msgs, err := syscall.ParseNetlinkMessage(buf[:n])
	if err != nil {
		return 0, 0, false, err
	}
	for _, m := range msgs {
		switch m.Header.Type {
		case unix.NLMSG_ERROR:
			if len(m.Data) >= 4 {
				if code := int32(ne.Uint32(m.Data)); code != 0 {
					return 0, 0, false, unix.Errno(-code)
				}
			}
		case unix.SOCK_DIAG_BY_FAMILY:
			// The attributes follow the 16-byte struct unix_diag_msg.
			for a := m.Data[min(16, len(m.Data)):]; len(a) >= 4; {
				size := int(ne.Uint16(a))
				if size < 4 || size > len(a) {
					break
				}
				if ne.Uint16(a[2:]) == unixDiagVFS && size >= 12 {
					// udiag_vfs_dev is the kernel's dev_t: a 12-bit major over a 20-bit minor.
					kdev := ne.Uint32(a[8:])
					return unix.Mkdev(kdev>>20, kdev&(1<<20-1)), ne.Uint32(a[4:]), true, nil
				}
				a = a[min((size+3)&^3, len(a)):]
			}
		}
	}
	return 0, 0, false, nil
}
