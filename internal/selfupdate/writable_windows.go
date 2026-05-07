//go:build windows

package selfupdate

// CheckWritable verifies the running binary can be replaced.
// On Windows, the running .exe cannot be opened O_WRONLY
// (ERROR_SHARING_VIOLATION), so probe the parent directory instead.
func CheckWritable(path string) error { return CheckParentWritable(path) }
