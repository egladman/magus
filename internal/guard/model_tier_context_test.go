package guard

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// A leased subagent is advised once its reported context passes its row's tier cap, and
// the cap is read from the declaration once per session.
func TestModelTierContextCapAdvisesPastTheTiersCap(t *testing.T) {
	set := testModelTiers(t, "claude-code")
	ctx := tierFixture(t, set, fixture.FleetLeases()[0])
	at := hookLocation(ctx, Dependencies{})
	who := hookAttribution{Host: "claude-code", Session: "s", Agent: "a1"}
	facts := hint.NewGate(at.cacheDir, who.factsKey())
	asked := 0
	deps := Dependencies{ModelTiers: func() *types.ModelTierSet {
		asked++
		return set
	}}
	advise := func(lease string) advice { return adviseModelTierContext(ctx, deps, at, who, facts, lease) }

	assert.Equal(t, advice{}, advise("lease-a"), "no context reported")
	under := int64(90_000)
	writeSpawnedAgent(facts, "a1", spawnedAgent{ContextTokens: &under})
	assert.Equal(t, advice{}, advise("lease-a"), "under the economy cap")
	over := int64(120_000)
	writeSpawnedAgent(facts, "a1", spawnedAgent{ContextTokens: &over})

	got := advise("lease-a")

	assert.Equal(t, "magus workspace: lease lease-a runs at model tier economy, which caps a worker's context at 100k tokens, and yours has reached 120k: report what you have and stop.", got.Say)
	assert.Contains(t, got.Why, "model tier economy (rank 0)")
	assert.Equal(t, 1, asked, "the cap is kept for the session")
	assert.Equal(t, advice{}, advise(""), "no lease, no row")
	assert.Equal(t, advice{}, adviseModelTierContext(ctx, withSetting(string(advisoryModelTierContextCap), builtin.Off), at, who, facts, "lease-a"))
}

// A row forked with no tier, or at a tier with no cap, is never judged.
func TestModelTierContextCapNeedsATierWithACap(t *testing.T) {
	set := testModelTiers(t, "claude-code")
	lease := fixture.FleetLeases()[0]
	lease.Model = "sonnet"
	ctx := tierFixture(t, set, lease)
	at := hookLocation(ctx, Dependencies{})
	who := hookAttribution{Host: "claude-code", Session: "s", Agent: "a1"}
	facts := hint.NewGate(at.cacheDir, who.factsKey())
	over := int64(500_000)
	writeSpawnedAgent(facts, "a1", spawnedAgent{ContextTokens: &over})

	got := adviseModelTierContext(ctx, Dependencies{ModelTiers: func() *types.ModelTierSet { return set }}, at, who, facts, "lease-a")

	assert.Equal(t, advice{}, got, "standard caps no context")
	assert.Equal(t, advice{}, adviseModelTierContext(ctx, Dependencies{}, at, who, facts, "lease-a"), "no declaration")
}

// Raised to deny, the rule refuses the worker's write as a passed deadline does.
func TestModelTierContextCapRaisedToDenyRefusesWrites(t *testing.T) {
	set := testModelTiers(t, "claude-code")
	ctx := tierFixture(t, set, fixture.FleetLeases()[0])
	root := hookLocation(ctx, Dependencies{}).workspace
	who := hookAttribution{Host: "test-host", Session: "s"}
	writeCostState(hint.NewGate(hookLocation(ctx, Dependencies{}).cacheDir, who.factsKey()), costState{Tokens: 150_000})
	deps := withSetting(string(advisoryModelTierContextCap), builtin.Deny)
	deps.ModelTiers = func() *types.ModelTierSet { return set }

	v := Judge(ctx, deps, Request{Input: filepath.Join(root, "internal/ledger/store.go"), IsPath: true, Session: "s", Host: "test-host", Lease: "lease-a"})

	require.Equal(t, types.GuardJudgment{Decision: decisionDeny, Rule: string(advisoryModelTierContextCap)}, judgedAs(v))
	assert.Contains(t, v.Reason, "lease lease-a runs at model tier economy")
}
