package xdg

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// ScratchCheckoutDir creates and returns a new empty 0700 directory for one disposable
// copy of a repository: CacheDir()/magus/checkouts/<purpose>/<id>. purpose names the
// feature making it, such as graph-diff, and must be one path element; filepath.Dir of
// the result is the directory that feature cleans. The caller removes the copy. The
// cache, unlike the OS temp dir, lies outside every workspace and under one mise
// trusted_config_paths entry. It errors when no home directory resolves, purpose is not
// a plain name, or magus/checkouts or the purpose directory exists but is not a
// directory closed to other users.
func ScratchCheckoutDir(purpose string) (string, error) {
	if purpose == "" || purpose == "." || purpose == ".." || strings.ContainsAny(purpose, `/\`) {
		return "", fmt.Errorf("xdg: scratch checkout purpose %q is not a single path element", purpose)
	}
	cache, err := CacheDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(cache, "magus"), 0o700); err != nil {
		return "", fmt.Errorf("xdg: create the scratch checkout root: %w", err)
	}
	dir := filepath.Join(cache, "magus", "checkouts")
	for _, d := range []string{dir, filepath.Join(dir, purpose)} {
		if err := privateDir(d); err != nil {
			return "", err
		}
	}
	tree, err := os.MkdirTemp(filepath.Join(dir, purpose), "")
	if err != nil {
		return "", fmt.Errorf("xdg: create a scratch checkout: %w", err)
	}
	return tree, nil
}

// privateDir creates dir 0700 or accepts an existing real directory no other user can
// enter, so a planted symlink or open directory cannot redirect a checkout. Owner goes
// unchecked: another account's closed directory refuses this user's create anyway.
// Windows has no such bits, and its LocalAppData is per-user.
func privateDir(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("xdg: create %s: %w", dir, err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("xdg: %w", err)
	}
	if !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		return fmt.Errorf("xdg: %s is not a private directory of this user, remove it and let magus recreate it", dir)
	}
	return nil
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
