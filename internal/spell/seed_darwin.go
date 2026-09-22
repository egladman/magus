package spell

import (
	"errors"
	"io/fs"
	"syscall"

	"golang.org/x/sys/unix"
)

// APFS clones a whole directory tree in one clonefile(2) call. Linux has no directory
// clone, and a per-file reflink walk measured slower than the install it would save.
const cloneSupported = true

// cloneTree clones src to dst without following a symlink at src. dst must not exist.
func cloneTree(src, dst string) error {
	return unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW)
}

// renameExclusive renames from to to, failing with fs.ErrExist rather than replacing a
// directory that appeared at to in the meantime.
func renameExclusive(from, to string) error {
	err := unix.RenamexNp(from, to, unix.RENAME_EXCL)
	if errors.Is(err, syscall.EEXIST) {
		return fs.ErrExist
	}
	return err
}

func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
