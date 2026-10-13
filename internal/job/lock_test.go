package job

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A writer that gives up on the lock names the process holding it, so the person reading
// the error can find it rather than guess which of a dozen magus processes is stuck.
func TestALockWaitNamesTheHolder(t *testing.T) {
	t.Parallel()

	loc := tmpLoc(t, t.TempDir())
	holder, waiter := NewStore(loc), NewStore(loc)
	taken, release := make(chan struct{}), make(chan struct{})
	done := make(chan error)
	go func() {
		done <- holder.locked(context.Background(), func() error {
			close(taken)
			<-release
			return nil
		})
	}()
	<-taken

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err := waiter.Update(ctx, "blocked", func(*types.Job) {})
	close(release)
	require.NoError(t, <-done)

	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("the holder is pid %d (`%s", os.Getpid(), os.Args[0]))
}

func TestReadLockHolder(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "jobs.json.lock")
	now := time.Unix(1_000_100, 0)
	for _, tc := range []struct{ content, want string }{
		{"", ""},
		{"42 1000000 magus job fork x\n", "pid 42 (`magus job fork x`), which took it 1m40s ago"},
		{"not a pid 1000000 magus\n", ""},
		{"42 1000000\n", ""},
	} {
		require.NoError(t, os.WriteFile(path, []byte(tc.content), 0o600))
		assert.Equal(t, tc.want, readLockHolder(path, now), "content %q", tc.content)
	}
}
