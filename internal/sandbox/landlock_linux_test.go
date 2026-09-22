package sandbox

import (
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
