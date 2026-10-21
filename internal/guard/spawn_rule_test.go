package guard

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/hint"
	// The interpreter a real magusfile load needs, which cmd/magus links in production.
	"github.com/egladman/magus/internal/fixture"
	_ "github.com/egladman/magus/internal/interp/bindings"
	_ "github.com/egladman/magus/internal/interp/engine/buzz"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// spawnRuleProbe is a workspace rule that records what it was asked and answers as told.
type spawnRuleProbe struct {
	asked  []types.SpawnRequest
	gates  []hint.Gate
	answer types.GuardVerdict
	err    error
}

func (p *spawnRuleProbe) rule() workspace.SpawnRule {
	return func(_ context.Context, req types.SpawnRequest, facts hint.Gate) (types.GuardVerdict, error) {
		p.asked = append(p.asked, req)
		p.gates = append(p.gates, facts)
		return p.answer, p.err
	}
}

// spawnFixture pins the trail, markers and job store to temporary dirs, so a spawn test
// writes nothing a developer's checkout reads.
func spawnFixture(t *testing.T) (ctx context.Context, cacheDir string) {
	t.Helper()
	testkit.Isolate(t)
	root := testkit.NewWorkspace(t).Root()
	cacheDir = t.TempDir()
	return WithLocation(t.Context(), cacheDir, root, root), cacheDir
}

// Envelopes as each host's wiring hands them to `magus shell`, shapes copied from the
// existing guard and shell tests.
const (
	claudeSpawnEnvelope = `{"session_id":"8f2c6a1e","transcript_path":"/Users/dev/.claude/projects/p/8f2c6a1e.jsonl",` +
		`"cwd":"/Users/dev/repo","permission_mode":"default","hook_event_name":"PreToolUse","tool_name":"Agent",` +
		`"tool_input":{"description":"orchestrator/brisk-heron/implement adr 0002","prompt":"Implement ADR 0002.",` +
		`"subagent_type":"general-purpose","model":"sonnet","name":"brisk-heron","run_in_background":true,"isolation":"worktree"},` +
		`"tool_use_id":"toolu_01"}`
	claudeContinueEnvelope = `{"session_id":"8f2c6a1e","cwd":"/Users/dev/repo","hook_event_name":"PreToolUse",` +
		`"tool_name":"SendMessage","tool_input":{"to":"brisk-heron","message":"Now fix the review comments."},"tool_use_id":"toolu_02"}`
	// The Cursor glue rewrites subagentStart into this shape: the parent conversation as the
	// session, the handed task as the prompt.
	cursorGlueEnvelope = `{"hook_event_name":"subagentStart","session_id":"conv-parent",` +
		`"tool_input":{"prompt":"Audit internal/job","subagent_type":"explore"}}`
	// The raw subagentStart payload, which the decoder reads without the glue's help.
	cursorRawEnvelope = `{"hook_event_name":"subagentStart","conversation_id":"conv-child",` +
		`"parent_conversation_id":"conv-parent","subagent_type":"explore","task":"Audit internal/job"}`
	// No shipped Codex wiring routes a spawn, because its SubagentStart carries no prompt.
	// This pins that a Codex-shaped event carrying one would be read by shape all the same.
	codexShapedEnvelope = `{"session_id":"codex-s","turn_id":"turn-1","hook_event_name":"PreToolUse",` +
		`"tool_name":"spawn_agent","tool_input":{"prompt":"Audit internal/job"},"permission_mode":"default","cwd":"/Users/dev/repo"}`
)

// TestSpawnRuleSeesEveryHostsEnvelope is the host-agnostic contract: whichever host sent
// the event, the rule gets the same normalized request, and a field the host did not
// send stays empty rather than guessed.
func TestSpawnRuleSeesEveryHostsEnvelope(t *testing.T) {
	cases := []struct {
		name, host, event string
		want              types.SpawnRequest
	}{
		{
			name: "claude-code spawn", host: "claude-code", event: claudeSpawnEnvelope,
			want: types.SpawnRequest{
				Kind: types.SpawnKindSpawn, Host: "claude-code", Session: "8f2c6a1e", Model: "sonnet",
				AgentType: "general-purpose", Description: "orchestrator/brisk-heron/implement adr 0002",
				Name: "brisk-heron", Prompt: "Implement ADR 0002.", Background: true, Isolated: true,
				Role: types.AgentRoleRoot, Thread: types.AgentThreadMain, ThreadFrom: types.ThreadInferred,
			},
		},
		{
			name: "claude-code continue", host: "claude-code", event: claudeContinueEnvelope,
			want: types.SpawnRequest{
				Kind: types.SpawnKindContinue, Host: "claude-code", Session: "8f2c6a1e",
				Prompt: "Now fix the review comments.", Role: types.AgentRoleRoot, Thread: types.AgentThreadMain, ThreadFrom: types.ThreadInferred,
				Target: &types.SpawnTarget{Agent: "brisk-heron"},
			},
		},
		{
			name: "cursor through its glue", host: "cursor", event: cursorGlueEnvelope,
			want: types.SpawnRequest{
				Kind: types.SpawnKindSpawn, Host: "cursor", Session: "conv-parent", AgentType: "explore",
				Prompt: "Audit internal/job", Role: types.AgentRoleRoot, Thread: types.AgentThreadMain, ThreadFrom: types.ThreadInferred,
			},
		},
		{
			name: "cursor raw", host: "cursor", event: cursorRawEnvelope,
			want: types.SpawnRequest{
				Kind: types.SpawnKindSpawn, Host: "cursor", Session: "conv-parent", AgentType: "explore",
				Prompt: "Audit internal/job", Role: types.AgentRoleRoot, Thread: types.AgentThreadMain, ThreadFrom: types.ThreadInferred,
			},
		},
		{
			name: "codex-shaped", host: "codex", event: codexShapedEnvelope,
			want: types.SpawnRequest{
				Kind: types.SpawnKindSpawn, Host: "codex", Session: "codex-s",
				Prompt: "Audit internal/job", Role: types.AgentRoleRoot, Thread: types.AgentThreadMain, ThreadFrom: types.ThreadInferred,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := spawnFixture(t)
			probe := &spawnRuleProbe{}
			v := Judge(ctx, Dependencies{SpawnRule: probe.rule()}, Request{Input: tc.event, Host: tc.host})
			assert.Equal(t, "pass", v.Decision)
			require.Len(t, probe.asked, 1, "the rule is asked once per event")
			assert.Equal(t, tc.want, probe.asked[0])
			assert.Equal(t, FactsKey(tc.host, tc.want.Session), probe.gates[0].Session(),
				"once and count are keyed on the calling session")
		})
	}
}

// OpenCode's plugin hands the guard a bare command string, never an envelope, so there is
// no spawn for a rule to see there; a string is judged as the command it is.
func TestSpawnRuleIsNotAskedAboutACommand(t *testing.T) {
	ctx, _ := spawnFixture(t)
	probe := &spawnRuleProbe{answer: types.GuardVerdict{Decision: types.GuardDeny, Reason: "no"}}
	Judge(ctx, Dependencies{SpawnRule: probe.rule()}, Request{Input: "ls", Host: "opencode"})
	assert.Empty(t, probe.asked)
}

// Strengthen only: a workspace allow cannot lift a built-in deny, and it is not even
// asked when one stands.
func TestSpawnRuleCannotLiftABuiltInDeny(t *testing.T) {
	ctx, _ := spawnFixture(t)
	probe := &spawnRuleProbe{answer: types.GuardVerdict{Decision: types.GuardAllow}}
	v := Judge(ctx, strict(Dependencies{SpawnRule: probe.rule()}), Request{Input: claudeSpawnEnvelope, Host: "claude-code", ObservesSkillLoads: true})
	assert.Equal(t, verdictWithRule("deny", string(denySpawnUnbriefed)), unworded(v), "the built-in reason stands")
	assert.Empty(t, probe.asked)
}

// A built-in spawn deny is worded as a command's is: the verdict inline, the rationale and
// the rule's page behind the ref, and one line naming the rule on a repeat.
func TestSpawnDenyIsTheVerdictAndARef(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	deps := strict(Dependencies{})
	req := Request{Input: claudeSpawnEnvelope, Host: "claude-code", ObservesSkillLoads: true}

	stored := shortDeny(t, cacheDir, Judge(ctx, deps, req), denySpawnUnbriefed,
		"spawning before the magus-multi-agent skill loaded; load Skill(magus-multi-agent), then retry.")
	assert.Contains(t, stored, "\nIt carries the lease, worktree, model-naming and git rules")
	assert.Contains(t, stored, "\nsee: "+ruleDocsBase+string(denySpawnUnbriefed)+"/")

	repeat := Judge(ctx, deps, req)
	doc, ok := Rule(string(denySpawnUnbriefed))
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(repeat.Reason, "denied again [spawn-unbriefed]: "+doc.Catches+"\nfull verdict: "), repeat.Reason)
}

// The trail row of a denied spawn names the ref the deny cited, as a command's row does, so a
// reader of the row can reopen the refusal the agent was shown.
func TestSpawnDenyTrailRowCarriesTheVerdictRef(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	deps := strict(Dependencies{})
	req := Request{Input: claudeSpawnEnvelope, Host: "claude-code", ObservesSkillLoads: true}

	first := Judge(ctx, deps, req)
	repeat := Judge(ctx, deps, req)

	rows := trailEvents(t, cacheDir, trail.KindAgentSpawn)
	require.Len(t, rows, 2)
	for i, shown := range []types.GuardJudgment{first, repeat} {
		ref := verdictRef.FindString(shown.Reason)
		require.NotEmpty(t, ref, "the deny cites its stored verdict: %q", shown.Reason)
		assert.Equal(t, ref, rows[i].VerdictRef, "row %d", i)
		stored, err := trail.ReadBlob(cacheDir, rows[i].VerdictRef)
		require.NoError(t, err)
		assert.Contains(t, string(stored), "\nsee: "+ruleDocsBase+string(denySpawnUnbriefed)+"/")
	}
}

func TestSpawnUnbriefedAdvisesWhenSetToAdvise(t *testing.T) {
	ctx, _ := spawnFixture(t)
	req := Request{Input: claudeSpawnEnvelope, Host: "claude-code", ObservesSkillLoads: true}
	lowered := advising(Dependencies{}, string(denySpawnUnbriefed))

	first := Judge(ctx, lowered, req)
	// The context is the advice prose; the reason stays empty, which the comparison pins.
	got := first
	got.Context, got.Lease, got.LeaseFrom = "", "", ""
	assert.Equal(t, verdictWithRule("advise", string(denySpawnUnbriefed)), got)

	again := Judge(ctx, lowered, req)
	assert.NotEqual(t, string(denySpawnUnbriefed), again.Rule, "once per session")
}

func TestSpawnRuleDenyAndAdviseReachTheVerdict(t *testing.T) {
	ctx, _ := spawnFixture(t)
	deny := &spawnRuleProbe{answer: types.GuardVerdict{Decision: types.GuardDeny, Reason: "Name a model."}}
	v := Judge(ctx, Dependencies{SpawnRule: deny.rule()}, Request{Input: claudeContinueEnvelope, Host: "claude-code"})
	assert.Equal(t, types.GuardJudgment{SchemaVersion: v.SchemaVersion, Decision: "deny", Reason: "Name a model.", Rule: workspaceSpawnRule}, v)

	ctx, _ = spawnFixture(t)
	advise := &spawnRuleProbe{answer: types.GuardVerdict{Decision: types.GuardAdvise, Reason: "Add a Done when section."}}
	v = Judge(ctx, Dependencies{SpawnRule: advise.rule()}, Request{Input: cursorGlueEnvelope, Host: "cursor"})
	assert.Equal(t, types.GuardJudgment{SchemaVersion: v.SchemaVersion, Decision: "advise", Context: "Add a Done when section.", Rule: workspaceSpawnRule}, v)
}

// A broken rule judges nothing, following guard.shell's fail-open stance on a workspace
// rule it cannot use. Its failure is told in full once per session, and every spawn it
// skips still says which rules applied.
func TestSpawnRuleFailureFailsOpen(t *testing.T) {
	ctx, _ := spawnFixture(t)
	probe := &spawnRuleProbe{err: errors.New("magus\\guard.spawn: the rule raised: boom")}
	deps := Dependencies{SpawnRule: probe.rule()}

	first := Judge(ctx, deps, Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
	assert.Equal(t, verdictWithRule("advise", string(advisorySpawnRuleFailed)), unworded(first))
	assert.Contains(t, first.Context, "boom")
	assert.Contains(t, first.Context, "Only the built-in rules applied to this spawn.")

	repeat := Judge(ctx, deps, Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
	assert.Equal(t, "advise", repeat.Decision)
	assert.NotContains(t, repeat.Context, "boom", "the repeat is one line")
	assert.Contains(t, repeat.Context, "Only the built-in rules applied to this spawn.")
}

// The approved rule and the working-tree rule both run and the stricter answer stands,
// so an unapproved edit can tighten but never loosen.
func TestSpawnRulesKeepTheStricterSide(t *testing.T) {
	deny := types.GuardVerdict{Decision: types.GuardDeny, Reason: "unnamed model"}
	allow := types.GuardVerdict{Decision: types.GuardAllow}
	cases := []struct {
		name           string
		live, approved *types.GuardVerdict
		want           string
		decidedBy      string
	}{
		{"a tightening in the working tree applies at once", &deny, &allow, "deny", decidedByWorktree},
		{"a loosening in the working tree waits for approval", &allow, &deny, "deny", decidedByApproved},
		{"no approved side runs the working tree alone", &allow, nil, "pass", ""},
		{"a rule deleted from the working tree still answers from the approved side", nil, &deny, "deny", decidedByApproved},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cacheDir := spawnFixture(t)
			deps := Dependencies{}
			if tc.live != nil {
				deps.SpawnRule = (&spawnRuleProbe{answer: *tc.live}).rule()
			}
			if tc.approved != nil {
				approved := (&spawnRuleProbe{answer: *tc.approved}).rule()
				deps.ApprovedSpawnRule = func(context.Context) (workspace.SpawnRule, error) { return approved, nil }
			}
			v := Judge(ctx, deps, Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
			assert.Equal(t, tc.want, v.Decision)

			spawns := trailEvents(t, cacheDir, trail.KindAgentSpawn)
			require.Len(t, spawns, 1)
			assert.Equal(t, tc.decidedBy, spawns[0].DecidedBy, "the trail names which side decided")
		})
	}
}

// An approved rule that fails to resolve judges nothing and says so, following the stance
// on any broken workspace rule; only running out of time denies.
func TestApprovedResolveFailureIsReported(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	deps := Dependencies{ApprovedSpawnRule: func(context.Context) (workspace.SpawnRule, error) {
		return nil, errors.New("approved magusfile: magusfile.buzz:3:1: expected expression")
	}}
	v := Judge(ctx, deps, Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
	assert.Equal(t, "advise", v.Decision)
	assert.Contains(t, v.Context, "The approved magus\\guard.spawn rule judged nothing: the rule could not be resolved")
	assert.Contains(t, v.Context, "Only the built-in rules applied to this spawn.")

	spawns := trailEvents(t, cacheDir, trail.KindAgentSpawn)
	require.Len(t, spawns, 1)
	assert.Equal(t, []trail.RuleFailure{{
		Side:  decidedByApproved,
		Error: "the rule could not be resolved: approved magusfile: magusfile.buzz:3:1: expected expression",
	}}, readSpawnBlob(t, cacheDir, spawns[0]).RuleFailures)
}

// A built-in advice and a workspace advice both reach the reader, and the trail names both.
func TestDecidedByNamesBothAdvisingSides(t *testing.T) {
	row := types.Job{ID: "wave/worker", Criteria: "the guard", WritePaths: []string{"internal/guard/**"}, State: types.StateRunning, Registered: 1}
	ctx, _ := fleetFixture(t, row)
	cacheDir := hookLocation(ctx, Dependencies{}).cacheDir
	execHere(t, ctx, row.ID)

	probe := &spawnRuleProbe{answer: types.GuardVerdict{Decision: types.GuardAdvise, Reason: "Add a Done when section."}}
	Judge(ctx, Dependencies{SpawnRule: probe.rule()}, Request{Input: claudeSpawnEnvelope, Host: "claude-code"})

	spawns := trailEvents(t, cacheDir, trail.KindAgentSpawn)
	require.Len(t, spawns, 1)
	assert.Equal(t, decidedByBuiltin+decidedByJoin+decidedByWorktree, spawns[0].DecidedBy)
}

// Role is computed from the job store, never declared by the caller.
func TestSpawnRuleRoleIsComputed(t *testing.T) {
	row := types.Job{ID: "wave/worker", Criteria: "the guard", WritePaths: []string{"internal/guard/**"}, State: types.StateRunning, Registered: 1}
	ctx, _ := fleetFixture(t, row)

	probe := &spawnRuleProbe{}
	Judge(ctx, Dependencies{SpawnRule: probe.rule()}, Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
	require.Len(t, probe.asked, 1)
	assert.Equal(t, types.AgentRoleRoot, probe.asked[0].Role)
	assert.Nil(t, probe.asked[0].Lease)

	bindCaller(t, ctx, hookAttribution{Host: "claude-code", Session: "8f2c6a1e"}, row.ID)
	Judge(ctx, Dependencies{SpawnRule: probe.rule()}, Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
	require.Len(t, probe.asked, 2)
	assert.Equal(t, types.AgentRoleWorker, probe.asked[1].Role)
	require.NotNil(t, probe.asked[1].Lease)
	assert.Equal(t, row.ID, probe.asked[1].Lease.ID)
	assert.Equal(t, row.WritePaths, probe.asked[1].Lease.WritePaths, "the worker sees its whole lease row")
}

// A continue reports how long the addressed agent has been idle since magus last saw it,
// and nothing when magus never did.
func TestContinueReportsIdleTime(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	probe := &spawnRuleProbe{}
	deps := Dependencies{SpawnRule: probe.rule()}

	Judge(ctx, deps, Request{Input: claudeContinueEnvelope, Host: "claude-code"})
	require.Len(t, probe.asked, 1)
	assert.Nil(t, probe.asked[0].Target.IdleMs, "an agent magus never saw has no idle time")

	Judge(ctx, deps, Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
	Judge(ctx, deps, Request{Input: finishedSpawnEnvelope, Host: "claude-code"})
	seen := hint.MarkerPath(cacheDir, FactsKey("claude-code", "8f2c6a1e"), agentSeenKind("a1b2c3"))
	aged := time.Now().Add(-10 * time.Minute)
	require.NoError(t, os.Chtimes(seen, aged, aged))

	Judge(ctx, deps, Request{Input: claudeContinueEnvelope, Host: "claude-code"})
	require.Len(t, probe.asked, 3)
	idle := probe.asked[2].Target.IdleMs
	require.NotNil(t, idle)
	assert.GreaterOrEqual(t, *idle, (10 * time.Minute).Milliseconds(), "the name reads the clock filed under the id")

	byID := strings.Replace(claudeContinueEnvelope, `"to":"brisk-heron"`, `"to":"a1b2c3"`, 1)
	Judge(ctx, deps, Request{Input: byID, Host: "claude-code"})
	require.Len(t, probe.asked, 4)
	assert.Less(t, *probe.asked[3].Target.IdleMs, time.Minute.Milliseconds(),
		"a continue by name restarted the clock a continue by id reads")
}

// finishedSpawnEnvelope is claudeSpawnEnvelope's call after it ran, naming the child's id.
const finishedSpawnEnvelope = `{"session_id":"8f2c6a1e","hook_event_name":"PostToolUse","tool_name":"Agent",` +
	`"tool_input":{"description":"orchestrator/brisk-heron/implement adr 0002","prompt":"Implement ADR 0002.","name":"brisk-heron"},` +
	`"tool_response":{"status":"async_launched","agentId":"a1b2c3"}}`

// spawnBlob is the part of an agent_spawn request blob these tests read.
type spawnBlob struct {
	Target       string              `json:"target"`
	RuleFailures []trail.RuleFailure `json:"rule_failures"`
}

func readSpawnBlob(t *testing.T, cacheDir string, e trail.Event) spawnBlob {
	t.Helper()
	body, err := trail.ReadBlob(cacheDir, e.RequestRef)
	require.NoError(t, err)
	var got spawnBlob
	require.NoError(t, json.Unmarshal(body, &got))
	return got
}

// A syntax error left in the working tree cannot switch off the approved rule: the load
// failure no longer reads as "no rule registered", the approved side still denies, and
// the failure is reported and kept on the trail.
func TestBrokenWorkingTreeStillRunsTheApprovedRule(t *testing.T) {
	loadErr := errors.New("magusfile.buzz:3:1: expected expression")
	cases := []struct {
		name     string
		approved types.GuardVerdict
		want     string
		by       string
	}{
		{"an approved deny still denies", types.GuardVerdict{Decision: types.GuardDeny, Reason: "Name a model."}, "deny", decidedByApproved},
		{"an approved allow reports the failure", types.GuardVerdict{Decision: types.GuardAllow}, "advise", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cacheDir := spawnFixture(t)
			approved := (&spawnRuleProbe{answer: tc.approved}).rule()
			deps := Dependencies{
				LoadFailure:       loadErr,
				ApprovedSpawnRule: func(context.Context) (workspace.SpawnRule, error) { return approved, nil },
			}
			v := Judge(ctx, deps, Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
			assert.Equal(t, tc.want, v.Decision)
			if tc.want == "advise" {
				assert.Contains(t, v.Context, "failed to load")
				assert.Contains(t, v.Context, loadErr.Error())
				assert.Contains(t, v.Context, "Only the built-in rules and the approved magus\\guard.spawn rule applied to this spawn.")
			}

			spawns := trailEvents(t, cacheDir, trail.KindAgentSpawn)
			require.Len(t, spawns, 1)
			assert.Equal(t, tc.by, spawns[0].DecidedBy)
			assert.Equal(t, []trail.RuleFailure{{Side: decidedByWorktree, Error: "the magusfile failed to load: " + loadErr.Error()}},
				readSpawnBlob(t, cacheDir, spawns[0]).RuleFailures)
		})
	}
}

// A workspace advise joins a built-in one rather than being dropped behind it.
func TestWorkspaceAdviseJoinsABuiltInAdvise(t *testing.T) {
	row := types.Job{ID: "wave/worker", Criteria: "the guard", WritePaths: []string{"internal/guard/**"}, State: types.StateRunning, Registered: 1}
	ctx, _ := fleetFixture(t, row)
	execHere(t, ctx, row.ID)

	probe := &spawnRuleProbe{answer: types.GuardVerdict{Decision: types.GuardAdvise, Reason: "Add a Done when section."}}
	v := Judge(ctx, Dependencies{SpawnRule: probe.rule()}, Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
	assert.Equal(t, verdictWithRule("advise", string(advisorySharedCheckout)), unworded(v), "the built-in advice keeps its rule")
	assert.Contains(t, v.Context, "Add a Done when section.")
}

// A continuation is recorded like a spawn, naming the agent it addressed by its id.
func TestContinueIsRecordedOnTheTrail(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	deps := Dependencies{SpawnRule: (&spawnRuleProbe{}).rule()}
	Judge(ctx, deps, Request{Input: finishedSpawnEnvelope, Host: "claude-code"})
	Judge(ctx, deps, Request{Input: claudeContinueEnvelope, Host: "claude-code"})

	spawns := trailEvents(t, cacheDir, trail.KindAgentSpawn)
	require.Len(t, spawns, 1)
	assert.Equal(t, trail.ActionAgentContinue, spawns[0].Action)
	assert.Equal(t, spawnBlob{Target: "a1b2c3"}, readSpawnBlob(t, cacheDir, spawns[0]))
}

// parent is what the CALLING agent was spawned as, recorded from the finished spawn call
// that started it and looked up by the agent id its own later calls carry.
func TestSpawnRuleSeesTheCallersParent(t *testing.T) {
	ctx, _ := spawnFixture(t)
	probe := &spawnRuleProbe{}
	deps := Dependencies{SpawnRule: probe.rule()}

	Judge(ctx, deps, Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
	require.Len(t, probe.asked, 1)
	assert.Empty(t, probe.asked[0].Parent, "the root session has no parent")

	finished := `{"session_id":"8f2c6a1e","hook_event_name":"PostToolUse","tool_name":"Agent",` +
		`"tool_input":{"description":"orchestrator/brisk-heron/implement adr 0002","prompt":"Implement ADR 0002.","name":"brisk-heron"},` +
		`"tool_response":{"status":"async_launched","agentId":"a1b2c3"}}`
	assert.Equal(t, "pass", Judge(ctx, deps, Request{Input: finished, Host: "claude-code"}).Decision)
	require.Len(t, probe.asked, 1, "a finished spawn call is recorded, not judged")
	anonymous := `{"session_id":"8f2c6a1e","hook_event_name":"PostToolUse","tool_name":"Agent",` +
		`"tool_input":{"prompt":"Implement ADR 0002."},"tool_response":"done"}`
	assert.Equal(t, "pass", Judge(ctx, deps, Request{Input: anonymous, Host: "claude-code"}).Decision)
	require.Len(t, probe.asked, 1, "a finished call naming no child is still not judged a second time")

	helper := `{"session_id":"8f2c6a1e","agent_id":"a1b2c3","agent_type":"general-purpose","hook_event_name":"PreToolUse",` +
		`"tool_name":"Agent","tool_input":{"description":"brisk-heron/reviewer/read shell.go","prompt":"Review shell.go.","model":"haiku"}}`
	Judge(ctx, deps, Request{Input: helper, Host: "claude-code"})
	require.Len(t, probe.asked, 2)
	assert.Equal(t, "orchestrator/brisk-heron/implement adr 0002", probe.asked[1].Parent)

	unknown := `{"session_id":"8f2c6a1e","agent_id":"never-seen","hook_event_name":"PreToolUse",` +
		`"tool_name":"Agent","tool_input":{"prompt":"x"}}`
	Judge(ctx, deps, Request{Input: unknown, Host: "claude-code"})
	assert.Empty(t, probe.asked[2].Parent, "an agent whose spawn magus never saw finish has no recorded parent")

	Judge(ctx, deps, Request{Input: cursorGlueEnvelope, Host: "cursor"})
	assert.Empty(t, probe.asked[3].Parent, "a host with no subagent identity reports none")
}

// The spawn request names the calling subagent by the payload's id whether or not magus saw
// it spawned, so a rule can tell it from the main agent of the same session.
func TestSpawnRuleSeesTheHostsSubagentID(t *testing.T) {
	ctx, _ := spawnFixture(t)
	probe := &spawnRuleProbe{}
	deps := Dependencies{SpawnRule: probe.rule()}

	unseen := `{"session_id":"8f2c6a1e","agent_id":"never-seen","hook_event_name":"PreToolUse",` +
		`"tool_name":"Agent","tool_input":{"prompt":"x"}}`
	Judge(ctx, deps, Request{Input: unseen, Host: "claude-code"})
	Judge(ctx, deps, Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
	require.Len(t, probe.asked, 2)
	assert.Equal(t, types.SpawnRequest{
		Kind:       types.SpawnKindSpawn,
		Host:       "claude-code",
		Session:    probe.asked[1].Session,
		Prompt:     "x",
		Agent:      "never-seen",
		Role:       types.AgentRoleRoot,
		Thread:     types.AgentThreadSub,
		ThreadFrom: types.ThreadReported,
	}, probe.asked[0], "the subagent reports the main session's id; magus never saw it spawned, so no Parent")
	assert.Empty(t, probe.asked[1].Agent, "the main agent carries no subagent id")
}

// A spawn titled for a live row carries the model tier the row was forked at; a title that
// names no row carries none.
func TestSpawnRuleSeesTheRowsModelTier(t *testing.T) {
	ctx := tierFixture(t, testModelTiers(t, "claude-code"), types.Job{ID: "scout", ReadOnly: true, State: types.StateDeclared, Validated: 1})
	probe := &spawnRuleProbe{}
	deps := Dependencies{SpawnRule: probe.rule()}

	judgeSpawn(ctx, t, deps, "haiku")
	Judge(ctx, deps, Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
	require.Len(t, probe.asked, 2)
	assert.Equal(t, &types.JobModelTier{Name: "economy", Rank: 0, Skills: types.SkillVariantFull, Source: types.ModelTierSourceMatrix,
		Reason: "two files", Site: "hack/tiers.buzz:12"}, probe.asked[0].ModelTier)
	assert.Nil(t, probe.asked[1].ModelTier)
}

// A row that owns a checkout runs its worker there, so the spawn needs no host isolation.
func TestSpawnRuleCountsARowsOwnCheckoutAsIsolation(t *testing.T) {
	ctx := tierFixture(t, testModelTiers(t, "claude-code"),
		types.Job{ID: "scout", ReadOnly: true, State: types.StateDeclared, Validated: 1},
		types.Job{ID: "other", ReadOnly: true, State: types.StateDeclared, Validated: 1})
	at, _ := ctx.Value(locationKey{}).(location)
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: at.cacheDir, Root: at.workspace})
	_, err := store.RecordWorktree(ctx, "scout", types.JobWorktree{Root: t.TempDir(), Source: at.workspace})
	require.NoError(t, err)

	probe := &spawnRuleProbe{}
	deps := Dependencies{SpawnRule: probe.rule()}
	judgeSpawn(ctx, t, deps, "haiku")
	Judge(ctx, deps, Request{Host: "claude-code", Input: hookJSON(t, map[string]any{
		"session_id": "s", "hook_event_name": "PreToolUse", "tool_name": "Agent",
		"tool_input": map[string]any{"description": "orchestrator/scout other", "prompt": "Read internal/job and report."},
	})})
	require.Len(t, probe.asked, 2)
	assert.True(t, probe.asked[0].Isolated, "the title names a row with a checkout of its own")
	assert.False(t, probe.asked[1].Isolated, "a row with none still needs the host's isolation")
}

// trailEvents returns matching events oldest-first: ReadRecent reports newest-first, and a
// lineage assertion reads left-to-right as it happened.
func trailEvents(t *testing.T, base string, kind trail.Kind) []trail.Event {
	t.Helper()
	all, err := trail.ReadRecent(base, 1000)
	require.NoError(t, err)
	var out []trail.Event
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Kind == kind {
			out = append(out, all[i])
		}
	}
	return out
}

// bindCaller records who as acting under id, the record bindOnExec writes.
func bindCaller(t *testing.T, ctx context.Context, who hookAttribution, id string) {
	t.Helper()
	at := hookLocation(ctx, Dependencies{})
	require.NoError(t, job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: at.cacheDir, Root: at.workspace}).Bind(who.caller(), id))
}

// execHere records id as taken in the fixture's checkout, as `magus job exec` does. The
// checkout is committed to git first, since exec reads its base from the tree.
func execHere(t *testing.T, ctx context.Context, id string) {
	t.Helper()
	at := hookLocation(ctx, Dependencies{})
	commitCheckout(t, at.workspace)
	_, err := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: at.cacheDir, Root: at.workspace}).Exec(ctx, id)
	require.NoError(t, err)
}

// commitCheckout makes root a git checkout with everything it holds committed, and returns
// HEAD: a registered base is read from the checkout itself.
func commitCheckout(t *testing.T, root string) string {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		fixtureGit(t, root, "init", "-q", "-b", "main")
	}
	fixtureGit(t, root, "add", "-A")
	fixtureGit(t, root, "commit", "-q", "--allow-empty", "-m", "fixture")
	return fixtureGit(t, root, "rev-parse", "HEAD")
}

// linkedWorktree adds a worktree of the git checkout at main and returns it. Its link names
// main by the path given rather than the one git resolves, since that path is what main's
// job store is keyed on, and the two checkouts share the store only when the keys agree.
func linkedWorktree(t *testing.T, main string) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), "worker")
	fixtureGit(t, main, "worktree", "add", "-q", "-b", "worker", wt)
	require.NoError(t, os.WriteFile(filepath.Join(wt, ".git"),
		[]byte("gitdir: "+filepath.Join(main, ".git", "worktrees", "worker")+"\n"), 0o644))
	return wt
}

// fixtureGit runs git in dir with no global or system config, so a developer's settings
// never reach a fixture, and returns its trimmed output.
func fixtureGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	isolated := []string{"-C", dir, "-c", "user.name=magus test", "-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false", "-c", "core.excludesFile=" + os.DevNull,
		"-c", "maintenance.auto=false", "-c", "gc.auto=0"}
	cmd := exec.Command("git", append(isolated, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

// hookJSON renders an envelope the way a host writes it to the hook's stdin.
func hookJSON(t *testing.T, env map[string]any) string {
	t.Helper()
	body, err := json.Marshal(env)
	require.NoError(t, err)
	return string(body)
}

// finishedSpawn is Claude Code's PostToolUse for a background Agent call: the same
// tool_input the PreToolUse carried, and a response naming the child's id.
func finishedSpawn(t *testing.T, description, name, model, isolation, agentID string) string {
	t.Helper()
	input := map[string]any{"description": description, "prompt": "Carry the job to its done-when.", "subagent_type": "general-purpose", "run_in_background": true}
	for key, value := range map[string]string{"name": name, "model": model, "isolation": isolation} {
		if value != "" {
			input[key] = value
		}
	}
	response := map[string]any{"status": "async_launched", "agentId": agentID, "description": description}
	return hookJSON(t, map[string]any{"session_id": "8f2c6a1e", "transcript_path": "/Users/dev/.claude/projects/p/8f2c6a1e.jsonl", "cwd": "/Users/dev/repo", "permission_mode": "default", "hook_event_name": "PostToolUse", "tool_name": "Agent", "tool_input": input, "tool_use_id": "toolu_01", "tool_response": response})
}

// subagentStop is Claude Code's SubagentStop: no tool call, and the path of the
// subagent's own transcript.
func subagentStop(t *testing.T, agentID, transcript string) string {
	t.Helper()
	return hookJSON(t, map[string]any{"session_id": "8f2c6a1e", "transcript_path": "/Users/dev/.claude/projects/p/8f2c6a1e.jsonl", "cwd": "/Users/dev/repo", "permission_mode": "default", "hook_event_name": "SubagentStop", "stop_hook_active": false, "agent_id": agentID, "agent_type": "general-purpose", "agent_transcript_path": transcript})
}

// Transcript records shaped like a Claude Code subagent log: a user turn, two assistant
// turns carrying usage, then a tool result carrying none.
const (
	transcriptUser      = `{"parentUuid":null,"isSidechain":true,"agentId":"a1b2c3","type":"user","message":{"role":"user","content":"Carry the job to its done-when."},"uuid":"u1","timestamp":"2026-09-22T10:00:00.000Z"}`
	transcriptFirstTurn = `{"parentUuid":"u1","isSidechain":true,"agentId":"a1b2c3","type":"assistant","message":{"model":"claude-sonnet-4-5","id":"msg_01","type":"message","role":"assistant","content":[{"type":"text","text":"Reading the seam."}],"usage":{"input_tokens":3,"cache_creation_input_tokens":5120,"cache_read_input_tokens":14000,"output_tokens":42,"service_tier":"standard"}},"uuid":"a1","timestamp":"2026-09-22T10:00:04.000Z"}`
	transcriptLastTurn  = `{"parentUuid":"a1","isSidechain":true,"agentId":"a1b2c3","type":"assistant","message":{"model":"claude-sonnet-4-5","id":"msg_02","type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_9","name":"Read","input":{"file_path":"/Users/dev/repo/types/spawn.go"}}],"usage":{"input_tokens":5,"cache_creation_input_tokens":800,"cache_read_input_tokens":19120,"output_tokens":210,"service_tier":"standard"}},"uuid":"a2","timestamp":"2026-09-22T10:00:09.000Z"}`
	transcriptToolReply = `{"parentUuid":"a2","isSidechain":true,"agentId":"a1b2c3","type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_9","content":"package types"}]},"uuid":"u2","timestamp":"2026-09-22T10:00:09.500Z"}`
	// transcriptLastTokens is transcriptLastTurn's input + cache read + cache write.
	transcriptLastTokens = int64(5 + 19120 + 800)
)

func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent-a1b2c3.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644))
	return path
}

func TestLastContextUsageReadsTheTail(t *testing.T) {
	// A record bigger than the tail, so the only usage before it is out of reach.
	filler := `{"type":"user","message":{"role":"user","content":"` + strings.Repeat("x", agentUsageTail+1024) + `"}}`
	cases := []struct {
		name string
		path func(t *testing.T) string
		want int64
		ok   bool
	}{
		{"the last usage record wins", func(t *testing.T) string {
			return writeTranscript(t, transcriptUser, transcriptFirstTurn, transcriptLastTurn, transcriptToolReply)
		}, transcriptLastTokens, true},
		{"a usage record after a cut first line", func(t *testing.T) string {
			return writeTranscript(t, transcriptFirstTurn, filler, transcriptLastTurn)
		}, transcriptLastTokens, true},
		{"usage only before the tail is not read", func(t *testing.T) string {
			return writeTranscript(t, transcriptFirstTurn, filler, transcriptToolReply)
		}, 0, false},
		{"no usage at all", func(t *testing.T) string {
			return writeTranscript(t, transcriptUser, transcriptToolReply)
		}, 0, false},
		{"a malformed line is skipped", func(t *testing.T) string {
			return writeTranscript(t, transcriptLastTurn, `{"type":"assistant","message":`)
		}, transcriptLastTokens, true},
		{"an empty file", func(t *testing.T) string { return writeTranscript(t) }, 0, false},
		{"a missing file", func(t *testing.T) string { return filepath.Join(t.TempDir(), "gone.jsonl") }, 0, false},
		{"a relative path", func(*testing.T) string { return "agent-a1b2c3.jsonl" }, 0, false},
		{"a directory", func(t *testing.T) string { return t.TempDir() }, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := lastContextUsage(tc.path(t))
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got.Tokens)
		})
	}
}

// A continue carries what magus recorded about its target: the title and model its spawn
// named, and the context size its host last reported. Addressed by name or by id alike.
func TestContinueTargetCarriesTheTargetsSpawnFacts(t *testing.T) {
	continueTo := func(to string) string {
		return `{"session_id":"8f2c6a1e","cwd":"/Users/dev/repo","hook_event_name":"PreToolUse",` +
			`"tool_name":"SendMessage","tool_input":{"to":"` + to + `","message":"Now fix the review comments."},"tool_use_id":"toolu_02"}`
	}
	tokens := transcriptLastTokens
	const title = "orchestrator/integrator guard-facts"
	// Positional: spawned, stopped, stopFirst, the address, the target wanted, whether the
	// idle clock is running.
	cases := []struct {
		name      string
		spawned   bool
		stopped   bool
		stopFirst bool
		to        string
		want      types.SpawnTarget
		wantIdle  bool
	}{
		{"by name, after the spawn and a stop", true, true, false, "brisk-heron",
			types.SpawnTarget{Agent: "brisk-heron", Description: title, Model: "sonnet", ContextTokens: &tokens}, true},
		{"by id, after the spawn and a stop", true, true, false, "a1b2c3",
			types.SpawnTarget{Agent: "a1b2c3", Description: title, Model: "sonnet", ContextTokens: &tokens}, true},
		{"before the host reported any usage", true, false, false, "brisk-heron",
			types.SpawnTarget{Agent: "brisk-heron", Description: title, Model: "sonnet"}, true},
		// A foreground spawn returns after its child stopped, so the stop is filed first.
		{"a stop filed before the spawn record", true, true, true, "a1b2c3",
			types.SpawnTarget{Agent: "a1b2c3", Description: title, Model: "sonnet", ContextTokens: &tokens}, true},
		{"an agent magus never saw", false, false, false, "ghost", types.SpawnTarget{Agent: "ghost"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := spawnFixture(t)
			probe := &spawnRuleProbe{}
			deps := Dependencies{SpawnRule: probe.rule()}
			transcript := writeTranscript(t, transcriptUser, transcriptFirstTurn, transcriptLastTurn, transcriptToolReply)
			stop := func() {
				assert.Equal(t, "pass", Judge(ctx, deps, Request{Input: subagentStop(t, "a1b2c3", transcript), Host: "claude-code"}).Decision)
			}
			if tc.stopped && tc.stopFirst {
				stop()
			}
			if tc.spawned {
				Judge(ctx, deps, Request{Input: finishedSpawn(t, title, "brisk-heron", "sonnet", "", "a1b2c3"), Host: "claude-code"})
			}
			if tc.stopped && !tc.stopFirst {
				stop()
			}
			require.Empty(t, probe.asked, "neither the stop nor the finished spawn is judged")

			Judge(ctx, deps, Request{Input: continueTo(tc.to), Host: "claude-code"})
			require.Len(t, probe.asked, 1)
			got := probe.asked[0].Target
			require.NotNil(t, got)
			if tc.wantIdle {
				require.NotNil(t, got.IdleMs, "the finished spawn started the clock")
				tc.want.IdleMs = got.IdleMs
			}
			assert.Equal(t, tc.want, *got)
		})
	}
}

// An agent record is published 0644, not with its temp file's 0600.
func TestAgentMarkerMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX modes")
	}
	facts := hint.NewGate(t.TempDir(), FactsKey("claude-code", "8f2c6a1e"))
	writeSpawnedAgent(facts, "a1b2c3", spawnedAgent{})

	fi, err := os.Stat(hint.MarkerPath(facts.CacheDir(), facts.Session(), spawnedAgentKind("a1b2c3")))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), fi.Mode().Perm())
}

// A marker that does not decode is absent, never a zero record that reads as present.
func TestCorruptMarkerReadsAsAbsent(t *testing.T) {
	facts := hint.NewGate(t.TempDir(), FactsKey("claude-code", "8f2c6a1e"))
	for _, kind := range []hint.MarkerKind{spawnedAgentKind("a1b2c3"), costStateKind, stallRecordKind} {
		path := hint.MarkerPath(facts.CacheDir(), facts.Session(), kind)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(`{"truncated`), 0o644))
	}

	_, ok := readMarker[spawnedAgent](facts, spawnedAgentKind("a1b2c3"))
	assert.False(t, ok)
	assert.Equal(t, costState{}, readCostState(facts))
	assert.Equal(t, stallRecord{}, readStall(facts))
}

// A stop for an agent magus never saw spawned still records its size, and a transcript
// with no usage records nothing rather than a zero.
func TestSubagentStopRecordsOnlyReportedUsage(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	facts := hint.NewGate(cacheDir, FactsKey("claude-code", "8f2c6a1e"))

	Judge(ctx, Dependencies{}, Request{Input: subagentStop(t, "a1b2c3", writeTranscript(t, transcriptUser)), Host: "claude-code"})
	_, ok := readSpawnedAgent(facts, "a1b2c3")
	assert.False(t, ok, "no usage, no record")

	Judge(ctx, Dependencies{}, Request{Input: subagentStop(t, "a1b2c3", writeTranscript(t, transcriptFirstTurn, transcriptLastTurn)), Host: "claude-code"})
	rec, ok := readSpawnedAgent(facts, "a1b2c3")
	require.True(t, ok)
	tokens := transcriptLastTokens
	assert.Equal(t, spawnedAgent{ContextTokens: &tokens}, rec)
}

// The rule reads the same job rows the guard graded the call against, through the
// snapshot magus\job.list answers from.
func TestSpawnRuleSeesTheGuardsJobRows(t *testing.T) {
	row := types.Job{ID: "guard-facts", Criteria: "the seam", WritePaths: []string{"internal/guard/**"}, State: types.StateRunning, Registered: 1}
	ctx, _ := fleetFixture(t, row)
	var seen []job.Snapshot
	rule := func(ctx context.Context, _ types.SpawnRequest, _ hint.Gate) (types.GuardVerdict, error) {
		snap, ok := job.SnapshotFromContext(ctx)
		require.True(t, ok, "the rule runs under the guard's rows")
		seen = append(seen, snap)
		return types.GuardVerdict{}, nil
	}
	Judge(ctx, Dependencies{SpawnRule: rule}, Request{Input: claudeContinueEnvelope, Host: "claude-code"})
	require.Len(t, seen, 1)
	require.NoError(t, seen[0].Err)
	require.Len(t, seen[0].Rows, 1)
	assert.Equal(t, row.ID, seen[0].Rows[0].ID)
	assert.Equal(t, row.WritePaths, seen[0].Rows[0].WritePaths)
}

// A spawn titled `<parent>/<role> <job>` naming a live job attributes the child to it:
// the child's later calls are graded under that lease, and the child's first call records
// its checkout's base for a job that never reported one.
func TestSpawnTitleAttributesTheChildToItsJob(t *testing.T) {
	// base stands for the checkout's own HEAD, which each case reads once it has one.
	const base = "<the checkout's HEAD>"
	type outcome struct {
		Lease        string
		Denied       bool
		ReportedBase string
		Registered   bool
	}
	const title = "orchestrator/integrator guard-facts"
	running := types.Job{ID: "guard-facts", State: types.StateRunning, WritePaths: []string{"internal/guard/**"}}
	reported := types.Job{ID: "guard-facts", State: types.StateDeclared, WritePaths: []string{"internal/guard/**"}, ReportedBase: "77aa01c", Registered: 1}
	finished := types.Job{ID: "guard-facts", State: types.StatePass, WritePaths: []string{"internal/guard/**"}}
	nested := types.Job{ID: "orchestrator/facts", Parent: "orchestrator", State: types.StateRunning, WritePaths: []string{"internal/guard/**"}}
	// Positional: the spawn title, its isolation, an explicit --lease, the stored row, and
	// what the child's write then meets.
	cases := []struct {
		name, title, isolation, lease string
		row                           types.Job
		want                          outcome
		// elsewhere is a call whose reported directory is outside the checkout magus
		// resolved, which says nothing about where the child runs.
		elsewhere bool
	}{
		{"a live job, shared checkout", title, "", "", running, outcome{Lease: "guard-facts", ReportedBase: base, Registered: true}, false},
		{"a call placed outside the resolved checkout registers nothing", title, "worktree", "", running, outcome{Lease: "guard-facts", Denied: true}, true},
		{"an isolated child's base is recorded all the same", title, "worktree", "", running, outcome{Lease: "guard-facts", ReportedBase: base, Registered: true}, false},
		{"a job forked beneath the title's parent", "orchestrator/integrator facts", "", "", nested, outcome{Lease: "orchestrator/facts", ReportedBase: base, Registered: true}, false},
		{"a base already reported is kept", title, "", "", reported, outcome{Lease: "guard-facts", ReportedBase: "77aa01c", Registered: true}, false},
		{"a finished job attributes nothing", title, "", "", finished, outcome{}, false},
		{"a title in another form attributes nothing", "orchestrator/brisk-heron/implement adr 0002", "", "", running, outcome{}, false},
		{"a title naming no role attributes nothing", "orchestrator guard-facts", "", "", running, outcome{}, false},
		{"an explicit lease outranks the attribution", title, "", "other", running, outcome{Lease: "other", Denied: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, root := fleetFixture(t, tc.row)
			at := hookLocation(ctx, Dependencies{})
			deps := Dependencies{}
			target := filepath.Join(root, "internal", "guard", "guard.go")
			require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
			require.NoError(t, os.WriteFile(target, []byte("package guard\n"), 0o644))
			head := commitCheckout(t, root)
			want := tc.want
			if want.ReportedBase == base {
				want.ReportedBase = head
			}

			Judge(ctx, deps, Request{Input: finishedSpawn(t, tc.title, "brisk-heron", "sonnet", tc.isolation, "a1b2c3"), Host: "claude-code"})
			edit := map[string]any{"file_path": target, "old_string": "package guard", "new_string": "package guard // edited"}
			cwd := root
			if tc.elsewhere {
				cwd = t.TempDir()
			}
			write := hookJSON(t, map[string]any{"session_id": "8f2c6a1e", "agent_id": "a1b2c3", "agent_type": "general-purpose", "cwd": cwd, "permission_mode": "default", "hook_event_name": "PreToolUse", "tool_name": "Edit", "tool_input": edit})
			v := Judge(ctx, deps, Request{Input: write, Host: "claude-code", Lease: tc.lease})

			rows, err := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: at.cacheDir, Root: at.workspace}).List()
			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.Equal(t, want, outcome{
				Lease:        v.Lease,
				Denied:       v.Decision == "deny",
				ReportedBase: rows[0].ReportedBase,
				Registered:   rows[0].Registered != 0,
			})
		})
	}
}

// The attribution is the agent's own: its parent, calling with no agent id, is not
// graded under the job it handed out.
func TestAttributionDoesNotReachTheParent(t *testing.T) {
	row := types.Job{ID: "guard-facts", State: types.StateRunning, WritePaths: []string{"internal/guard/**"}, Registered: 1, ReportedBase: "77aa01c"}
	ctx, _ := fleetFixture(t, row)
	Judge(ctx, Dependencies{}, Request{Input: finishedSpawn(t, "orchestrator/integrator guard-facts", "", "", "", "a1b2c3"), Host: "claude-code"})
	v := Judge(ctx, Dependencies{}, Request{Input: `{"session_id":"8f2c6a1e","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}`, Host: "claude-code"})
	assert.Empty(t, v.Lease)
}

// A worker spawned the documented way, a background Agent call titled for a live job, into
// its own worktree: its hooks run with a cache dir the spawner never wrote to, and its
// calls are still graded under the job, with its base recorded from its own checkout and
// the lease on every trail event.
func TestAWorkerInItsOwnWorktreeIsGradedUnderItsJob(t *testing.T) {
	row := types.Job{ID: "guard-facts", State: types.StateRunning, WritePaths: []string{"internal/guard/**"}}
	spawnerCtx, spawnerRoot := fleetFixture(t, row)
	spawnerHead := commitCheckout(t, spawnerRoot)
	// A linked worktree of the spawner's repository, which is what shares the job store.
	workerRoot, workerCache := linkedWorktree(t, spawnerRoot), t.TempDir()
	fixtureGit(t, workerRoot, "commit", "-q", "--allow-empty", "-m", "the worker's own")
	workerHead := fixtureGit(t, workerRoot, "rev-parse", "HEAD")
	require.NotEqual(t, spawnerHead, workerHead, "the worker's checkout is on a revision of its own")
	workerCtx := WithLocation(t.Context(), workerCache, workerRoot, workerRoot)
	deps := Dependencies{}

	Judge(spawnerCtx, deps, Request{Input: finishedSpawn(t, "orchestrator/fix guard-facts", "guard-facts", "opus", "worktree", "a1b2c3"), Host: "claude-code"})

	edit := func(rel string) types.GuardJudgment {
		target := filepath.Join(workerRoot, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
		require.NoError(t, os.WriteFile(target, []byte("package x\n"), 0o644))
		input := map[string]any{"file_path": target, "old_string": "package x", "new_string": "package y"}
		return Judge(workerCtx, deps, Request{Host: "claude-code", Input: hookJSON(t, map[string]any{
			"session_id": "8f2c6a1e", "agent_id": "a1b2c3", "agent_type": "general-purpose", "cwd": workerRoot,
			"hook_event_name": "PreToolUse", "tool_name": "Edit", "tool_input": input,
		})})
	}
	type graded struct {
		Decision string
		Lease    string
		From     types.LeaseSource
	}
	inside, outside := edit("internal/guard/guard.go"), edit("internal/job/store.go")
	assert.Equal(t, graded{"pass", "guard-facts", types.LeaseSourceAgent}, graded{inside.Decision, inside.Lease, inside.LeaseFrom})
	assert.Equal(t, graded{"deny", "guard-facts", types.LeaseSourceAgent}, graded{outside.Decision, outside.Lease, outside.LeaseFrom})

	rows, err := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: workerCache, Root: workerRoot}).List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	rev, _ := types.ParseCheckpointToken(rows[0].ReportedBase)
	assert.Equal(t, workerHead, rev, "the base is read from the worker's checkout, not the spawner's")

	events := trailEvents(t, workerCache, trail.KindAgentCommand)
	require.Len(t, events, 2)
	for _, e := range events {
		assert.Equal(t, graded{Lease: "guard-facts", From: types.LeaseSourceAgent}, graded{Lease: e.Lease, From: e.LeaseFrom})
	}
}

// A subagent's first call registers its job in the checkout that call runs in, which the
// sweep later stats: a registration in the spawner's checkout would outlive the worker's
// worktree and keep the job live forever.
func TestRegisterAgentBaseRecordsTheCallersCheckout(t *testing.T) {
	row := types.Job{ID: "guard-facts", State: types.StateRunning, WritePaths: []string{"internal/guard/**"}, Validated: 1}
	_, spawnerRoot := fleetFixture(t, row)
	head := commitCheckout(t, spawnerRoot)
	workerRoot := linkedWorktree(t, spawnerRoot)
	worker := location{cacheDir: t.TempDir(), workspace: workerRoot, dir: workerRoot}
	deps := Dependencies{}

	require.True(t, registerAgentBase(t.Context(), deps, worker, row.ID))
	assert.False(t, registerAgentBase(t.Context(), deps, worker, row.ID), "a job registers once")

	rows, err := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: worker.cacheDir, Root: workerRoot}).List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	abs, err := filepath.Abs(workerRoot)
	require.NoError(t, err)
	// The store stamps the schema, the times, who registered and the holder; they are copied
	// across so the rest of the row is compared whole.
	got := rows[0]
	assert.Equal(t, types.Job{
		Schema:       got.Schema,
		ID:           row.ID,
		WritePaths:   row.WritePaths,
		State:        types.StateRunning,
		Holder:       got.Holder,
		ReportedBase: head,
		BaseVerdict:  got.BaseVerdict,
		RegisteredBy: got.RegisteredBy,
		Registered:   got.Registered,
		CheckoutRoot: abs,
		Generation:   1,
		Attempts:     1,
		Validated:    row.Validated,
		Created:      got.Created,
		Updated:      got.Updated,
	}, got)
}

// An isolated worker registers on its first call, at the revision its worktree was created
// on, and only then switches to the branch it was handed. The registration follows it there
// rather than staying frozen on a diverged creation base; a worker whose own commit moved
// a matching checkout is not re-registered, since its commit would read as a divergence.
func TestRegisterAgentBaseFollowsADivergedCheckout(t *testing.T) {
	// Commits of one fixture repository, by role: the checkpoint the job was handed, the
	// revision the worktree was created on, and one more.
	const checkpoint, created, other = "checkpoint", "created", "other"
	for _, tc := range []struct {
		name string
		// reported is where the checkout stood when it first registered, and live where it
		// stands at the call; "" for live is a checkout whose revision cannot be read.
		reported, live string
		wantBase       string
		wantVerdict    types.JobBaseVerdict
	}{
		{"switched onto its checkpoint", created, checkpoint, checkpoint, types.BaseMatch},
		{"switched onto another base", created, other, other, types.BaseDiverged},
		{"still where it registered", created, created, created, types.BaseDiverged},
		{"a commit on its checkpoint", checkpoint, other, checkpoint, types.BaseMatch},
		{"no revision to read", created, "", created, types.BaseDiverged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := types.Job{ID: "lease-life", State: types.StateRunning, WritePaths: []string{"internal/guard/**"}}
			ctx, root := fleetFixture(t, row)
			at := hookLocation(ctx, Dependencies{})
			revs := map[string]string{}
			for _, role := range []string{checkpoint, created, other} {
				revs[role] = commitCheckout(t, root)
			}
			store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: at.cacheDir, Root: at.workspace})
			_, err := store.Update(ctx, row.ID, func(u *types.Job) { u.Checkpoint = revs[checkpoint] })
			require.NoError(t, err)
			fixtureGit(t, root, "checkout", "-q", revs[tc.reported])
			_, err = store.Exec(ctx, row.ID)
			require.NoError(t, err)
			if tc.live != "" {
				fixtureGit(t, root, "checkout", "-q", revs[tc.live])
			}
			deps := Dependencies{
				Revision: func(_ context.Context, dir, rev string) string {
					assert.Equal(t, []string{root, ""}, []string{dir, rev}, "the revision is read from the worker's checkout")
					return revs[tc.live]
				},
			}

			wrote := registerAgentBase(ctx, deps, at, row.ID)

			rows, err := store.List()
			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.Equal(t, []any{tc.wantBase != tc.reported, revs[tc.wantBase], tc.wantVerdict},
				[]any{wrote, rows[0].ReportedBase, rows[0].BaseVerdict})
		})
	}
}

// A registration the job store refuses (another checkout holds the job) is not tried again
// until the row changes: each try reads the checkout's base, a VCS process, for nothing.
func TestRegisterAgentBaseStopsAfterARefusal(t *testing.T) {
	row := types.Job{ID: "lease-life", State: types.StateRunning, WritePaths: []string{"internal/guard/**"}, CheckoutRoot: t.TempDir()}
	ctx, root := fleetFixture(t, row)
	commitCheckout(t, root)
	at := hookLocation(ctx, Dependencies{})

	require.False(t, refusalRemembered(t, ctx, at, row.ID))
	assert.False(t, registerAgentBase(ctx, Dependencies{}, at, row.ID))
	assert.True(t, refusalRemembered(t, ctx, at, row.ID), "the refused row is not tried again")
	assert.False(t, registerAgentBase(ctx, Dependencies{}, at, row.ID))

	changed := row
	changed.CheckoutRoot = ""
	assert.NotEqual(t, agentBaseRefusedKind(row, at.workspace), agentBaseRefusedKind(changed, at.workspace), "a changed row is tried again")
}

// A registration that fails for a reason that passes, here a cancelled wait for a store
// lock another process holds, is tried again on the next call: remembering it would refuse
// the worker's writes for as long as the row stands. That holds for a row another checkout
// holds too: only the store's holder refusal is remembered, never the row's look.
func TestRegisterAgentBaseRetriesAPassingFailure(t *testing.T) {
	t.Run("a declared row", func(t *testing.T) {
		row := types.Job{ID: "lease-life", State: types.StateDeclared, WritePaths: []string{"internal/guard/**"}}
		ctx, root := fleetFixture(t, row)
		commitCheckout(t, root)
		at := hookLocation(ctx, Dependencies{})

		withLapsedLock(t, ctx, at, func(lapsed context.Context) {
			assert.False(t, registerAgentBase(lapsed, Dependencies{}, at, row.ID))
		})
		assert.True(t, registerAgentBase(ctx, Dependencies{}, at, row.ID), "the lapsed try is not remembered as a refusal")
	})
	t.Run("a row another checkout holds", func(t *testing.T) {
		row := types.Job{ID: "lease-life", State: types.StateRunning, WritePaths: []string{"internal/guard/**"}, CheckoutRoot: t.TempDir()}
		ctx, root := fleetFixture(t, row)
		commitCheckout(t, root)
		at := hookLocation(ctx, Dependencies{})

		withLapsedLock(t, ctx, at, func(lapsed context.Context) {
			assert.False(t, registerAgentBase(lapsed, Dependencies{}, at, row.ID))
		})
		assert.False(t, refusalRemembered(t, ctx, at, row.ID), "the lapsed try is tried again")
		assert.False(t, registerAgentBase(ctx, Dependencies{}, at, row.ID), "the holder refuses")
		assert.True(t, refusalRemembered(t, ctx, at, row.ID), "the holder's refusal is not tried again")
	})
}

// refusalRemembered reports whether registerAgentBase remembered the job store refusing
// id's row as it now stands, which is what keeps it from trying that row again.
func refusalRemembered(t *testing.T, ctx context.Context, at location, id string) bool {
	t.Helper()
	rows, err := leaseRows(ctx, at)
	require.NoError(t, err)
	for _, row := range rows {
		if row.ID == id {
			return hint.NewGate(at.cacheDir, agentBaseRefusals).AlreadyFired(agentBaseRefusedKind(row, at.workspace))
		}
	}
	t.Fatalf("no row %s", id)
	return false
}

// withLapsedLock runs fn with a cancelled context while another process holds the job
// store's lock.
func withLapsedLock(t *testing.T, ctx context.Context, at location, fn func(lapsed context.Context)) {
	t.Helper()
	path, err := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: at.cacheDir, Root: at.workspace}).Path()
	require.NoError(t, err)
	lapsed, cancel := context.WithCancel(ctx)
	cancel()
	require.NoError(t, file.WithLock(ctx, path+".lock", 0, func() error {
		fn(lapsed)
		return nil
	}))
}

// A checkout's current revision is read once per judgment, however many rules compare
// against it; a named revision is read each time.
func TestRevisionIsReadOncePerJudgment(t *testing.T) {
	reads := 0
	deps := Dependencies{
		Revision:  func(context.Context, string, string) string { reads++; return "9c0ffee" },
		revisions: &revisionMemo{current: map[string]string{}},
	}
	for range 3 {
		assert.Equal(t, "9c0ffee", deps.revision(t.Context(), "/w", ""))
	}
	deps.revision(t.Context(), "/w", "HEAD~1")
	assert.Equal(t, 2, reads)
}

// The command and path glue forward one field of the event, not the envelope, so the
// subagent id arrives as --agent beside the extracted command. It must attribute exactly as
// the envelope's agent_id does, or every subagent shell call is graded as its parent's.
func TestAgentFlagAttributesAnExtractedCommandToTheSubagentsJob(t *testing.T) {
	row := types.Job{ID: "guard-facts", State: types.StateRunning, WritePaths: []string{"internal/guard/**"}, Registered: 1, ReportedBase: "77aa01c"}
	ctx, _ := fleetFixture(t, row)
	Judge(ctx, Dependencies{}, Request{Input: finishedSpawn(t, "orchestrator/integrator guard-facts", "", "", "", "a1b2c3"), Host: "claude-code"})

	child := Judge(ctx, Dependencies{}, Request{Input: "ls", Host: "claude-code", Session: "8f2c6a1e", Agent: "a1b2c3"})
	assert.Equal(t, "guard-facts", child.Lease, "a subagent's extracted command is graded under its job")

	parent := Judge(ctx, Dependencies{}, Request{Input: "ls", Host: "claude-code", Session: "8f2c6a1e"})
	assert.Empty(t, parent.Lease, "the same command with no agent id is the parent's")
}

// A spawn title is the spawner's claim, and any process can pipe a spawn envelope into
// `magus shell`. A leased worker that names another live job must not be graded under it
// through a child id it made up; it may hand out only its own lease or a job forked beneath
// it. An unleased spawner, the orchestrator or a person, may name any live job.
func TestASpawnAttributesOnlyAJobItsSpawnerCanHandOut(t *testing.T) {
	running := func(id, parent string) types.Job {
		return types.Job{ID: id, Parent: parent, State: types.StateRunning, WritePaths: []string{"internal/" + id + "/**"}, Registered: 1, ReportedBase: "77aa01c"}
	}
	ctx, root := fleetFixture(t, running("worker-job", ""), running("victim-job", ""), running("sub-job", "worker-job"), running("grandchild-job", "sub-job"))
	cacheDir := hookLocation(ctx, Dependencies{}).cacheDir
	bindCaller(t, ctx, hookAttribution{Host: "claude-code", Session: "worker-session"}, "worker-job")
	facts := hint.NewGate(cacheDir, hookAttribution{Host: "claude-code", Session: "worker-session"}.factsKey())

	spawn := func(title, child string) {
		t.Helper()
		env := strings.Replace(finishedSpawn(t, title, "", "", "", child), `"session_id":"8f2c6a1e"`, `"session_id":"worker-session"`, 1)
		Judge(ctx, Dependencies{}, Request{Input: env, Host: "claude-code"})
	}
	graded := func(child string) types.GuardJudgment {
		return Judge(ctx, Dependencies{}, Request{Input: "ls", Host: "claude-code", Session: "worker-session", Agent: child})
	}

	spawn("worker/forger victim-job", "forged")
	rec, ok := readSpawnedAgent(facts, "forged")
	require.True(t, ok)
	assert.Empty(t, job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root}).Bound(job.Caller{Host: "claude-code", Session: "worker-session", Agent: "forged"}),
		"a leased spawner cannot hand out a job outside its own tree")
	assert.Equal(t, "victim-job", rec.UntrustedJob, "the claim is kept for a reader")
	assert.Empty(t, graded("forged").Lease, "no record answers for the forged child, and it never reads its spawner's")

	spawn("worker/integrator grandchild-job", "helper")
	assert.Equal(t, "grandchild-job", graded("helper").Lease, "a job forked beneath the spawner's lease is its to hand out")

	spawn("worker/integrator worker-job", "twin")
	assert.Equal(t, "worker-job", graded("twin").Lease, "its own lease is its to hand out")
}

// BAGGAGE reaches a hook from the host's environment, so it is the one lease answer a
// worker, or the orchestrator that spawned it, can rewrite from a shell. A spawn record
// and a checkout's binding are records, and each outranks it; the claim answers only
// when neither does. The trail records which source answered.
func TestARecordOutranksTheBaggageClaim(t *testing.T) {
	running := func(id string) types.Job {
		return types.Job{ID: id, State: types.StateRunning, WritePaths: []string{"internal/" + id + "/**"}, Registered: 1, ReportedBase: "77aa01c"}
	}
	ctx, _ := fleetFixture(t, running("agent-job"), running("bound-job"), running("claimed-job"))
	cacheDir := hookLocation(ctx, Dependencies{}).cacheDir
	bindCaller(t, ctx, hookAttribution{Host: "claude-code", Session: "bound-session"}, "bound-job")
	Judge(ctx, Dependencies{}, Request{Input: finishedSpawn(t, "orchestrator/integrator agent-job", "", "", "", "a1b2c3"), Host: "claude-code"})
	t.Setenv(trail.EnvBaggage, trail.BaggageLease+"=claimed-job")

	type answer struct {
		Lease string
		From  types.LeaseSource
	}
	for name, tc := range map[string]struct {
		req  Request
		want answer
	}{
		"the claim, when no record answers": {
			Request{Input: "ls", Host: "claude-code", Session: "free-session"},
			answer{"claimed-job", types.LeaseSourceEnv},
		},
		"the session's binding over the claim": {
			Request{Input: "ls", Host: "claude-code", Session: "bound-session"},
			answer{"bound-job", types.LeaseSourceContested},
		},
		"the subagent's job over the claim": {
			Request{Input: "ls", Host: "claude-code", Session: "8f2c6a1e", Agent: "a1b2c3"},
			answer{"agent-job", types.LeaseSourceContested},
		},
		"an explicit flag over every record": {
			Request{Input: "ls", Host: "claude-code", Session: "bound-session", Lease: "agent-job"},
			answer{"agent-job", types.LeaseSourceContested},
		},
	} {
		t.Run(name, func(t *testing.T) {
			v := Judge(ctx, Dependencies{}, tc.req)
			assert.Equal(t, tc.want, answer{v.Lease, v.LeaseFrom})

			events := trailEvents(t, cacheDir, trail.KindAgentCommand)
			require.NotEmpty(t, events)
			last := events[len(events)-1]
			assert.Equal(t, tc.want, answer{last.Lease, last.LeaseFrom}, "the trail records the same answer")
		})
	}
}

// bashCall is Claude Code's PreToolUse for a Bash call, from the subagent agent inside
// session, or from the root session when agent is "".
func bashCall(t *testing.T, session, agent, command string) string {
	t.Helper()
	env := map[string]any{"session_id": session, "cwd": "/Users/dev/repo", "hook_event_name": "PreToolUse",
		"tool_name": "Bash", "tool_input": map[string]any{"command": command}}
	if agent != "" {
		env["agent_id"], env["agent_type"] = agent, "general-purpose"
	}
	return hookJSON(t, env)
}

// graded is what a verdict says about who acted: whether it refused, and under which lease.
type graded struct {
	Denied bool
	Lease  string
	From   types.LeaseSource
}

func gradedAs(v types.GuardJudgment) graded {
	return graded{v.Decision == "deny", v.Lease, v.LeaseFrom}
}

// TestASubagentsExecNeverBindsItsParent is the 2026-09-26 incident. An orchestrator spawned
// a researcher in the foreground, so no spawn response attributed it while it ran, and the
// researcher ran `magus job exec` on a read-only job in the orchestrator's checkout. The
// CLI wrote a checkout-wide marker, the orchestrator's session fell back to it, and its
// next `job fork` and `git merge` were refused as the worker's.
func TestASubagentsExecNeverBindsItsParent(t *testing.T) {
	research := types.Job{ID: "linux-platform-design", Parent: "harness-audit", Criteria: "research only", ReadOnly: true, State: types.StateDeclared, Checkpoint: "23558048b"}
	ctx, root := fleetFixture(t,
		types.Job{ID: "harness-audit", Criteria: "the audit", State: types.StateRunning, WritePaths: []string{"internal/guard/**"}},
		research)
	cacheDir := hookLocation(ctx, Dependencies{}).cacheDir
	const session = "8f2c6a1e"

	// The foreground spawn's PreToolUse; its PostToolUse lands only after the child stops.
	foreground := strings.Replace(claudeSpawnEnvelope, `"run_in_background":true`, `"run_in_background":false`, 1)
	require.NotEqual(t, "deny", Judge(ctx, Dependencies{}, Request{Input: foreground, Host: "claude-code"}).Decision)

	took := Judge(ctx, Dependencies{}, Request{Input: bashCall(t, session, "a1b2c3", "./magus job exec "+research.ID), Host: "claude-code"})
	require.Equal(t, graded{}, gradedAs(took), "the child held nothing when it took the job")
	commitCheckout(t, root)
	_, err := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root}).Exec(ctx, research.ID)
	require.NoError(t, err, "the CLI half of the exec")

	child := Judge(ctx, Dependencies{}, Request{Input: bashCall(t, session, "a1b2c3", "ls"), Host: "claude-code"})
	assert.Equal(t, graded{Lease: research.ID, From: types.LeaseSourceAgent}, gradedAs(child), "the child is graded under the job it took")

	got := map[string]graded{}
	for _, command := range []string{"./magus job fork harness-audit/next --criteria 'the next step'", "git merge linux-platform-design"} {
		got[command] = gradedAs(Judge(ctx, Dependencies{}, Request{Input: bashCall(t, session, "", command), Host: "claude-code"}))
	}
	assert.Equal(t, map[string]graded{
		"./magus job fork harness-audit/next --criteria 'the next step'": {},
		"git merge linux-platform-design":                                {},
	}, got, "the parent sharing the child's session and checkout acts under nothing")

	lease, from := job.ActingLease(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir}, "")
	assert.Equal(t, graded{}, graded{Lease: lease, From: from}, "a process that is not a hook, the sandbox's run included, reads no binding either")
}

// Exec binds exactly the identity the host named on the call, through either door, and
// nobody sharing its session or checkout.
func TestExecBindsTheCallersIdentity(t *testing.T) {
	running := func(id string) types.Job {
		return types.Job{ID: id, State: types.StateRunning, WritePaths: []string{"internal/" + id + "/**"}, Registered: 1, ReportedBase: "77aa01c"}
	}
	ctx, _ := fleetFixture(t, running("session-job"), running("agent-job"), running("tool-job"))

	Judge(ctx, Dependencies{}, Request{Input: "magus job exec session-job", Host: "codex", Session: "s1"})
	Judge(ctx, Dependencies{}, Request{Input: "magus --root . job exec agent-job", Host: "claude-code", Session: "s2", Agent: "a1"})
	tool := hookJSON(t, map[string]any{"session_id": "s3", "hook_event_name": "PreToolUse", "tool_name": "mcp__magus__client",
		"tool_input": map[string]any{"script": `import "magus"; magus\job.register("tool-job");`}})
	Judge(ctx, Dependencies{}, Request{Input: tool, Host: "claude-code"})

	got := map[string]string{}
	for name, req := range map[string]Request{
		"the session-only caller":   {Input: "ls", Host: "codex", Session: "s1"},
		"the subagent":              {Input: "ls", Host: "claude-code", Session: "s2", Agent: "a1"},
		"the job tool's caller":     {Input: "ls", Host: "claude-code", Session: "s3"},
		"the subagent's parent":     {Input: "ls", Host: "claude-code", Session: "s2"},
		"a sibling subagent":        {Input: "ls", Host: "claude-code", Session: "s2", Agent: "a2"},
		"the session on other host": {Input: "ls", Host: "claude-code", Session: "s1"},
		"an identity-less caller":   {Input: "ls"},
	} {
		got[name] = Judge(ctx, Dependencies{}, req).Lease
	}
	assert.Equal(t, map[string]string{
		"the session-only caller":   "session-job",
		"the subagent":              "agent-job",
		"the job tool's caller":     "tool-job",
		"the subagent's parent":     "",
		"a sibling subagent":        "",
		"the session on other host": "",
		"an identity-less caller":   "",
	}, got)
}

// A job that has exited or ended holds its caller to nothing, so the next exec takes the
// next job with nothing to give up first. Exited is live, yet nobody is required to wait
// on it, so a caller held to it could be stuck for good. A job a verdict ended has
// released its caller by the time the exec is graded.
func TestExecAfterAnEndedJobRebinds(t *testing.T) {
	for _, state := range []types.JobState{types.StateExited, types.StatePass, types.StateFail, types.StateNoReturn} {
		t.Run(string(state), func(t *testing.T) {
			held := types.Job{ID: "held-job", State: state, WritePaths: []string{"internal/held/**"}, Registered: 1}
			next := types.Job{ID: "next-job", State: types.StateDeclared, WritePaths: []string{"internal/next/**"}}
			ctx, _ := fleetFixture(t, held, next)
			who := hookAttribution{Host: "claude-code", Session: "s1", Agent: "a1"}
			bindCaller(t, ctx, who, held.ID)

			v := Judge(ctx, Dependencies{}, Request{Input: "magus job exec next-job", Host: who.Host, Session: who.Session, Agent: who.Agent})
			want := graded{}
			if state.Live() {
				want = graded{Lease: held.ID, From: types.LeaseSourceAgent}
			}
			assert.Equal(t, want, gradedAs(v))
			assert.Equal(t, job.Binding{Job: next.ID}, boundJob(who, hookLocation(ctx, Dependencies{})))
		})
	}
}

// A job still declared or running holds its caller: taking another is how a worker would
// be graded against a boundary nobody handed it. Taking its own again passes.
func TestExecUnderARunningJobIsRefused(t *testing.T) {
	for _, state := range []types.JobState{types.StateDeclared, types.StateRunning} {
		t.Run(string(state), func(t *testing.T) {
			held := types.Job{ID: "held-job", State: state, WritePaths: []string{"internal/held/**"}, Registered: 1}
			other := types.Job{ID: "other-job", State: types.StateDeclared, WritePaths: []string{"internal/other/**"}}
			ctx, _ := fleetFixture(t, held, other)
			who := hookAttribution{Host: "claude-code", Session: "s1", Agent: "a1"}
			bindCaller(t, ctx, who, held.ID)
			judge := func(input string) graded {
				return gradedAs(Judge(ctx, Dependencies{}, Request{Input: input, Host: who.Host, Session: who.Session, Agent: who.Agent}))
			}

			assert.Equal(t, graded{Denied: true, Lease: held.ID, From: types.LeaseSourceAgent}, judge("magus job exec other-job"))
			assert.Equal(t, graded{Denied: true, Lease: held.ID, From: types.LeaseSourceAgent}, judge("client op=register id=other-job"))
			assert.Equal(t, graded{Lease: held.ID, From: types.LeaseSourceAgent}, judge("magus job exec held-job"))
			assert.Equal(t, job.Binding{Job: held.ID}, boundJob(who, hookLocation(ctx, Dependencies{})), "a refused exec rebinds nothing")
		})
	}
}

// A worker its spawn already attributed is bound; its exec records the base and binds
// nothing new, here or in the checkout.
func TestAttributedWorkersExecIsIdempotent(t *testing.T) {
	row := types.Job{ID: "guard-facts", State: types.StateRunning, WritePaths: []string{"internal/guard/**"}, Registered: 1, ReportedBase: "77aa01c"}
	ctx, _ := fleetFixture(t, row)
	Judge(ctx, Dependencies{}, Request{Input: finishedSpawn(t, "orchestrator/integrator guard-facts", "", "", "", "a1b2c3"), Host: "claude-code"})
	who := hookAttribution{Host: "claude-code", Session: "8f2c6a1e", Agent: "a1b2c3"}
	at := hookLocation(ctx, Dependencies{})
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: at.cacheDir, Root: at.workspace})
	jobs, err := store.Path()
	require.NoError(t, err)
	records := func() []string {
		entries, err := os.ReadDir(filepath.Join(filepath.Dir(jobs), "agents"))
		require.NoError(t, err)
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		return names
	}
	before := records()
	require.Len(t, before, 1, "the spawn recorded the child")

	v := Judge(ctx, Dependencies{}, Request{Input: bashCall(t, who.Session, who.Agent, "magus job exec guard-facts"), Host: "claude-code"})
	assert.Equal(t, graded{Lease: row.ID, From: types.LeaseSourceAgent}, gradedAs(v))

	assert.Equal(t, before, records(), "no second record")
	assert.Equal(t, row.ID, store.Bound(who.caller()))
	_, err = os.Stat(at.jobs().MarkerPath())
	assert.True(t, os.IsNotExist(err), "no checkout record")
	rows, err := store.List()
	require.NoError(t, err)
	assert.Equal(t, "77aa01c", rows[0].ReportedBase, "the base already reported is kept")
}

// denyEverySpawn is a magusfile whose spawn rule denies every spawn.
const denyEverySpawn = `import "magus";

magus\guard.spawn(fun (req: SpawnRequest) > GuardVerdict {
    return magus\guard.deny("Name a model.");
});
`

// committedSpawnRule is a git repository whose committed magusfile is denyEverySpawn and
// whose working tree then holds worktree in its place.
func committedSpawnRule(t *testing.T, worktree string) string {
	t.Helper()
	r := testkit.NewRepo(t, map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": denyEverySpawn})
	r.Magusfile(worktree)
	return r.Root()
}

func loadRules(t *testing.T, root string) *WorkspaceRules {
	t.Helper()
	rules, err := LoadWorkspaceRules(t.Context(), root, types.VCSOptions{})
	require.NoError(t, err, "the working tree still loads")
	return rules
}

// A fresh checkout has no lineage recording that a spawn rule ever existed, so deleting the
// rule from its working tree must not be what decides whether the approved rule is asked.
func TestApprovedRuleAppliesAfterTheWorkingTreeDeletesIt(t *testing.T) {
	m := loadRules(t, committedSpawnRule(t, "import \"magus\";\n"))
	require.Nil(t, m.SpawnRule(), "the working tree registers no rule")

	ctx, _ := spawnFixture(t)
	v := Judge(ctx, Dependencies{SpawnRule: m.SpawnRule(), ApprovedSpawnRule: m.ApprovedSpawnRule},
		Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
	assert.Equal(t, types.GuardJudgment{SchemaVersion: v.SchemaVersion, Decision: "deny", Reason: "Name a model.", Rule: workspaceSpawnRule, Lease: v.Lease, LeaseFrom: v.LeaseFrom}, v)
}

// Resolving the approved rule is the one part of a spawn an agent can slow down, so running
// out of time denies rather than letting the working tree's looser rule stand alone.
func TestSlowApprovedRuleDenies(t *testing.T) {
	m := loadRules(t, committedSpawnRule(t, `import "magus";

magus\guard.spawn(fun (req: SpawnRequest) > GuardVerdict {
    return magus\guard.allow();
});
`))
	require.NotNil(t, m.SpawnRule())

	slow := func(ctx context.Context) (workspace.SpawnRule, error) {
		<-ctx.Done()
		return m.ApprovedSpawnRule(ctx)
	}

	ctx, cacheDir := spawnFixture(t)
	ctx, cancel := context.WithTimeout(ctx, judgeReserve+600*time.Millisecond)
	defer cancel()
	v := Judge(ctx, Dependencies{SpawnRule: m.SpawnRule(), ApprovedSpawnRule: slow},
		Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
	assert.Equal(t, verdictWithRule("deny", workspaceSpawnRule), unworded(v))
	assert.Regexp(t, `magus ran out of time judging a subagent spawn: resolving the approved magus\\guard\.spawn rule did not finish within \d+ms`, v.Reason)

	spawns := trailEvents(t, cacheDir, trail.KindAgentSpawn)
	require.Len(t, spawns, 1)
	assert.Equal(t, decidedByApproved, spawns[0].DecidedBy)
	failures := readSpawnBlob(t, cacheDir, spawns[0]).RuleFailures
	require.Len(t, failures, 1)
	assert.Equal(t, decidedByApproved, failures[0].Side)
	assert.Regexp(t, `^resolving the rule did not finish within \d+ms$`, failures[0].Error)
	require.NotEmpty(t, spawns[0].Phases)
	assert.Equal(t, phaseApprovedResolve, spawns[0].Phases[0].Name)
	assert.True(t, spawns[0].Phases[0].Lapsed)
}

// Codex reports a finished spawn's response as a string holding JSON, and its v2 spawn
// names the child by path rather than by id.
func TestSpawnedAgentIDShapes(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		response any
		want     string
	}{
		"a map, camel case":     {map[string]any{"agentId": "a1b2c3"}, "a1b2c3"},
		"a map, snake case":     {map[string]any{"agent_id": "a1b2c3"}, "a1b2c3"},
		"a JSON string":         {`{"agent_id":"019e4c22-1b3d-7e90-8c44-5a6f7d8e9012","nickname":"Hubble"}`, "019e4c22-1b3d-7e90-8c44-5a6f7d8e9012"},
		"a JSON string, v2":     {`{"task_name":"/root/audit_guard"}`, "/root/audit_guard"},
		"a map, v2":             {map[string]any{"task_name": "/root/audit_guard", "nickname": "Hubble"}, "/root/audit_guard"},
		"an id beats a path":    {`{"agent_id":"a1b2c3","task_name":"/root/x"}`, "a1b2c3"},
		"a string, not JSON":    {"Agent spawned", ""},
		"a JSON string, a list": {`["a1b2c3"]`, ""},
		"an empty string":       {"", ""},
		"no response":           {nil, ""},
		"a number":              {42.0, ""},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, spawnedAgentID(tc.response))
		})
	}
}

// The PostToolUse of a Codex spawn_agent carries the response as a string, and the
// decoder must hand its child id on.
func TestDecodeHookEnvelopeSpawnCodexResponse(t *testing.T) {
	t.Parallel()

	req, ok := decodeTestEnvelope(t, `{"session_id":"019e4c21","cwd":"/Users/dev/repo","hook_event_name":"PostToolUse","tool_name":"spawn_agent",`+
		`"tool_input":{"message":"Audit internal/guard.","agent_type":"explorer"},"tool_use_id":"call_Qx8aTz1uJm",`+
		`"tool_response":"{\"agent_id\":\"019e4c22-1b3d-7e90-8c44-5a6f7d8e9012\",\"nickname\":\"Hubble\"}"}`)
	require.True(t, ok)
	assert.True(t, req.IsSpawn)
	assert.True(t, req.AfterCall)
	assert.Equal(t, "019e4c22-1b3d-7e90-8c44-5a6f7d8e9012", req.SpawnedAgent)
}

// Codex records usage as event_msg/token_count lines in its rollout. Its input_tokens
// already counts the cached tokens, so the size is that figure and no sum.
func TestTranscriptUsageCodexTokenCount(t *testing.T) {
	const (
		started = `{"timestamp":"2026-10-13T09:14:02.100Z","type":"event_msg","payload":{"type":"task_started","turn_id":"019e4c21"}}`
		first   = `{"timestamp":"2026-10-13T09:14:09.500Z","type":"event_msg","payload":{"type":"token_count","info":{` +
			`"total_token_usage":{"input_tokens":30673,"cached_input_tokens":11008,"output_tokens":314,"reasoning_output_tokens":56,"total_tokens":30987},` +
			`"last_token_usage":{"input_tokens":30673,"cached_input_tokens":11008,"output_tokens":314,"reasoning_output_tokens":56,"total_tokens":30987},` +
			`"model_context_window":258400},"rate_limits":{"primary":{"used_percent":4.0}}}}`
		last = `{"timestamp":"2026-10-13T09:14:21.900Z","type":"event_msg","payload":{"type":"token_count","info":{` +
			`"total_token_usage":{"input_tokens":93120,"cached_input_tokens":61000,"output_tokens":901,"reasoning_output_tokens":120,"total_tokens":94021},` +
			`"last_token_usage":{"input_tokens":62447,"cached_input_tokens":50000,"output_tokens":587,"reasoning_output_tokens":64,"total_tokens":63034},` +
			`"model_context_window":258400},"rate_limits":{"primary":{"used_percent":5.0}}}}`
		rateLimitsOnly = `{"timestamp":"2026-10-13T09:14:22.000Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"primary":{"used_percent":5.0}}}}`
		otherEvent     = `{"timestamp":"2026-10-13T09:14:22.100Z","type":"event_msg","payload":{"type":"agent_message","message":"done"}}`
	)

	t.Run("the last count wins, with its window", func(t *testing.T) {
		got, ok := lastContextUsage(writeTranscript(t, started, first, last, otherEvent))
		require.True(t, ok)
		assert.Equal(t, types.ContextWindow{Tokens: 62447, Window: 258400, Source: types.ContextFromTranscript}, got)
	})
	t.Run("a count with no info is skipped", func(t *testing.T) {
		got, ok := lastContextUsage(writeTranscript(t, first, rateLimitsOnly))
		require.True(t, ok)
		assert.Equal(t, int64(30673), got.Tokens)
	})
	t.Run("no count at all", func(t *testing.T) {
		_, ok := lastContextUsage(writeTranscript(t, started, rateLimitsOnly, otherEvent))
		assert.False(t, ok)
	})
	t.Run("a Claude Code record reports no window", func(t *testing.T) {
		got, ok := lastContextUsage(writeTranscript(t, transcriptLastTurn))
		require.True(t, ok)
		assert.Equal(t, transcriptLastTokens, got.Tokens)
		assert.Zero(t, got.Window)
	})
	t.Run("a stop files the size and the window", func(t *testing.T) {
		ctx, cacheDir := spawnFixture(t)
		facts := hint.NewGate(cacheDir, FactsKey("claude-code", "8f2c6a1e"))
		Judge(ctx, Dependencies{}, Request{Input: subagentStop(t, "a1b2c3", writeTranscript(t, first, last)), Host: "claude-code"})
		rec, ok := readSpawnedAgent(facts, "a1b2c3")
		require.True(t, ok)
		tokens := int64(62447)
		assert.Equal(t, spawnedAgent{ContextTokens: &tokens, ContextWindow: 258400}, rec)
	})
}

// A session bound to a job it exited is graded under the job its BAGGAGE claims, when that
// one is still declared or running: a resumed worker handed a new job was judged as the
// one it had finished, so every write to its new paths was refused. The binding is
// released, and the claim answers from then on. Without a claim, or with one naming no job
// in flight, the exited job still holds, since a rejected wait sends its holder back to it.
func TestAnExitedBindingYieldsToABaggageClaim(t *testing.T) {
	who := hookAttribution{Host: "claude-code", Session: "s1", Agent: "a1"}
	row := func(id string, state types.JobState) types.Job {
		return types.Job{ID: id, State: state, WritePaths: []string{"internal/" + id + "/**"}, Registered: 1, ReportedBase: "77aa01c"}
	}
	for _, tc := range []struct {
		name  string
		held  types.JobState
		claim string
		want  graded
		bound job.Binding
	}{
		{"an exited job and a claim on a running one", types.StateExited, "next-job",
			graded{Lease: "next-job", From: types.LeaseSourceEnv}, job.Binding{}},
		{"an exited job and a claim on a declared one", types.StateExited, "fresh-job",
			graded{Lease: "fresh-job", From: types.LeaseSourceEnv}, job.Binding{}},
		{"an exited job and no claim", types.StateExited, "",
			graded{Lease: "held-job", From: types.LeaseSourceAgent}, job.Binding{Job: "held-job"}},
		{"an exited job and a claim on itself", types.StateExited, "held-job",
			graded{Lease: "held-job", From: types.LeaseSourceAgent}, job.Binding{Job: "held-job"}},
		{"an exited job and a claim on another exited one", types.StateExited, "done-job",
			graded{Lease: "held-job", From: types.LeaseSourceContested}, job.Binding{Job: "held-job"}},
		{"an exited job and a claim on no job", types.StateExited, "absent-job",
			graded{Lease: "held-job", From: types.LeaseSourceContested}, job.Binding{Job: "held-job"}},
		{"a running job outranks the claim", types.StateRunning, "next-job",
			graded{Lease: "held-job", From: types.LeaseSourceContested}, job.Binding{Job: "held-job"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := fleetFixture(t, row("held-job", tc.held), row("next-job", types.StateRunning),
				row("fresh-job", types.StateDeclared), row("done-job", types.StateExited))
			bindCaller(t, ctx, who, "held-job")
			if tc.claim != "" {
				t.Setenv(trail.EnvBaggage, trail.BaggageLease+"="+tc.claim)
			} else {
				t.Setenv(trail.EnvBaggage, "")
			}

			v := Judge(ctx, Dependencies{}, Request{Input: bashCall(t, who.Session, who.Agent, "ls"), Host: who.Host})
			assert.Equal(t, tc.want, gradedAs(v), v.Reason)
			assert.Equal(t, tc.bound, boundJob(who, hookLocation(ctx, Dependencies{})), "the caller's record")
		})
	}
}

// No guard test reads this machine's broker: a full budget there would advise on every
// root spawn the tests expect to pass.
func init() {
	budgetSnapshot = func(context.Context) (types.MachineSnapshot, error) {
		return types.MachineSnapshot{}, errors.New("no broker in a guard test")
	}
}

// standBudget makes s the budget every spawn reads until the test ends. Callers never run
// in parallel, since the reader is the package's.
func standBudget(t *testing.T, s types.MachineSnapshot) {
	t.Helper()
	was := budgetSnapshot
	budgetSnapshot = func(context.Context) (types.MachineSnapshot, error) { return s, nil }
	t.Cleanup(func() { budgetSnapshot = was })
}

// The orchestrator waits for the build budget so its workers never do: a spawn into a
// full budget is told how full it is and what holds it, and a worker's own spawn is not.
func TestSpawnIntoAFullBuildBudgetIsAdvised(t *testing.T) {
	ctx, _ := spawnFixture(t)
	spawn := func(session, agent string) types.GuardJudgment {
		env := map[string]any{
			"session_id": session, "hook_event_name": "PreToolUse", "tool_name": "Agent",
			"tool_input": map[string]any{"description": "orchestrator/impl pricing", "prompt": "Carry it out."},
		}
		if agent != "" {
			env["agent_id"] = agent
		}
		return Judge(ctx, Dependencies{}, Request{Input: hookJSON(t, env), Host: "claude-code"})
	}
	since := time.Now().Add(-6 * time.Minute)
	full := types.MachineSnapshot{BudgetSlots: 2, HeldSlots: 2, Holders: []types.MachineClaimant{
		{Project: ".", Target: "go-test", Dir: "/w/a", Since: since},
		{Project: "docs", Target: "lint", Dir: "/w/b", Since: since.Add(time.Minute)},
	}}

	standBudget(t, types.MachineSnapshot{BudgetSlots: 2, HeldSlots: 1})
	assert.Equal(t, "pass", spawn("budget-room", "").Decision, "a free slot says nothing")

	standBudget(t, full)
	v := spawn("budget-full", "")
	require.Equal(t, types.GuardJudgment{Decision: "advise", Rule: string(denyRuleSleepPoll)}, judgedAs(v))
	assert.Contains(t, v.Context, "slots 2/2")
	assert.Contains(t, v.Context, "go-test . in /w/a, has held its seat 6m")
	assert.Equal(t, "advise", spawn("budget-full", "").Decision, "each spawn into a full budget hears it")
	assert.Equal(t, "pass", spawn("budget-worker", "a1b2c3").Decision, "a worker reports a full budget rather than waiting on it")
}
