//go:build darwin

package endpoint

import "fmt"

// sunPathLen is the size of sockaddr_un's sun_path on darwin, its NUL included.
const sunPathLen = 104

// unixName is path, which darwin can only bind or dial when it fits sun_path: it has
// no /proc to reach a directory through, and a symlink to a shorter name would be a
// file another process could swap.
func unixName(path string) (string, func(), error) {
	if len(path) >= sunPathLen {
		return "", nil, fmt.Errorf("endpoint: unix socket path %s is %d bytes, and darwin holds at most %d", path, len(path), sunPathLen-1)
	}
	return path, func() {}, nil
}
