package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// UnmarshalText is the one door from a name to each spawn enum, so it is where a
// misspelling stops, and an empty name stays unset.
func TestSpawnEnumsUnmarshalText(t *testing.T) {
	for _, name := range []string{"", "spawn", "continue"} {
		var k SpawnKind
		require.NoError(t, k.UnmarshalText([]byte(name)), name)
		assert.Equal(t, SpawnKind(name), k)
	}
	for _, name := range []string{"", "root", "worker"} {
		var r SpawnRole
		require.NoError(t, r.UnmarshalText([]byte(name)), name)
		assert.Equal(t, SpawnRole(name), r)
	}
	for _, name := range []string{"", "allow", "advise", "deny"} {
		var d SpawnDecision
		require.NoError(t, d.UnmarshalText([]byte(name)), name)
		assert.Equal(t, SpawnDecision(name), d)
	}

	k := SpawnKindContinue
	require.EqualError(t, k.UnmarshalText([]byte("resume")), `unknown spawn kind "resume" (want one of [spawn continue])`)
	assert.Equal(t, SpawnKindContinue, k, "a refused name leaves the kind as it was")

	r := SpawnRoleWorker
	require.EqualError(t, r.UnmarshalText([]byte("lead")), `unknown spawn role "lead" (want one of [root worker])`)
	assert.Equal(t, SpawnRoleWorker, r)

	d := SpawnDeny
	require.EqualError(t, d.UnmarshalText([]byte("block")), `unknown spawn decision "block" (want one of [allow advise deny])`)
	assert.Equal(t, SpawnDeny, d)
}

func TestSpawnEnumsRenderUnsetByName(t *testing.T) {
	assert.Equal(t, "unset", SpawnKind("").String())
	assert.Equal(t, "unset", SpawnRole("").String())
	assert.Equal(t, "unset", SpawnDecision("").String())
	assert.Equal(t, "continue", SpawnKindContinue.String())
	assert.Equal(t, "worker", SpawnRoleWorker.String())
	assert.Equal(t, "deny", SpawnDeny.String())
}

// The committed and the working-tree rule are merged with this, which is what makes an
// uncommitted tightening apply at once and an uncommitted loosening wait for a commit.
func TestStricterSpawnVerdict(t *testing.T) {
	allow := SpawnVerdict{}
	adviseA := SpawnVerdict{Decision: SpawnAdvise, Reason: "name a model"}
	adviseB := SpawnVerdict{Decision: SpawnAdvise, Reason: "add Done when"}
	denyA := SpawnVerdict{Decision: SpawnDeny, Reason: "unnamed model"}
	denyB := SpawnVerdict{Decision: SpawnDeny, Reason: "no completion goal"}

	cases := []struct {
		name string
		a, b SpawnVerdict
		want SpawnVerdict
	}{
		{"deny beats allow", allow, denyA, denyA},
		{"deny beats advise either way round", denyA, adviseA, denyA},
		{"advise beats allow", adviseA, allow, adviseA},
		{"explicit allow and zero allow", SpawnVerdict{Decision: SpawnAllow}, allow, SpawnVerdict{Decision: SpawnAllow}},
		{"two advisories keep both", adviseA, adviseB, SpawnVerdict{Decision: SpawnAdvise, Reason: "name a model\n\nadd Done when"}},
		{"the same advisory twice says it once", adviseA, adviseA, adviseA},
		{"two denials keep both", denyA, denyB, SpawnVerdict{Decision: SpawnDeny, Reason: "unnamed model\n\nno completion goal"}},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, StricterSpawnVerdict(tc.a, tc.b), tc.name)
	}
}
