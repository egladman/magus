//go:build linux

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
