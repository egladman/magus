package sessions

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/file"
)

// A load blocked behind another loader gives up when its caller does, rather than
// sleeping out the two-minute bound after the caller has gone.
func TestSessionLoadLockWaitEndsWithCaller(t *testing.T) {
	dir := t.TempDir()
	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- file.WithLock(context.Background(), filepath.Join(dir, loadLockName), time.Second, func() error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	defer func() {
		close(release)
		require.NoError(t, <-done)
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := LoadEvents(ctx, dir, []LoadEvent{loadable("s1", "h1", "r1", 10)}, InvocationStart{})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 10*time.Second)
}
