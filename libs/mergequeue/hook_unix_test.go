//go:build linux || darwin || dragonfly || freebsd || netbsd || openbsd

package mergequeue

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The leader's exit is seen without reaping it, so its process id, the group's id, is
// still the hook's when the group is killed: Wait then reaps it with its own status.
func TestAwaitExitLeavesTheLeaderForWaitToReap(t *testing.T) {
	for name, delay := range map[string]time.Duration{"still running": 0, "already exited": 200 * time.Millisecond} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command("sh", "-c", "sleep 0.1; exit 3")
			isolate(cmd)
			require.NoError(t, cmd.Start())
			time.Sleep(delay)
			require.NoError(t, awaitExit(cmd.Process.Pid))
			var exit *exec.ExitError
			require.ErrorAs(t, cmd.Wait(), &exit, "not reaped by awaitExit")
			assert.Equal(t, 3, exit.ExitCode())
		})
	}
}

// Before, the group was killed after the leader was reaped, when its id could already
// name another hook's group. Now the rest of the group dies before the reap, and no
// signal is sent to the id after it.
func TestAGroupIsKilledBeforeItsLeaderIsReapedAndNeverAfter(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("sh", "-c", `(sleep 0.3; touch late) & exit 0`)
	cmd.Dir = dir
	isolate(cmd)
	require.NoError(t, cmd.Start())
	g := &group{cmd: cmd}
	require.NoError(t, g.wait())
	assert.ErrorIs(t, g.signal(true), os.ErrProcessDone, "nothing signals a reaped leader's id")
	time.Sleep(500 * time.Millisecond)
	assert.NoFileExists(t, filepath.Join(dir, "late"))
}
