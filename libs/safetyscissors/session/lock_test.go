package session

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
)

type LoadLockSuite struct{ suite.Suite }

func TestLoadLock(t *testing.T) { suite.Run(t, new(LoadLockSuite)) }

// A load blocked behind another loader gives up when its caller does, rather than
// sleeping out the two-minute bound after the caller has gone.
func (s *LoadLockSuite) TestWaitEndsWithCaller() {
	dir := s.T().TempDir()
	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- codec.WithLock(context.Background(), filepath.Join(dir, loadLockName), time.Second, func() error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	defer func() {
		close(release)
		s.Require().NoError(<-done)
	}()

	ctx, cancel := context.WithTimeout(s.T().Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := LoadEvents(ctx, testConfig(s.T(), dir, "", InvocationStart{}), []LoadEvent{loadable("s1", "h1", "r1", 10)})
	s.Require().ErrorIs(err, context.DeadlineExceeded)
	s.Less(time.Since(start), 10*time.Second)
}
