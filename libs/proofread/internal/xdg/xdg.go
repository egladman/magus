package xdg

import (
	"os"
	"path/filepath"
	"runtime"
)

// A copy of libs/xdg until libs/xdg/v0.1.0 is tagged; libs cannot use a replace to it.

// ConfigDir returns the base directory for user config, honoring XDG_CONFIG_HOME on
// every platform. os.UserConfigDir consults it only on Linux, so it is checked first,
// and an unset or relative value falls back to the OS default.
func ConfigDir() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" && filepath.IsAbs(x) {
		return x, nil
	}
	return os.UserConfigDir()
}

// StateDir returns the base directory for user state: XDG_STATE_HOME when absolute,
// LocalAppData on Windows, else ~/.local/state, the XDG Base Directory default.
func StateDir() (string, error) {
	return homeDir("XDG_STATE_HOME", ".local", "state")
}

// CacheDir returns the base directory for user caches: XDG_CACHE_HOME when absolute,
// LocalAppData on Windows, else ~/.cache on every platform, macOS included.
func CacheDir() (string, error) {
	return homeDir("XDG_CACHE_HOME", ".cache")
}

// homeDir is the XDG variable when it holds an absolute path, LocalAppData on Windows,
// else the default under the home directory.
func homeDir(variable string, fallback ...string) (string, error) {
	if x := os.Getenv(variable); x != "" && filepath.IsAbs(x) {
		return x, nil
	}
	if runtime.GOOS == "windows" {
		if lad := os.Getenv("LocalAppData"); lad != "" {
			return lad, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{home}, fallback...)...), nil
}
