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
	ctx, deps, _ := ceilingFixture(t, "sonnet", true)

	v := judgeSpawnBy(ctx, t, deps, "", "opus")

	shape, text := spoken(v)
	assert.Equal(t, types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRuleSpawnAboveParentTier)}, shape)
	assert.Contains(t, text, "this spawn runs opus, model tier principal (rank 2), above this session's model, sonnet, model tier standard (rank 1)")
	assert.Contains(t, text, "spawn it on sonnet or lower")
}

func TestSpawnAboveParentTierPassesAtOrBelowTheCeiling(t *testing.T) {
	for _, model := range []string{"sonnet", "haiku"} {
		t.Run(model, func(t *testing.T) {
			ctx, deps, _ := ceilingFixture(t, "sonnet", true)
			v := judgeSpawnBy(ctx, t, deps, "", model)
			assert.NotEqual(t, string(denyRuleSpawnAboveParentTier), v.Rule)
			assert.NotEqual(t, decisionDeny, v.Decision)
		})
	}
}

// A thread its host has not reported may not be the session's main thread, so the
// session's model is only an inference and the ceiling advises.
func TestSpawnAboveParentTierAdvisesAnInferredCeiling(t *testing.T) {
	ctx, deps, _ := ceilingFixture(t, "sonnet", false)

	v := judgeSpawnBy(ctx, t, deps, "", "opus")

	shape, text := spoken(v)
	assert.Equal(t, types.GuardJudgment{Decision: decisionAdvise, Rule: string(denyRuleSpawnAboveParentTier)}, shape)
	assert.Contains(t, text, "this spawn runs opus, model tier principal (rank 2)")
}

// A subagent's ceiling is the model its own spawn named, proven; one spawned on no model
// inherited the session's, inferred.
func TestSpawnAboveParentTierReadsASubagentsSpawnedModel(t *testing.T) {
	ctx, deps, at := ceilingFixture(t, "opus", true)
	facts := hint.NewGate(at.cacheDir, hookAttribution{Host: "claude-code", Session: "s"}.factsKey())
	body, err := json.Marshal(spawnedAgent{Model: "haiku"})
	require.NoError(t, err)
	writeAgentMarker(facts, spawnedAgentKind("a1"), body)

	v := judgeSpawnBy(ctx, t, deps, "a1", "sonnet")
	shape, text := spoken(v)
	assert.Equal(t, types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRuleSpawnAboveParentTier)}, shape)
	assert.Contains(t, text, "above the model this agent was spawned on, haiku, model tier economy (rank 0)")

	ctx, deps, _ = ceilingFixture(t, "sonnet", true)
	v = judgeSpawnBy(ctx, t, deps, "a2", "opus")
	assert.Equal(t, decisionAdvise, v.Decision, "a subagent with no spawn record inherited the session's model")
	assert.Contains(t, v.Context, "above the session's model, which this agent inherited, sonnet")
}

// The level comes from magus\guard.builtins, as every built-in's does.
func TestSpawnAboveParentTierTakesItsDeclaredSetting(t *testing.T) {
	ctx, deps, _ := ceilingFixture(t, "sonnet", true)
	deps.Builtins[string(denyRuleSpawnAboveParentTier)] = builtin.Setting{Decision: builtin.Advise}
	v := judgeSpawnBy(ctx, t, deps, "", "opus")
	shape, _ := spoken(v)
	assert.Equal(t, types.GuardJudgment{Decision: decisionAdvise, Rule: string(denyRuleSpawnAboveParentTier)}, shape, "set to advise, proof is advised")

	ctx, deps, _ = ceilingFixture(t, "sonnet", true)
	deps.Builtins[string(denyRuleSpawnAboveParentTier)] = builtin.Setting{Decision: builtin.Off}
	v = judgeSpawnBy(ctx, t, deps, "", "opus")
	assert.NotEqual(t, string(denyRuleSpawnAboveParentTier), v.Rule)
}

// Nothing is judged without a reported session model or a declaration.
func TestSpawnAboveParentTierNeedsASessionModelAndADeclaration(t *testing.T) {
	ctx, deps, _ := ceilingFixture(t, "", true)
	assert.NotEqual(t, string(denyRuleSpawnAboveParentTier), judgeSpawnBy(ctx, t, deps, "", "opus").Rule, "no session model")

	ctx, deps, _ = ceilingFixture(t, "sonnet", true)
	deps.ModelTiers = func() *types.ModelTierSet { return nil }
	assert.NotEqual(t, string(denyRuleSpawnAboveParentTier), judgeSpawnBy(ctx, t, deps, "", "opus").Rule, "tiers turned off")
}

// A tier is an exact alias of the declaration or the exact id of a harness row naming one.
// A model id that only contains an alias, or a model no row names, has an unknown tier: the
// rule advises and names the row to add, never guessing.
func TestSpawnAboveParentTierAdvisesAnUnknownTier(t *testing.T) {
	tests := []struct {
		name, session, spawn, why, add string
	}{
		{"spawn model id", "sonnet", "claude-opus-5-5", "this spawn's model, claude-opus-5-5 is in no harness_models row", "add a row for claude-opus-5-5"},
		{"session model id", "claude-sonnet-5-5", "opus", "this session's model, claude-sonnet-5-5 is in no harness_models row", "add a row for claude-sonnet-5-5"},
		{"both", "claude-sonnet-5-5", "gpt-5", "this spawn's model, gpt-5 and this session's model, claude-sonnet-5-5 are in no harness_models row", "add a row for gpt-5 and claude-sonnet-5-5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, deps, _ := ceilingFixture(t, tt.session, true)
			v := judgeSpawnBy(ctx, t, deps, "", tt.spawn)
			shape, text := spoken(v)
			assert.Equal(t, types.GuardJudgment{Decision: decisionAdvise, Rule: string(denyRuleSpawnAboveParentTier)}, shape)
			assert.Contains(t, text, tt.why)
			assert.Contains(t, text, tt.add+` with "tier" to the claude-code spell's harness_models`)
			assert.Contains(t, text, "the declaration at hack/tiers.buzz:3 maps haiku, sonnet, opus for claude-code")
		})
	}
}

// A session's full model id resolves by the harness row naming its tier, exactly, beside the
// aliases a declaration maps for spawns.
func TestSpawnAboveParentTierReadsTheHarnessRowsTier(t *testing.T) {
	ctx, deps, _ := ceilingFixture(t, "claude-sonnet-5-5", true)
	deps.ModelRows = func() []types.ModelPricing {
		return []types.ModelPricing{{ID: "claude-sonnet-5-5", Tier: "standard"}, {ID: "claude-opus-5-5", Tier: "principal"}}
	}

	v := judgeSpawnBy(ctx, t, deps, "", "opus")
	shape, text := spoken(v)
	assert.Equal(t, types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRuleSpawnAboveParentTier)}, shape)
	assert.Contains(t, text, "above this session's model, claude-sonnet-5-5, model tier standard (rank 1)")

	v = judgeSpawnBy(ctx, t, deps, "", "claude-sonnet-5-5-20261001")
	assert.Equal(t, decisionAdvise, v.Decision, "a dated spelling is another id until a row names it")
	assert.Contains(t, v.Context, "add a row for claude-sonnet-5-5-20261001")
}

// A row's alias resolves a spawn that names it exactly, with no declaration mapping it; an
// alias with a prefix or another case is no alias, and a row with an alias but no tier names
// none.
func TestSpawnAboveParentTierReadsTheHarnessRowsAlias(t *testing.T) {
	ctx, deps, _ := ceilingFixture(t, "claude-sonnet-5-5", true)
	deps.ModelRows = func() []types.ModelPricing {
		return []types.ModelPricing{
			{ID: "claude-sonnet-5-5", Tier: "standard", Alias: "mid"},
			{ID: "claude-opus-5-5", Tier: "principal", Alias: "top"},
			{ID: "claude-fable-5-1", Alias: "fable"},
		}
	}

	v := judgeSpawnBy(ctx, t, deps, "", "top")
	shape, text := spoken(v)
	assert.Equal(t, types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRuleSpawnAboveParentTier)}, shape)
	assert.Contains(t, text, "this spawn runs top, model tier principal (rank 2)")

	for _, spawn := range []string{"top-1", "Top", "fable"} {
		v = judgeSpawnBy(ctx, t, deps, "", spawn)
		assert.Equal(t, decisionAdvise, v.Decision, spawn)
		assert.Contains(t, v.Context, "add a row for "+spawn, spawn)
	}
}

// spoken splits a judgment into its shape, to compare whole, and the text it said, to
// assert on: the text is wording a field-by-field comparison would pin to the letter.
func spoken(v types.GuardJudgment) (shape types.GuardJudgment, text string) {
	text = v.Reason + v.Context
	v.Reason, v.Context, v.SchemaVersion = "", "", 0
	return v, text
}

// A host the declaration does not map names no tier, and the advice says so.
func TestSpawnAboveParentTierNamesAnUnmappedHost(t *testing.T) {
	assert.Contains(t, declaredAliases(testModelTiers(t, "codex"), types.HarnessKey{ID: "claude-code"}), "maps no model for harness claude-code: its hosts are codex")
}
