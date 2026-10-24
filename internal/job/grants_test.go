package job

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func running(id string) types.Job {
	row := lease(id)
	row.State = types.StateRunning
	return row
}

func TestGrantTheLastLinePerJobAndRuleWins(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	row := seed(t, s, running("a"))
	now := time.Now().Unix()

	_, err := s.Grant(t.Context(), "a", "hooks-off", now+60)
	require.NoError(t, err)
	_, err = s.Grant(t.Context(), "a", "hooks-off", 0)
	require.NoError(t, err)

	got, live, err := s.LiveGrant(row, "hooks-off", now+120)
	require.NoError(t, err)
	require.True(t, live, "the second grant has no expiry and replaced the first")
	assert.Zero(t, got.Until)
	assert.Equal(t, "a", got.Job)
	assert.NotZero(t, got.At)

	grants, err := s.Grants()
	require.NoError(t, err)
	assert.Len(t, grants, 1, "one entry per job and rule")
}

func TestGrantLapsesAtItsDuration(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	row := seed(t, s, running("a"))
	now := time.Now().Unix()
	_, err := s.Grant(t.Context(), "a", "hooks-off", now+60)
	require.NoError(t, err)

	_, live, err := s.LiveGrant(row, "hooks-off", now)
	require.NoError(t, err)
	assert.True(t, live)
	_, live, err = s.LiveGrant(row, "hooks-off", now+60)
	require.NoError(t, err)
	assert.False(t, live, "a grant ends at its --for")
	_, live, err = s.LiveGrant(row, "stage-all", now)
	require.NoError(t, err)
	assert.False(t, live, "a grant lowers only its own rule")
}

func TestGrantEndsWithTheJob(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	row := seed(t, s, running("a"))
	_, err := s.Grant(t.Context(), "a", "hooks-off", 0)
	require.NoError(t, err)
	now := time.Now().Unix()

	ended := row
	ended.State = types.StatePass
	_, live, err := s.LiveGrant(ended, "hooks-off", now)
	require.NoError(t, err)
	assert.False(t, live, "a grant ends at the job's terminal state")

	reforked := row
	reforked.Attempts = row.Attempts + 1
	_, live, err = s.LiveGrant(reforked, "hooks-off", now)
	require.NoError(t, err)
	assert.False(t, live, "the next attempt is not the one the person approved")

	ended.ID = "b"
	seed(t, s, ended)
	_, err = s.Grant(t.Context(), "b", "hooks-off", 0)
	assert.ErrorContains(t, err, "already ended", "an ended job takes no grant")
	_, err = s.Grant(t.Context(), "nobody", "hooks-off", 0)
	assert.ErrorIs(t, err, ErrUnknownJob)
}

func TestGrantRevokeIsALine(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	row := seed(t, s, running("a"))
	_, err := s.Grant(t.Context(), "a", "hooks-off", 0)
	require.NoError(t, err)
	_, err = s.Revoke(t.Context(), "a", "hooks-off")
	require.NoError(t, err)

	_, live, err := s.LiveGrant(row, "hooks-off", time.Now().Unix())
	require.NoError(t, err)
	assert.False(t, live)
	grants, err := s.Grants()
	require.NoError(t, err)
	require.Len(t, grants, 1)
	assert.True(t, grants[0].Revoked, "the revocation is kept, so who revoked it stays on record")
}

func TestGrantIsRefusedToALeaseHolder(t *testing.T) {
	t.Parallel()

	loc := tempLocation(t, t.TempDir())
	seed(t, NewStore(loc), running("a"))

	_, err := boundStore(loc, "a").Grant(t.Context(), "a", "hooks-off", 0)
	assert.ErrorContains(t, err, "a person's")
	_, err = boundStore(loc, "a").Revoke(t.Context(), "a", "hooks-off")
	assert.ErrorContains(t, err, "a person's")
}
