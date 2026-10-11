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

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/hint"
	// The interpreter a real magusfile load needs, which cmd/magus links in production.
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
	root := t.TempDir()
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
				Role: types.AgentRoleRoot,
			},
		},
		{
			name: "claude-code continue", host: "claude-code", event: claudeContinueEnvelope,
			want: types.SpawnRequest{
				Kind: types.SpawnKindContinue, Host: "claude-code", Session: "8f2c6a1e",
				Prompt: "Now fix the review comments.", Role: types.AgentRoleRoot,
				Target: &types.SpawnTarget{Agent: "brisk-heron"},
			},
		},
		{
			name: "cursor through its glue", host: "cursor", event: cursorGlueEnvelope,
			want: types.SpawnRequest{
				Kind: types.SpawnKindSpawn, Host: "cursor", Session: "conv-parent", AgentType: "explore",
				Prompt: "Audit internal/job", Role: types.AgentRoleRoot,
			},
		},
		{
			name: "cursor raw", host: "cursor", event: cursorRawEnvelope,
			want: types.SpawnRequest{
				Kind: types.SpawnKindSpawn, Host: "cursor", Session: "conv-parent", AgentType: "explore",
				Prompt: "Audit internal/job", Role: types.AgentRoleRoot,
			},
		},
		{
			name: "codex-shaped", host: "codex", event: codexShapedEnvelope,
			want: types.SpawnRequest{
				Kind: types.SpawnKindSpawn, Host: "codex", Session: "codex-s",
				Prompt: "Audit internal/job", Role: types.AgentRoleRoot,
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
	v := Judge(ctx, strict(Dependencies{SpawnRule: probe.rule()}), Request{Input: claudeSpawnEnvelope, Host: "claude-code", ReportsSkills: true})
	assert.Equal(t, verdictWithRule("deny", string(denySpawnUnbriefed)), unworded(v), "the built-in reason stands")
	assert.Empty(t, probe.asked)
}

func TestSpawnUnbriefedAdvisesByDefault(t *testing.T) {
	ctx, _ := spawnFixture(t)
	req := Request{Input: claudeSpawnEnvelope, Host: "claude-code", ReportsSkills: true}

	first := Judge(ctx, Dependencies{}, req)
	// The context is the advice prose; the reason stays empty, which the comparison pins.
	got := first
	got.Context, got.Lease, got.LeaseFrom = "", "", ""
	assert.Equal(t, verdictWithRule("advise", string(denySpawnUnbriefed)), got)

	again := Judge(ctx, Dependencies{}, req)
	assert.NotEqual(t, string(denySpawnUnbriefed), again.Rule, "once per session")
}

func TestSpawnRuleDenyAndAdviseReachTheVerdict(t *testing.T) {
	ctx, _ := spawnFixture(t)
	deny := &spawnRuleProbe{answer: types.GuardVerdict{Decision: types.GuardDeny, Reason: "Name a model."}}
	v := Judge(ctx, Dependencies{SpawnRule: deny.rule()}, Request{Input: claudeContinueEnvelope, Host: "claude-code"})
	assert.Equal(t, Verdict{SchemaVersion: v.SchemaVersion, Decision: "deny", Reason: "Name a model.", Rule: workspaceSpawnRule}, v)

	ctx, _ = spawnFixture(t)
	advise := &spawnRuleProbe{answer: types.GuardVerdict{Decision: types.GuardAdvise, Reason: "Add a Done when section."}}
	v = Judge(ctx, Dependencies{SpawnRule: advise.rule()}, Request{Input: cursorGlueEnvelope, Host: "cursor"})
	assert.Equal(t, Verdict{SchemaVersion: v.SchemaVersion, Decision: "advise", Context: "Add a Done when section.", Rule: workspaceSpawnRule}, v)
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
		Kind:    types.SpawnKindSpawn,
		Host:    "claude-code",
		Session: probe.asked[1].Session,
		Prompt:  "x",
		Agent:   "never-seen",
		Role:    types.AgentRoleRoot,
	}, probe.asked[0], "the subagent reports the main session's id; magus never saw it spawned, so no Parent")
	assert.Empty(t, probe.asked[1].Agent, "the main agent carries no subagent id")
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
	require.NoError(t, job.NewStore(job.Location{CacheDir: at.cacheDir, Root: at.workspace}).Bind(who.caller(), id))
}

// execHere records id as taken in the fixture's checkout, as `magus job exec` does.
func execHere(t *testing.T, ctx context.Context, id string) {
	t.Helper()
	at := hookLocation(ctx, Dependencies{})
	_, err := job.NewStore(job.Location{CacheDir: at.cacheDir, Root: at.workspace}).Exec(ctx, id, "rev")
	require.NoError(t, err)
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

func TestLastContextTokens(t *testing.T) {
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
			got, ok := lastContextTokens(tc.path(t))
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
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
	const base = "4f1c2e9+0d3b7a51"
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
			deps := Dependencies{CheckoutBase: func(_ context.Context, got string) string {
				assert.Equal(t, root, got, "the base is read from the checkout the child runs in")
				return base
			}}
			target := filepath.Join(root, "internal", "guard", "guard.go")
			require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
			require.NoError(t, os.WriteFile(target, []byte("package guard\n"), 0o644))

			Judge(ctx, deps, Request{Input: finishedSpawn(t, tc.title, "brisk-heron", "sonnet", tc.isolation, "a1b2c3"), Host: "claude-code"})
			edit := map[string]any{"file_path": target, "old_string": "package guard", "new_string": "package guard // edited"}
			cwd := root
			if tc.elsewhere {
				cwd = t.TempDir()
			}
			write := hookJSON(t, map[string]any{"session_id": "8f2c6a1e", "agent_id": "a1b2c3", "agent_type": "general-purpose", "cwd": cwd, "permission_mode": "default", "hook_event_name": "PreToolUse", "tool_name": "Edit", "tool_input": edit})
			v := Judge(ctx, deps, Request{Input: write, Host: "claude-code", Lease: tc.lease})

			rows, err := job.NewStore(job.Location{CacheDir: at.cacheDir, Root: at.workspace}).List()
			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.Equal(t, tc.want, outcome{
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
	const base = "9c0ffee+1a2b3c4d"
	row := types.Job{ID: "guard-facts", State: types.StateRunning, WritePaths: []string{"internal/guard/**"}}
	spawnerCtx, spawnerRoot := fleetFixture(t, row)
	workerRoot, workerCache := t.TempDir(), t.TempDir()
	// A linked worktree of the spawner's repository, which is what shares the job store.
	require.NoError(t, os.WriteFile(filepath.Join(workerRoot, ".git"),
		[]byte("gitdir: "+filepath.Join(spawnerRoot, ".git", "worktrees", "worker")+"\n"), 0o644))
	workerCtx := WithLocation(t.Context(), workerCache, workerRoot, workerRoot)
	deps := Dependencies{CheckoutBase: func(_ context.Context, got string) string {
		assert.Equal(t, workerRoot, got, "the base is the worker's checkout")
		return base
	}}

	Judge(spawnerCtx, deps, Request{Input: finishedSpawn(t, "orchestrator/fix guard-facts", "guard-facts", "opus", "worktree", "a1b2c3"), Host: "claude-code"})

	edit := func(rel string) Verdict {
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

	rows, err := job.NewStore(job.Location{CacheDir: workerCache, Root: workerRoot}).List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, base, rows[0].ReportedBase)

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
	row := types.Job{ID: "guard-facts", State: types.StateRunning, WritePaths: []string{"internal/guard/**"}}
	_, spawnerRoot := fleetFixture(t, row)
	workerRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workerRoot, ".git"),
		[]byte("gitdir: "+filepath.Join(spawnerRoot, ".git", "worktrees", "worker")+"\n"), 0o644))
	worker := location{cacheDir: t.TempDir(), workspace: workerRoot, dir: workerRoot}
	deps := Dependencies{CheckoutBase: func(context.Context, string) string { return "9c0ffee" }}

	require.True(t, registerAgentBase(t.Context(), deps, worker, row.ID))
	assert.False(t, registerAgentBase(t.Context(), deps, worker, row.ID), "a job registers once")

	rows, err := job.NewStore(job.Location{CacheDir: worker.cacheDir, Root: workerRoot}).List()
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
		ReportedBase: "9c0ffee",
		BaseVerdict:  got.BaseVerdict,
		RegisteredBy: got.RegisteredBy,
		Registered:   got.Registered,
		CheckoutRoot: abs,
		Created:      got.Created,
		Updated:      got.Updated,
	}, got)
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
	t.Setenv(trail.EnvBaggage, "")
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
	graded := func(child string) Verdict {
		return Judge(ctx, Dependencies{}, Request{Input: "ls", Host: "claude-code", Session: "worker-session", Agent: child})
	}

	spawn("worker/forger victim-job", "forged")
	rec, ok := readSpawnedAgent(facts, "forged")
	require.True(t, ok)
	assert.Empty(t, job.NewStore(job.Location{CacheDir: cacheDir, Root: root}).Bound(job.Caller{Host: "claude-code", Session: "worker-session", Agent: "forged"}),
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

func gradedAs(v Verdict) graded { return graded{v.Decision == "deny", v.Lease, v.LeaseFrom} }

// TestASubagentsExecNeverBindsItsParent is the 2026-09-26 incident. An orchestrator spawned
// a researcher in the foreground, so no spawn response attributed it while it ran, and the
// researcher ran `magus job exec` on a read-only job in the orchestrator's checkout. The
// CLI wrote a checkout-wide marker, the orchestrator's session fell back to it, and its
// next `job fork` and `git merge` were refused as the worker's.
func TestASubagentsExecNeverBindsItsParent(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	research := types.Job{ID: "linux-platform-design", Parent: "harness-audit", Criteria: "research only", ReadOnly: true, State: types.StateDeclared, Checkpoint: "23558048b"}
	ctx, root := fleetFixture(t,
		types.Job{ID: "harness-audit", Criteria: "the audit", State: types.StateRunning, WritePaths: []string{"internal/guard/**"}},
		research)
	cacheDir := hookLocation(ctx, Dependencies{}).cacheDir
	const session = "8f2c6a1e"

	// The foreground spawn's PreToolUse; its PostToolUse lands only after the child stops.
	foreground := strings.Replace(claudeSpawnEnvelope, `"run_in_background":true`, `"run_in_background":false`, 1)
	require.NotEqual(t, "deny", Judge(ctx, Dependencies{}, Request{Input: foreground, Host: "claude-code"}).Decision)

	exec := Judge(ctx, Dependencies{}, Request{Input: bashCall(t, session, "a1b2c3", "./magus job exec "+research.ID), Host: "claude-code"})
	require.Equal(t, graded{}, gradedAs(exec), "the child held nothing when it took the job")
	_, err := job.NewStore(job.Location{CacheDir: cacheDir, Root: root}).Exec(ctx, research.ID, "23558048b")
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

	lease, from := job.ActingLease(cacheDir, "")
	assert.Equal(t, graded{}, graded{Lease: lease, From: from}, "a process that is not a hook, the sandbox's run included, reads no binding either")
}

// Exec binds exactly the identity the host named on the call, through either door, and
// nobody sharing its session or checkout.
func TestExecBindsTheCallersIdentity(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	running := func(id string) types.Job {
		return types.Job{ID: id, State: types.StateRunning, WritePaths: []string{"internal/" + id + "/**"}, Registered: 1, ReportedBase: "77aa01c"}
	}
	ctx, _ := fleetFixture(t, running("session-job"), running("agent-job"), running("tool-job"))

	Judge(ctx, Dependencies{}, Request{Input: "magus job exec session-job", Host: "codex", Session: "s1"})
	Judge(ctx, Dependencies{}, Request{Input: "magus --root . job exec --base 77aa01c agent-job", Host: "claude-code", Session: "s2", Agent: "a1"})
	tool := hookJSON(t, map[string]any{"session_id": "s3", "hook_event_name": "PreToolUse", "tool_name": "mcp__magus__client",
		"tool_input": map[string]any{"script": `import "magus"; magus\job.register("tool-job", reported_base: "77aa01c");`}})
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
// on it, so a caller held to it could be stuck for good.
func TestExecAfterAnEndedJobRebinds(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	for _, state := range []types.JobState{types.StateExited, types.StatePass, types.StateFail, types.StateNoReturn} {
		t.Run(string(state), func(t *testing.T) {
			held := types.Job{ID: "held-job", State: state, WritePaths: []string{"internal/held/**"}, Registered: 1}
			next := types.Job{ID: "next-job", State: types.StateDeclared, WritePaths: []string{"internal/next/**"}}
			ctx, _ := fleetFixture(t, held, next)
			who := hookAttribution{Host: "claude-code", Session: "s1", Agent: "a1"}
			bindCaller(t, ctx, who, held.ID)

			v := Judge(ctx, Dependencies{}, Request{Input: "magus job exec next-job", Host: who.Host, Session: who.Session, Agent: who.Agent})
			assert.Equal(t, graded{Lease: held.ID, From: types.LeaseSourceAgent}, gradedAs(v))
			assert.Equal(t, job.Binding{Job: next.ID}, boundJob(who, hookLocation(ctx, Dependencies{})))
		})
	}
}

// A job still declared or running holds its caller: taking another is how a worker would
// be graded against a boundary nobody handed it. Taking its own again passes.
func TestExecUnderARunningJobIsRefused(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
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
	t.Setenv(trail.EnvBaggage, "")
	row := types.Job{ID: "guard-facts", State: types.StateRunning, WritePaths: []string{"internal/guard/**"}, Registered: 1, ReportedBase: "77aa01c"}
	ctx, _ := fleetFixture(t, row)
	Judge(ctx, Dependencies{}, Request{Input: finishedSpawn(t, "orchestrator/integrator guard-facts", "", "", "", "a1b2c3"), Host: "claude-code"})
	who := hookAttribution{Host: "claude-code", Session: "8f2c6a1e", Agent: "a1b2c3"}
	at := hookLocation(ctx, Dependencies{})
	store := job.NewStore(job.Location{CacheDir: at.cacheDir, Root: at.workspace})
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
	_, err = os.Stat(job.MarkerPath(at.cacheDir))
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
	root := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root, "-c", "user.email=test@example.com", "-c", "user.name=test"}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	runGit("init", "-q")
	magusfile := filepath.Join(root, "magusfile.buzz")
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("{}\n"), 0o644))
	require.NoError(t, os.WriteFile(magusfile, []byte(denyEverySpawn), 0o644))
	runGit("add", "-A")
	runGit("commit", "-q", "-m", "init")
	require.NoError(t, os.WriteFile(magusfile, []byte(worktree), 0o644))
	return root
}

func inspectMagus(t *testing.T, root string) *magus.Magus {
	t.Helper()
	ws, err := magus.Inspect(t.Context(), root)
	require.NoError(t, err, "the working tree still loads")
	m, ok := ws.(*magus.Magus)
	require.True(t, ok)
	return m
}

// A fresh checkout has no lineage recording that a spawn rule ever existed, so deleting the
// rule from its working tree must not be what decides whether the approved rule is asked.
func TestApprovedRuleAppliesAfterTheWorkingTreeDeletesIt(t *testing.T) {
	m := inspectMagus(t, committedSpawnRule(t, "import \"magus\";\n"))
	require.Nil(t, m.SpawnRule(), "the working tree registers no rule")

	ctx, _ := spawnFixture(t)
	v := Judge(ctx, Dependencies{SpawnRule: m.SpawnRule(), ApprovedSpawnRule: m.ApprovedSpawnRule},
		Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
	assert.Equal(t, Verdict{SchemaVersion: v.SchemaVersion, Decision: "deny", Reason: "Name a model.", Rule: workspaceSpawnRule, Lease: v.Lease, LeaseFrom: v.LeaseFrom}, v)
}

// Resolving the approved rule is the one part of a spawn an agent can slow down, so running
// out of time denies rather than letting the working tree's looser rule stand alone.
func TestSlowApprovedRuleDenies(t *testing.T) {
	m := inspectMagus(t, committedSpawnRule(t, `import "magus";

magus\guard.spawn(fun (req: SpawnRequest) > GuardVerdict {
    return magus\guard.allow();
});
`))
	require.NotNil(t, m.SpawnRule())

	prev := approvedResolveTimeout
	approvedResolveTimeout = 50 * time.Millisecond
	t.Cleanup(func() { approvedResolveTimeout = prev })
	slow := func(ctx context.Context) (workspace.SpawnRule, error) {
		<-ctx.Done()
		return m.ApprovedSpawnRule(ctx)
	}

	ctx, cacheDir := spawnFixture(t)
	v := Judge(ctx, Dependencies{SpawnRule: m.SpawnRule(), ApprovedSpawnRule: slow},
		Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
	assert.Equal(t, Verdict{SchemaVersion: v.SchemaVersion, Decision: "deny", Reason: approvedRuleTimedOut(seamSpawn), Rule: workspaceSpawnRule, Lease: v.Lease, LeaseFrom: v.LeaseFrom}, v)

	spawns := trailEvents(t, cacheDir, trail.KindAgentSpawn)
	require.Len(t, spawns, 1)
	assert.Equal(t, decidedByApproved, spawns[0].DecidedBy)
	assert.Equal(t, []trail.RuleFailure{{Side: decidedByApproved, Error: "resolving the rule took longer than 50ms"}},
		readSpawnBlob(t, cacheDir, spawns[0]).RuleFailures)
}
