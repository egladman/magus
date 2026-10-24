package guard

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/types"
)

// fileReleaseRequest files a release request for holder the way a refused fork does.
func fileReleaseRequest(t *testing.T, ctx context.Context, holder string) string {
	t.Helper()
	dir, err := sessions.Dir(hookLocation(ctx, Dependencies{}).workspace)
	require.NoError(t, err)
	id, _, err := sessions.OpenRequest(dir, holder, sessions.AttentionOpen{
		Outcome: string(types.OutcomeWaiting),
		Source:  job.ReleaseRequestSource,
		Where:   "internal/ledger/store.go",
		Lease:   "lease-b",
		Files:   []types.FileRef{{Value: "internal/ledger/store.go"}},
		Message: fmt.Sprintf("job %[1]s holds internal/ledger/store.go. %[1]s has changed nothing under internal/ledger/**", holder),
	}, sessions.InvocationStart{})
	require.NoError(t, err)
	return id
}

func TestReleaseRequestedAdvisesTheHolderOnce(t *testing.T) {
	call := func(ctx context.Context, session, lease string) types.GuardJudgment {
		return Judge(ctx, Dependencies{}, Request{Input: bashCall(t, session, "", "ls"), Host: "claude-code", Lease: lease})
	}

	t.Run("an open request for the lease advises once", func(t *testing.T) {
		ctx, _ := fleetFixture(t, fixture.FleetLeases()...)
		id := fileReleaseRequest(t, ctx, "lease-a")
		v := call(ctx, "holder", "lease-a")
		assert.Contains(t, v.Context, fmt.Sprintf("a fork under lease lease-b was refused for internal/ledger/store.go (request %s)", id))
		assert.Contains(t, v.Context, "lease-a has changed nothing under internal/ledger/**")
		assert.Contains(t, v.Context, "magus job apply -f <record>")
		require.Equal(t, types.GuardJudgment{
			SchemaVersion: agent.GuardSchemaVersion, Decision: decisionAdvise, Context: v.Context,
			Rule: string(advisoryReleaseRequested), Lease: "lease-a", LeaseFrom: types.LeaseSourceFlag,
		}, v, v.Context)
		assert.NotEqual(t, string(advisoryReleaseRequested), call(ctx, "holder", "lease-a").Rule, "said once a session")
	})

	t.Run("another lease's request is silent", func(t *testing.T) {
		ctx, _ := fleetFixture(t, fixture.FleetLeases()...)
		fileReleaseRequest(t, ctx, "lease-b")
		assert.NotEqual(t, string(advisoryReleaseRequested), call(ctx, "holder", "lease-a").Rule)
	})

	t.Run("a disposed request is silent", func(t *testing.T) {
		ctx, _ := fleetFixture(t, fixture.FleetLeases()...)
		id := fileReleaseRequest(t, ctx, "lease-a")
		dir, err := sessions.Dir(hookLocation(ctx, Dependencies{}).workspace)
		require.NoError(t, err)
		_, err = sessions.DisposeRequest(dir, id, "narrowed", sessions.InvocationStart{})
		require.NoError(t, err)
		assert.NotEqual(t, string(advisoryReleaseRequested), call(ctx, "holder", "lease-a").Rule)
	})

	t.Run("a session with no lease is silent", func(t *testing.T) {
		ctx, _ := fleetFixture(t, fixture.FleetLeases()...)
		fileReleaseRequest(t, ctx, "lease-a")
		assert.NotEqual(t, string(advisoryReleaseRequested), call(ctx, "nobody", "").Rule)
	})
}
