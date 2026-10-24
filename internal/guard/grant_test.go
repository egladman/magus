package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// grantRows are two live parentless rows, so a root session holding either commits from
// its own checkout and hooks-off is the only rule a --no-verify commit meets.
func grantRows() (granted, other types.Job) {
	granted, other = narrowLease(), narrowLease()
	granted.ID, other.ID = "guard/granted", "guard/other"
	return granted, other
}

func TestGrantedHooksOffAdvisesForThatLeaseAlone(t *testing.T) {
	granted, other := grantRows()
	ctx, root := fleetFixture(t, granted, other)
	at := ctx.Value(locationKey{}).(location)
	store := job.NewStore(job.Location{CacheDir: at.cacheDir, StateBase: fixture.StateBase(t), Root: root, Actor: &job.Actor{}})
	_, err := store.Grant(t.Context(), granted.ID, string(denyRuleHooksOff), 0)
	require.NoError(t, err)

	line := bashCall(t, "s1", "", "git commit --no-verify -m x")
	v := Judge(ctx, Dependencies{}, Request{Input: line, Host: "claude-code", Lease: granted.ID})
	require.Equal(t, types.GuardJudgment{Decision: decisionAdvise, Rule: string(denyRuleHooksOff)}, judgedAs(v), "the grant lowers hooks-off: %s", v.Reason)
	assert.Contains(t, v.Context, "grant")
	assert.Contains(t, v.Context, "until the job ends")

	v = Judge(ctx, Dependencies{}, Request{Input: line, Host: "claude-code", Lease: other.ID})
	assert.Equal(t, types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRuleHooksOff)}, judgedAs(v), "another lease's call is still refused")

	_, err = store.Revoke(t.Context(), granted.ID, string(denyRuleHooksOff))
	require.NoError(t, err)
	v = Judge(ctx, Dependencies{}, Request{Input: line, Host: "claude-code", Lease: granted.ID})
	assert.Equal(t, types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRuleHooksOff)}, judgedAs(v), "a revoked grant lowers nothing")
}

func TestAgentGrantIsRefused(t *testing.T) {
	granted, _ := grantRows()
	ctx, _ := fleetFixture(t, granted)

	for _, line := range []string{
		"magus job grant guard/granted hooks-off",
		"./magus job grant --revoke guard/granted hooks-off",
		"/usr/local/bin/magus -C /tmp job grant guard/granted hooks-off --for 1h",
		"cd /tmp && magus job grant guard/granted hooks-off",
		"env A=1 magus job grant guard/granted hooks-off",
		"bash -c 'magus job grant guard/granted hooks-off'",
	} {
		for _, lease := range []string{"", granted.ID} {
			v := Judge(ctx, Dependencies{}, Request{Input: line, Lease: lease})
			assert.Equal(t, types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRuleGrantIsAPersons)}, judgedAs(v), "%q under %q", line, lease)
		}
	}
	for _, line := range []string{"magus job ls", "echo magus job grant", "magus describe rules"} {
		assert.False(t, denyAgentGrant(line, DialectBash).refused(), "%q", line)
	}
}
