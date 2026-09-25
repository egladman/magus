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

// Declared ahead of TestApplyLinuxEnforcement, which confines this binary for good: the
// re-exec below has to run before it does.
func TestApplyReadOnlyConfinesTheProcessAndItsChildren(t *testing.T) {
	if !Supported() {
		t.Skip("landlock not available on this kernel")
	}
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

// TestApplyLinuxEnforcement verifies that on a kernel with landlock support,
// Apply actually confines the process: writes inside the allowed dir succeed,
// reads of paths outside the allowlist fail with EACCES, and child processes
// inherit the restriction.
//
// This test calls Apply which permanently restricts the test process.  It must
// run in isolation (go test -run TestApplyLinuxEnforcement -count=1) because
// subsequent tests in the same process will also be restricted.  The test is
// skipped when Supported() is false (kernel <5.13 or landlock disabled).
func TestApplyLinuxEnforcement(t *testing.T) {
	if !Supported() {
		t.Skip("landlock not available on this kernel; skipping enforcement test")
	}

	ws := t.TempDir()
	p := BuildPolicy(ws, nil, nil, nil, nil)

	require.NoError(t, Apply(p))

	// Write inside workspace must succeed.
	allowed := filepath.Join(ws, "hello.txt")
	assert.NoError(t, os.WriteFile(allowed, []byte("ok"), 0o644), "WriteFile inside workspace should succeed")

	// Read of /etc/passwd must be denied.
	_, err := os.ReadFile("/etc/passwd")
	assert.Error(t, err, "ReadFile /etc/passwd should be denied after Apply")

	// Child process must also be confined: `cat /etc/passwd` should fail.
	cmd := exec.Command("cat", "/etc/passwd")
	assert.Error(t, cmd.Run(), "child `cat /etc/passwd` should fail under landlock")
}
