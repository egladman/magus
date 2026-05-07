package cache

import (
	"os"

	"golang.org/x/sys/unix"
)

// statMtime uses statx(AT_STATX_DONT_SYNC) to fetch only mtime/size/mode with one alloc.
// AT_STATX_DONT_SYNC skips remote revalidation on NFS. Falls back to stat_other.go on non-Linux.
func statMtime(path string) (mtime, size int64, mode os.FileMode, err error) {
	var st unix.Statx_t
	if err = unix.Statx(
		unix.AT_FDCWD, path,
		unix.AT_STATX_DONT_SYNC,
		unix.STATX_MTIME|unix.STATX_SIZE|unix.STATX_MODE,
		&st,
	); err != nil {
		return 0, 0, 0, &os.PathError{Op: "statx", Path: path, Err: err}
	}
	return st.Mtime.Sec*1e9 + int64(st.Mtime.Nsec), int64(st.Size), os.FileMode(st.Mode & 0o777), nil
}
