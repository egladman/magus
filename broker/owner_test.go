package broker

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/types"
)

// leasedClient is a client acting under lease in a tree whose runs the broker seats
// maxValidations at a time.
func leasedClient(t *testing.T, addr, lease, tree string, maxValidations int) *Client {
	t.Helper()
	c := NewClient(addr)
	c.hello.Lease, c.hello.Tree, c.hello.MaxValidations = lease, tree, maxValidations
	c.scopeOnce.Do(func() {})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestAJobTreeRunsAsManyValidationsAsItsCap(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(0, 10))
	first := leasedClient(t, addr, "plan/a", "plan", 1)
	second := leasedClient(t, addr, "plan/b", "plan", 1)
	nested := leasedClient(t, addr, "plan/a", "plan", 1)
	root := NewClient(addr)
	t.Cleanup(func() { _ = root.Close() })

	v, err := first.Request(t.Context(), types.MachineClaim{Target: "go-test", Invocation: "1:a"})
	require.NoError(t, err)
	require.True(t, v.Granted)

	v, err = second.Request(t.Context(), types.MachineClaim{Target: "go-test", Invocation: "2:b"})
	require.NoError(t, err)
	assert.False(t, v.Granted, "a second run of the tree waits for the seat")
	assert.True(t, v.OwnRun, "and waits rather than refusing")
	assert.True(t, second.seatWaiting.Load(), "the wait is said")

	v, err = nested.Request(t.Context(), types.MachineClaim{Target: "build", Invocation: "3:c", Ancestors: []string{"1:a"}})
	require.NoError(t, err)
	assert.True(t, v.Granted, "a magus nested under the seated run shares its seat")

	v, err = root.Request(t.Context(), types.MachineClaim{Target: "test", Invocation: "4:d"})
	require.NoError(t, err)
	assert.True(t, v.Granted, "a run nobody leased is never counted")

	require.NoError(t, first.Close())
	require.NoError(t, nested.Close())
	assert.Eventually(t, func() bool {
		v, err := second.Request(t.Context(), types.MachineClaim{Target: "go-test", Invocation: "2:b"})
		return err == nil && v.Granted
	}, 2*time.Second, 10*time.Millisecond, "the seat passes on once its run's connections close")
	assert.False(t, second.seatWaiting.Load())
}

func TestASeatWaitNamesTheRunHoldingIt(t *testing.T) {
	wait := SeatWait{Tree: "plan", Max: 1, Holders: []Work{{Kind: WorkRun, Lease: "plan/a", PID: 42, Command: "magus run go-test .", Dir: "/w"}}}
	assert.Equal(t, "waiting for job tree plan's validation seat: jobs.max_validations is 1, and it is held by plan/a (pid 42, magus run go-test ., in /w)", wait.String())
}

func TestWorkNamesEachRunsOwner(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(0, 10))
	worker := leasedClient(t, addr, "plan/a", "plan", 0)
	_, err := worker.Request(t.Context(), types.MachineClaim{Target: "go-test", Slots: 4})
	require.NoError(t, err)

	r, err := QueryWork(t.Context(), addr)
	require.NoError(t, err)
	require.Len(t, r.Running, 1)
	assert.Equal(t, "plan/a", r.Running[0].Owner())
	got := r.Running[0]
	got.Dir, got.Command, got.Since = "", "", time.Time{}
	assert.Equal(t, Work{Kind: WorkRun, Lease: "plan/a", PID: os.Getpid(), Slots: 4}, got)
}

func TestValidationScopeReadsTheWorkspacesCap(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("jobs:\n  max_validations: 2\n"), 0o644))
	sub := filepath.Join(root, "a", "b")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	tree, n, err := validationScope(sub, "plan/a")
	require.NoError(t, err)
	assert.Equal(t, "plan/a", tree, "a lease no row declares is its own tree")
	assert.Equal(t, 2, n)

	tree, n, err = validationScope(t.TempDir(), "w")
	require.NoError(t, err)
	assert.Equal(t, "w", tree)
	assert.Equal(t, 1, n, "outside a workspace the default holds")

	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("jobs:\n  max_validations: -1\n"), 0o644))
	_, _, err = validationScope(sub, "plan/a")
	require.ErrorContains(t, err, "jobs.max_validations is -1")
}

// groupHolderEnv makes the test binary a run that dies holding a claim and the process
// group its step left behind (see holdAGroupUntilKilled); groupRootEnv names the
// workspace it runs in.
const (
	groupHolderEnv = "MAGUS_BROKER_TEST_GROUP_HOLDER"
	groupRootEnv   = "MAGUS_BROKER_TEST_GROUP_ROOT"
)

// rootedWorkspace is a workspace that answers only its root, as a run's context carries.
type rootedWorkspace struct {
	types.WorkspaceRepository
	root string
}

func (w rootedWorkspace) Root() string { return w.root }

// startOrphanGroup starts what a step can leave behind: a leader that exits at once, and
// the background sleep it started, still running in the leader's process group. The
// sleep ends by itself within 30 seconds whatever a test does.
func startOrphanGroup() (int, error) {
	cmd := exec.Command("sh", "-c", "sleep 30 &")
	InOwnGroup(cmd)
	if err := cmd.Run(); err != nil {
		return 0, err
	}
	return cmd.Process.Pid, nil
}

// orphanGroup is startOrphanGroup for a test, which kills the group when it ends.
func orphanGroup(t *testing.T) int {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("a process group outliving its leader is a unix thing")
	}
	pgid, err := startOrphanGroup()
	require.NoError(t, err)
	t.Cleanup(func() { _ = SignalGroup(pgid, true) })
	require.True(t, groupAlive(pgid), "the background sleep outlives its leader")
	return pgid
}

// holdAGroupUntilKilled claims a slot, starts an orphan group under the claim, prints the
// group's id, and holds both until stdin closes. The test kills it first; a test binary
// that dies closes the pipe, so this process never outlives it.
func holdAGroupUntilKilled(addr string) int {
	ctx := types.WithWorkspace(context.Background(), rootedWorkspace{root: os.Getenv(groupRootEnv)})
	c, err := Dial(ctx, addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	v, err := c.Request(ctx, types.MachineClaim{Project: ".", Target: "go-test", Slots: 1})
	if err != nil || !v.Granted {
		fmt.Fprintln(os.Stderr, "claim not granted", err)
		return 2
	}
	pgid, err := startOrphanGroup()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if _, err := c.HoldGroup(ctx, v.ID, pgid, "sleep 30"); err != nil {
		_ = SignalGroup(pgid, true)
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	fmt.Println(pgid)
	_, _ = io.Copy(io.Discard, os.Stdin)
	return 0
}

// A run killed mid-step runs no release: its connection closing is all the broker hears,
// and the group its step left behind ends with it. Groups that outlived their claims,
// invisible to the budget, forked for days and stalled the host on 2026-10-15.
func TestAStepGroupEndsWhenItsRunIsKilled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a process group outliving its leader is a unix thing")
	}
	addr := testAddr(t)
	serve(t, addr, WithCapacity(1000, 4))
	root := t.TempDir()

	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), groupHolderEnv+"="+addr, groupRootEnv+"="+root)
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	out, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })

	line, err := bufio.NewReader(out).ReadString('\n')
	require.NoError(t, err)
	pgid, err := strconv.Atoi(strings.TrimSpace(line))
	require.NoError(t, err, "the holder prints its group, not %q", line)
	t.Cleanup(func() { _ = SignalGroup(pgid, true) })
	require.True(t, groupAlive(pgid))

	require.NoError(t, cmd.Process.Kill())
	_ = cmd.Wait()
	assert.Eventually(t, func() bool { return !groupAlive(pgid) }, 5*time.Second, 20*time.Millisecond,
		"the group ends with the run that held its claim")
	r, err := QueryWork(t.Context(), addr)
	require.NoError(t, err)
	require.Len(t, r.Ended, 1)
	got := r.Ended[0]
	assert.Contains(t, got.Reason, "outlived the magus run whose step started it")
	wd, err := os.Getwd()
	require.NoError(t, err)
	assert.Equal(t, Ended{Work: Work{Kind: WorkRun, PID: got.PID, Dir: wd, Command: "sleep 30", Since: got.Since}, Reason: got.Reason, At: got.At}, got)

	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	var queue []sessions.AttentionRequest
	require.Eventually(t, func() bool {
		fold, err := sessions.ReadAll(dir)
		queue = sessions.AttentionQueue(fold)
		return err == nil && len(queue) > 0
	}, 5*time.Second, 20*time.Millisecond, "the ending is filed in the run's workspace")
	require.Len(t, queue, 1)
	assert.Contains(t, queue[0].Message, "outlived the magus run whose step started it")
}

// A claim handed back while a group it carries still runs takes the group with it.
func TestAStepGroupEndsWithItsClaim(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(1000, 4))
	c := dial(t, addr)
	pgid := orphanGroup(t)

	v, err := c.Request(t.Context(), types.MachineClaim{Project: ".", Target: "go-test", Slots: 1})
	require.NoError(t, err)
	_, err = c.HoldGroup(t.Context(), v.ID, pgid, "sleep 30")
	require.NoError(t, err)
	r, err := c.Work(t.Context())
	require.NoError(t, err)
	require.Len(t, r.Running, 1, "the group is part of its run, not work of its own")
	assert.Equal(t, WorkRun, r.Running[0].Kind)
	require.True(t, groupAlive(pgid), "a held group runs until its claim ends")

	c.Release(t.Context(), v.ID)
	assert.Eventually(t, func() bool { return !groupAlive(pgid) }, 5*time.Second, 20*time.Millisecond)
	r, err = c.Work(t.Context())
	require.NoError(t, err)
	require.Len(t, r.Ended, 1)
	assert.Contains(t, r.Ended[0].Reason, "outlived the step whose claim it rode")
}

// A group whose leader ended is released before its claim, and the claim's release then
// signals nothing: by then the group's id may name another group.
func TestAReleasedStepGroupIsLeftAlone(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(1000, 4))
	c := dial(t, addr)
	pgid := orphanGroup(t)

	v, err := c.Request(t.Context(), types.MachineClaim{Project: ".", Target: "go-test", Slots: 1})
	require.NoError(t, err)
	id, err := c.HoldGroup(t.Context(), v.ID, pgid, "sleep 30")
	require.NoError(t, err)
	c.ReleaseHold(t.Context(), id)
	c.Release(t.Context(), v.ID)
	require.NoError(t, c.Close())

	assert.Eventually(t, func() bool { return len(holders(t, addr)) == 0 }, 2*time.Second, 10*time.Millisecond)
	r, err := QueryWork(t.Context(), addr)
	require.NoError(t, err)
	assert.Empty(t, r.Ended)
	assert.True(t, groupAlive(pgid), "a released group is never stopped")
}

// A group rides only a claim its own connection holds.
func TestAStepGroupNeedsAClaimOfItsConnection(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(1000, 4))
	c := dial(t, addr)
	other := dial(t, addr)
	v, err := other.Request(t.Context(), types.MachineClaim{Project: ".", Target: "go-test", Slots: 1})
	require.NoError(t, err)

	_, err = c.HoldGroup(t.Context(), v.ID, 4242, "sleep 30")
	require.ErrorContains(t, err, "holds no claim")
	_, err = c.Hold(t.Context(), Hold{Claim: v.ID, PGID: 4242, Command: "sleep 30"})
	require.ErrorContains(t, err, "which this connection does not hold")
}
