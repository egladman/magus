package xdg

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
		{"data", "XDG_DATA_HOME", DataDir, filepath.Join(home, ".local", "share")},
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
	t.Setenv("XDG_DATA_HOME", "")
	assertDir(t, StateDir, lad)
	assertDir(t, CacheDir, lad)
	assertDir(t, DataDir, lad)
}

func TestDirsFailWithNoHome(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" {
		t.Skip("the home directory is not read from HOME here")
	}
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	for name, resolve := range map[string]func() (string, error){
		"config": ConfigDir, "state": StateDir, "cache": CacheDir, "data": DataDir,
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

func TestScratchCheckoutDirNestsEachCopyUnderItsPurpose(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	purposeDir := filepath.Join(cache, "magus", "checkouts", "graph-diff")
	first, err := ScratchCheckoutDir("graph-diff")
	if err != nil {
		t.Fatal(err)
	}
	second, err := ScratchCheckoutDir("graph-diff")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(first) != purposeDir || filepath.Dir(second) != purposeDir || first == second {
		t.Errorf("got %q and %q; want two distinct directories in %q", first, second, purposeDir)
	}
	for _, dir := range []string{filepath.Dir(purposeDir), purposeDir, first} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
			t.Errorf("%s has mode %v, want 0700", dir, info.Mode().Perm())
		}
	}
}

func TestScratchCheckoutDirRefusesAPurposeThatIsNotOneName(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for _, purpose := range []string{"", ".", "..", "merge/queue", `merge\queue`} {
		if got, err := ScratchCheckoutDir(purpose); err == nil {
			t.Errorf("purpose %q: got %q, want an error", purpose, got)
		}
	}
}

func TestScratchCheckoutDirRefusesAPlantedDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows reports no permission bits to refuse on")
	}
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	checkouts := filepath.Join(cache, "magus", "checkouts")
	if err := os.MkdirAll(checkouts, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ScratchCheckoutDir("merge"); err == nil || !strings.Contains(err.Error(), "not a private directory") {
		t.Errorf("open checkouts dir: got %v, want a refusal", err)
	}

	if err := os.Chmod(checkouts, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(checkouts, "merge")); err != nil {
		t.Fatal(err)
	}
	if _, err := ScratchCheckoutDir("merge"); err == nil || !strings.Contains(err.Error(), "not a private directory") {
		t.Errorf("symlinked purpose dir: got %v, want a refusal", err)
	}
}
