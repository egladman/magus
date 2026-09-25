//go:build linux || darwin || dragonfly || freebsd || netbsd || openbsd

package run

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The leader's exit is seen without reaping it, so its process id, the group's id, is
// still the child's when the group is killed: Wait then reaps it with its own status.
func TestAwaitExitLeavesTheLeaderForWaitToReap(t *testing.T) {
	for name, delay := range map[string]time.Duration{"still running": 0, "already exited": 200 * time.Millisecond} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command("sh", "-c", "sleep 0.1; exit 3")
			SetupProcessGroup(cmd)
			require.NoError(t, cmd.Start())
			time.Sleep(delay)
			require.NoError(t, awaitExit(cmd.Process.Pid))
			var exit *exec.ExitError
			require.ErrorAs(t, cmd.Wait(), &exit, "not reaped by awaitExit")
			assert.Equal(t, 3, exit.ExitCode())
		})
	}
}

// The rest of the group dies before the leader is reaped, and no signal is sent to its
// id after, when the id can already name another group.
func TestAGroupIsKilledBeforeItsLeaderIsReapedAndNeverAfter(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.CommandContext(context.Background(), "sh", "-c", `(sleep 0.3; touch late) & exit 0`)
	cmd.Dir = dir
	g := setCancel(cmd)
	require.NoError(t, cmd.Start())
	require.NoError(t, g.wait())
	assert.ErrorIs(t, cmd.Cancel(), os.ErrProcessDone, "nothing signals a reaped leader's id")
	time.Sleep(500 * time.Millisecond)
	assert.NoFileExists(t, filepath.Join(dir, "late"))
}

// A background process a command leaves behind dies with it, and Exec returns at the
// command's exit rather than when that process lets go of the output it inherited. The
// shell forks the sleep before it prints, so no fork is in flight when the group dies.
func TestExecKillsWhatOutlivesTheChild(t *testing.T) {
	start := time.Now()
	res, err := Exec(context.Background(), "sh", []string{"-c", `sleep 30 & echo $!`},
		ExecOptions{Capture: true, Quiet: true})
	require.NoError(t, err)
	assert.Less(t, time.Since(start), time.Second, "Exec waited on the orphan's copy of stdout")
	pid, err := strconv.Atoi(strings.TrimSpace(res.Stdout))
	require.NoError(t, err)
	gone := func() bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) }
	if !assert.Eventually(t, gone, time.Second, 10*time.Millisecond, "the background sleep outlived Exec") {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

// Members that fork without pause all die with the group, wherever their forks fall
// against the kill.
func TestKillGroupLeavesNoMemberAlive(t *testing.T) {
	deadline := time.Now().Add(30 * time.Second)
	for round := 0; round < 25 && time.Now().Before(deadline); round++ {
		cmd := exec.Command("sh", "-c", `for i in 1 2 3 4; do (while :; do : & wait; done) & done; sleep 0.05`)
		SetupProcessGroup(cmd)
		require.NoError(t, cmd.Start())
		pgid := cmd.Process.Pid
		require.NoError(t, awaitExit(pgid))
		killGroup(pgid)
		_ = cmd.Wait()
		// With the leader reaped, the group lasts only as long as a member does, so a
		// signal to it cannot reach another group until it reports ESRCH.
		empty := func() bool { return errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH) }
		if !assert.Eventually(t, empty, 2*time.Second, 5*time.Millisecond, "round %d: a member outlived the group kill", round) {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			return
		}
	}
}

// A cancelled child gets CancelGrace to stop after SIGTERM, and its group dies with it
// once it is killed.
func TestExecGivesACancelledChildItsGraceThenKillsTheGroup(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, err := Exec(ctx, "sh", []string{"-c", `trap 'echo term > got' TERM; (trap '' TERM; sleep 3; touch late) & while :; do sleep 0.05; done`},
		ExecOptions{Dir: dir, CancelGrace: 500 * time.Millisecond, Quiet: true})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.True(t, res.Started)
	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, 600*time.Millisecond, "killed before its grace ran out")
	assert.Less(t, elapsed, 2*time.Second)
	got, err := os.ReadFile(filepath.Join(dir, "got"))
	require.NoError(t, err)
	assert.Equal(t, "term\n", string(got))
	time.Sleep(3500 * time.Millisecond)
	assert.NoFileExists(t, filepath.Join(dir, "late"))
}
