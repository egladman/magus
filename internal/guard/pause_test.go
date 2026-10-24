package guard

import (
	"testing"

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
		assert.Equal(t, "magus workspace: job "+paused.ID+" is paused, so its holder starts no magus run until `magus job resume "+paused.ID+"`.", reason.Say)
	}
	for _, command := range []string{"magus describe job " + paused.ID, "magus job exit " + paused.ID + " --stdin", "go version"} {
		assert.False(t, denyPausedLease(ctx, Dependencies{}, paused.ID, command).refused(), "%q is not a run", command)
	}

	resumed, _ := fleetFixture(t, narrowLease())
	assert.False(t, denyPausedLease(resumed, Dependencies{}, paused.ID, "magus run go::go-test .").refused(), "a resumed job runs")
}
