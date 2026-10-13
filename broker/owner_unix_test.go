//go:build unix

package broker

import (
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/sessions"
)

// startGroup runs a command that outlives any test unless something stops it, as the
// leader of its own process group, and returns it with a channel its exit arrives on.
func startGroup(t *testing.T) (*exec.Cmd, <-chan error) {
	t.Helper()
	cmd := exec.Command("sh", "-c", "sleep 600 & sleep 600")
	InOwnGroup(cmd)
	require.NoError(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() { _ = SignalGroup(cmd.Process.Pid, true) })
	return cmd, done
}

// endedWork is the one ending the broker at addr records, once it has.
func endedWork(t *testing.T, addr string) Ended {
	t.Helper()
	var r WorkReport
	require.Eventually(t, func() bool {
		var err error
		r, err = QueryWork(t.Context(), addr)
		return err == nil && len(r.Ended) > 0
	}, 5*time.Second, 20*time.Millisecond)
	require.Len(t, r.Ended, 1)
	return r.Ended[0]
}

func awaitExit(t *testing.T, done <-chan error, why string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal(why)
	}
}

// A held command's deadline is obeyed: the broker stops its whole group, remembers why,
// and files one attention row in the command's workspace.
func TestAHeldCommandStopsAtItsDeadline(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(0, 10))
	c := dial(t, addr)
	root := t.TempDir()
	cmd, done := startGroup(t)
	pgid := cmd.Process.Pid

	_, err := c.Hold(t.Context(), Hold{Lease: "plan/a", PGID: pgid, Command: "sleep 600", Root: root, Slots: 2,
		Deadline: time.Now().Add(200 * time.Millisecond)})
	require.NoError(t, err)
	running, err := c.Work(t.Context())
	require.NoError(t, err)
	require.Len(t, running.Running, 1)
	assert.Equal(t, WorkExec, running.Running[0].Kind)
	assert.Equal(t, 2, running.Running[0].Slots)

	awaitExit(t, done, "the group outlived its deadline")
	assert.Eventually(t, func() bool { return !GroupAlive(pgid) }, 5*time.Second, 20*time.Millisecond,
		"the background sleep in the group is stopped too")

	r := endedWork(t, addr)
	assert.Equal(t, "plan/a", r.Owner())
	assert.Contains(t, r.Reason, "passed its deadline")

	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	var queue []sessions.AttentionRequest
	require.Eventually(t, func() bool {
		fold, err := sessions.ReadAll(dir)
		queue = sessions.AttentionQueue(fold)
		return err == nil && len(queue) > 0
	}, 5*time.Second, 20*time.Millisecond)
	require.Len(t, queue, 1, "the root hears of it once")
	assert.Equal(t, "plan/a", queue[0].Lease)
	assert.Contains(t, queue[0].Message, "sleep 600")
}

// A held command whose magus job exec died is reaped: nothing is left to wait on it.
func TestAHeldCommandIsReapedWhenItsConnectionCloses(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(0, 10))
	c := dial(t, addr)
	cmd, done := startGroup(t)

	_, err := c.Hold(t.Context(), Hold{PGID: cmd.Process.Pid, Command: "sleep 600", Deadline: time.Now().Add(time.Hour)})
	require.NoError(t, err)
	require.NoError(t, c.Close())

	awaitExit(t, done, "the group outlived the connection that held it")
	ended := endedWork(t, addr)
	assert.Equal(t, "unleased", ended.Owner())
	assert.Contains(t, ended.Reason, "outlived the magus job exec")
	assert.Eventually(t, func() bool { return len(holders(t, addr)) == 0 }, 5*time.Second, 20*time.Millisecond,
		"its slots come back")
}

// A command that ends on its own is released and stopped by nobody.
func TestAReleasedHoldIsLeftAlone(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(0, 10))
	c := dial(t, addr)
	cmd, _ := startGroup(t)

	id, err := c.Hold(t.Context(), Hold{PGID: cmd.Process.Pid, Command: "sleep 600", Deadline: time.Now().Add(time.Hour)})
	require.NoError(t, err)
	require.Len(t, holders(t, addr), 1, "its slots count against the budget")
	c.ReleaseHold(t.Context(), id)
	require.NoError(t, c.Close())

	assert.Eventually(t, func() bool { return len(holders(t, addr)) == 0 }, 2*time.Second, 10*time.Millisecond)
	r, err := QueryWork(t.Context(), addr)
	require.NoError(t, err)
	assert.Empty(t, r.Ended)
	assert.True(t, GroupAlive(cmd.Process.Pid), "a released hold is never stopped")
}
