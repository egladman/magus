package maintenance

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/libs/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// Production installs a resolver in main; the journal refuses to write without one.
func withSecrets(ctx context.Context) context.Context {
	return testkit.WithEnvResolver(ctx)
}

func TestBuildSchedule_SkipsDisabledAndResolvesArgv(t *testing.T) {
	m := config.Maintenance{
		RotateActivities: 24 * time.Hour,
		RotateLogs:       0, // disabled
		PrunePreserved:   24 * time.Hour,
		SyncGraph:        6 * time.Hour,
	}

	require.Equal(t, []scheduledJob{
		{argv: []string{"server", "rotate-activities"}, interval: 24 * time.Hour, action: "server rotate-activities"},
		{argv: []string{"server", "prune-preserved"}, interval: 24 * time.Hour, action: "server prune-preserved"},
		{argv: []string{"graph", "build"}, interval: 6 * time.Hour, action: "graph build"},
	}, buildSchedule(m))
}

func TestBuildSchedule_AllDisabledIsEmpty(t *testing.T) {
	require.Empty(t, buildSchedule(config.Maintenance{}))
}

func TestIsDue(t *testing.T) {
	dir := t.TempDir()
	j := scheduledJob{argv: []string{"server", "rotate-activities"}, interval: 24 * time.Hour, action: "server rotate-activities"}
	now := time.UnixMilli(1_000_000_000)
	due := func(j scheduledJob) bool {
		ok, err := isDue(t.Context(), dir, j, now)
		require.NoError(t, err)
		return ok
	}

	// Never run within the trail: due.
	require.True(t, due(j))

	// Ran 25h ago (start + duration = finish): interval elapsed, due.
	old := now.Add(-25 * time.Hour)
	ctx := withSecrets(t.Context())
	recorder.AppendCall(ctx, dir, journal.Call{Time: old.Add(-time.Second), Duration: time.Second, Kind: journal.CallKindJob, Name: "server rotate-activities", Outcome: journal.OutcomeOK})
	require.True(t, due(j))

	// A more recent run (1h ago) is newest, so LastRun returns it: not due.
	recent := now.Add(-time.Hour)
	recorder.AppendCall(ctx, dir, journal.Call{Time: recent.Add(-time.Second), Duration: time.Second, Kind: journal.CallKindJob, Name: "server rotate-activities", Outcome: journal.OutcomeOK})
	require.False(t, due(j))

	// A run of a DIFFERENT job does not satisfy this one.
	require.True(t, due(scheduledJob{argv: []string{"graph", "build"}, interval: time.Hour, action: "graph build"}))
}

// A log that does not read is not "never run": the job waits rather than
// running on a guess.
func TestIsDueReturnsALogThatDoesNotRead(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "activity", "events.jsonl"), 0o700))
	_, err := isDue(t.Context(), dir, scheduledJob{action: "graph build", interval: time.Hour}, time.Now())
	require.Error(t, err)
}
