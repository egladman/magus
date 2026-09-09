package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestYieldRunIsolationLetsExclusiveChildRun(t *testing.T) {
	ctx, releaseParent := acquireRunIsolation(WithRunScope(context.Background()), false)
	t.Cleanup(releaseParent)
	parentLease := isolationLeaseFrom(ctx)

	childAcquired := make(chan struct{})
	releaseChild := make(chan struct{})
	childDone := make(chan struct{})
	go func() {
		_, release := acquireRunIsolation(ctx, true)
		close(childAcquired)
		<-releaseChild
		release()
		close(childDone)
	}()

	err := YieldRunIsolation(ctx, func(yielded context.Context) error {
		if got := isolationLeaseFrom(yielded); got != nil {
			t.Errorf("yielded context retained lease: %#v", got)
		}
		select {
		case <-childAcquired:
		case <-time.After(time.Second):
			t.Error("exclusive child did not acquire isolation while parent yielded")
		}
		close(releaseChild)
		select {
		case <-childDone:
		case <-time.After(time.Second):
			t.Error("exclusive child did not release isolation")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("YieldRunIsolation: %v", err)
	}
	if got := isolationLeaseFrom(ctx); got != parentLease {
		t.Fatalf("parent lease after yield = %#v, want %#v", got, parentLease)
	}
}

// TestYieldRunIsolationKeepsAnExclusiveWriteLock proves the property without timing, and
// proves it against the half-fix as well as the bug.
//
// Removing the release alone would pass a test that only watches for overlap, then hang in
// production the first time a child tried to take a lease behind the parent's write lock.
// So this admits one INSIDE the yield: it must return immediately, and the write lock must
// still be held when it does.
func TestYieldRunIsolationKeepsAnExclusiveWriteLock(t *testing.T) {
	ctx := WithRunScope(context.Background())
	isolation := isolationFrom(ctx)

	ctx, release := acquireRunIsolation(ctx, true)
	defer release()

	var admitted bool
	require.NoError(t, YieldRunIsolation(ctx, func(child context.Context) error {
		// TryRLock ACQUIRES on success, so a bare assert.False would leak the read lock and
		// hang the deferred re-Lock instead of failing. Give it straight back.
		requireStillWriteLocked(t, isolation, "the write lock was released during the yield, so the step is not exclusive while it fans out")

		// The half-fix hangs here instead of returning.
		_, innerRelease := acquireRunIsolation(child, false)
		innerRelease()
		admitted = true

		// An EXCLUSIVE child is subsumed too, which is Step.Exclusive's own contract: it
		// excludes BATCH peers, and a needs child admitted through RunAside has no batch.
		// The ancestor's write lock already excludes every one of them. Pinned because the
		// alternative reading - nest a fresh region so it excludes its siblings - is the
		// change someone will otherwise make on the way past.
		_, exclusiveRelease := acquireRunIsolation(child, true)
		exclusiveRelease()
		requireStillWriteLocked(t, isolation, "an exclusive child inside the region took a lease of its own")

		requireStillWriteLocked(t, isolation, "a child inside the region took and released a lease of its own")
		return nil
	}))
	assert.True(t, admitted, "the yield never admitted a child")
}

// requireStillWriteLocked fails when isolation's write lock is not held, without keeping
// the read lock TryRLock takes on success.
func requireStillWriteLocked(t *testing.T, isolation *runIsolation, msg string) {
	t.Helper()
	if isolation.mu.TryRLock() {
		isolation.mu.RUnlock()
		t.Error(msg)
	}
}
