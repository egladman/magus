package maintenance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/types"
)

// Two acquisitions in one process exclude each other, the case of a manual build and the
// server's in-process sync-graph job: the second waits, names the first, and gets the lock
// once the first releases it.
func TestAcquireGraphBuildSerializesTwoBuilds(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "knowledge")
	server := GraphBuildHolder{PID: os.Getpid(), Started: time.Now(), By: "the server's sync-graph job"}
	manual := GraphBuildHolder{PID: os.Getpid(), Started: time.Now(), By: "`magus graph build`"}

	var quiet bytes.Buffer
	first, waited, err := acquireGraphBuild(t.Context(), dir, server, &quiet, time.Millisecond)
	require.NoError(t, err)
	assert.Nil(t, waited)
	assert.Empty(t, quiet.String(), "a free lock says nothing")
	running, ok := RunningGraphBuild(dir)
	require.True(t, ok)
	assert.Equal(t, server.By, running.By)

	type result struct {
		lock   *GraphBuildLock
		waited *GraphBuildHolder
		err    error
	}
	var notices syncBuffer
	second := make(chan result, 1)
	go func() {
		l, w, err := acquireGraphBuild(t.Context(), dir, manual, &notices, time.Millisecond)
		second <- result{l, w, err}
	}()
	select {
	case r := <-second:
		t.Fatalf("the second build got the lock while the first held it: %+v", r)
	case <-time.After(50 * time.Millisecond):
	}
	assert.Contains(t, notices.String(), "waiting for the server's sync-graph job (pid ")

	require.NoError(t, first.Release())
	var r result
	select {
	case r = <-second:
	case <-time.After(5 * time.Second):
		t.Fatal("the second build never got the lock")
	}
	require.NoError(t, r.err)
	require.NotNil(t, r.waited)
	assert.Equal(t, server.By, r.waited.By, "the waiter learns whose build it can load from")
	assert.NotContains(t, notices.String(), "took over", "a released lock leaves no record to reclaim")
	running, ok = RunningGraphBuild(dir)
	require.True(t, ok)
	assert.Equal(t, manual.By, running.By)

	require.NoError(t, r.lock.Release())
	_, ok = RunningGraphBuild(dir)
	assert.False(t, ok)
}

// A build killed while it held the lock leaves its record and no lock: the kernel dropped
// the flock with the process. The next build takes over at once, saying whose it was.
func TestAcquireGraphBuildReclaimsAStaleLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "knowledge")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	dead := deadPID(t)
	data, err := codec.Marshal(GraphBuildHolder{PID: dead, Started: time.Now().Add(-time.Hour), By: "`magus graph build`"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, graphBuildHolderFile), data, 0o600))

	_, ok := RunningGraphBuild(dir)
	assert.False(t, ok, "a dead holder is not a running build")

	var notices bytes.Buffer
	lock, waited, err := acquireGraphBuild(t.Context(), dir, GraphBuildHolder{PID: os.Getpid(), Started: time.Now(), By: "`magus graph build`"}, &notices, time.Millisecond)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })
	assert.Nil(t, waited, "nothing held the lock, so nothing was waited on")
	assert.Contains(t, notices.String(), "took over the build lock from `magus graph build` (pid ")
	assert.Contains(t, notices.String(), "which ended without releasing it")
	running, ok := RunningGraphBuild(dir)
	require.True(t, ok)
	assert.Equal(t, os.Getpid(), running.PID)
}

func TestAcquireGraphBuildStopsWaitingWhenCancelled(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "knowledge")
	held, _, err := acquireGraphBuild(t.Context(), dir, GraphBuildHolder{PID: os.Getpid(), Started: time.Now(), By: "`magus graph build`"}, &bytes.Buffer{}, time.Millisecond)
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Release() })

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, _, err = acquireGraphBuild(ctx, dir, GraphBuildHolder{PID: os.Getpid(), By: "the server's sync-graph job"}, &bytes.Buffer{}, time.Millisecond)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, err.Error(), "stopped waiting for `magus graph build`")
}

func TestTryGraphBuildNamesTheHolderAndTakesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "knowledge")
	first := GraphBuildHolder{PID: os.Getpid(), Started: time.Now(), By: "the first"}
	held, busy, err := tryGraphBuild(dir, first, &bytes.Buffer{})
	require.NoError(t, err)
	require.NotNil(t, held)
	assert.Nil(t, busy)

	var notices bytes.Buffer
	lock, busy, err := tryGraphBuild(dir, GraphBuildHolder{PID: os.Getpid(), By: "the second"}, &notices)
	require.NoError(t, err)
	assert.Nil(t, lock)
	require.NotNil(t, busy)
	assert.Equal(t, "the first", busy.By)
	assert.Empty(t, notices.String(), "stopping is the caller's to say")
	running, ok := RunningGraphBuild(dir)
	require.True(t, ok)
	assert.Equal(t, "the first", running.By, "a refused try leaves the holder's record")

	require.NoError(t, held.Release())
	lock, busy, err = tryGraphBuild(dir, GraphBuildHolder{PID: os.Getpid(), By: "the second"}, &notices)
	require.NoError(t, err)
	assert.Nil(t, busy)
	require.NoError(t, lock.Release())
}

func TestMachineGraphBuildIsOnePlaceForEveryCheckout(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	me := GraphBuildHolder{PID: os.Getpid(), Started: time.Now(), By: "the first"}
	lock, _, err := TryMachineGraphBuild(me, &bytes.Buffer{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })

	_, busy, err := TryMachineGraphBuild(GraphBuildHolder{PID: os.Getpid(), By: "the second"}, &bytes.Buffer{})
	require.NoError(t, err)
	require.NotNil(t, busy)
	assert.Equal(t, me.By, busy.By)

	running, ok, err := RunningMachineGraphBuild()
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, me.By, running.By)
	assert.Equal(t, filepath.Join(state, "magus", "graph-build"), filepath.Dir(lock.holderPath))
}

func TestGraphBuildHolderRecordsItsStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "machine")
	me := GraphBuildHolder{PID: os.Getpid(), Started: time.Now(), By: "the first", Store: "/ws/knowledge"}
	lock, _, err := tryGraphBuild(dir, me, &bytes.Buffer{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })

	_, busy, err := tryGraphBuild(dir, GraphBuildHolder{PID: os.Getpid(), By: "the second"}, &bytes.Buffer{})

	require.NoError(t, err)
	require.NotNil(t, busy)
	assert.Equal(t, "/ws/knowledge", busy.Store)
}

// deadPID returns the pid of a process that has exited and been reaped.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	require.NoError(t, cmd.Run())
	return cmd.Process.Pid
}

// syncBuffer is a bytes.Buffer safe to read while another goroutine writes it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestAcquireGraphBuildsWaitsOnAnotherCheckoutsMachineLock(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	lock, _, err := acquireMachineGraphBuild(t.Context(),
		GraphBuildHolder{PID: os.Getpid(), Started: time.Now(), By: "the symbol refresh of /other"}, io.Discard, time.Millisecond)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })
	store := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	_, _, err = AcquireGraphBuilds(ctx, "/ws", store, io.Discard)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, err.Error(), "stopped waiting for the symbol refresh of /other")
	_, running := RunningGraphBuild(store)
	assert.False(t, running, "a waiter holds no store, so a read does not take it for a build")
}

// acquireBehindBuild runs AcquireGraphBuilds queued behind a holder of the machine lock
// that wrote ahead's store, and returns who it can load from.
func acquireBehindBuild(t *testing.T, aheadStore, store string) *GraphBuildHolder {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ahead := GraphBuildHolder{PID: os.Getpid(), Started: time.Now(), By: "the build ahead", Store: aheadStore}
	lock, _, err := acquireMachineGraphBuild(t.Context(), ahead, io.Discard, time.Millisecond)
	require.NoError(t, err)

	type result struct {
		release  func() error
		waitedOn *GraphBuildHolder
		err      error
	}
	got := make(chan result, 1)
	go func() {
		release, waitedOn, err := AcquireGraphBuilds(t.Context(), "/ws", store, io.Discard)
		got <- result{release, waitedOn, err}
	}()
	select {
	case r := <-got:
		t.Fatalf("it took the machine lock while another held it: %+v", r)
	case <-time.After(50 * time.Millisecond):
	}
	require.NoError(t, lock.Release())
	r := <-got
	require.NoError(t, r.err)
	require.NoError(t, r.release())
	return r.waitedOn
}

func TestAcquireGraphBuildsLoadsFromTheBuildAheadOnItsOwnStore(t *testing.T) {
	store := t.TempDir()

	waitedOn := acquireBehindBuild(t, store, store)

	require.NotNil(t, waitedOn)
	assert.Equal(t, "the build ahead", waitedOn.By)
}

func TestAcquireGraphBuildsStillBuildsBehindAnotherCheckout(t *testing.T) {
	waitedOn := acquireBehindBuild(t, "/other/knowledge", t.TempDir())

	assert.Nil(t, waitedOn)
}

// graph build exited 0 when the root's reindex was refused by the run it was nested in,
// and quiet hid the refusal, so the target that ran it read a stale index. A missing
// indexer must still not fail the build.
func TestGraphBuildFailsOnlyOnALockRefusal(t *testing.T) {
	refusal := fmt.Errorf("magus: %w", types.DiagnosticErrorf(types.ProjectLockHeldByAncestor,
		"project . is locked by the magus run this one is nested inside"))
	missing := errors.New("docs: exit status 127; install scip-typescript")

	require.NoError(t, reindexRefusals(missing), "a missing indexer stays a warning")

	got := reindexRefusals(errors.Join(missing, refusal))
	require.ErrorIs(t, got, types.ProjectLockHeldByAncestor)
	assert.NotContains(t, got.Error(), "scip-typescript", "only the refusal fails the build")

	require.ErrorIs(t, reindexRefusals(refusal), types.ProjectLockHeldByAncestor, "an unjoined refusal counts too")

	full := types.DiagnosticErrorf(types.MachineBudgetExhausted, "not starting scip: this machine's build budget is full")
	got = reindexRefusals(errors.Join(missing, full))
	require.ErrorIs(t, got, types.MachineBudgetExhausted, "a full budget fails the build")
	assert.NotContains(t, got.Error(), "scip-typescript", "only the refusal fails the build")
}

type GraphReindexPressureSuite struct {
	suite.Suite
}

func TestGraphReindexPressureSuite(t *testing.T) { suite.Run(t, new(GraphReindexPressureSuite)) }

func (s *GraphReindexPressureSuite) TestARefusedReindexExitsTempFail() {
	short := types.ExitError{Code: exit.TempFail, Err: types.DiagnosticErrorf(types.MemoryPressureHigh, "not starting scip: this machine is short of memory")}
	missing := errors.New("docs: exit status 127; install scip-typescript")
	got := reindexRefusals(errors.Join(missing, short))
	s.Require().ErrorIs(got, types.MemoryPressureHigh)
	s.NotContains(got.Error(), "scip-typescript", "only the refusal fails the build")
	s.Equal(exit.TempFail, exit.Code(got))
}
