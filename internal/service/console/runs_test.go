package console

import (
	"context"
	"testing"
	"time"

	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/require"
)

// emit sends one event to the registry through the same slog capture path production uses:
// journal.Emit -> capture logger -> RunRegistry.Handle -> EventFromRecord -> fold.
func emit(reg *RunRegistry, inv string, e journal.Event) {
	ctx := journal.WithLogger(context.Background(), journal.NewLogger(reg))
	ctx = journal.WithInvocationID(ctx, inv)
	journal.Emit(ctx, e)
}

func TestRunRegistryFoldsTargetLifecycle(t *testing.T) {
	reg := NewRunRegistry()

	start := time.UnixMilli(1_000)
	execAt := time.UnixMilli(2_000)
	doneAt := time.UnixMilli(5_000)

	emit(reg, "inv1", journal.Event{Ts: start.UnixMilli(), Kind: journal.KindStarted, Command: &journal.Command{Trigger: journal.TriggerRun}})
	emit(reg, "inv1", journal.Event{Ts: execAt.UnixMilli(), Kind: journal.KindExec, Project: "svc/api", Target: "build"})
	emit(reg, "inv1", journal.Event{Ts: doneAt.UnixMilli(), Kind: journal.KindResult, Project: "svc/api", Target: "build", Status: journal.StatusPass, Ref: "ref1a2b3c", DurMs: 3_000})

	got := reg.Snapshot()
	want := []types.StatusRun{{
		Inv:       "inv1",
		Trigger:   journal.TriggerRun,
		StartedAt: start,
		Targets: []types.StatusTargetRun{{
			Project:    "svc/api",
			Target:     "build",
			State:      types.TargetRunPassed,
			StartedAt:  execAt,
			EndedAt:    doneAt,
			OutputRef:  "ref1a2b3c",
			DurationMs: 3_000,
		}},
	}}
	require.Equal(t, want, got)
}

// A running (not yet finished) target reports RUNNING with a start but no end/ref.
func TestRunRegistryRunningTarget(t *testing.T) {
	reg := NewRunRegistry()
	emit(reg, "inv1", journal.Event{Ts: 1_000, Kind: journal.KindStarted, Command: &journal.Command{Trigger: journal.TriggerAffected}})
	emit(reg, "inv1", journal.Event{Ts: 2_000, Kind: journal.KindExec, Project: "svc/api", Target: "test"})

	got := reg.Snapshot()
	want := []types.StatusRun{{
		Inv:       "inv1",
		Trigger:   journal.TriggerAffected,
		StartedAt: time.UnixMilli(1_000),
		Targets: []types.StatusTargetRun{{
			Project:   "svc/api",
			Target:    "test",
			State:     types.TargetRunRunning,
			StartedAt: time.UnixMilli(2_000),
		}},
	}}
	require.Equal(t, want, got)
}

// A cache hit emits a result with no preceding exec: CACHED, start anchored to the result.
func TestRunRegistryCachedResultAnchorsStart(t *testing.T) {
	reg := NewRunRegistry()
	emit(reg, "inv1", journal.Event{Ts: 1_000, Kind: journal.KindStarted})
	emit(reg, "inv1", journal.Event{Ts: 4_000, Kind: journal.KindResult, Project: "svc/api", Target: "lint", Status: journal.StatusCached, Ref: "refcafe", DurMs: 0})

	got := reg.Snapshot()
	require.Len(t, got, 1)
	require.Equal(t, []types.StatusTargetRun{{
		Project:   "svc/api",
		Target:    "lint",
		State:     types.TargetRunCached,
		StartedAt: time.UnixMilli(4_000),
		EndedAt:   time.UnixMilli(4_000),
		OutputRef: "refcafe",
	}}, got[0].Targets)
}

func TestRunRegistryFailedResult(t *testing.T) {
	reg := NewRunRegistry()
	emit(reg, "inv1", journal.Event{Ts: 1_000, Kind: journal.KindStarted})
	emit(reg, "inv1", journal.Event{Ts: 2_000, Kind: journal.KindExec, Project: "p", Target: "t"})
	emit(reg, "inv1", journal.Event{Ts: 3_000, Kind: journal.KindResult, Project: "p", Target: "t", Status: journal.StatusFail, DurMs: 1_000})

	got := reg.Snapshot()
	require.Len(t, got, 1)
	require.Equal(t, types.TargetRunFailed, got[0].Targets[0].State)
}

// Multiple targets keep first-seen order within a run; multiple runs sort newest-started first.
func TestRunRegistryOrdering(t *testing.T) {
	reg := NewRunRegistry()

	emit(reg, "invOld", journal.Event{Ts: 1_000, Kind: journal.KindStarted})
	emit(reg, "invOld", journal.Event{Ts: 1_100, Kind: journal.KindExec, Project: "p", Target: "a"})
	emit(reg, "invOld", journal.Event{Ts: 1_200, Kind: journal.KindExec, Project: "p", Target: "b"})

	emit(reg, "invNew", journal.Event{Ts: 9_000, Kind: journal.KindStarted})
	emit(reg, "invNew", journal.Event{Ts: 9_100, Kind: journal.KindExec, Project: "q", Target: "z"})

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
	emit(reg, "inv1", journal.Event{Ts: 1_000, Kind: journal.KindStarted})
	emit(reg, "inv1", journal.Event{Ts: 2_000, Kind: journal.KindExec, Project: "p", Target: "t"})
	emit(reg, "inv1", journal.Event{Ts: 3_000, Kind: journal.KindResult, Project: "p", Target: "t", Status: journal.StatusPass, Ref: "r"})
	emit(reg, "inv1", journal.Event{Ts: 4_000, Kind: journal.KindExec, Project: "p", Target: "t"})

	got := reg.Snapshot()
	require.Equal(t, types.TargetRunPassed, got[0].Targets[0].State)
}

// A finished run lingers within the retention window, then is pruned.
func TestRunRegistryPrunesAfterRetention(t *testing.T) {
	reg := NewRunRegistry()
	now := time.UnixMilli(100_000)
	reg.retain = 10 * time.Second
	reg.nowFn = func() time.Time { return now }

	emit(reg, "inv1", journal.Event{Ts: 1_000, Kind: journal.KindStarted})
	emit(reg, "inv1", journal.Event{Ts: 2_000, Kind: journal.KindResult, Project: "p", Target: "t", Status: journal.StatusPass})
	emit(reg, "inv1", journal.Event{Ts: 3_000, Kind: journal.KindFinished, Status: journal.StatusPass})

	// Within retention: still visible.
	require.Len(t, reg.Snapshot(), 1)

	// Past retention: pruned.
	now = now.Add(11 * time.Second)
	require.Empty(t, reg.Snapshot())
}

// Events with no invocation id cannot be attributed and are dropped.
func TestRunRegistryIgnoresEventsWithoutInv(t *testing.T) {
	reg := NewRunRegistry()
	// Emit through a logger but with no invocation id on ctx and an explicit empty Inv.
	ctx := journal.WithLogger(context.Background(), journal.NewLogger(reg))
	journal.Emit(ctx, journal.Event{Ts: 1_000, Kind: journal.KindExec, Project: "p", Target: "t"})
	require.Empty(t, reg.Snapshot())
}
