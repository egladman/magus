package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/maintenance"
	"github.com/egladman/magus/types"
)

// holdMachineRefresh is a workspace root, and another checkout's refresh
// holding this machine's refresh lock for the test.
func holdMachineRefresh(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), nil, 0o644))
	lock, err := maintenance.TakeRefreshLock(t.Context(), maintenance.MachineRefreshLock(),
		maintenance.GraphBuildHolder{PID: os.Getpid(), Started: time.Now(), By: "the symbol refresh of /other"}, "magus refs", false, io.Discard, time.Millisecond)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })
	return root
}

func TestStaleReadFindsTheMachinesRefreshAndStartsNothing(t *testing.T) {
	root := holdMachineRefresh(t)
	ans := types.KnowledgeAnswer{Verdict: types.VerdictFound, StaleIndexes: []string{"."}}

	cause := refreshInBackground(t.Context(), root, ans, "magus refs Foo --refresh")

	require.NotNil(t, cause)
	assert.Contains(t, cause.Why, "this read started no refresh")
	assert.Contains(t, cause.Why, fmt.Sprintf("the symbol refresh of /other (pid %d", os.Getpid()))
	assert.Contains(t, cause.Hint, "magus refs Foo --refresh")
}

func TestDetachedRefreshExitsWhenTheMachineLockIsHeld(t *testing.T) {
	root := holdMachineRefresh(t)
	t.Setenv(detachedRefreshEnv, "1")

	assert.True(t, refreshIndexesNow(t.Context(), root, "magus refs"), "it must not go on to answer a read")
	assert.Empty(t, os.Getenv(detachedRefreshEnv), "a run it starts is not the detached process")
}

func TestForegroundRefreshWaitsOnTheMachineLockThenStillAnswers(t *testing.T) {
	root := holdMachineRefresh(t)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	assert.False(t, refreshIndexesNow(ctx, root, "magus refs"), "a reader is waiting on the answer")
	assert.ErrorIs(t, ctx.Err(), context.DeadlineExceeded, "it waited for the lock until ctx ended")
}
