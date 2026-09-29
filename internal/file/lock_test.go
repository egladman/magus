package file

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hold takes the lock at path in a goroutine and keeps it until the returned release
// is called.
func hold(t *testing.T, path string) (release func()) {
	t.Helper()
	held := make(chan struct{})
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- WithLock(context.Background(), path, time.Second, func() error {
			close(held)
			<-stop
			return nil
		})
	}()
	<-held
	return func() {
		close(stop)
		require.NoError(t, <-done)
	}
}

func TestWithLockExcludes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "x.lock")
	release := hold(t, path)

	err := WithLock(t.Context(), path, 50*time.Millisecond, func() error {
		t.Error("fn ran while another holder had the lock")
		return nil
	})
	require.ErrorContains(t, err, "another process has held the lock")

	release()
	ran := false
	require.NoError(t, WithLock(t.Context(), path, time.Second, func() error {
		ran = true
		return nil
	}))
	assert.True(t, ran, "the lock is free once its holder returns")
}

// Cancellation is reported as the caller's, not blamed on a stuck holder, and it ends
// the wait long before the bound.
func TestWithLockHonorsCancel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	release := hold(t, path)
	defer release()

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := WithLock(ctx, path, time.Minute, func() error { return nil })
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 10*time.Second)
}

func TestWithLockReturnsFnError(t *testing.T) {
	boom := errors.New("boom")
	err := WithLock(t.Context(), filepath.Join(t.TempDir(), "x.lock"), time.Second, func() error { return boom })
	require.ErrorIs(t, err, boom)
}
