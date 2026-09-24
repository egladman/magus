//go:build unix

package file

import (
	"os"
	"syscall"
)

// Identity returns fi's inode and hard-link count. ok is false where the platform
// reports neither.
func Identity(fi os.FileInfo) (ino, nlink uint64, ok bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return uint64(st.Ino), uint64(st.Nlink), true //nolint:unconvert // the field widths differ by platform
}
