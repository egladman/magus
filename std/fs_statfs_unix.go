//go:build unix

package std

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// statDisk reads the filesystem holding path. Bavail, not Bfree: the blocks reserved for
// root are free to the kernel and not to the process asking.
func statDisk(path string) (total, free int64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, fmt.Errorf("statfs %q: %w", path, err)
	}
	size := uint64(st.Bsize)
	return int64(st.Blocks * size), int64(st.Bavail * size), nil
}
