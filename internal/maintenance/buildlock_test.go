package maintenance

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Two acquisitions in one process exclude each other, the case of a manual build and the
// server's in-process sync-graph job: the second waits, names the first, and gets the lock
// once the first releases it.
func TestAcquireGraphBuildSerializesTwoBuilds(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "knowledge")
	server := GraphBuildHolder{PID: os.Getpid(), Started: time.Now(), By: "the server's sync-graph job"}
	manual := GraphBuildHolder{PID: os.Getpid(), Started: time.Now(), By: "`magus graph build`"}

	var quiet bytes.Buffer
	first, waited, err := AcquireGraphBuild(t.Context(), dir, server, &quiet, time.Millisecond)
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
		l, w, err := AcquireGraphBuild(t.Context(), dir, manual, &notices, time.Millisecond)
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
	data, err := json.Marshal(GraphBuildHolder{PID: dead, Started: time.Now().Add(-time.Hour), By: "`magus graph build`"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, graphBuildHolderFile), data, 0o600))

	_, ok := RunningGraphBuild(dir)
	assert.False(t, ok, "a dead holder is not a running build")

	var notices bytes.Buffer
	lock, waited, err := AcquireGraphBuild(t.Context(), dir, GraphBuildHolder{PID: os.Getpid(), Started: time.Now(), By: "`magus graph build`"}, &notices, time.Millisecond)
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
	held, _, err := AcquireGraphBuild(t.Context(), dir, GraphBuildHolder{PID: os.Getpid(), Started: time.Now(), By: "`magus graph build`"}, &bytes.Buffer{}, time.Millisecond)
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Release() })

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, _, err = AcquireGraphBuild(ctx, dir, GraphBuildHolder{PID: os.Getpid(), By: "the server's sync-graph job"}, &bytes.Buffer{}, time.Millisecond)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, err.Error(), "stopped waiting for `magus graph build`")
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
