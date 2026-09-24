package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// UnmarshalText is the one door from a name to each guard enum, so it is where a
// misspelling stops, and an empty name stays unset.
func TestGuardEnumsUnmarshalText(t *testing.T) {
	for _, name := range []string{"", "root", "worker"} {
		var r AgentRole
		require.NoError(t, r.UnmarshalText([]byte(name)), name)
		assert.Equal(t, AgentRole(name), r)
	}
	for _, name := range []string{"", "allow", "advise", "deny"} {
		var d GuardDecision
		require.NoError(t, d.UnmarshalText([]byte(name)), name)
		assert.Equal(t, GuardDecision(name), d)
	}

	r := AgentRoleWorker
	require.EqualError(t, r.UnmarshalText([]byte("lead")), `unknown agent role "lead" (want one of [root worker])`)
	assert.Equal(t, AgentRoleWorker, r)

	d := GuardDeny
	require.EqualError(t, d.UnmarshalText([]byte("block")), `unknown guard decision "block" (want one of [allow advise deny])`)
	assert.Equal(t, GuardDeny, d)
}

func TestGuardEnumsRenderUnsetByName(t *testing.T) {
	assert.Equal(t, "unset", AgentRole("").String())
	assert.Equal(t, "unset", GuardDecision("").String())
	assert.Equal(t, "worker", AgentRoleWorker.String())
	assert.Equal(t, "deny", GuardDeny.String())
}

// A decision outside the declared set never reads as allow: it outranks advise and ties
// with deny, so a verdict nothing can interpret still blocks.
func TestStricterGuardVerdictTreatsAnUnknownDecisionAsDeny(t *testing.T) {
	unknown := GuardVerdict{Decision: "block", Reason: "typo"}
	assert.Equal(t, unknown, StricterGuardVerdict(GuardVerdict{}, unknown))
	assert.Equal(t, unknown, StricterGuardVerdict(unknown, GuardVerdict{Decision: GuardAdvise, Reason: "x"}))
}

// The approved and the working-tree rule are merged with this, which is what makes an
// uncommitted tightening apply at once and an uncommitted loosening wait for approval.
func TestStricterGuardVerdict(t *testing.T) {
	allow := GuardVerdict{}
	adviseA := GuardVerdict{Decision: GuardAdvise, Reason: "name a model"}
	adviseB := GuardVerdict{Decision: GuardAdvise, Reason: "add Done when"}
	denyA := GuardVerdict{Decision: GuardDeny, Reason: "unnamed model"}
	denyB := GuardVerdict{Decision: GuardDeny, Reason: "no completion goal"}

	cases := []struct {
		name string
		a, b GuardVerdict
		want GuardVerdict
	}{
		{"deny beats allow", allow, denyA, denyA},
		{"deny beats advise either way round", denyA, adviseA, denyA},
		{"advise beats allow", adviseA, allow, adviseA},
		{"explicit allow and zero allow", GuardVerdict{Decision: GuardAllow}, allow, GuardVerdict{Decision: GuardAllow}},
		{"two advisories keep both", adviseA, adviseB, GuardVerdict{Decision: GuardAdvise, Reason: "name a model\n\nadd Done when"}},
		{"the same advisory twice says it once", adviseA, adviseA, adviseA},
		{"two denials keep both", denyA, denyB, GuardVerdict{Decision: GuardDeny, Reason: "unnamed model\n\nno completion goal"}},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, StricterGuardVerdict(tc.a, tc.b), tc.name)
	}
}
