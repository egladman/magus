//go:build unix || js

package vcs

import (
	"os"
	"syscall"
)

// pathOwnedByCurrentUser reports whether path belongs to the effective uid, the test git's
// safe.directory check makes. A path that cannot be stat'ed counts as not owned, which
// sends the caller to git.
func pathOwnedByCurrentUser(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Geteuid()
}

// pathDevice returns the device path lives on. ok is false when it cannot be read.
func pathDevice(path string) (dev uint64, ok bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	st, isStat := fi.Sys().(*syscall.Stat_t)
	if !isStat {
		return 0, false
	}
	return uint64(st.Dev), true //nolint:unconvert // Dev is int32 on darwin and uint64 on linux
}
