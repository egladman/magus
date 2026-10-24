package job

import (
	"context"
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPauseRecordsItsOwnerReasonAndBound(t *testing.T) {
	s := tempStore(t, t.TempDir())
	seed(t, s, types.Job{ID: "w", State: types.StateRunning, Registered: 1})
	until := time.Now().Add(time.Hour).Unix()

	rows, err := s.Pause(t.Context(), "release build needs the machine", until, "w")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	first := rows[0]
	assert.NotZero(t, first.Paused)
	assert.NotEmpty(t, first.PausedBy.User, "the pause names the account that wrote it")
	got := first
	// Who and when are the store's and the clock's own; the pause fields are the subject.
	got.Paused, got.PausedBy, got.Updated, got.Created, got.RegisteredBy = 0, types.Origin{}, 0, 0, types.Origin{}
	assert.Equal(t, types.Job{Schema: types.Schema{Version: types.JobSchemaVersion}, ID: "w", State: types.StateRunning,
		Registered: 1, Generation: 1, Attempts: 1, PauseReason: "release build needs the machine", PausedUntil: until}, got)

	rows, err = s.Pause(t.Context(), "still building", 0, "w")
	require.NoError(t, err)
	again := first
	again.PauseReason, again.PausedUntil, again.Updated = "still building", 0, rows[0].Updated
	assert.Equal(t, again, rows[0], "pausing again keeps the first time and takes the new reason and bound")

	rows, err = s.Resume(t.Context(), "w")
	require.NoError(t, err)
	assert.Equal(t, []any{int64(0), types.Origin{}, "", int64(0)},
		[]any{rows[0].Paused, rows[0].PausedBy, rows[0].PauseReason, rows[0].PausedUntil}, "a resume clears who paused it, why and until when")
}

func TestPauseRefusesNoReasonAndAPastBound(t *testing.T) {
	s := tempStore(t, t.TempDir())
	seed(t, s, types.Job{ID: "w", State: types.StateRunning, Registered: 1})

	_, err := s.Pause(t.Context(), " ", 0, "w")
	assert.EqualError(t, err, "job: a pause names its reason, which the holder and the person resuming it read")

	_, err = s.Pause(t.Context(), "busy", time.Now().Add(-time.Minute).Unix(), "w")
	assert.EqualError(t, err, "job: w: the pause's bound is already past, so it would lapse as it was written")
}

func TestPausedLineNamesWhoPausedItAndWhy(t *testing.T) {
	t.Parallel()

	row := types.Job{ID: "w", Paused: 1, PausedBy: types.Origin{User: "eli"}, PauseReason: "release build", PausedUntil: 1_800_000_000}
	assert.Equal(t, "job w is paused by eli: release build (until 2027-01-15T08:00:00Z), so its holder starts no magus run until `magus job resume w`", PausedLine(row))
	assert.Equal(t, "job w is paused, so its holder starts no magus run until `magus job resume w`", PausedLine(types.Job{ID: "w", Paused: 1}))
}

// A paused row outlives the read sweep and `prune --all` while its pause holds; past its
// bound both end it as they would any row.
func TestPausedJobOutlivesTheSweepAndPruneUntilItsPauseLapses(t *testing.T) {
	const now = int64(100_000)
	old := now - int64((3 * time.Hour).Seconds())
	s, _ := sweepStore(t, now, 2*time.Hour)
	s.landed = func(context.Context, types.Job) string { return "" }
	plant(t, s,
		types.Job{ID: "held", State: types.StateDeclared, Updated: old, Paused: old, PauseReason: "busy", PausedUntil: now + 60},
		types.Job{ID: "lapsed", State: types.StateDeclared, Updated: old, Paused: old, PauseReason: "busy", PausedUntil: now - 60},
		types.Job{ID: "taken", State: types.StateRunning, Registered: old, Updated: old, Paused: old, PauseReason: "busy"},
		types.Job{ID: "taken-lapsed", State: types.StateRunning, Registered: old, Updated: old, Paused: old, PauseReason: "busy", PausedUntil: now},
	)

	rows := states(t, s)
	assert.Equal(t, types.StateDeclared, rows["held"].State, "the sweep leaves a paused row alone")
	assert.Equal(t, types.Job{ID: "lapsed", State: types.StateNoReturn, Updated: now, Paused: old, PauseReason: "busy", PausedUntil: now - 60,
		EndReason: "declared and never taken, untouched for 3h0m0s (jobs.stale_after is 2h0m0s)"}, rows["lapsed"])

	got, err := s.Prune(t.Context(), PruneOptions{All: true})
	require.NoError(t, err)
	assert.Equal(t, []Ending{{ID: "taken-lapsed", Reason: "taken, then untouched for 3h0m0s (jobs.stale_after is 2h0m0s)"}}, got,
		"--all skips a paused row until its pause lapses")
}
