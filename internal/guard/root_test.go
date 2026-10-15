package guard

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

func TestSettingFigure(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 5, settingFigure(builtin.Setting{Decision: builtin.Advise, Profile: types.GuardProfileStrict}, 0, heavyFigures))
	assert.Equal(t, 8, settingFigure(builtin.Setting{Decision: builtin.Advise, Profile: types.GuardProfileStandard}, 0, heavyFigures))
	assert.Equal(t, 6, settingFigure(builtin.Setting{Decision: builtin.Advise, Calls: 6, Profile: types.GuardProfileStrict}, 6, heavyFigures),
		"a stated figure replaces the level's")
	assert.Equal(t, 8, settingFigure(builtin.Setting{Decision: builtin.Advise}, 0, heavyFigures), "an advise with no level reads standard's")
	assert.Zero(t, settingFigure(builtin.Setting{Decision: builtin.Off, Profile: types.GuardProfileStrict}, 0, heavyFigures))
	assert.Zero(t, settingFigure(builtin.Setting{Decision: builtin.Deny, Profile: types.GuardProfileStrict}, 0, heavyFigures),
		"a meter only advises, and Resolve refuses a deny declared for one")
}

func TestBudgetFiguresFollowBilling(t *testing.T) {
	t.Parallel()
	for billing, want := range map[types.AgentBilling][2]int{
		types.AgentBillingMetered: {250_000, 150_000},
		types.AgentBillingQuota:   {400_000, 250_000},
		"":                        {300_000, 200_000},
	} {
		figures := budgetFigures(billing, 1_000_000)
		assert.Equal(t, want[0], figures(types.GuardProfileStandard), billing)
		assert.Equal(t, want[1], figures(types.GuardProfileStrict), billing)
		assert.Equal(t, 500_000, figures(types.GuardProfileLight), "half the window")
	}
	assert.Zero(t, budgetFigures("", 0)(types.GuardProfileLight), "no window, no window-relative figure")
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
	diff := mainCall{from: types.ThreadReported, window: 1_000_000, class: types.DelegableDiff, line: "git diff"}
	quiet := mainCall{from: types.ThreadReported, window: 1_000_000, line: "git status --short"}

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
		denyRuleRootContextBudget: false,
		denyRuleRootHeavyCommand:  false,
		denyRuleRootRepeat:        false,
		denyRuleRootResultCap:     false,
	}, rulesOf(mainFindings(levelSetting(types.GuardProfileStrict), st, diff)), "a strict session with no record of orchestrating is only advised")

	assert.Equal(t, map[denyRuleName]bool{
		denyRuleRootContextBudget: false,
		denyRuleRootResultCap:     false,
	}, rulesOf(mainFindings(levelSetting(types.GuardProfileStrict), st, quiet)), "a call no subagent would make is never counted or refused")

	small := costState{Tokens: 90_000, Streak: 3, Results: map[string]int{"Bash": 5_000}}
	assert.Empty(t, mainFindings(levelSetting(types.GuardProfileStrict), small, diff))
}

// A main thread past its budget and its streak is neither refused nor advised for a call
// that prints a ref or one line, at any level, and is refused for the calls that read.
func TestFreeCallsEscapeTheBudgetRules(t *testing.T) {
	t.Parallel()
	st := costState{Tokens: 900_000, Streak: 20, Repeats: map[string]int{}}
	call := func(line string) mainCall {
		return mainCall{from: types.ThreadReported, window: 1_000_000, class: delegableClass(line), free: costFree(line), line: line}
	}
	for _, level := range []types.GuardProfile{types.GuardProfileLight, types.GuardProfileStandard, types.GuardProfileStrict} {
		for _, line := range costFreeLines {
			rules := rulesOf(mainFindings(levelSetting(level), st, call(line)))
			assert.NotContains(t, rules, denyRuleRootContextBudget, "%s at %s", line, level)
			assert.NotContains(t, rules, denyRuleRootHeavyCommand, "%s at %s", line, level)
		}
	}
	for line := range costBoundLines {
		if delegableClass(line) == "" {
			continue
		}
		rules := rulesOf(mainFindings(levelSetting(types.GuardProfileStrict), st, call(line)))
		assert.Contains(t, rules, denyRuleRootContextBudget, line)
		assert.Contains(t, rules, denyRuleRootHeavyCommand, line)
	}
	for _, line := range []string{"./magus describe job a1 -o json", "git status"} {
		rules := rulesOf(mainFindings(levelSetting(types.GuardProfileStandard), st, call(line)))
		assert.Contains(t, rules, denyRuleRootContextBudget, "a call that is not free is still advised: %s", line)
	}
}

// A worker may not build ./magus, so the main thread past its budget must still be able to.
// Judged whole, the quiet calls pass the budget rules and the reads are advised, never
// refused: size alone refuses nothing.
func TestMainThreadPastTheBudgetStillBuilds(t *testing.T) {
	ctx, cache := spawnFixture(t)
	deps := strictDeps(t)
	who := hookAttribution{Host: "claude-code", Session: "s"}
	hostGate(cache, who.Host).MarkFired(hostAgentsKind)
	budgetRule := func(v types.GuardJudgment) bool {
		return v.Rule == string(denyRuleRootContextBudget) || v.Rule == string(denyRuleRootHeavyCommand)
	}
	judge := func(line string) types.GuardJudgment {
		writeCostState(hint.NewGate(cache, who.factsKey()), costState{Tokens: 900_000, Streak: 20})
		return Judge(ctx, deps, Request{Input: line, Host: who.Host, Session: who.Session})
	}

	for _, line := range []string{"./magus run go-build . -s", "git status --short", "./magus job wait a1", "./magus describe job a1 -o name"} {
		assert.False(t, budgetRule(judge(line)), line)
	}
	v := judge("git diff")
	assert.Equal(t, decisionAdvise, v.Decision)
	assert.Contains(t, v.Context, "magus workspace: this session's context is 900k tokens")
	assert.NotEqual(t, decisionDeny, judge("git show HEAD").Decision)
}

// Handing work to a subagent is what the budget rules ask for, so a spawn is never refused
// or advised by them however large the context.
func TestSpawnsEscapeTheBudgetRules(t *testing.T) {
	ctx, cache := spawnFixture(t)
	deps := strictDeps(t)
	who := hookAttribution{Host: "claude-code", Session: "s"}
	hostGate(cache, who.Host).MarkFired(hostAgentsKind)
	writeCostState(hint.NewGate(cache, who.factsKey()), costState{Tokens: 900_000, Streak: 20})

	v := Judge(ctx, deps, Request{Host: "claude-code", Input: hookJSON(t, map[string]any{
		"session_id": "s", "hook_event_name": "PreToolUse", "tool_name": "Agent",
		"tool_input": map[string]any{"description": "orchestrator/scout scout", "prompt": "Read internal/job and report.", "model": "haiku"},
	})})
	assert.NotEqual(t, string(denyRuleRootContextBudget), v.Rule)
	assert.NotEqual(t, string(denyRuleRootHeavyCommand), v.Rule)
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
	refusal := []costFinding{{rule: denyRuleOrchestratorGroundwork, deny: true, key: "k", say: "first line.\nwhy."}}
	pass := types.GuardJudgment{Decision: decisionPass}

	assert.Equal(t, types.GuardJudgment{
		Decision: decisionDeny, Reason: "first line.\nwhy.", Rule: string(denyRuleOrchestratorGroundwork),
	}, applyFindings(markers, pass, refusal, types.ThreadReported))

	assert.Equal(t, types.GuardJudgment{
		Decision: decisionAdvise, Rule: string(denyRuleOrchestratorGroundwork),
		Context: "first line.", // advice carries the verdict line
	}, applyFindings(markers, pass, refusal, types.ThreadInferred))
	assert.Equal(t, decisionPass, applyFindings(markers, pass, refusal, types.ThreadInferred).Decision, "once a session")

	advised := types.GuardJudgment{Decision: decisionAdvise, Context: "earlier", Rule: "graph-stale"}
	assert.Equal(t, types.GuardJudgment{
		Decision: decisionAdvise, Rule: "graph-stale",
		Context: "earlier\n\nagain.", // added to the advice already there
	}, applyFindings(hint.NewGate(t.TempDir(), "s"), advised, []costFinding{{rule: denyRuleRootRepeat, key: "r", say: "again."}}, types.ThreadReported))
}

func TestShadowVerdicts(t *testing.T) {
	t.Parallel()
	st := costState{Tokens: 400_000, Streak: 9}
	call := mainCall{from: types.ThreadReported, window: 1_000_000, class: types.DelegableSearch, line: "rg x", work: types.DelegableSearch}
	assert.Equal(t, map[types.GuardProfile]types.GuardDecision{
		types.GuardProfileOff:      types.GuardAllow,
		types.GuardProfileLight:    types.GuardAllow,
		types.GuardProfileStandard: types.GuardAdvise,
		types.GuardProfileStrict:   types.GuardAdvise,
	}, shadowVerdicts(st, call), "with no record of orchestrating, no level refuses")

	call.orchestration = types.OrchestrationProven
	assert.Equal(t, map[types.GuardProfile]types.GuardDecision{
		types.GuardProfileOff:      types.GuardDeny,
		types.GuardProfileLight:    types.GuardDeny,
		types.GuardProfileStandard: types.GuardDeny,
		types.GuardProfileStrict:   types.GuardDeny,
	}, shadowVerdicts(st, call), "orchestrator-groundwork refuses proof at every level, off included")

	call.from, call.orchestration = types.ThreadInferred, types.OrchestrationInferred
	for level, decision := range shadowVerdicts(st, call) {
		assert.Equal(t, types.GuardAdvise, decision, "an inferred thread is never refused, at %s", level)
	}
}

// A proven orchestrator at strict passes every orchestration call and is refused every
// piece of groundwork; a strict session with no record of orchestrating is only advised.
func TestOrchestratorGroundworkByRole(t *testing.T) {
	t.Parallel()
	at, notes := orchestratorFixture(t)
	st := costState{Tokens: 900_000, Streak: 20}
	call := func(line string, o types.Orchestration) mainCall {
		return mainCall{from: types.ThreadReported, window: 1_000_000, class: delegableClass(line), free: costFree(line), line: line,
			orchestration: o, work: rootWorkClass(line, at)}
	}
	strict := levelSetting(types.GuardProfileStrict)
	refuses := func(findings []costFinding) bool {
		return slices.ContainsFunc(findings, func(f costFinding) bool { return f.deny })
	}
	for _, line := range orchestrationLines(notes) {
		findings := mainFindings(strict, st, call(line, types.OrchestrationProven))
		assert.NotContains(t, rulesOf(findings), denyRuleOrchestratorGroundwork, line)
		assert.False(t, refuses(findings), line)
	}
	for _, g := range groundworkLines {
		rules := rulesOf(mainFindings(strict, st, call(g.line, types.OrchestrationProven)))
		assert.True(t, rules[denyRuleOrchestratorGroundwork], g.line)
		assert.NotContains(t, rules, denyRuleRootHeavyCommand, "an orchestrator is past the nudge: %s", g.line)
		assert.NotContains(t, rules, denyRuleRootRepeat, g.line)

		rules = rulesOf(mainFindings(levelSetting(types.GuardProfileOff), costState{}, call(g.line, types.OrchestrationProven)))
		assert.True(t, rules[denyRuleOrchestratorGroundwork], "at any size and level, off included: %s", g.line)

		findings := mainFindings(strict, st, call(g.line, types.OrchestrationInferred))
		assert.Contains(t, rulesOf(findings), denyRuleOrchestratorGroundwork, g.line)
		assert.False(t, refuses(findings), "an inferred record is only advised: %s", g.line)

		findings = mainFindings(strict, st, call(g.line, ""))
		assert.NotContains(t, rulesOf(findings), denyRuleOrchestratorGroundwork, g.line)
		assert.False(t, refuses(findings), "a strict session with no record is only advised: %s", g.line)
	}

	advise := builtin.Setting{Decision: builtin.Advise}
	f, ok := groundworkFinding(advise, call("git diff", types.OrchestrationProven))
	require.True(t, ok)
	assert.False(t, f.deny, "a workspace that sets the rule to advise is advised")
	_, ok = groundworkFinding(builtin.Setting{Decision: builtin.Off}, call("git diff", types.OrchestrationProven))
	assert.False(t, ok)
	inferred, _ := groundworkFinding(advise, call("git diff", types.OrchestrationInferred))
	search, _ := groundworkFinding(advise, call("rg x", types.OrchestrationInferred))
	assert.NotEqual(t, inferred.key, search.key, "advised once per kind of work")
}

// Proof is the session's from its first bound spawn: its groundwork is refused from the
// next call, its graph reads pass, and a subagent of the same session is never judged.
func TestOrchestratorGroundworkAfterABoundSpawn(t *testing.T) {
	ctx, _ := fleetFixture(t, types.Job{ID: "scout", ReadOnly: true, State: types.StateDeclared})
	deps := levelDeps(t, types.GuardProfileLight)
	at := hookLocation(ctx, deps)
	hostGate(at.cacheDir, "claude-code").MarkFired(hostAgentsKind)
	judge := func(line, agent string) types.GuardJudgment {
		return Judge(ctx, deps, Request{Input: line, Host: "claude-code", Session: "s", Agent: agent})
	}

	require.NotEqual(t, string(denyRuleOrchestratorGroundwork), judge("git diff", "").Rule, "no record yet")
	require.NotEqual(t, decisionDeny, judgeSpawn(ctx, t, deps, "haiku").Decision)

	v := judge("git show HEAD", "")
	assert.Equal(t, types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRuleOrchestratorGroundwork)}, judgedAs(v))
	assert.NotEqual(t, string(denyRuleOrchestratorGroundwork), judge("./magus explain internal/guard", "").Rule)
	assert.NotEqual(t, string(denyRuleOrchestratorGroundwork), judge("git show HEAD", "a1").Rule, "a subagent's diff is its job")
}

// A proven orchestrator's write inside a checkout is refused, but advised while the
// checkout is midway through a cherry-pick; notes, a leased caller and a session with no
// record are never judged.
func TestGradeMainWrite(t *testing.T) {
	t.Parallel()
	deps := strictDeps(t)
	who := hookAttribution{Host: "claude-code", Session: "s"}
	grade := func(orchestrates types.Orchestration, path, lease string, prepare func(at location)) types.GuardJudgment {
		at, _ := orchestratorFixture(t)
		hostGate(at.cacheDir, who.Host).MarkFired(hostAgentsKind)
		writeCostState(hint.NewGate(at.cacheDir, who.factsKey()), costState{Orchestrates: orchestrates})
		if prepare != nil {
			prepare(at)
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(at.workspace, path)
		}
		return gradeMainWrite(deps, types.GuardJudgment{Decision: decisionPass}, path, lease, who, at)
	}

	v := grade(types.OrchestrationProven, "internal/a.go", "", nil)
	assert.Equal(t, types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRuleOrchestratorGroundwork)}, judgedAs(v))

	v = grade(types.OrchestrationProven, "internal/a.go", "", func(at location) {
		require.NoError(t, os.WriteFile(filepath.Join(at.workspace, ".git", "CHERRY_PICK_HEAD"), []byte("abc\n"), 0o644))
	})
	assert.Equal(t, decisionAdvise, v.Decision, "resolving a conflict is integration")

	assert.Equal(t, decisionAdvise, grade(types.OrchestrationInferred, "internal/a.go", "", nil).Decision)
	assert.Equal(t, decisionPass, grade(types.OrchestrationProven, "AGENTS.md", "", nil).Decision)
	assert.Equal(t, decisionPass, grade(types.OrchestrationProven, filepath.Join(t.TempDir(), "plan.md"), "", nil).Decision)
	assert.Equal(t, decisionPass, grade(types.OrchestrationProven, "internal/a.go", "job-a", nil).Decision)
	assert.Equal(t, decisionPass, grade("", "internal/a.go", "", nil).Decision)
}

// strictDeps is a workspace at the strict level with nothing else declared.
func strictDeps(t *testing.T) Dependencies {
	t.Helper()
	return levelDeps(t, types.GuardProfileStrict)
}

// The meters judge the main thread only, advise it at any level, and leave a subagent's
// calls alone.
func TestMainThreadCommandsAreJudgedByTheCostRules(t *testing.T) {
	ctx, cache := spawnFixture(t)
	deps := strictDeps(t)
	main := hookAttribution{Host: "claude-code", Session: "s"}
	writeCostState(hint.NewGate(cache, main.factsKey()), costState{Streak: 7})
	judge := func(agent string) types.GuardJudgment {
		return Judge(ctx, deps, Request{Input: "git diff", Host: "claude-code", Session: "s", Agent: agent})
	}

	inferred := judge("")
	assert.Equal(t, types.GuardJudgment{Decision: decisionAdvise, Rule: string(denyRuleRootHeavyCommand)}, judgedAs(inferred),
		"the host has named no subagent, so the thread is inferred")

	assert.NotEqual(t, decisionDeny, judge("a1").Decision, "a subagent's diff is its job")
	assert.NotEqual(t, decisionDeny, judge("a1").Decision)

	assert.NotEqual(t, decisionDeny, judge("").Decision, "the host has now named a subagent, and a strict meter still only advises")
}

// A spawn naming no model is refused at strict. A row forked with no model tier has no tier
// to compare a named model against, and the declaration is never opened for it.
func TestMainThreadSpawnsAreJudgedByTheSpawnRules(t *testing.T) {
	ctx, _ := fleetFixture(t, types.Job{ID: "scout", ReadOnly: true, State: types.StateDeclared, Model: "haiku"})
	deps := strictDeps(t)
	asked := 0
	deps.ModelTiers = func() *types.ModelTierSet {
		asked++
		return testModelTiers(t, "claude-code")
	}
	at := hookLocation(ctx, deps)
	hostGate(at.cacheDir, "claude-code").MarkFired(hostAgentsKind)

	v := judgeSpawn(ctx, t, deps, "")
	assert.Equal(t, types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRuleSpawnNamesModel)}, judgedAs(v))

	v = judgeSpawn(ctx, t, deps, "opus")
	assert.NotEqual(t, string(denyRuleSpawnNamesModel), v.Rule, "the row records no model tier")
	assert.NotEqual(t, string(denyRuleSpawnAboveModelTier), v.Rule)
	assert.Zero(t, asked, "a row with no tier never opens the declaration")
}

// At strict a spawn runs at the model tier its row was forked at, compared by rank through
// the host's aliases, and the refusal names the row's whole assignment.
func TestSpawnNamesModelComparesModelTierRanks(t *testing.T) {
	set := testModelTiers(t, "claude-code")
	ctx := tierFixture(t, set, types.Job{ID: "scout", ReadOnly: true, State: types.StateDeclared})
	deps := strictDeps(t)
	deps.ModelTiers = func() *types.ModelTierSet { return set }
	at := hookLocation(ctx, deps)
	hostGate(at.cacheDir, "claude-code").MarkFired(hostAgentsKind)

	v := judgeSpawn(ctx, t, deps, "haiku")
	assert.NotEqual(t, string(denyRuleSpawnNamesModel), v.Rule, "haiku is the economy alias")

	v = judgeSpawn(ctx, t, deps, "opus")
	assert.Equal(t, types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRuleSpawnNamesModel)}, judgedAs(v))
	assert.Contains(t, v.Reason, `magus workspace: this spawn runs opus, model tier principal (rank 2), and job scout was forked at `+
		`economy (rank 0) -> haiku, matrix: "two files" at hack/tiers.buzz:12; spawn it on haiku, or re-fork the job at the tier its work needs.`)

	v = judgeSpawn(ctx, t, deps, "claude-haiku-5-5")
	assert.Equal(t, decisionDeny, v.Decision, "an id no alias names has no tier")
	assert.Contains(t, storedVerdict(t, at.cacheDir, v.Reason), "claude-haiku-5-5, which harness claude-code maps to no model tier",
		"a repeat cites the rule's summary inline and stores the full reason")
}

// A declaration that maps no model for the spawning host is refused at strict, naming the
// hosts entry to add.
func TestSpawnNamesModelNamesTheHostEntryToAdd(t *testing.T) {
	set := testModelTiers(t, "other-host")
	ctx := tierFixture(t, set, types.Job{ID: "scout", ReadOnly: true, State: types.StateDeclared})
	deps := strictDeps(t)
	deps.ModelTiers = func() *types.ModelTierSet { return set }
	at := hookLocation(ctx, deps)
	hostGate(at.cacheDir, "claude-code").MarkFired(hostAgentsKind)

	v := judgeSpawn(ctx, t, deps, "haiku")

	assert.Equal(t, types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRuleSpawnNamesModel)}, judgedAs(v))
	assert.Contains(t, v.Reason, "maps no model for harness claude-code: its hosts are other-host")
	assert.Contains(t, v.Reason, "add a claude-code entry to the hosts magus\\job.modelTiers declares")
}

// A spawn on a tier ranked above its row's is advised, never refused, and one at or below
// it is not.
func TestSpawnAboveModelTierAdvisesByRank(t *testing.T) {
	set := testModelTiers(t, "claude-code")
	ctx := tierFixture(t, set, types.Job{ID: "scout", ReadOnly: true, State: types.StateDeclared})
	deps := levelDeps(t, types.GuardProfileStandard)
	deps.ModelTiers = func() *types.ModelTierSet { return set }
	at := hookLocation(ctx, deps)
	hostGate(at.cacheDir, "claude-code").MarkFired(hostAgentsKind)

	v := judgeSpawn(ctx, t, deps, "haiku")
	assert.NotEqual(t, string(denyRuleSpawnAboveModelTier), v.Rule)

	v = judgeSpawn(ctx, t, deps, "opus")
	assert.Equal(t, decisionAdvise, v.Decision, "it never refuses")
	assert.Contains(t, v.Context, `this spawn runs opus, model tier principal (rank 2), above job scout's economy (rank 0) -> haiku, matrix: "two files" at hack/tiers.buzz:12`)
}

// A root-context-budget figure above every window the model table holds is refused as a
// misdeclaration on the main thread's commands; with no table there is nothing to bound it.
func TestBudgetTokensPastEveryWindowIsRefused(t *testing.T) {
	deps := strictDeps(t)
	deps.Builtins[string(denyRuleRootContextBudget)] = builtin.Setting{Decision: builtin.Advise, Tokens: 2_000_000}
	judge := func(rows bool) types.GuardJudgment {
		ctx, cache := spawnFixture(t)
		d := deps
		if rows {
			d.ModelRows = fixtureModelSource
		}
		hostGate(cache, "claude-code").MarkFired(hostAgentsKind)
		return Judge(ctx, d, Request{Input: "ls", Host: "claude-code", Session: "s"})
	}

	assert.NotEqual(t, string(denyRuleRootContextBudget), judge(false).Rule, "no rows, no window to bound by")

	v := judge(true)
	assert.Equal(t, types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRuleRootContextBudget)}, judgedAs(v))
	assert.Contains(t, v.Reason, string(types.GuardRuleMisdeclared))
	assert.Contains(t, v.Reason, `built-in rule "root-context-budget": tokens is 2000000, past the largest context window in the model table (1000000); give 1000000 or less`)
}

func TestBudgetPastEveryWindowReadsTheTableOnlyForAStatedFigure(t *testing.T) {
	t.Parallel()
	reads := 0
	table := func() model.Table {
		reads++
		return fixtureModelTable()
	}
	_, refused := budgetPastEveryWindow(builtin.Setting{Decision: builtin.Deny}, table)
	assert.False(t, refused)
	assert.Zero(t, reads, "no figure, no table")
	_, refused = budgetPastEveryWindow(builtin.Setting{Decision: builtin.Off, Tokens: 5_000_000}, table)
	assert.False(t, refused, "an off rule fires at no figure")
	_, refused = budgetPastEveryWindow(builtin.Setting{Decision: builtin.Advise, Tokens: 1_000_000}, table)
	assert.False(t, refused, "the largest window itself is reachable")
	_, refused = budgetPastEveryWindow(builtin.Setting{Decision: builtin.Advise, Tokens: 1_000_001}, table)
	assert.True(t, refused)
}

// levelDeps is a workspace at level with nothing else declared.
func levelDeps(t *testing.T, level types.GuardProfile) Dependencies {
	t.Helper()
	expanded, err := builtin.ResolveProfile(level, nil)
	require.NoError(t, err)
	resolved, err := builtin.Resolve(expanded)
	require.NoError(t, err)
	return Dependencies{Builtins: resolved}
}

// testModelTiers is the shipped tier shape with host mapping its aliases and an economy
// context cap of 100k.
func testModelTiers(t *testing.T, host string) *types.ModelTierSet {
	t.Helper()
	set, err := types.NewModelTierSet(types.ModelTierDeclaration{
		Enum: "ModelTier", Site: "hack/tiers.buzz:3", Cases: []string{"economy", "standard", "principal"}, Default: 1,
		Bounds: []types.ModelTierBounds{{MaxContext: 100_000}, {}, {}},
		Skills: []types.SkillVariant{types.SkillVariantFull, types.SkillVariantShort, types.SkillVariantShort},
		Hosts:  map[types.HarnessKey][]string{{ID: host}: {"haiku", "sonnet", "opus"}},
	})
	require.NoError(t, err)
	return set
}

// tierFixture forks rows through a job store that assigns their model tiers from set, every
// one at economy by a matrix, and pins the location there.
func tierFixture(t *testing.T, set *types.ModelTierSet, rows ...types.Job) context.Context {
	t.Helper()
	testkit.Isolate(t)
	root, cacheDir := t.TempDir(), t.TempDir()
	matrix := func(context.Context, types.ModelTierRequest) (*types.JobModelTier, error) {
		return &types.JobModelTier{Name: "economy", Rank: 0, Skills: types.SkillVariantFull, Reason: "two files", Site: "hack/tiers.buzz:12"}, nil
	}
	store := job.NewStore(job.Location{CacheDir: cacheDir, Root: root, ModelTiers: set, ModelTierMatrix: matrix})
	for _, row := range rows {
		_, err := store.Update(t.Context(), row.ID, func(cur *types.Job) { *cur = row })
		require.NoError(t, err)
	}
	return context.WithValue(t.Context(), locationKey{}, location{cacheDir: cacheDir, workspace: root})
}

// judgeSpawn judges a Claude Code main-thread spawn of job scout on model, "" for none.
func judgeSpawn(ctx context.Context, t *testing.T, deps Dependencies, model string) types.GuardJudgment {
	t.Helper()
	input := map[string]any{"description": "orchestrator/scout scout", "prompt": "Read internal/job and report."}
	if model != "" {
		input["model"] = model
	}
	return Judge(ctx, deps, Request{Host: "claude-code", Input: hookJSON(t, map[string]any{
		"session_id": "s", "hook_event_name": "PreToolUse", "tool_name": "Agent", "tool_input": input,
	})})
}
