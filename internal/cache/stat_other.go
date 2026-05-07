//go:build !linux

package cache

import "os"

// statMtime returns (mtime nanoseconds, size bytes, perm bits) for path.
// On non-Linux platforms this delegates to os.Stat.
func statMtime(path string) (mtime, size int64, mode os.FileMode, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0, 0, err
	}
	return info.ModTime().UnixNano(), info.Size(), info.Mode().Perm(), nil
}
