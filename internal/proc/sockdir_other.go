//go:build windows

package proc

import "os"

// SockDir returns the directory where magus proc sockets are stored.
func SockDir() string { return sockDir() }

// sockDir returns the base directory for the socket file.
// On Windows, os.TempDir() resolves to %LOCALAPPDATA%\Temp which is
// already user-specific, so no subdirectory is needed.
//
//nolint:revive // confusing-naming: SockDir is the exported cross-platform API; sockDir is the per-platform implementation.
func sockDir() string {
	return os.TempDir()
}
