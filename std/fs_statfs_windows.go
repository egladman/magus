//go:build windows

package std

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// statDisk reads the volume holding path. The free figure is the one available to the
// caller, which honors per-user quotas the volume's own free count does not.
func statDisk(path string) (total, free int64, err error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, fmt.Errorf("GetDiskFreeSpaceEx %q: %w", path, err)
	}
	var avail, size, all uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &size, &all); err != nil {
		return 0, 0, fmt.Errorf("GetDiskFreeSpaceEx %q: %w", path, err)
	}
	return int64(size), int64(avail), nil
}
