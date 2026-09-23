package spell

import (
	"errors"
	"io/fs"
	"os"
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

// acquireSeedLock creates path and takes a non-blocking exclusive flock on it, to be
// held open for as long as the seed clone runs. The caller closes it (releasing the
// lock) once the clone completes or fails.
func acquireSeedLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// seedAbandoned reports whether the seed lock at path is free: no process holds it, or
// none ever created it. Taking and releasing the lock IS the check, and it never blocks.
func seedAbandoned(path string) bool {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return false
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return false
	}
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	return true
}
