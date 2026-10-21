package xdg

import (
	"os"
	"path/filepath"
	"runtime"
)

// ConfigDir returns the base directory for user config, honoring XDG_CONFIG_HOME on
// every platform. os.UserConfigDir consults it only on Linux (macOS answers
// ~/Library/Application Support), so it is checked first. An unset or relative value
// falls through to os.UserConfigDir, whose error says no home directory resolved.
func ConfigDir() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" && filepath.IsAbs(x) {
		return x, nil
	}
	return os.UserConfigDir()
}

// StateDir returns the base directory for user state, data nothing can rebuild:
// XDG_STATE_HOME when absolute, LocalAppData on Windows, else ~/.local/state, the XDG
// Base Directory default. It errors when no home directory resolves.
func StateDir() (string, error) {
	return homeDir("XDG_STATE_HOME", ".local", "state")
}

// CacheDir returns the base directory for user caches, data a download can restore:
// XDG_CACHE_HOME when absolute, LocalAppData on Windows, else ~/.cache on every
// platform, macOS included. It errors when no home directory resolves.
func CacheDir() (string, error) {
	return homeDir("XDG_CACHE_HOME", ".cache")
}

// DataDir returns the base directory for user data, files the user keeps and other
// programs read, such as man pages: XDG_DATA_HOME when absolute, LocalAppData on
// Windows, else ~/.local/share on every platform. It errors when no home directory
// resolves.
func DataDir() (string, error) {
	return homeDir("XDG_DATA_HOME", ".local", "share")
}

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
