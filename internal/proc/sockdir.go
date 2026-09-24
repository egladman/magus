package proc

import "github.com/egladman/magus/internal/proc/sockdir"

// SockDir returns the directory where magus keeps its sockets; see sockdir.Dir.
func SockDir() string { return sockdir.Dir() }
