package sandbox

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/egladman/magus/internal/sandbox/filesystem"
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

func TestABIAgreesWithSupported(t *testing.T) {
	requireLandlock(t)
	abi, err := ABI()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, abi, 1)
	assert.True(t, Supported())
	t.Logf("landlock ABI %d", abi)
}

// helperApply is the "apply" helper: it confines itself in process to a policy
// granting ws, then proves reads outside ws fail for it and for a child it starts.
func helperApply(ws string) int {
	exe, err := os.Executable()
	if err != nil {
		return exitApplyWrong
	}
	outside := filepath.Join(filepath.Dir(ws), "outside")
	rules := []filesystem.Rule{
		{Path: ws, Read: true, Write: true},
		{Path: filesystem.ResolveRulePath(exe), Read: true, Exec: true},
	}
	for _, dir := range []string{"/lib", "/lib64", "/usr/lib", "/usr/lib64"} {
		rules = append(rules, filesystem.Rule{Path: filesystem.ResolveRulePath(dir), Read: true, Exec: true})
	}
	if err := Apply(&Policy{FS: filesystem.Ruleset{Rules: rules}}); err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		if errors.Is(err, ErrUnsupported) {
			return exitUnsupported
		}
		return exitApplyWrong
	}

	check := func(ok bool, what string) bool {
		if !ok {
			os.Stderr.WriteString(what + "\n")
		}
		return ok
	}
	_, readOutside := os.ReadFile(filepath.Join(outside, "f"))
	child := func(path string) int {
		cmd := exec.Command(exe)
		cmd.Env = append(os.Environ(), helperEnv+"=read", helperPathEnv+"="+path)
		err := cmd.Run()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		if err != nil {
			return -1
		}
		return 0
	}
	if !check(os.WriteFile(filepath.Join(ws, "hello"), []byte("ok"), 0o600) == nil, "write inside ws failed") ||
		!check(errors.Is(readOutside, os.ErrPermission), "read outside ws was not denied") ||
		!check(child(filepath.Join(ws, "hello")) == 0, "child could not read inside ws") ||
		!check(child(filepath.Join(outside, "f")) == exitDenied, "child read outside ws was not denied") {
		return exitApplyWrong
	}
	return 0
}

// TestApplyLinuxEnforcement confines a subprocess in place with Apply, the path the
// multi-workspace server takes, and has it prove the confinement from inside: a
// write inside the workspace works, a read outside is denied, and a child it
// starts is confined too. The test process itself is never confined.
func TestApplyLinuxEnforcement(t *testing.T) {
	requireLandlock(t)

	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.Mkdir(ws, 0o700))
	require.NoError(t, os.Mkdir(outside, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "f"), []byte("x"), 0o600))

	exe, err := os.Executable()
	require.NoError(t, err)
	code, out := runHelper(t, exec.Command(exe), "apply", filesystem.ResolveRulePath(ws))
	if code == exitUnsupported {
		if os.Getenv("MAGUS_TEST_REQUIRE_LANDLOCK") == "1" {
			t.Fatalf("landlock is required here and Apply reports it unsupported: %s", out)
		}
		t.Skipf("Apply unsupported in this build: %s", out)
	}
	assert.Equal(t, 0, code, out)
}
