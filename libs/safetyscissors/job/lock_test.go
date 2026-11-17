package job

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type LockSuite struct {
	suite.Suite
}

func TestLockSuite(t *testing.T) { suite.Run(t, new(LockSuite)) }

// A writer that gives up on the lock names the process holding it, so the person reading
// the error can find it rather than guess which of a dozen processes is stuck.
func (s *LockSuite) TestALockWaitNamesTheHolder() {
	dir := s.T().TempDir()
	holder, err := Open(dir, writable()...)
	s.Require().NoError(err)
	waiter, err := Open(dir, writable()...)
	s.Require().NoError(err)
	taken, release := make(chan struct{}), make(chan struct{})
	done := make(chan error)
	go func() {
		done <- holder.locked(s.T().Context(), func() error {
			close(taken)
			<-release
			return nil
		})
	}()
	<-taken

	ctx, cancel := context.WithTimeout(s.T().Context(), 50*time.Millisecond)
	defer cancel()
	_, err = waiter.Update(ctx, "blocked", func(*Job) {})
	close(release)
	s.Require().NoError(<-done)

	s.Require().Error(err)
	s.Contains(err.Error(), fmt.Sprintf("the holder is pid %d, which took it", os.Getpid()))
	s.NotContains(err.Error(), os.Args[0])
}

func (s *LockSuite) TestReadLockHolderNamesARecordedHolder() {
	path := filepath.Join(s.T().TempDir(), "jobs.jsonl.lock")
	s.Require().NoError(os.WriteFile(path, []byte("42 1000000\n"), 0o600))

	s.Equal("pid 42, which took it 1m40s ago", readLockHolder(path, time.Unix(1_000_100, 0)))
}

func (s *LockSuite) TestReadLockHolderNamesNoneForARecordItCannotRead() {
	path := filepath.Join(s.T().TempDir(), "jobs.jsonl.lock")
	for _, content := range []string{"", "not-a-pid 1000000\n", "42\n", "42 1000000 magus job fork x\n"} {
		s.Require().NoError(os.WriteFile(path, []byte(content), 0o600))
		s.Empty(readLockHolder(path, time.Unix(1_000_100, 0)), "content %q", content)
	}
}
