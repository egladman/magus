//go:build unix

package guard

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/trail"
)

// The 10-13 lapses stalled in the job store's read, which sweeps it and can run the VCS
// once per row: every hook ran out after its load_rules phase with no phase named. The read
// is now a timed phase bounded by the deadline: a store that never answers pins the
// deadline's error, records job_rows lapsed, answers a second read at once, and reaches the
// workspace rules as a lapse. A FIFO with no writer stands in for the stalled store.
func TestJobRowsReadIsBoundedByTheDeadline(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	at := hookLocation(ctx, Dependencies{})
	path, err := job.NewStore(job.Location{CacheDir: cacheDir, Root: at.workspace}).Path()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, syscall.Mkfifo(path, 0o644))
	t.Cleanup(func() {
		// A writer that opens and closes ends the stalled read with EOF.
		if w, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = w.Close()
		}
	})

	ctx = withPhaseLog(ctx, trail.Phase{})
	ctx, cancel := context.WithTimeout(ctx, judgeReserve+200*time.Millisecond)
	defer cancel()
	start := time.Now()
	pinned := withJobStoreRows(ctx, at)
	assert.Less(t, time.Since(start), time.Second, "bounded by the deadline less the reserve")
	snap, ok := job.SnapshotFromContext(pinned)
	require.True(t, ok)
	require.ErrorIs(t, snap.Err, context.DeadlineExceeded)
	phase, ok := judgmentPhases(ctx).lapsedPhase(phaseJobRows)
	require.True(t, ok, "recorded as the lapsed job_rows phase")
	assert.Positive(t, phase.BudgetMs)

	start = time.Now()
	snap, _ = job.SnapshotFromContext(withJobStoreRows(ctx, at))
	assert.Less(t, time.Since(start), 50*time.Millisecond, "a second read answers at once")
	assert.ErrorIs(t, snap.Err, context.DeadlineExceeded)

	asked := askWorkspaceRules(ctx, seamCommand, Dependencies{}, nil, nil)
	require.NotNil(t, asked.lapse)
	assert.Contains(t, asked.lapse.what(), "reading the job store did not finish within")
}
