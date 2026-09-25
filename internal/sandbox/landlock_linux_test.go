package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// requireLandlock skips a test that needs the kernel sandbox where there is none,
// and fails it instead under MAGUS_TEST_REQUIRE_LANDLOCK=1, so a host that must
// have landlock cannot pass by skipping.
func requireLandlock(t *testing.T) {
	t.Helper()
	if _, err := ABI(); err != nil {
		if os.Getenv("MAGUS_TEST_REQUIRE_LANDLOCK") == "1" {
			t.Fatalf("landlock is required here and unavailable: %v", err)
		}
		t.Skipf("landlock unavailable: %v", err)
	}
}

// TestAccessForPathTypeDropsDirRightsOnFiles pins the masking rule that keeps
// landlock_add_rule from returning EINVAL. Directory-only rights on a regular file are
// invalid, and an allowlist entry naming a file (a resolv.conf, a socket, a config) is
// ordinary; it took down every sandboxed run on a systemd host before this masked.
func TestAccessForPathTypeDropsDirRightsOnFiles(t *testing.T) {
	both := fsAccessReadOnly | unix.LANDLOCK_ACCESS_FS_WRITE_FILE

	kept := accessForPathType(both, true)
	assert.Equal(t, both, kept, "a directory holds every requested right")

	masked := accessForPathType(both, false)
	assert.Equal(t, uint64(0), masked&fsAccessDirOnly, "no directory-only right survives on a file")
	assert.NotZero(t, masked&unix.LANDLOCK_ACCESS_FS_READ_FILE, "reading the file is still allowed")
	assert.NotZero(t, masked&unix.LANDLOCK_ACCESS_FS_WRITE_FILE, "writing it is still allowed")
}

// TestAccessForPathTypeCanEmptyTheMask covers the case addPathRule then skips: a rule
// asking ONLY for directory rights leaves nothing to request on a file.
func TestAccessForPathTypeCanEmptyTheMask(t *testing.T) {
	assert.Equal(t, uint64(0), accessForPathType(unix.LANDLOCK_ACCESS_FS_READ_DIR, false))
}

// readOnlyProbeEnv re-executes the test binary as the confined process, since landlock
// is permanent and would confine every later test here.
const readOnlyProbeEnv = "MAGUS_READONLY_PROBE_DIR"

// TestApplyReadOnlyProbe is not a test on its own: re-executed with readOnlyProbeEnv set,
// it applies the read-only layer and exits 3 on the first access that went the wrong way.
func TestApplyReadOnlyProbe(t *testing.T) {
	dir := os.Getenv(readOnlyProbeEnv)
	if dir == "" {
		return
	}
	fail := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
		os.Exit(3)
	}
	if err := ApplyReadOnly(); err != nil {
		fail("apply: %v", err)
	}
	if _, err := os.ReadFile(filepath.Join(dir, "in.txt")); err != nil {
		fail("a read was refused: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "out.txt"), []byte("x"), 0o644); err == nil {
		fail("a write succeeded")
	}
	if err := os.WriteFile(os.DevNull, []byte("x"), 0o644); err != nil {
		fail("a write to the null device was refused: %v", err)
	}
	// A child inherits the layer: it may start, and may not write.
	if err := exec.Command("sh", "-c", "echo x > "+filepath.Join(dir, "child.txt")).Run(); err == nil {
		fail("a child wrote")
	}
	os.Exit(0)
}

func TestApplyReadOnlyConfinesTheProcessAndItsChildren(t *testing.T) {
	requireLandlock(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "in.txt"), []byte("in"), 0o644))
	self, err := os.Executable()
	require.NoError(t, err)

	cmd := exec.Command(self, "-test.run=^TestApplyReadOnlyProbe$", "-test.count=1")
	cmd.Env = append(os.Environ(), readOnlyProbeEnv+"="+dir)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 3 {
		t.Fatalf("the read-only layer let the wrong access through:\n%s", out)
	}
	require.NoError(t, err, "%s", out)
	assert.NoFileExists(t, filepath.Join(dir, "out.txt"))
	assert.NoFileExists(t, filepath.Join(dir, "child.txt"))
}

// TestHandledRightsFollowTheABI pins which right each ABI adds, since asking a
// kernel for one it predates fails the whole ruleset with EINVAL.
func TestHandledRightsFollowTheABI(t *testing.T) {
	cases := []struct {
		abi    int
		fs     uint64
		scopes uint64
	}{
		{1, fsAccessV1, 0},
		{2, fsAccessV1 | unix.LANDLOCK_ACCESS_FS_REFER, 0},
		{3, fsAccessV1 | unix.LANDLOCK_ACCESS_FS_REFER | unix.LANDLOCK_ACCESS_FS_TRUNCATE, 0},
		{4, fsAccessV1 | unix.LANDLOCK_ACCESS_FS_REFER | unix.LANDLOCK_ACCESS_FS_TRUNCATE, 0},
		{5, fsAccessV1 | unix.LANDLOCK_ACCESS_FS_REFER | unix.LANDLOCK_ACCESS_FS_TRUNCATE | unix.LANDLOCK_ACCESS_FS_IOCTL_DEV, 0},
		{6, fsAccessV1 | unix.LANDLOCK_ACCESS_FS_REFER | unix.LANDLOCK_ACCESS_FS_TRUNCATE | unix.LANDLOCK_ACCESS_FS_IOCTL_DEV,
			unix.LANDLOCK_SCOPE_SIGNAL | unix.LANDLOCK_SCOPE_ABSTRACT_UNIX_SOCKET},
	}
	for _, c := range cases {
		assert.Equal(t, c.fs, handledAccessFS(c.abi), "filesystem rights at ABI %d", c.abi)
		assert.Equal(t, c.scopes, handledScopes(c.abi), "scopes at ABI %d", c.abi)
	}
}

func TestABIReportsAVersion(t *testing.T) {
	requireLandlock(t)
	abi, err := ABI()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, abi, 1)
	t.Logf("landlock ABI %d", abi)
}
