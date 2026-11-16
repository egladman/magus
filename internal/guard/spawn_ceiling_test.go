package guard

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/egladman/magus/types"
)

// ceilingFixture is a workspace whose tiers map claude-code to haiku, sonnet and opus, a
// session s whose transcript reported model, and whether its host has reported the thread.
func ceilingFixture(t *testing.T, model string, reported bool) (context.Context, Dependencies, location) {
	t.Helper()
	set := testModelTiers(t, "claude-code")
	ctx := tierFixture(t, set)
	deps := levelDeps(t, types.GuardProfileStandard)
	deps.ModelTiers = func() *tier.Set { return set }
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
func judgeSpawnBy(ctx context.Context, t *testing.T, deps Dependencies, agent, model string) verdict.Judgment {
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

// spoken splits a judgment into its shape, to compare whole, and the text it said, to
// assert on: the text is wording a field-by-field comparison would pin to the letter.
func spoken(v verdict.Judgment) (shape verdict.Judgment, text string) {
	text = v.Reason + v.Context
	v.Reason, v.Context, v.APIVersion = "", "", ""
	return v, text
}

type SpawnAboveParentTierSuite struct {
	suite.Suite
}

func TestSpawnAboveParentTierSuite(t *testing.T) { suite.Run(t, new(SpawnAboveParentTierSuite)) }

func (s *SpawnAboveParentTierSuite) advised() verdict.Judgment {
	return verdict.Judgment{Decision: decisionAdvise, Rule: string(denyRuleSpawnAboveParentTier)}
}

func (s *SpawnAboveParentTierSuite) denied() verdict.Judgment {
	return verdict.Judgment{Decision: decisionDeny, Rule: string(denyRuleSpawnAboveParentTier)}
}

// pricedRows price sonnet and opus by alias at the tiers the declaration maps.
func pricedRows() []tier.Pricing {
	return []tier.Pricing{
		{ID: "claude-sonnet-5-5", Alias: "sonnet", Tier: "standard", Input: 3, Output: 15},
		{ID: "claude-opus-5-5", Alias: "opus", Tier: "principal", Input: 5, Output: 25},
	}
}

// The way up is allowed. The advice names both tiers, each model's cost, the
// alias at the parent's tier and the setting that stops it.
func (s *SpawnAboveParentTierSuite) TestAProvenHigherTierIsAdvisedWithItsCost() {
	ctx, deps, _ := ceilingFixture(s.T(), "sonnet", true)
	deps.ModelRows = pricedRows

	shape, text := spoken(judgeSpawnBy(ctx, s.T(), deps, "", "opus"))

	s.Equal(s.advised(), shape)
	s.Contains(text, "this spawn runs opus, model tier principal (rank 2), above this session's model, sonnet, model tier standard (rank 1)")
	s.Contains(text, "per million tokens, opus costs $5.00 input and $25.00 output, and sonnet costs $3.00 input and $15.00 output")
	s.Contains(text, "sonnet stays at the parent's tier")
	s.Contains(text, `setting spawn-above-parent-tier off in magus\guard.builtins stops this advice`)
	s.NotContains(text, "or lower")
}

func (s *SpawnAboveParentTierSuite) TestAPriceNoRowGivesIsUnknown() {
	ctx, deps, _ := ceilingFixture(s.T(), "sonnet", true)
	deps.ModelRows = func() []tier.Pricing {
		return []tier.Pricing{{ID: "claude-opus-5-5", Alias: "opus", Tier: "principal", Input: 5}}
	}

	shape, text := spoken(judgeSpawnBy(ctx, s.T(), deps, "", "opus"))

	s.Equal(s.advised(), shape)
	s.Contains(text, "opus costs $5.00 input and an unknown output price")
	s.Contains(text, "sonnet's price is unknown, since no mgs_listModels row names it")
}

func (s *SpawnAboveParentTierSuite) TestTheSameTierSaysNothing() {
	ctx, deps, _ := ceilingFixture(s.T(), "sonnet", true)

	v := judgeSpawnBy(ctx, s.T(), deps, "", "sonnet")

	s.NotEqual(string(denyRuleSpawnAboveParentTier), v.Rule)
	s.NotContains(v.Reason+v.Context, "above this session's model")
}

func (s *SpawnAboveParentTierSuite) TestALowerTierSaysNothing() {
	ctx, deps, _ := ceilingFixture(s.T(), "sonnet", true)

	v := judgeSpawnBy(ctx, s.T(), deps, "", "haiku")

	s.NotEqual(string(denyRuleSpawnAboveParentTier), v.Rule)
	s.NotContains(v.Reason+v.Context, "above this session's model")
}

// A thread its host has not reported may not be the session's main thread, so the
// session's model is only an inference.
func (s *SpawnAboveParentTierSuite) TestAnInferredCeilingIsAdvised() {
	ctx, deps, _ := ceilingFixture(s.T(), "sonnet", false)

	shape, text := spoken(judgeSpawnBy(ctx, s.T(), deps, "", "opus"))

	s.Equal(s.advised(), shape)
	s.Contains(text, "this spawn runs opus, model tier principal (rank 2)")
}

func (s *SpawnAboveParentTierSuite) TestADenySettingRefusesAProvenCeiling() {
	ctx, deps, _ := ceilingFixture(s.T(), "sonnet", true)
	deps.Builtins[string(denyRuleSpawnAboveParentTier)] = builtin.Setting{Decision: builtin.Deny}

	shape, text := spoken(judgeSpawnBy(ctx, s.T(), deps, "", "opus"))

	s.Equal(s.denied(), shape)
	s.Contains(text, "this workspace sets spawn-above-parent-tier to deny, so spawn it on sonnet or lower")
}

func (s *SpawnAboveParentTierSuite) TestADenySettingOnlyAdvisesAnInferredCeiling() {
	ctx, deps, _ := ceilingFixture(s.T(), "sonnet", false)
	deps.Builtins[string(denyRuleSpawnAboveParentTier)] = builtin.Setting{Decision: builtin.Deny}

	shape, _ := spoken(judgeSpawnBy(ctx, s.T(), deps, "", "opus"))

	s.Equal(s.advised(), shape)
}

func (s *SpawnAboveParentTierSuite) TestOffSilencesTheRule() {
	ctx, deps, _ := ceilingFixture(s.T(), "sonnet", true)
	deps.Builtins[string(denyRuleSpawnAboveParentTier)] = builtin.Setting{Decision: builtin.Off}

	v := judgeSpawnBy(ctx, s.T(), deps, "", "opus")

	s.NotEqual(string(denyRuleSpawnAboveParentTier), v.Rule)
	s.NotContains(v.Reason+v.Context, "above this session's model")
}

// A subagent's ceiling is the model its own spawn named, proven.
func (s *SpawnAboveParentTierSuite) TestASubagentsCeilingIsTheModelItsSpawnNamed() {
	ctx, deps, at := ceilingFixture(s.T(), "opus", true)
	facts := hint.NewGate(at.cacheDir, hookAttribution{Host: "claude-code", Session: "s"}.factsKey())
	body, err := codec.Marshal(spawnedAgent{Model: "haiku"})
	s.Require().NoError(err)
	writeAgentMarker(facts, spawnedAgentKind("a1"), body)

	shape, text := spoken(judgeSpawnBy(ctx, s.T(), deps, "a1", "sonnet"))

	s.Equal(s.advised(), shape)
	s.Contains(text, "above the model this agent was spawned on, haiku, model tier economy (rank 0)")
}

func (s *SpawnAboveParentTierSuite) TestASubagentSpawnedOnNoModelInheritsTheSessions() {
	ctx, deps, _ := ceilingFixture(s.T(), "sonnet", true)

	v := judgeSpawnBy(ctx, s.T(), deps, "a2", "opus")

	s.Equal(decisionAdvise, v.Decision)
	s.Contains(v.Context, "above the session's model, which this agent inherited, sonnet")
}

func (s *SpawnAboveParentTierSuite) TestNoSessionModelIsNotJudged() {
	ctx, deps, _ := ceilingFixture(s.T(), "", true)

	s.NotEqual(string(denyRuleSpawnAboveParentTier), judgeSpawnBy(ctx, s.T(), deps, "", "opus").Rule)
}

func (s *SpawnAboveParentTierSuite) TestNoDeclarationIsNotJudged() {
	ctx, deps, _ := ceilingFixture(s.T(), "sonnet", true)
	deps.ModelTiers = func() *tier.Set { return nil }

	s.NotEqual(string(denyRuleSpawnAboveParentTier), judgeSpawnBy(ctx, s.T(), deps, "", "opus").Rule)
}

// assertUnknownTier asserts the rule advises why a tier is unknown and which
// row to add, never guessing one.
func (s *SpawnAboveParentTierSuite) assertUnknownTier(session, spawn, why, add string) {
	ctx, deps, _ := ceilingFixture(s.T(), session, true)

	shape, text := spoken(judgeSpawnBy(ctx, s.T(), deps, "", spawn))

	s.Equal(s.advised(), shape)
	s.Contains(text, why)
	s.Contains(text, add+` with a tier to the claude-code spell's mgs_listModels`)
	s.Contains(text, "the declaration at hack/tiers.buzz:3 maps haiku, sonnet, opus for claude-code")
}

// A model id that only contains an alias has an unknown tier.
func (s *SpawnAboveParentTierSuite) TestAnUnknownSpawnModelIsAdvised() {
	s.assertUnknownTier("sonnet", "claude-opus-5-5", "this spawn's model, claude-opus-5-5 is in no mgs_listModels row", "add a row for claude-opus-5-5")
}

func (s *SpawnAboveParentTierSuite) TestAnUnknownSessionModelIsAdvised() {
	s.assertUnknownTier("claude-sonnet-5-5", "opus", "this session's model, claude-sonnet-5-5 is in no mgs_listModels row", "add a row for claude-sonnet-5-5")
}

func (s *SpawnAboveParentTierSuite) TestTwoUnknownModelsAreAdvisedTogether() {
	s.assertUnknownTier("claude-sonnet-5-5", "gpt-5",
		"this spawn's model, gpt-5 and this session's model, claude-sonnet-5-5 are in no mgs_listModels row", "add a row for gpt-5 and claude-sonnet-5-5")
}

// A session's full model id resolves by the harness row naming its tier.
func (s *SpawnAboveParentTierSuite) TestTheHarnessRowsTierResolvesASessionsID() {
	ctx, deps, _ := ceilingFixture(s.T(), "claude-sonnet-5-5", true)
	deps.ModelRows = func() []tier.Pricing {
		return []tier.Pricing{{ID: "claude-sonnet-5-5", Tier: "standard"}, {ID: "claude-opus-5-5", Tier: "principal"}}
	}

	shape, text := spoken(judgeSpawnBy(ctx, s.T(), deps, "", "opus"))

	s.Equal(s.advised(), shape)
	s.Contains(text, "above this session's model, claude-sonnet-5-5, model tier standard (rank 1)")
}

func (s *SpawnAboveParentTierSuite) TestADatedSpellingIsAnotherID() {
	ctx, deps, _ := ceilingFixture(s.T(), "claude-sonnet-5-5", true)
	deps.ModelRows = func() []tier.Pricing {
		return []tier.Pricing{{ID: "claude-sonnet-5-5", Tier: "standard"}, {ID: "claude-opus-5-5", Tier: "principal"}}
	}

	v := judgeSpawnBy(ctx, s.T(), deps, "", "claude-sonnet-5-5-20261001")

	s.Equal(decisionAdvise, v.Decision)
	s.Contains(v.Context, "add a row for claude-sonnet-5-5-20261001")
}

// aliasedRows carry aliases no declaration maps; fable's names no tier.
func aliasedRows() []tier.Pricing {
	return []tier.Pricing{
		{ID: "claude-sonnet-5-5", Tier: "standard", Alias: "mid"},
		{ID: "claude-opus-5-5", Tier: "principal", Alias: "top"},
		{ID: "claude-fable-5-1", Alias: "fable"},
	}
}

// judgeAliased spawns spawn from a session on claude-sonnet-5-5.
func (s *SpawnAboveParentTierSuite) judgeAliased(spawn string) verdict.Judgment {
	ctx, deps, _ := ceilingFixture(s.T(), "claude-sonnet-5-5", true)
	deps.ModelRows = aliasedRows
	return judgeSpawnBy(ctx, s.T(), deps, "", spawn)
}

func (s *SpawnAboveParentTierSuite) TestTheHarnessRowsAliasResolvesASpawn() {
	shape, text := spoken(s.judgeAliased("top"))

	s.Equal(s.advised(), shape)
	s.Contains(text, "this spawn runs top, model tier principal (rank 2)")
	s.Contains(text, "top costs an unknown input price and an unknown output price")
}

func (s *SpawnAboveParentTierSuite) TestAPrefixedAliasIsNoAlias() {
	v := s.judgeAliased("top-1")

	s.Equal(decisionAdvise, v.Decision)
	s.Contains(v.Context, "add a row for top-1")
}

func (s *SpawnAboveParentTierSuite) TestAnAliasInAnotherCaseIsNoAlias() {
	v := s.judgeAliased("Top")

	s.Equal(decisionAdvise, v.Decision)
	s.Contains(v.Context, "add a row for Top")
}

func (s *SpawnAboveParentTierSuite) TestARowWithNoTierNamesNone() {
	v := s.judgeAliased("fable")

	s.Equal(decisionAdvise, v.Decision)
	s.Contains(v.Context, "add a row for fable")
}

// A host the declaration does not map names no tier.
func (s *SpawnAboveParentTierSuite) TestAnUnmappedHostIsNamed() {
	s.Contains(declaredAliases(testModelTiers(s.T(), "codex"), "claude-code"), "maps no model for harness claude-code: its hosts are codex")
}

// secondRows put two rows at the principal tier.
func secondRows() []tier.Pricing {
	return []tier.Pricing{
		{ID: "claude-sonnet-5-5", Tier: "standard"},
		{ID: "claude-opus-5-5", Tier: "principal", Alias: "opus"},
		{ID: "claude-next-1", Tier: "principal", Alias: "next"},
	}
}

func (s *SpawnAboveParentTierSuite) TestASecondRowAtOneTierResolvesByID() {
	ctx, deps, _ := ceilingFixture(s.T(), "claude-sonnet-5-5", true)
	deps.ModelRows = secondRows

	shape, text := spoken(judgeSpawnBy(ctx, s.T(), deps, "", "claude-next-1"))

	s.Equal(s.advised(), shape)
	s.Contains(text, "model tier principal (rank 2)")
}

func (s *SpawnAboveParentTierSuite) TestASecondRowAtOneTierResolvesByAlias() {
	ctx, deps, _ := ceilingFixture(s.T(), "claude-sonnet-5-5", true)
	deps.ModelRows = secondRows

	shape, text := spoken(judgeSpawnBy(ctx, s.T(), deps, "", "next"))

	s.Equal(s.advised(), shape)
	s.Contains(text, "model tier principal (rank 2)")
}

func (s *SpawnAboveParentTierSuite) TestASessionOnASecondRowAtOneTierPassesItsPeer() {
	ctx, deps, _ := ceilingFixture(s.T(), "claude-next-1", true)
	deps.ModelRows = func() []tier.Pricing {
		return []tier.Pricing{
			{ID: "claude-opus-5-5", Tier: "principal", Alias: "opus"},
			{ID: "claude-next-1", Tier: "principal", Alias: "next"},
		}
	}

	v := judgeSpawnBy(ctx, s.T(), deps, "", "opus")

	s.NotEqual(string(denyRuleSpawnAboveParentTier), v.Rule, "a principal child under a principal parent passes")
}
