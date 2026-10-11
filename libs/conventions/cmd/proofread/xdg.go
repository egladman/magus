package main

import (
	"os"
	"path/filepath"
	"runtime"
)

// These copy UserConfigDir and UserStateDir of magus's internal/config, which
// this module cannot import: libs/conventions takes only the standard library.

// userConfigDir returns the base directory for proofread's user config,
// honoring XDG_CONFIG_HOME on every platform. os.UserConfigDir consults it
// only on Linux, so it is checked first, and an unset or relative value falls
// back to the OS default.
func userConfigDir() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" && filepath.IsAbs(x) {
		return x, nil
	}

	return os.UserConfigDir()
}

// userStateDir returns the base directory for proofread's state, honoring
// XDG_STATE_HOME on every platform and falling back to %LocalAppData% on
// Windows and ~/.local/state elsewhere, the XDG Base Directory default.
func userStateDir() (string, error) {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" && filepath.IsAbs(x) {
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

	return filepath.Join(home, ".local", "state"), nil
}
