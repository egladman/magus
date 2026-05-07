//go:build darwin

package selfupdate

import "path/filepath"

// resolveExePath follows symlinks on macOS. os.Executable uses
// _NSGetExecutablePath, which returns the as-invoked path, so running
// `mgs selfupdate` would overwrite the symlink instead of the real binary.
// filepath.EvalSymlinks resolves that before we hand the path to the updater.
func resolveExePath(exePath string) string {
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		return resolved
	}
	return exePath
}
