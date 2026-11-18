package console

import (
	"context"
	"github.com/egladman/magus/types/daemon"
	"testing"
	"time"

	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/stretchr/testify/require"
)

// emit hands one record to the registry, as a run's sinks would, stamped at ms.
func emit[B journal.Body](reg *RunRegistry, inv string, ms int64, body B) {
	reg.Handle(context.Background(), journal.NewRecord(time.UnixMilli(ms), inv, body))
}

func TestRunRegistryFoldsTargetLifecycle(t *testing.T) {
	reg := NewRunRegistry()

	start := time.UnixMilli(1_000)
	execAt := time.UnixMilli(2_000)
	doneAt := time.UnixMilli(5_000)

	emit(reg, "inv1", start.UnixMilli(), journal.Started{Command: journal.Command{Trigger: journal.TriggerRun}})
	emit(reg, "inv1", execAt.UnixMilli(), journal.Exec{Project: "svc/api", Target: "build"})
	emit(reg, "inv1", doneAt.UnixMilli(), journal.Run{Project: "svc/api", Target: "build", Status: journal.StatusPass, Ref: "out1a2b3c", Duration: 3 * time.Second})

	got := reg.Snapshot()
	want := []daemon.Run{{
		Inv:       "inv1",
		Trigger:   string(journal.TriggerRun),
		StartedAt: start,
		Targets: []daemon.TargetRun{{
			Project:    "svc/api",
			Target:     "build",
			State:      daemon.TargetRunPassed,
			StartedAt:  execAt,
			EndedAt:    doneAt,
			OutputRef:  "out1a2b3c",
			DurationMs: 3_000,
		}},
	}}
	require.Equal(t, want, got)
}

// A running (not yet finished) target reports RUNNING with a start but no end/ref.
func TestRunRegistryRunningTarget(t *testing.T) {
	reg := NewRunRegistry()
	emit(reg, "inv1", 1_000, journal.Started{Command: journal.Command{Trigger: journal.TriggerAffected}})
	emit(reg, "inv1", 2_000, journal.Exec{Project: "svc/api", Target: "test"})

	got := reg.Snapshot()
	want := []daemon.Run{{
		Inv:       "inv1",
		Trigger:   string(journal.TriggerAffected),
		StartedAt: time.UnixMilli(1_000),
		Targets: []daemon.TargetRun{{
			Project:   "svc/api",
			Target:    "test",
			State:     daemon.TargetRunRunning,
			StartedAt: time.UnixMilli(2_000),
		}},
	}}
	require.Equal(t, want, got)
}

// A cache hit emits a result with no preceding exec: CACHED, start anchored to the result.
func TestRunRegistryCachedResultAnchorsStart(t *testing.T) {
	reg := NewRunRegistry()
	emit(reg, "inv1", 1_000, journal.Started{})
	emit(reg, "inv1", 4_000, journal.Run{Project: "svc/api", Target: "lint", Status: journal.StatusCached, Ref: "outcafe"})

	got := reg.Snapshot()
	require.Len(t, got, 1)
	require.Equal(t, []daemon.TargetRun{{
		Project:   "svc/api",
		Target:    "lint",
		State:     daemon.TargetRunCached,
		StartedAt: time.UnixMilli(4_000),
		EndedAt:   time.UnixMilli(4_000),
		OutputRef: "outcafe",
	}}, got[0].Targets)
}

func TestRunRegistryFailedResult(t *testing.T) {
	reg := NewRunRegistry()
	emit(reg, "inv1", 1_000, journal.Started{})
	emit(reg, "inv1", 2_000, journal.Exec{Project: "p", Target: "t"})
	emit(reg, "inv1", 3_000, journal.Run{Project: "p", Target: "t", Status: journal.StatusFail, Duration: time.Second})

	got := reg.Snapshot()
	require.Len(t, got, 1)
	require.Equal(t, daemon.TargetRunFailed, got[0].Targets[0].State)
}

// Multiple targets keep first-seen order within a run; multiple runs sort newest-started first.
func TestRunRegistryOrdering(t *testing.T) {
	reg := NewRunRegistry()

	emit(reg, "invOld", 1_000, journal.Started{})
	emit(reg, "invOld", 1_100, journal.Exec{Project: "p", Target: "a"})
	emit(reg, "invOld", 1_200, journal.Exec{Project: "p", Target: "b"})

	emit(reg, "invNew", 9_000, journal.Started{})
	emit(reg, "invNew", 9_100, journal.Exec{Project: "q", Target: "z"})

	got := reg.Snapshot()
	require.Len(t, got, 2)
	require.Equal(t, "invNew", got[0].Inv) // newest first
	require.Equal(t, "invOld", got[1].Inv)

	require.Equal(t, "a", got[1].Targets[0].Target)
	require.Equal(t, "b", got[1].Targets[1].Target)
}

// A later exec (a target running several subprocesses) must not regress a terminal result.
func TestRunRegistryResultNotRegressedByLaterExec(t *testing.T) {
	reg := NewRunRegistry()
	emit(reg, "inv1", 1_000, journal.Started{})
	emit(reg, "inv1", 2_000, journal.Exec{Project: "p", Target: "t"})
	emit(reg, "inv1", 3_000, journal.Run{Project: "p", Target: "t", Status: journal.StatusPass, Ref: "r"})
	emit(reg, "inv1", 4_000, journal.Exec{Project: "p", Target: "t"})

	got := reg.Snapshot()
	require.Equal(t, daemon.TargetRunPassed, got[0].Targets[0].State)
}

// A finished run lingers within the retention window, then is pruned.
func TestRunRegistryPrunesAfterRetention(t *testing.T) {
	reg := NewRunRegistry()
	now := time.UnixMilli(100_000)
	reg.retain = 10 * time.Second
	reg.nowFn = func() time.Time { return now }

	emit(reg, "inv1", 1_000, journal.Started{})
	emit(reg, "inv1", 2_000, journal.Run{Project: "p", Target: "t", Status: journal.StatusPass})
	emit(reg, "inv1", 3_000, journal.Finished{Status: journal.StatusPass})

	// Within retention: still visible.
	require.Len(t, reg.Snapshot(), 1)

	// Past retention: pruned.
	now = now.Add(11 * time.Second)
	require.Empty(t, reg.Snapshot())
}

// resultState maps known journal statuses onto explicit run states; an unrecognized status
// (a future journal status) maps to the zero/unspecified state, never silently to PASSED.
func TestResultState(t *testing.T) {
	require.Equal(t, daemon.TargetRunPassed, resultState(journal.StatusPass))
	require.Equal(t, daemon.TargetRunCached, resultState(journal.StatusCached))
	require.Equal(t, daemon.TargetRunPermitted, resultState(journal.StatusPermitted))
	require.Equal(t, daemon.TargetRunFailed, resultState(journal.StatusFail))
	require.Equal(t, daemon.TargetRunState(""), resultState("some-future-status"))
}

// An unfinished run that never emits KindFinished is evicted once its last event ages past
// the maxRunAge ceiling, so a crashed dispatch does not linger forever.
func TestRunRegistryPrunesStaleUnfinishedRun(t *testing.T) {
	reg := NewRunRegistry()
	now := time.UnixMilli(100_000)
	reg.nowFn = func() time.Time { return now }

	emit(reg, "inv1", 1_000, journal.Started{})
	emit(reg, "inv1", 2_000, journal.Exec{Project: "p", Target: "t"})

	// Still in-flight and recent: visible.
	require.Len(t, reg.Snapshot(), 1)

	// No KindFinished ever arrives; once silent past maxRunAge it is evicted anyway.
	now = now.Add(maxRunAge + time.Second)
	require.Empty(t, reg.Snapshot())
}

// Events with no invocation id cannot be attributed and are dropped.
func TestRunRegistryIgnoresEventsWithoutInv(t *testing.T) {
	reg := NewRunRegistry()
	emit(reg, "", 1_000, journal.Exec{Project: "p", Target: "t"})
	require.Empty(t, reg.Snapshot())
}
