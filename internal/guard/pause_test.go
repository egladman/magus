package guard

import (
	"fmt"
	"testing"
	"time"

	"github.com/egladman/magus/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDenyPausedLeaseRefusesARunUntilResume(t *testing.T) {
	paused := narrowLease()
	paused.Paused = 1
	ctx, _ := fleetFixture(t, paused)

	for _, command := range []string{"magus run go::go-test .", "./magus affected lint"} {
		reason := denyPausedLease(ctx, Dependencies{}, paused.ID, command)
		require.True(t, reason.refused(), "%q", command)
		assert.Equal(t, fmt.Sprintf("magus workspace: job %[1]s is paused, so its holder starts no magus run until `magus job resume %[1]s`.", paused.ID), reason.Say)
	}
	for _, command := range []string{"magus describe job " + paused.ID, fmt.Sprintf("magus job exit %s --stdin", paused.ID), "go version"} {
		assert.False(t, denyPausedLease(ctx, Dependencies{}, paused.ID, command).refused(), "%q is not a run", command)
	}

	resumed, _ := fleetFixture(t, narrowLease())
	assert.False(t, denyPausedLease(resumed, Dependencies{}, paused.ID, "magus run go::go-test .").refused(), "a resumed job runs")
}

func TestDenyPausedLeaseNamesWhyAndLiftsAtTheBound(t *testing.T) {
	held := narrowLease()
	held.Paused = 1
	held.PausedBy, held.PauseReason, held.PausedUntil = types.Origin{User: "eli"}, "release build", time.Now().Add(time.Hour).Unix()
	ctx, _ := fleetFixture(t, held)
	reason := denyPausedLease(ctx, Dependencies{}, held.ID, "magus run go::go-test .")
	require.True(t, reason.refused())
	assert.Equal(t, fmt.Sprintf("magus workspace: job %s is paused by eli: release build (until %s), so its holder starts no magus run until `magus job resume %s`.",
		held.ID, time.Unix(held.PausedUntil, 0).UTC().Format(time.RFC3339), held.ID), reason.Say)

	lapsed := narrowLease()
	lapsed.Paused = 1
	lapsed.PauseReason, lapsed.PausedUntil = "release build", time.Now().Add(-time.Minute).Unix()
	ctx, _ = fleetFixture(t, lapsed)
	assert.False(t, denyPausedLease(ctx, Dependencies{}, lapsed.ID, "magus run go::go-test .").refused(), "a pause past its bound has lapsed")
}
