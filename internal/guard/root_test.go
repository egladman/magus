package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

func TestSettingLadder(t *testing.T) {
	t.Parallel()
	strict := builtin.Setting{Decision: builtin.Deny, Profile: types.GuardProfileStrict}
	assert.Equal(t, ladder{advise: 5, deny: 8}, settingLadder(strict, 0, heavyFigures))
	assert.Equal(t, ladder{advise: 8}, settingLadder(builtin.Setting{Decision: builtin.Advise, Profile: types.GuardProfileStandard}, 0, heavyFigures))
	assert.Equal(t, ladder{advise: 5}, settingLadder(builtin.Setting{Decision: builtin.Advise, Profile: types.GuardProfileStrict}, 0, heavyFigures),
		"a strict level set to advise advises from its first figure and never refuses")
	assert.Equal(t, ladder{deny: 6}, settingLadder(builtin.Setting{Decision: builtin.Deny, Calls: 6}, 6, heavyFigures), "a stated figure replaces the level's")
	assert.Equal(t, ladder{advise: 5, deny: 8}, settingLadder(builtin.Setting{Decision: builtin.Deny}, 0, heavyFigures), "a deny with no level reads strict's")
	assert.Equal(t, ladder{advise: 8}, settingLadder(builtin.Setting{Decision: builtin.Advise}, 0, heavyFigures), "an advise with no level reads standard's")
	assert.Equal(t, ladder{}, settingLadder(builtin.Setting{Decision: builtin.Off, Profile: types.GuardProfileStrict}, 0, heavyFigures))
}

func TestBudgetFiguresFollowBilling(t *testing.T) {
	t.Parallel()
	for billing, want := range map[types.AgentBilling][2]ladder{
		types.AgentBillingMetered: {{advise: 250_000}, {advise: 150_000, deny: 300_000}},
		types.AgentBillingQuota:   {{advise: 400_000}, {advise: 250_000, deny: 450_000}},
		"":                        {{advise: 300_000}, {advise: 200_000, deny: 350_000}},
	} {
		figures := budgetFigures(billing, 1_000_000)
		assert.Equal(t, want[0], figures(types.GuardProfileStandard), billing)
		assert.Equal(t, want[1], figures(types.GuardProfileStrict), billing)
		assert.Equal(t, ladder{advise: 500_000}, figures(types.GuardProfileLight), "half the window")
	}
	assert.Equal(t, ladder{}, budgetFigures("", 0)(types.GuardProfileLight), "no window, no window-relative figure")
}

// levelSetting is each level's resolved settings, as a workspace that declared only the
// level gets them.
func levelSetting(level types.GuardProfile) func(string) builtin.Setting {
	settings := levelSettings()[level]
	return func(rule string) builtin.Setting { return settings[rule] }
}

func rulesOf(findings []costFinding) map[denyRuleName]bool {
	out := map[denyRuleName]bool{}
	for _, f := range findings {
		out[f.rule] = f.deny
	}
	return out
}

func TestMainFindingsByLevel(t *testing.T) {
	t.Parallel()
	diff := mainCall{known: true, window: 1_000_000, class: delegableDiff, line: "git diff"}
	quiet := mainCall{known: true, window: 1_000_000, line: "git status --short"}

	st := costState{Tokens: 520_000, Streak: 7, Repeats: map[string]int{lineDigest("git diff"): 2}, Results: map[string]int{"Bash": 45_000}}
	assert.Equal(t, map[denyRuleName]bool{}, rulesOf(mainFindings(levelSetting(types.GuardProfileOff), st, diff)))
	assert.Equal(t, map[denyRuleName]bool{
		denyRuleRootContextBudget: false,
		denyRuleRootResultCap:     false,
	}, rulesOf(mainFindings(levelSetting(types.GuardProfileLight), st, diff)))
	assert.Equal(t, map[denyRuleName]bool{
		denyRuleRootContextBudget: false,
		denyRuleRootHeavyCommand:  false,
		denyRuleRootRepeat:        false,
		denyRuleRootResultCap:     false,
	}, rulesOf(mainFindings(levelSetting(types.GuardProfileStandard), st, diff)))
	assert.Equal(t, map[denyRuleName]bool{
		denyRuleRootContextBudget: true,
		denyRuleRootHeavyCommand:  true,
		denyRuleRootRepeat:        true,
		denyRuleRootResultCap:     false,
	}, rulesOf(mainFindings(levelSetting(types.GuardProfileStrict), st, diff)))

	assert.Equal(t, map[denyRuleName]bool{
		denyRuleRootContextBudget: false,
		denyRuleRootResultCap:     false,
	}, rulesOf(mainFindings(levelSetting(types.GuardProfileStrict), st, quiet)), "a call no subagent would make is never counted or refused")

	small := costState{Tokens: 90_000, Streak: 3, Results: map[string]int{"Bash": 5_000}}
	assert.Empty(t, mainFindings(levelSetting(types.GuardProfileStrict), small, diff))
}

// A result past the size is said once a session at light, and once per tool above it.
func TestResultCapKeys(t *testing.T) {
	t.Parallel()
	st := costState{Results: map[string]int{"Bash": 50_000, "Read": 60_000}}
	keys := func(level types.GuardProfile) []hint.MarkerKind {
		var out []hint.MarkerKind
		for _, f := range mainFindings(levelSetting(level), st, mainCall{}) {
			out = append(out, f.key)
		}
		return out
	}
	light := keys(types.GuardProfileLight)
	require.Len(t, light, 2)
	assert.Equal(t, light[0], light[1])
	standard := keys(types.GuardProfileStandard)
	require.Len(t, standard, 2)
	assert.NotEqual(t, standard[0], standard[1])
}

func TestApplyFindingsSaysAnInferredRefusalAsAdvice(t *testing.T) {
	t.Parallel()
	markers := hint.NewGate(t.TempDir(), "s")
	refusal := []costFinding{{rule: denyRuleRootHeavyCommand, deny: true, key: "k", say: "first line.\nwhy."}}
	pass := Verdict{Decision: decisionPass}

	v := applyFindings(markers, pass, refusal, true)
	assert.Equal(t, decisionDeny, v.Decision)
	assert.Equal(t, "first line.\nwhy.", v.Reason)

	v = applyFindings(markers, pass, refusal, false)
	assert.Equal(t, decisionAdvise, v.Decision)
	assert.Equal(t, "first line.", v.Context, "advice carries the verdict line")
	assert.Equal(t, string(denyRuleRootHeavyCommand), v.Rule)
	assert.Equal(t, decisionPass, applyFindings(markers, pass, refusal, false).Decision, "once a session")

	advised := Verdict{Decision: decisionAdvise, Context: "earlier", Rule: "graph-stale"}
	v = applyFindings(hint.NewGate(t.TempDir(), "s"), advised, []costFinding{{rule: denyRuleRootRepeat, key: "r", say: "again."}}, true)
	assert.Equal(t, "earlier\n\nagain.", v.Context)
	assert.Equal(t, "graph-stale", v.Rule, "added to the advice already there")
}

func TestShadowVerdicts(t *testing.T) {
	t.Parallel()
	st := costState{Tokens: 400_000, Streak: 9}
	got := shadowVerdicts(st, mainCall{known: true, window: 1_000_000, class: delegableSearch, line: "rg x"})
	assert.Equal(t, map[types.GuardProfile]types.GuardDecision{
		types.GuardProfileOff:      types.GuardAllow,
		types.GuardProfileLight:    types.GuardAllow,
		types.GuardProfileStandard: types.GuardAdvise,
		types.GuardProfileStrict:   types.GuardDeny,
	}, got)
	inferred := shadowVerdicts(st, mainCall{window: 1_000_000, class: delegableSearch, line: "rg x"})
	assert.Equal(t, types.GuardAdvise, inferred[types.GuardProfileStrict], "an inferred thread is never refused")
}

func TestPrincipalPricedReadsTheTable(t *testing.T) {
	t.Parallel()
	table := model.Shipped()
	assert.True(t, principalPriced("opus", table))
	assert.True(t, principalPriced("claude-fable-5-1", table))
	assert.False(t, principalPriced("sonnet", table))
	assert.False(t, principalPriced("claude-haiku-5-5", table))
	assert.False(t, principalPriced("", table))
	assert.True(t, differentModels("sonnet", "opus", table))
	assert.False(t, differentModels("sonnet", "claude-sonnet-5-5", table))
	assert.False(t, differentModels("sonnet", "economy", table), "a tier word names no row")
}

// strictDeps is a workspace at the strict level with nothing else declared.
func strictDeps(t *testing.T) Dependencies {
	t.Helper()
	expanded, err := builtin.ResolveProfile(types.GuardProfileStrict, nil)
	require.NoError(t, err)
	resolved, err := builtin.Resolve(expanded)
	require.NoError(t, err)
	return Dependencies{Builtins: resolved}
}

// The cost rules judge the main thread only, refuse it only where its host named it, and
// leave a subagent's calls alone.
func TestMainThreadCommandsAreJudgedByTheCostRules(t *testing.T) {
	ctx, cache := spawnFixture(t)
	deps := strictDeps(t)
	main := hookAttribution{Host: "claude-code", Session: "s"}
	writeCostState(hint.NewGate(cache, main.factsKey()), costState{Streak: 7})
	judge := func(agent string) Verdict {
		return Judge(ctx, deps, Request{Input: "git diff", Host: "claude-code", Session: "s", Agent: agent})
	}

	inferred := judge("")
	assert.Equal(t, decisionAdvise, inferred.Decision, "the host has named no subagent, so the thread is inferred")
	assert.Equal(t, string(denyRuleRootHeavyCommand), inferred.Rule)

	assert.NotEqual(t, decisionDeny, judge("a1").Decision, "a subagent's diff is its job")
	assert.NotEqual(t, decisionDeny, judge("a1").Decision)

	known := judge("")
	assert.Equal(t, decisionDeny, known.Decision, "the host has now named a subagent")
	assert.Equal(t, string(denyRuleRootHeavyCommand), known.Rule)
}

// A spawn naming no model is refused at strict, and a read-only job's spawn on one of the
// costliest models is advised.
func TestMainThreadSpawnsAreJudgedByTheSpawnRules(t *testing.T) {
	ctx, _ := fleetFixture(t, types.Job{ID: "scout", ReadOnly: true, State: types.StateDeclared, Model: "haiku"})
	deps := strictDeps(t)
	at := hookLocation(ctx, deps)
	hostGate(at.cacheDir, "claude-code").MarkFired(hostAgentsKind)
	spawn := func(model string) Verdict {
		input := map[string]any{"description": "orchestrator/scout scout", "prompt": "Read internal/job and report."}
		if model != "" {
			input["model"] = model
		}
		return Judge(ctx, deps, Request{Host: "claude-code", Input: hookJSON(t, map[string]any{
			"session_id": "s", "hook_event_name": "PreToolUse", "tool_name": "Agent", "tool_input": input,
		})})
	}

	v := spawn("")
	assert.Equal(t, decisionDeny, v.Decision)
	assert.Equal(t, string(denyRuleSpawnNamesModel), v.Rule)

	v = spawn("opus")
	assert.Equal(t, decisionDeny, v.Decision, "the job was forked for another model")
	assert.Equal(t, string(denyRuleSpawnNamesModel), v.Rule)
}
