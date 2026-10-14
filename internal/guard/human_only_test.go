package guard

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/gen/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const benchReason = "launches paid Claude sessions, $0.05-$4 each"

// benchWorkspace declares agent-bench-run human_only in the root, composed by bench-all,
// and evals:grade human_only, composed across projects by the root's evals-all.
func benchWorkspace(t *testing.T) Dependencies {
	t.Helper()
	policies := map[string]types.Target{
		"agent-bench-run": {HumanOnly: true, HumanOnlyReason: benchReason, SkipCache: true, SkipCacheReason: "a replay is a run that never happened"},
	}
	chains := map[string][]types.ChainStep{
		"bench-all": {{Target: "agent-bench-run"}},
		"evals-all": {{Project: "evals", Target: "grade"}},
		"test":      {{Target: "go-test"}},
	}
	root := &types.Project{Path: ".", TargetPolicies: policies, TargetChains: chains}
	evals := &types.Project{
		Path:           "evals",
		TargetPolicies: map[string]types.Target{"grade": {HumanOnly: true, HumanOnlyReason: "spends model tokens per case"}},
	}
	projects := []*types.Project{root, evals}
	ws := mocks.NewMockWorkspaceRepository(t)
	ws.EXPECT().All().Return(projects).Maybe()
	ws.EXPECT().Get(mock.Anything).RunAndReturn(func(path string) *types.Project {
		for _, p := range projects {
			if p.Path == path {
				return p
			}
		}
		return nil
	}).Maybe()
	ws.EXPECT().ClassifyFiles(mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	ws.EXPECT().TargetGraph(mock.Anything).Return(types.TargetGraphOutput{}, nil).Maybe()
	return Dependencies{Inspect: func(context.Context, string) (types.WorkspaceRepository, error) { return ws, nil }}
}

// benchWorker is a leased worker whose own check is the human-only target, so
// worker-check-only passes the run and human-only is the rule that answers.
func benchWorker() types.Job {
	return types.Job{
		ID:         "bench/run",
		Parent:     "root",
		Criteria:   "run the benchmark",
		WritePaths: []string{"benchmarks/agent/**"},
		Check:      &types.LeaseCheck{Target: "agent-bench-run", Project: "."},
		State:      types.StateRunning,
		Registered: 1,
	}
}

// TestHumanOnlyVerdicts pins the verdict table: an unbound session naming a human-only
// target is asked through a hook that renders ask and refused through one that cannot, a
// leased worker is refused, and a run reaching one through a chain is refused for anyone.
func TestHumanOnlyVerdicts(t *testing.T) {
	for _, tt := range []struct {
		name       string
		command    string
		lease      bool
		rendersAsk bool
		want       string
		contains   []string
	}{
		{"an unbound session through a hook that prompts", "magus run agent-bench-run .", false, true, "ask",
			[]string{"`magus run agent-bench-run .` is run by a person: " + benchReason, "never cached (skip_cache)", "Approving runs it once. To run it yourself: magus run agent-bench-run ."}},
		{"affected naming it", "magus affected agent-bench-run", false, true, "ask",
			[]string{"`magus affected agent-bench-run` is run by a person"}},
		{"an unbound session through a hook that cannot prompt", "magus run agent-bench-run .", false, false, "deny",
			[]string{"predates approval prompts", "running `magus run agent-bench-run .`", "magus describe harness"}},
		{"a leased worker", "magus run agent-bench-run .", true, true, "deny",
			[]string{benchReason, "bound to job bench/run", "Workers do not spend"}},
		{"a chain reaching it", "magus run bench-all .", false, true, "deny",
			[]string{"- .:agent-bench-run: " + benchReason, "run the targets it composes that are not human-only"}},		{"a chain reaching one in another project", "magus run evals-all .", false, true, "deny",
			[]string{"- evals:grade: spends model tokens per case"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			row := benchWorker()
			ctx, _ := fleetFixture(t, row)
			req := Request{Input: tt.command, RendersAsk: tt.rendersAsk}
			if tt.lease {
				req.Lease = row.ID
			}
			v := Judge(ctx, benchWorkspace(t), req)
			assert.Equal(t, verdictWithRule(tt.want, string(denyRuleHumanOnly)), unworded(v))
			full := v.Reason
			if tt.want == "deny" {
				full = storedVerdict(t, hookLocation(ctx, Dependencies{}).cacheDir, v.Reason)
			}
			for _, s := range tt.contains {
				assert.Contains(t, full, s)
			}
		})
	}
}

// TestHumanOnlyJudgesAClientScript pins the MCP door: a client script's literal run is
// asked about as the shell line would be, and a run the guard cannot read is refused
// wherever a human-only target is declared.
func TestHumanOnlyJudgesAClientScript(t *testing.T) {
	client := func(t *testing.T, script string) string {
		return hookJSON(t, map[string]any{"session_id": "s", "hook_event_name": "PreToolUse",
			"tool_name": "mcp__magus__client", "tool_input": map[string]any{"script": `import "magus"; ` + script}})
	}
	for _, tt := range []struct {
		name, script, want, contains string
	}{
		{"a literal run", `magus\run(["agent-bench-run", "."]);`, "ask", "is run by a person: " + benchReason},
		{"a literal affected", `magus\cmd("affected", ["agent-bench-run"]);`, "ask", "is run by a person: " + benchReason},
		{"a computed argv", `final a = ["agent-bench-run"]; magus\run(a);`, "deny", "- .:agent-bench-run: " + benchReason},
		{"a script that does not parse", `magus\run(`, "deny", "- evals:grade: spends model tokens per case"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := fleetFixture(t)
			v := Judge(ctx, benchWorkspace(t), Request{Input: client(t, tt.script), Host: "claude-code", RendersAsk: true})
			assert.Equal(t, verdictWithRule(tt.want, string(denyRuleHumanOnly)), unworded(v))
			full := v.Reason
			if tt.want == "deny" {
				full = storedVerdict(t, hookLocation(ctx, Dependencies{}).cacheDir, v.Reason)
				assert.Contains(t, full, "cannot read")
			}
			assert.Contains(t, full, tt.contains)
		})
	}

	ctx, _ := fleetFixture(t)
	v := Judge(ctx, benchWorkspace(t), Request{Input: client(t, `magus\run(["test", "."]);`), Host: "claude-code", RendersAsk: true})
	assert.NotEqual(t, string(denyRuleHumanOnly), v.Rule, "a run reaching no human-only target is left alone")
}

// TestHumanOnlyLeavesOtherRunsAlone pins that the rule fires only on a declared target a
// parsed run reaches: another target, a report about the run, and a project that does not
// declare it all pass.
func TestHumanOnlyLeavesOtherRunsAlone(t *testing.T) {
	ctx, _ := fleetFixture(t)
	deps := benchWorkspace(t)
	for _, command := range []string{
		"magus run test .",
		"magus run agent-bench-run . --plan",
		"magus run agent-bench-run evals",
		"magus explain target:.:agent-bench-run",
		"echo magus run agent-bench-run .",
	} {
		decision, reason := gradeHumanOnly(ctx, deps, "", command)
		assert.Empty(t, decision, "%q: %s", command, reason)
	}
}

// TestHumanOnlyNeverYieldsToPreauth pins that a command magus itself served is still put
// to the person: the cost is theirs, not the suggestion's.
func TestHumanOnlyNeverYieldsToPreauth(t *testing.T) {
	ctx, _ := fleetFixture(t)
	gate := hint.NewGate(hookLocation(ctx, Dependencies{}).cacheDir, "")
	serveNext(t, gate, "run-target", "magus", "run", "agent-bench-run", ".")
	require.Equal(t, "run-target", servedNextPreauthorizes(gate, "magus run agent-bench-run ."))

	v := Judge(ctx, benchWorkspace(t), Request{Input: "magus run agent-bench-run .", RendersAsk: true})
	assert.Equal(t, verdictWithRule("ask", string(denyRuleHumanOnly)), unworded(v))
}

// TestHumanOnlySetToAdviseLetsTheRunProceed pins the loosening a workspace may declare:
// the reason is told and nobody is asked.
func TestHumanOnlySetToAdviseLetsTheRunProceed(t *testing.T) {
	ctx, _ := fleetFixture(t)
	deps := benchWorkspace(t)
	deps.Builtins = map[string]builtin.Setting{string(denyRuleHumanOnly): {Decision: builtin.Advise}}
	v := Judge(ctx, deps, Request{Input: "magus run agent-bench-run .", RendersAsk: true})
	assert.NotContains(t, []string{"ask", "deny"}, v.Decision)
}

// TestHumanOnlyReasonCarriesTheRecord pins the facts magus adds to the declaration: the
// recorded p75 and run count, read from the run history the workspace configures.
func TestHumanOnlyReasonCarriesTheRecord(t *testing.T) {
	ctx, _ := fleetFixture(t)
	history := filepath.Join(t.TempDir(), "history.json")
	h := forecast.History{Version: forecast.HistoryVersion, Projects: map[string]map[string]forecast.Stats{
		".": {"magusfile/agent-bench-run": {P75Ms: 372_000, Samples: 4}},
	}}
	require.NoError(t, h.Save(ctx, history))
	t.Setenv("MAGUS_HISTORY_PATH", history)

	decision, reason := gradeHumanOnly(ctx, benchWorkspace(t), "", "magus run agent-bench-run .")
	assert.Equal(t, "ask", decision)
	assert.Contains(t, reason, "Recorded: p75 6m12s over 4 runs; never cached (skip_cache).")
}

// TestBriefCommandRefusesAHumanOnlyRun pins the spawn half: a brief telling a bound worker
// to run a human-only target is refused before the worker exists to be refused.
func TestBriefCommandRefusesAHumanOnlyRun(t *testing.T) {
	row := benchWorker()
	ctx, root := fleetFixture(t, row)
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), nil, 0o644))
	cacheDir := hookLocation(ctx, Dependencies{}).cacheDir
	deps := benchWorkspace(t)
	deps.CacheDir = func(string) (string, error) { return cacheDir, nil }
	deps.scope = workspaceScope{root: root}

	brief := "JOB ID: " + row.ID + "\nRun the benchmark:\n\n```sh\nmagus run agent-bench-run .\n```\n"
	v := judgeBriefOffCheck(deps, briefInput{text: brief})
	assert.Equal(t, denyRuleBriefCommand, v.Rule.Name)
	assert.Equal(t, string(denyRuleHumanOnly), v.Rule.Arg)
	assert.Contains(t, v.Deny, "job bench/run's worker to run `.:agent-bench-run`, which a person runs")
	assert.Contains(t, v.Why, benchReason)

	forbidding := "JOB ID: " + row.ID + "\nNever run `magus run agent-bench-run .` yourself.\n"
	assert.NotEqual(t, string(denyRuleHumanOnly), judgeBriefOffCheck(deps, briefInput{text: forbidding}).Rule.Arg,
		"a line that forbids the run teaches the opposite")
}
