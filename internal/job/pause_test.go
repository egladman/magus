package job

import (
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
	assert.Equal(t, "release build needs the machine", first.PauseReason)
	assert.Equal(t, until, first.PausedUntil)

	rows, err = s.Pause(t.Context(), "still building", 0, "w")
	require.NoError(t, err)
	assert.Equal(t, first.Paused, rows[0].Paused, "pausing again keeps the first time")
	assert.Equal(t, "still building", rows[0].PauseReason, "and takes the new reason")
	assert.Zero(t, rows[0].PausedUntil, "and the new bound")

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
