package maintenance

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/trail"
)

func TestBuildSchedule_SkipsDisabledAndResolvesArgv(t *testing.T) {
	m := config.Maintenance{
		RotateActivities: 24 * time.Hour,
		RotateLogs:       0, // disabled
		SyncGraph:        6 * time.Hour,
	}

	require.Equal(t, []scheduledJob{
		{argv: []string{"server", "rotate-activities"}, interval: 24 * time.Hour, action: "server rotate-activities"},
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

	// Never run within the trail: due.
	require.True(t, isDue(dir, j, now))

	// Ran 25h ago (start + duration = finish): interval elapsed, due.
	old := now.Add(-25 * time.Hour)
	trail.Append(dir, trail.Event{Ts: old.Add(-time.Second).UnixMilli(), DurMs: 1000, Kind: trail.KindJob, Actor: "daemon", Action: "server rotate-activities", Outcome: trail.OutcomeOK})
	require.True(t, isDue(dir, j, now))

	// A more recent run (1h ago) is newest, so LastRun returns it: not due.
	recent := now.Add(-time.Hour)
	trail.Append(dir, trail.Event{Ts: recent.Add(-time.Second).UnixMilli(), DurMs: 1000, Kind: trail.KindJob, Actor: "daemon", Action: "server rotate-activities", Outcome: trail.OutcomeOK})
	require.False(t, isDue(dir, j, now))

	// A run of a DIFFERENT job does not satisfy this one.
	require.True(t, isDue(dir, scheduledJob{argv: []string{"graph", "build"}, interval: time.Hour, action: "graph build"}, now))
}
