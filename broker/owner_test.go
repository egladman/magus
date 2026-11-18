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
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/libs/safetyscissors/budget"
	sessionstore "github.com/egladman/magus/libs/safetyscissors/session"
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

	v, err := first.Request(t.Context(), budget.Claim{Target: "go-test", Invocation: "1:a"})
	require.NoError(t, err)
	require.True(t, v.Granted)

	v, err = second.Request(t.Context(), budget.Claim{Target: "go-test", Invocation: "2:b"})
	require.NoError(t, err)
	assert.False(t, v.Granted, "a second run of the tree waits for the seat")
	assert.True(t, v.OwnRun, "and waits rather than refusing")
	assert.True(t, second.seatWaiting.Load(), "the wait is said")

	v, err = nested.Request(t.Context(), budget.Claim{Target: "build", Invocation: "3:c", Ancestors: []string{"1:a"}})
	require.NoError(t, err)
	assert.True(t, v.Granted, "a magus nested under the seated run shares its seat")

	v, err = root.Request(t.Context(), budget.Claim{Target: "test", Invocation: "4:d"})
	require.NoError(t, err)
	assert.True(t, v.Granted, "a run nobody leased is never counted")

	require.NoError(t, first.Close())
	require.NoError(t, nested.Close())
	assert.Eventually(t, func() bool {
		v, err := second.Request(t.Context(), budget.Claim{Target: "go-test", Invocation: "2:b"})
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
	_, err := worker.Request(t.Context(), budget.Claim{Target: "go-test", Slots: 4})
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

	tree, n, err := validationScope(t.Context(), sub, "plan/a")
	require.NoError(t, err)
	assert.Equal(t, "plan/a", tree, "a lease no row declares is its own tree")
	assert.Equal(t, 2, n)

	tree, n, err = validationScope(t.Context(), t.TempDir(), "w")
	require.NoError(t, err)
	assert.Equal(t, "w", tree)
	assert.Equal(t, 1, n, "outside a workspace the default holds")

	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("jobs:\n  max_validations: -1\n"), 0o644))
	_, _, err = validationScope(t.Context(), sub, "plan/a")
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

// startStepGroup starts what a step runs: a command leading its own process group. It
// ends by itself within 30 seconds whatever a test does.
func startStepGroup() (*exec.Cmd, error) {
	cmd := exec.Command("sleep", "30")
	InOwnGroup(cmd)
	return cmd, cmd.Start()
}

// stepGroup is startStepGroup for a test, with a channel closed once its leader is
// reaped. Unless it already exited, the test kills the group when it ends.
func stepGroup(t *testing.T) (int, <-chan struct{}) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("process groups are a unix thing")
	}
	cmd, err := startStepGroup()
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		select {
		case <-done:
		default:
			_ = SignalGroup(cmd.Process.Pid, true)
			<-done
		}
	})
	return cmd.Process.Pid, done
}

// exitsWithin reports whether done closes within d.
func exitsWithin(done <-chan struct{}, d time.Duration) bool {
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// orphanGroup starts what a step can leave behind: a leader that exits at once, and the
// background sleep it started, still running in the leader's process group. The test
// kills the sleep when it ends, and it ends by itself within 30 seconds anyway.
func orphanGroup(t *testing.T) int {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("a process group outliving its leader is a unix thing")
	}
	cmd := exec.Command("sh", "-c", "sleep 30 &")
	InOwnGroup(cmd)
	require.NoError(t, cmd.Run())
	pgid := cmd.Process.Pid
	t.Cleanup(func() {
		if groupAlive(pgid) {
			_ = SignalGroup(pgid, true)
		}
	})
	require.True(t, groupAlive(pgid), "the background sleep outlives its leader")
	return pgid
}

// holdAGroupUntilKilled claims a slot, starts a step group under the claim, prints the
// group's id, and holds both until stdin closes. The test kills it first; a test binary
// that dies closes the pipe, so this process never outlives it.
func holdAGroupUntilKilled(addr string) int {
	ctx := types.WithWorkspace(context.Background(), rootedWorkspace{root: os.Getenv(groupRootEnv)})
	c, err := Dial(ctx, addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	v, err := c.Request(ctx, budget.Claim{Project: ".", Target: "go-test", Slots: 1})
	if err != nil || !v.Granted {
		fmt.Fprintln(os.Stderr, "claim not granted", err)
		return 2
	}
	cmd, err := startStepGroup()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	pgid := cmd.Process.Pid
	if _, err := c.HoldGroup(ctx, v.ID, pgid, "sleep 30"); err != nil {
		_ = SignalGroup(pgid, true)
		_ = cmd.Wait()
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
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%s", groupHolderEnv, addr), fmt.Sprintf("%s=%s", groupRootEnv, root))
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
	// Not this test's child, so it cannot reap it; it kills the group only while the
	// group is still the one the holder printed.
	printed := time.Now()
	t.Cleanup(func() {
		if checkLeader(pgid, printed) == nil {
			_ = SignalGroup(pgid, true)
		}
	})
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
		fold, err := sessionstore.ReadAll(t.Context(), dir)
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
	pgid, done := stepGroup(t)

	v, err := c.Request(t.Context(), budget.Claim{Project: ".", Target: "go-test", Slots: 1})
	require.NoError(t, err)
	_, err = c.HoldGroup(t.Context(), v.ID, pgid, "sleep 30")
	require.NoError(t, err)
	r, err := c.Work(t.Context())
	require.NoError(t, err)
	require.Len(t, r.Running, 1, "the group is part of its run, not work of its own")
	assert.Equal(t, WorkRun, r.Running[0].Kind)
	require.True(t, groupAlive(pgid), "a held group runs until its claim ends")

	c.Release(t.Context(), v.ID)
	require.True(t, exitsWithin(done, 5*time.Second), "the group ends with its claim")
	r, err = c.Work(t.Context())
	require.NoError(t, err)
	require.Len(t, r.Ended, 1)
	assert.Contains(t, r.Ended[0].Reason, "outlived the step whose claim it rode")
}

// A step releases its group once the leader ends, before its claim, and the claim's
// release then signals nothing: by then the group's id may name another group.
func TestAReleasedStepGroupIsLeftAlone(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(1000, 4))
	c := dial(t, addr)
	pgid, _ := stepGroup(t)

	v, err := c.Request(t.Context(), budget.Claim{Project: ".", Target: "go-test", Slots: 1})
	require.NoError(t, err)
	id, err := c.HoldGroup(t.Context(), v.ID, pgid, "sleep 30")
	require.NoError(t, err)
	require.NoError(t, c.ReleaseHold(t.Context(), id))
	c.Release(t.Context(), v.ID)
	require.NoError(t, c.Close())

	assert.Eventually(t, func() bool { return len(holders(t, addr)) == 0 }, 2*time.Second, 10*time.Millisecond)
	r, err := QueryWork(t.Context(), addr)
	require.NoError(t, err)
	assert.Empty(t, r.Ended)
	assert.True(t, groupAlive(pgid), "a released group is never stopped")
}

// A group rides its claim onto the broker that replaces the one it was handed to, so a
// run that dies after a broker restart still takes its step's group with it.
func TestAStepGroupRidesItsClaimOntoANewBroker(t *testing.T) {
	old := redialEvery
	redialEvery = 20 * time.Millisecond
	t.Cleanup(func() { redialEvery = old })

	addr := testAddr(t)
	stopFirst, _ := serve(t, addr, WithCapacity(1000, 4))
	c := NewClient(addr)
	t.Cleanup(func() { _ = c.Close() })
	pgid, done := stepGroup(t)
	v, err := c.Request(t.Context(), budget.Claim{Project: ".", Target: "go-test", Slots: 1})
	require.NoError(t, err)
	_, err = c.HoldGroup(t.Context(), v.ID, pgid, "sleep 30")
	require.NoError(t, err)

	first := c.live()
	stopFirst()
	serve(t, addr, WithCapacity(1000, 4))
	require.Eventually(t, func() bool {
		cn := c.live()
		return cn != nil && cn != first
	}, 3*time.Second, 20*time.Millisecond, "the client reconnects on its own")
	require.False(t, exitsWithin(done, 100*time.Millisecond), "a stopping broker leaves the group to its run")

	require.NoError(t, c.Close())
	require.True(t, exitsWithin(done, 5*time.Second), "the new broker ends the group with the run")
	var r WorkReport
	require.Eventually(t, func() bool {
		r, err = QueryWork(t.Context(), addr)
		return err == nil && len(r.Ended) > 0
	}, 5*time.Second, 20*time.Millisecond)
	require.Len(t, r.Ended, 1)
	assert.Contains(t, r.Ended[0].Reason, "outlived the magus run whose step started it")
}

// A stale hold whose group emptied names whatever group takes the id next. That group's
// leader started after the hold, so the claim's release leaves it alone.
func TestAGroupThatTookAHeldIdIsLeftAlone(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(1000, 4))
	c := dial(t, addr)
	pgid, done := stepGroup(t)

	v, err := c.Request(t.Context(), budget.Claim{Project: ".", Target: "go-test", Slots: 1})
	require.NoError(t, err)
	_, err = c.Hold(t.Context(), Hold{Claim: v.ID, PGID: pgid, Command: "sleep 30", Held: time.Now().Add(-time.Hour)})
	require.NoError(t, err)

	c.Release(t.Context(), v.ID)
	assert.False(t, exitsWithin(done, 500*time.Millisecond), "the group that took the id runs on")
	r, err := c.Work(t.Context())
	require.NoError(t, err)
	assert.Empty(t, r.Ended)
}

// A held group whose leader exited has no process left to say whose group it is, so the
// broker leaves it running and tells the workspace why.
func TestALeaderlessStepGroupIsLeftRunning(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(1000, 4))
	c := dial(t, addr)
	root := t.TempDir()
	pgid := orphanGroup(t)

	v, err := c.Request(t.Context(), budget.Claim{Project: ".", Target: "go-test", Slots: 1})
	require.NoError(t, err)
	_, err = c.HoldGroup(types.WithWorkspace(t.Context(), rootedWorkspace{root: root}), v.ID, pgid, "sleep 30")
	require.NoError(t, err)

	c.Release(t.Context(), v.ID)
	r, err := c.Work(t.Context())
	require.NoError(t, err)
	assert.Empty(t, r.Ended)
	assert.True(t, groupAlive(pgid))

	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	fold, err := sessionstore.ReadAll(t.Context(), dir)
	require.NoError(t, err)
	queue := sessions.AttentionQueue(fold)
	require.Len(t, queue, 1)
	assert.Contains(t, queue[0].Message, "outlived the step whose claim it rode, but its leader has exited")
}

// A group rides only a claim its own connection holds.
func TestAStepGroupNeedsAClaimOfItsConnection(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(1000, 4))
	c := dial(t, addr)
	other := dial(t, addr)
	v, err := other.Request(t.Context(), budget.Claim{Project: ".", Target: "go-test", Slots: 1})
	require.NoError(t, err)

	_, err = c.HoldGroup(t.Context(), v.ID, 4242, "sleep 30")
	require.ErrorContains(t, err, "holds no claim")
	_, err = c.Hold(t.Context(), Hold{Claim: v.ID, PGID: 4242, Command: "sleep 30"})
	require.ErrorContains(t, err, "which this connection does not hold")
}

// A magus under a held command never competes with the hold's claim.
func TestARunUnderAHeldCommandSharesItsClaim(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(0, 1))
	wrapper := "1:" + types.WrapperInvocation("exec")
	cmd, _ := startGroup(t)
	_, err := dial(t, addr).Hold(t.Context(), Hold{
		PGID: cmd.Process.Pid, Command: "magus job fork", Slots: 1, Deadline: time.Now().Add(time.Hour),
		Invocation: wrapper,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	v, err := dial(t, addr).Request(ctx, budget.Claim{
		Project: "libs/x", Target: "install", Slots: 1, PID: 2, Invocation: "2:run", Ancestors: []string{wrapper},
	})
	require.NoError(t, err)
	assert.True(t, v.Granted, "the only slot is its parent's")
}

// A hold shares its slots only: a budget strangers filled refuses its child.
func TestARunUnderAHeldCommandGetsNoSeatOnAFullBudget(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(1000, 4))
	wrapper := "1:" + types.WrapperInvocation("exec")
	stranger, err := dial(t, addr).Request(t.Context(), budget.Claim{Project: "other", Target: "test", MemoryMB: 900, PID: 3})
	require.NoError(t, err)
	require.True(t, stranger.Granted)
	cmd, _ := startGroup(t)
	_, err = dial(t, addr).Hold(t.Context(), Hold{
		PGID: cmd.Process.Pid, Command: "magus run test .", Slots: 1, Deadline: time.Now().Add(time.Hour),
		Invocation: wrapper,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	v, err := dial(t, addr).Request(ctx, budget.Claim{
		Project: "libs/x", Target: "test", MemoryMB: 500, PID: 2, Invocation: "2:run", Ancestors: []string{wrapper},
	})
	require.NoError(t, err)
	assert.False(t, v.Granted, "it would hold 1400 MB of a 1000 MB budget")
	assert.True(t, v.Fits)
}

// JobStopSuite pins `magus job stop`: it signals only the groups the broker recorded for
// the job, frees their claims and seats, and refuses what it cannot prove it recorded.
type JobStopSuite struct {
	suite.Suite
	addr string
}

func TestJobStopSuite(t *testing.T) { suite.Run(t, new(JobStopSuite)) }

func (s *JobStopSuite) SetupTest() {
	if runtime.GOOS == "windows" {
		s.T().Skip("process groups are a unix thing")
	}
	s.addr = testAddr(s.T())
	serve(s.T(), s.addr, WithCapacity(1000, 4))
}

// seated is a client of job lease in a tree that seats one run, holding a claim and a
// step group riding it.
func (s *JobStopSuite) seated(lease, tree string) (int, <-chan struct{}) {
	t := s.T()
	c := leasedClient(t, s.addr, lease, tree, 1)
	v, err := c.Request(t.Context(), budget.Claim{Project: ".", Target: "go-test", Slots: 1, Invocation: "1:" + lease})
	s.Require().NoError(err)
	s.Require().True(v.Granted)
	pgid, done := stepGroup(t)
	_, err = c.HoldGroup(t.Context(), v.ID, pgid, "sleep 30")
	s.Require().NoError(err)
	return pgid, done
}

func (s *JobStopSuite) TestWorkNamesEachSeatsHolderAndItsGroups() {
	pgid, _ := s.seated("plan/a", "plan")
	r, err := QueryWork(s.T().Context(), s.addr)
	s.Require().NoError(err)
	s.Require().Len(r.Seats, 1)
	seat := r.Seats[0]
	s.False(seat.Since.IsZero())
	s.Require().Len(seat.Groups, 1)
	s.False(seat.Groups[0].Held.IsZero())
	seat.Since, seat.Groups[0].Held = time.Time{}, time.Time{}
	s.Equal(Seat{
		Tree: "plan", Lease: "plan/a", PID: os.Getpid(), PIDAlive: true,
		Groups: []Group{{PGID: pgid, Lease: "plan/a", Command: "sleep 30", Alive: true}},
	}, seat)
	s.False(seat.Dead())
	s.Require().Len(r.Groups, 1)
	s.Equal(pgid, r.Groups[0].PGID)
}

func (s *JobStopSuite) TestStopsTheJobsGroupAndFreesItsSeatAndClaim() {
	pgid, done := s.seated("plan/a", "plan")
	otherPGID, otherDone := s.seated("other", "other")
	waiting := leasedClient(s.T(), s.addr, "plan/b", "plan", 1)
	v, err := waiting.Request(s.T().Context(), budget.Claim{Target: "go-test", Invocation: "2:b"})
	s.Require().NoError(err)
	s.Require().False(v.Granted, "the tree's one seat is held")

	stop, err := StopJob(s.T().Context(), s.addr, "plan/a")
	s.Require().NoError(err)
	s.Require().Len(stop.Stopped, 1)
	stop.Stopped[0].Held, stop.Stopped[0].Alive = time.Time{}, false
	s.Equal(JobStop{Lease: "plan/a", Stopped: []Group{{PGID: pgid, Lease: "plan/a", Command: "sleep 30"}}, Seats: 1}, stop)
	s.True(exitsWithin(done, 5*time.Second), "the recorded group ends")
	s.False(exitsWithin(otherDone, 200*time.Millisecond), "another job's group is never signaled")
	s.True(groupAlive(otherPGID))

	v, err = waiting.Request(s.T().Context(), budget.Claim{Target: "go-test", Invocation: "2:b"})
	s.Require().NoError(err)
	s.True(v.Granted, "the freed seat passes on")
	r, err := QueryWork(s.T().Context(), s.addr)
	s.Require().NoError(err)
	s.Require().Len(r.Ended, 1)
	s.Equal(jobStopReason, r.Ended[0].Reason)
	for _, g := range r.Groups {
		s.NotEqual(pgid, g.PGID, "the stopped group is no longer recorded")
	}
}

func (s *JobStopSuite) TestFreesTheSeatOfAGroupAlreadyGone() {
	pgid, done := s.seated("plan/a", "plan")
	s.Require().NoError(SignalGroup(pgid, true))
	s.Require().True(exitsWithin(done, 5*time.Second))

	stop, err := StopJob(s.T().Context(), s.addr, "plan/a")
	s.Require().NoError(err)
	s.Require().Len(stop.Gone, 1)
	stop.Gone[0].Held = time.Time{}
	s.Equal(JobStop{Lease: "plan/a", Gone: []Group{{PGID: pgid, Lease: "plan/a", Command: "sleep 30"}}, Seats: 1}, stop)
	r, err := QueryWork(s.T().Context(), s.addr)
	s.Require().NoError(err)
	s.Empty(r.Seats)
	s.Empty(r.Ended, "a gone group is released, never signaled")
}

func (s *JobStopSuite) TestRefusesAJobWithNoRecordedGroup() {
	c := leasedClient(s.T(), s.addr, "plan/a", "plan", 1)
	_, err := c.Request(s.T().Context(), budget.Claim{Target: "go-test", Invocation: "1:a"})
	s.Require().NoError(err)

	_, err = StopJob(s.T().Context(), s.addr, "plan/a")
	var refusal *Error
	s.Require().ErrorAs(err, &refusal)
	s.Equal(CodeNoGroup, refusal.Code)
	s.Contains(refusal.Message, "job plan/a holds no process group")
	r, err := QueryWork(s.T().Context(), s.addr)
	s.Require().NoError(err)
	s.Len(r.Seats, 1, "a refused stop frees nothing")
}

func (s *JobStopSuite) TestRefusesAGroupThatTookTheRecordedId() {
	c := leasedClient(s.T(), s.addr, "plan/a", "plan", 1)
	v, err := c.Request(s.T().Context(), budget.Claim{Project: ".", Target: "go-test", Slots: 1, Invocation: "1:a"})
	s.Require().NoError(err)
	pgid, done := stepGroup(s.T())
	_, err = c.Hold(s.T().Context(), Hold{Claim: v.ID, PGID: pgid, Command: "sleep 30", Held: time.Now().Add(-time.Hour)})
	s.Require().NoError(err)

	_, err = StopJob(s.T().Context(), s.addr, "plan/a")
	var refusal *Error
	s.Require().ErrorAs(err, &refusal)
	s.Equal(CodeGroupChanged, refusal.Code)
	s.Contains(refusal.Message, "another group took its id")
	s.Contains(refusal.Message, "signaled nothing")
	s.False(exitsWithin(done, 300*time.Millisecond), "the group that took the id runs on")
	r, err := QueryWork(s.T().Context(), s.addr)
	s.Require().NoError(err)
	s.Len(r.Seats, 1)
}

func (s *JobStopSuite) TestASeatIsDeadOnlyOnceEverythingItRecordedIsGone() {
	gone := []Group{{PGID: 7}}
	s.True(Seat{Groups: gone}.Dead())
	s.False(Seat{Groups: gone, PIDAlive: true}.Dead(), "its magus still runs")
	s.False(Seat{Groups: []Group{{PGID: 7}, {PGID: 8, Alive: true}}}.Dead())
	s.False(Seat{}.Dead(), "a seat with no recorded group is nothing job stop answers for")
}
