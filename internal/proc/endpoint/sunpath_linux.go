//go:build linux

package endpoint

import (
	"fmt"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"
)

// sunPathLen is the size of sockaddr_un's sun_path on linux, its NUL included.
const sunPathLen = 108

// unixName is the name to bind or dial path by, and done releases what that name
// holds. A path that fits sun_path is its own name. A longer one is reached through
// its directory, held open: /proc/self/fd/<fd>/<base> resolves to that directory, so
// the socket is made at path itself, and removing or finding it by path works as
// usual. done must run once the bind or connect has returned.
func unixName(path string) (string, func(), error) {
	if len(path) < sunPathLen {
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
	if len(name) >= sunPathLen {
		_ = unix.Close(fd)
		return "", nil, fmt.Errorf("endpoint: unix socket name %q is %d bytes, and linux holds at most %d", base, len(base), sunPathLen-1)
	}
	return name, func() { _ = unix.Close(fd) }, nil
}
