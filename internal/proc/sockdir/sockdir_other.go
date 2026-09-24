//go:build windows

package sockdir

import "os"

// Dir returns the base directory for socket files. On Windows, os.TempDir() resolves to
// %LOCALAPPDATA%\Temp, which is already user-specific, so no subdirectory is needed.
func Dir() string {
	return os.TempDir()
}
