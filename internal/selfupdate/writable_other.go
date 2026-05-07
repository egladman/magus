//go:build !windows

package selfupdate

// CheckWritable verifies the running binary can be replaced.
// On Unix-like systems, replacing the inode of an executing process is
// supported, so probe the binary path directly.
func CheckWritable(path string) error { return CheckFileWritable(path) }
