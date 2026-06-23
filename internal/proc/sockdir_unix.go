//go:build !windows

package proc

import (
	"fmt"
	"os"
	"path/filepath"
)

// SockDir returns the directory where magus proc sockets are stored.
func SockDir() string { return sockDir() }

// sockDir prefers $XDG_RUNTIME_DIR/magus/ and falls back to $TMPDIR/magus-$UID/.
func sockDir() string {
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		dir := filepath.Join(xdg, "magus")
		if err := os.MkdirAll(dir, 0o700); err == nil {
			return dir
		}
	}
	// Fail closed: always return the private per-UID (0700) path. Falling back to
	// the shared, world-traversable os.TempDir() would drop the isolation the
	// socket's security depends on; if MkdirAll failed, a later bind errors out
	// safely instead of placing the socket in a shared directory.
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("magus-%d", os.Getuid()))
	_ = os.MkdirAll(dir, 0o700)
	return dir
}
