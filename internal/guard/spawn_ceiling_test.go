package guard

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// ceilingFixture is a workspace whose tiers map claude-code to haiku, sonnet and opus, a
// session s whose transcript reported model, and whether its host has reported the thread.
func ceilingFixture(t *testing.T, model string, reported bool) (context.Context, Dependencies, location) {
	t.Helper()
	set := testModelTiers(t, "claude-code")
	ctx := tierFixture(t, set)
	deps := levelDeps(t, types.GuardProfileStandard)
	deps.ModelTiers = func() *types.ModelTierSet { return set }
	at := hookLocation(ctx, deps)
	if reported {
		hostGate(at.cacheDir, "claude-code").MarkFired(hostAgentsKind)
	}
	who := hookAttribution{Host: "claude-code", Session: "s"}
	writeCostState(hint.NewGate(at.cacheDir, who.factsKey()), costState{Model: model})
	return ctx, deps, at
}

// judgeSpawnBy judges a Claude Code spawn on model made by subagent agent, "" for the main
// thread.
func judgeSpawnBy(ctx context.Context, t *testing.T, deps Dependencies, agent, model string) types.GuardJudgment {
	t.Helper()
	env := map[string]any{
		"session_id": "s", "hook_event_name": "PreToolUse", "tool_name": "Agent",
		"tool_input": map[string]any{"description": "orchestrator/scout scout", "prompt": "Read internal/job and report.", "model": model},
	}
	if agent != "" {
		env["agent_id"] = agent
	}
	return Judge(ctx, deps, Request{Host: "claude-code", Input: hookJSON(t, env)})
}

// The root session runs on the model the person picked, and a spawn above its tier is
// refused once the host has reported the thread, naming the way up.
func TestSpawnAboveParentTierRefusesAProvenCeiling(t *testing.T) {
	ctx, deps, _ := ceilingFixture(t, "claude-sonnet-5-5", true)

	v := judgeSpawnBy(ctx, t, deps, "", "opus")

	assert.Equal(t, decisionDeny, v.Decision)
	assert.Equal(t, string(denyRuleSpawnAboveParentTier), v.Rule)
	assert.Contains(t, v.Reason, "this spawn runs opus, model tier principal (rank 2), above this session's model, claude-sonnet-5-5, model tier standard (rank 1)")
	assert.Contains(t, v.Reason, "spawn it on sonnet or a lower tier, or a person forks the higher-tier row")
}

func TestSpawnAboveParentTierPassesAtOrBelowTheCeiling(t *testing.T) {
	for _, model := range []string{"sonnet", "haiku", "claude-haiku-5-5"} {
		t.Run(model, func(t *testing.T) {
			ctx, deps, _ := ceilingFixture(t, "claude-sonnet-5-5", true)
			v := judgeSpawnBy(ctx, t, deps, "", model)
			assert.NotEqual(t, string(denyRuleSpawnAboveParentTier), v.Rule)
			assert.NotEqual(t, decisionDeny, v.Decision)
		})
	}
}

// A thread its host has not reported may not be the session's main thread, so the
// session's model is only an inference and the ceiling advises.
func TestSpawnAboveParentTierAdvisesAnInferredCeiling(t *testing.T) {
	ctx, deps, _ := ceilingFixture(t, "claude-sonnet-5-5", false)

	v := judgeSpawnBy(ctx, t, deps, "", "opus")

	assert.Equal(t, decisionAdvise, v.Decision)
	assert.Equal(t, string(denyRuleSpawnAboveParentTier), v.Rule)
	assert.Contains(t, v.Context, "this spawn runs opus, model tier principal (rank 2)")
}

// A subagent's ceiling is the model its own spawn named, proven; one spawned on no model
// inherited the session's, inferred.
func TestSpawnAboveParentTierReadsASubagentsSpawnedModel(t *testing.T) {
	ctx, deps, at := ceilingFixture(t, "claude-opus-5-5", true)
	facts := hint.NewGate(at.cacheDir, hookAttribution{Host: "claude-code", Session: "s"}.factsKey())
	body, err := json.Marshal(spawnedAgent{Model: "haiku"})
	require.NoError(t, err)
	writeAgentMarker(facts, spawnedAgentKind("a1"), body)

	v := judgeSpawnBy(ctx, t, deps, "a1", "sonnet")
	assert.Equal(t, decisionDeny, v.Decision)
	assert.Equal(t, string(denyRuleSpawnAboveParentTier), v.Rule)
	assert.Contains(t, v.Reason, "above the model this agent was spawned on, haiku, model tier economy (rank 0)")

	ctx, deps, _ = ceilingFixture(t, "claude-sonnet-5-5", true)
	v = judgeSpawnBy(ctx, t, deps, "a2", "opus")
	assert.Equal(t, decisionAdvise, v.Decision, "a subagent with no spawn record inherited the session's model")
	assert.Contains(t, v.Context, "above the session's model, which this agent inherited, claude-sonnet-5-5")
}

// The level comes from magus\guard.builtins, as every built-in's does.
func TestSpawnAboveParentTierTakesItsDeclaredSetting(t *testing.T) {
	ctx, deps, _ := ceilingFixture(t, "claude-sonnet-5-5", true)
	deps.Builtins[string(denyRuleSpawnAboveParentTier)] = builtin.Setting{Decision: builtin.Advise}
	v := judgeSpawnBy(ctx, t, deps, "", "opus")
	assert.Equal(t, decisionAdvise, v.Decision, "set to advise, proof is advised")
	assert.Equal(t, string(denyRuleSpawnAboveParentTier), v.Rule)

	ctx, deps, _ = ceilingFixture(t, "claude-sonnet-5-5", true)
	deps.Builtins[string(denyRuleSpawnAboveParentTier)] = builtin.Setting{Decision: builtin.Off}
	v = judgeSpawnBy(ctx, t, deps, "", "opus")
	assert.NotEqual(t, string(denyRuleSpawnAboveParentTier), v.Rule)
}

// Nothing is judged without both tiers: no model reported for the session, a spawn model no
// tier maps, or a workspace with no declaration.
func TestSpawnAboveParentTierJudgesOnlyKnownTiers(t *testing.T) {
	ctx, deps, _ := ceilingFixture(t, "", true)
	assert.NotEqual(t, string(denyRuleSpawnAboveParentTier), judgeSpawnBy(ctx, t, deps, "", "opus").Rule, "no session model")

	ctx, deps, _ = ceilingFixture(t, "claude-sonnet-5-5", true)
	assert.NotEqual(t, string(denyRuleSpawnAboveParentTier), judgeSpawnBy(ctx, t, deps, "", "gpt-5").Rule, "a model no tier maps")

	ctx, deps, _ = ceilingFixture(t, "claude-sonnet-5-5", true)
	deps.ModelTiers = func() *types.ModelTierSet { return nil }
	assert.NotEqual(t, string(denyRuleSpawnAboveParentTier), judgeSpawnBy(ctx, t, deps, "", "opus").Rule, "tiers turned off")
}

func TestModelTierResolvesAnAliasOrOneWordOfAnID(t *testing.T) {
	t.Parallel()
	host := types.HarnessKey{ID: "claude-code"}
	set := testModelTiers(t, host.ID)

	tests := []struct {
		model string
		want  string
	}{
		{"opus", "principal"},
		{"claude-sonnet-5-5", "standard"},
		{"Claude-Haiku-4", "economy"},
		{"claude-opus-sonnet", ""},
		{"gpt-5", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got, ok := modelTier(set, host, tt.model)
		assert.Equal(t, tt.want != "", ok, tt.model)
		assert.Equal(t, tt.want, got.Name(), tt.model)
	}

	_, ok := modelTier(set, types.HarnessKey{ID: "other"}, "claude-opus-5-5")
	assert.False(t, ok, "a host the declaration does not map names no tier")
}
