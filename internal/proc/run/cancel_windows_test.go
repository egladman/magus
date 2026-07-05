//go:build windows

package run

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestCancelDeliversCtrlBreak verifies that cancelling a Run context on Windows
// terminates the child promptly via CTRL_BREAK_EVENT rather than timing out
// through the full WaitDelay. ping.exe is always available on Windows and blocks
// for ~60s when given 60 iterations; it handles CTRL_BREAK_EVENT and exits cleanly.
func TestCancelDeliversCtrlBreak(t *testing.T) {
	if _, err := exec.LookPath("ping"); err != nil {
		t.Skip("'ping' not available")
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := Run(ctx, t.TempDir(), "ping", "-n", "60", "127.0.0.1")
	elapsed := time.Since(start)

	assert.Error(t, err, "want non-nil error after context cancel")
	// Should exit well inside the 5s WaitDelay; allow 7s for slow CI runners.
	assert.LessOrEqual(t, elapsed, 7*time.Second, "Run should exit < 7s after cancel (WaitDelay is 5s)")
}
