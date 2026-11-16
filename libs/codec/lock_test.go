package codec

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type LockSuite struct{ suite.Suite }

func TestLock(t *testing.T) { suite.Run(t, new(LockSuite)) }

// hold takes the lock at path in a goroutine and keeps it until the returned release
// is called.
func (s *LockSuite) hold(path string) (release func()) {
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
		require.NoError(s.T(), <-done)
	}
}

func (s *LockSuite) TestExcludes() {
	path := filepath.Join(s.T().TempDir(), "sub", "x.lock")
	release := s.hold(path)

	err := WithLock(s.T().Context(), path, 50*time.Millisecond, func() error {
		s.T().Error("fn ran while another holder had the lock")
		return nil
	})
	require.ErrorContains(s.T(), err, "another process has held the lock")

	release()
	ran := false
	require.NoError(s.T(), WithLock(s.T().Context(), path, time.Second, func() error {
		ran = true
		return nil
	}))
	assert.True(s.T(), ran, "the lock is free once its holder returns")
}

// Cancellation is reported as the caller's, not blamed on a stuck holder, and it ends
// the wait long before the bound.
func (s *LockSuite) TestHonorsCancel() {
	path := filepath.Join(s.T().TempDir(), "x.lock")
	release := s.hold(path)
	defer release()

	ctx, cancel := context.WithTimeout(s.T().Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := WithLock(ctx, path, time.Minute, func() error { return nil })
	require.ErrorIs(s.T(), err, context.DeadlineExceeded)
	assert.Less(s.T(), time.Since(start), 10*time.Second)
}

// A zero wait is one try: a held lock fails at once instead of polling.
func (s *LockSuite) TestZeroWaitTriesOnce() {
	path := filepath.Join(s.T().TempDir(), "x.lock")
	release := s.hold(path)
	defer release()

	start := time.Now()
	err := WithLock(s.T().Context(), path, 0, func() error {
		s.T().Error("fn ran while another holder had the lock")
		return nil
	})
	require.ErrorContains(s.T(), err, "another process has held the lock")
	assert.Less(s.T(), time.Since(start), time.Second)
}

func (s *LockSuite) TestReturnsTheFunctionsError() {
	boom := errors.New("boom")
	err := WithLock(s.T().Context(), filepath.Join(s.T().TempDir(), "x.lock"), time.Second, func() error { return boom })
	require.ErrorIs(s.T(), err, boom)
}
