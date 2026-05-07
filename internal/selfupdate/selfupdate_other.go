//go:build !darwin

package selfupdate

// resolveExePath is a no-op on Linux/Windows/BSD: os.Executable already
// returns the symlink-resolved path on those platforms.
func resolveExePath(exePath string) string { return exePath }
