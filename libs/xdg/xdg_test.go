package xdg

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestDirs(t *testing.T) {
	home := t.TempDir()
	abs := filepath.Join(home, "xdg")
	cases := []struct {
		name     string
		variable string
		resolve  func() (string, error)
		fallback string
	}{
		{"state", "XDG_STATE_HOME", StateDir, filepath.Join(home, ".local", "state")},
		{"cache", "XDG_CACHE_HOME", CacheDir, filepath.Join(home, ".cache")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("LocalAppData", "")

			t.Setenv(c.variable, abs)
			assertDir(t, c.resolve, abs)

			t.Setenv(c.variable, "relative")
			assertDir(t, c.resolve, c.fallback)

			t.Setenv(c.variable, "")
			assertDir(t, c.resolve, c.fallback)
		})
	}
}

func TestDirsUseLocalAppDataOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("LocalAppData is read only on windows")
	}
	lad := t.TempDir()
	t.Setenv("LocalAppData", lad)
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	assertDir(t, StateDir, lad)
	assertDir(t, CacheDir, lad)
}

func TestDirsFailWithNoHome(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" {
		t.Skip("the home directory is not read from HOME here")
	}
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	for name, resolve := range map[string]func() (string, error){
		"config": ConfigDir, "state": StateDir, "cache": CacheDir,
	} {
		if got, err := resolve(); err == nil {
			t.Errorf("%s with no home: got %q, want an error", name, got)
		}
	}
}

func TestConfigDirHonorsAnAbsoluteXDGConfigHome(t *testing.T) {
	abs := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", abs)
	assertDir(t, ConfigDir, abs)
}

func assertDir(t *testing.T, resolve func() (string, error), want string) {
	t.Helper()
	got, err := resolve()
	if err != nil || got != want {
		t.Errorf("got %q, %v; want %q", got, err, want)
	}
}
