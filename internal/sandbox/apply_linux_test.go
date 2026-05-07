//go:build linux

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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

	if err := Apply(p); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Write inside workspace must succeed.
	allowed := filepath.Join(ws, "hello.txt")
	if err := os.WriteFile(allowed, []byte("ok"), 0o644); err != nil {
		t.Errorf("WriteFile inside workspace failed: %v", err)
	}

	// Read of /etc/passwd must be denied.
	if _, err := os.ReadFile("/etc/passwd"); err == nil {
		t.Error("ReadFile /etc/passwd should be denied after Apply, but succeeded")
	}

	// Child process must also be confined: `cat /etc/passwd` should fail.
	cmd := exec.Command("cat", "/etc/passwd")
	if err := cmd.Run(); err == nil {
		t.Error("child `cat /etc/passwd` should fail under landlock, but succeeded")
	}
}
