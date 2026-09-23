package guard

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// spawnRuleProbe is a workspace rule that records what it was asked and answers as told.
type spawnRuleProbe struct {
	asked  []types.SpawnRequest
	gates  []hint.Gate
	answer types.SpawnVerdict
	err    error
}

func (p *spawnRuleProbe) rule() workspace.SpawnRule {
	return func(_ context.Context, req types.SpawnRequest, facts hint.Gate) (types.SpawnVerdict, error) {
		p.asked = append(p.asked, req)
		p.gates = append(p.gates, facts)
		return p.answer, p.err
	}
}

// spawnFixture pins the trail, markers and job store to temporary dirs, so a spawn test
// writes nothing a developer's checkout reads.
func spawnFixture(t *testing.T) (ctx context.Context, cacheDir string) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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
				Role: types.SpawnRoleRoot,
			},
		},
		{
			name: "claude-code continue", host: "claude-code", event: claudeContinueEnvelope,
			want: types.SpawnRequest{
				Kind: types.SpawnKindContinue, Host: "claude-code", Session: "8f2c6a1e",
				Prompt: "Now fix the review comments.", Role: types.SpawnRoleRoot,
				Target: &types.SpawnTarget{Agent: "brisk-heron"},
			},
		},
		{
			name: "cursor through its glue", host: "cursor", event: cursorGlueEnvelope,
			want: types.SpawnRequest{
				Kind: types.SpawnKindSpawn, Host: "cursor", Session: "conv-parent", AgentType: "explore",
				Prompt: "Audit internal/job", Role: types.SpawnRoleRoot,
			},
		},
		{
			name: "cursor raw", host: "cursor", event: cursorRawEnvelope,
			want: types.SpawnRequest{
				Kind: types.SpawnKindSpawn, Host: "cursor", Session: "conv-parent", AgentType: "explore",
				Prompt: "Audit internal/job", Role: types.SpawnRoleRoot,
			},
		},
		{
			name: "codex-shaped", host: "codex", event: codexShapedEnvelope,
			want: types.SpawnRequest{
				Kind: types.SpawnKindSpawn, Host: "codex", Session: "codex-s",
				Prompt: "Audit internal/job", Role: types.SpawnRoleRoot,
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
			assert.Equal(t, SessionKey(tc.host, tc.want.Session), probe.gates[0].Session(),
				"once and count are keyed on the calling session")
		})
	}
}

// OpenCode's plugin hands the guard a bare command string, never an envelope, so there is
// no spawn for a rule to see there; a string is judged as the command it is.
func TestSpawnRuleIsNotAskedAboutACommand(t *testing.T) {
	ctx, _ := spawnFixture(t)
	probe := &spawnRuleProbe{answer: types.SpawnVerdict{Decision: types.SpawnDeny, Reason: "no"}}
	Judge(ctx, Dependencies{SpawnRule: probe.rule()}, Request{Input: "ls", Host: "opencode"})
	assert.Empty(t, probe.asked)
}

// Strengthen only: a workspace allow cannot lift a built-in deny, and it is not even
// asked when one stands.
func TestSpawnRuleCannotLiftABuiltInDeny(t *testing.T) {
	ctx, _ := spawnFixture(t)
	probe := &spawnRuleProbe{answer: types.SpawnVerdict{Decision: types.SpawnAllow}}
	v := Judge(ctx, Dependencies{SpawnRule: probe.rule()}, Request{Input: claudeSpawnEnvelope, Host: "claude-code", ObservesSkillLoads: true})
	assert.Equal(t, "deny", v.Decision)
	assert.Equal(t, string(denySpawnUnbriefed), v.Rule, "the built-in reason stands")
	assert.Empty(t, probe.asked)
}

func TestSpawnRuleDenyAndAdviseReachTheVerdict(t *testing.T) {
	ctx, _ := spawnFixture(t)
	deny := &spawnRuleProbe{answer: types.SpawnVerdict{Decision: types.SpawnDeny, Reason: "Name a model."}}
	v := Judge(ctx, Dependencies{SpawnRule: deny.rule()}, Request{Input: claudeContinueEnvelope, Host: "claude-code"})
	assert.Equal(t, Verdict{SchemaVersion: v.SchemaVersion, Decision: "deny", Reason: "Name a model.", Rule: workspaceSpawnRule}, v)

	ctx, _ = spawnFixture(t)
	advise := &spawnRuleProbe{answer: types.SpawnVerdict{Decision: types.SpawnAdvise, Reason: "Add a Done when section."}}
	v = Judge(ctx, Dependencies{SpawnRule: advise.rule()}, Request{Input: cursorGlueEnvelope, Host: "cursor"})
	assert.Equal(t, Verdict{SchemaVersion: v.SchemaVersion, Decision: "advise", Context: "Add a Done when section.", Rule: workspaceSpawnRule}, v)
}

// A broken rule judges nothing and says so once per session, following guard.shell's
// fail-open stance on a workspace rule it cannot use.
func TestSpawnRuleFailureFailsOpen(t *testing.T) {
	ctx, _ := spawnFixture(t)
	probe := &spawnRuleProbe{err: errors.New("magus\\guard.spawn: the rule raised: boom")}
	deps := Dependencies{SpawnRule: probe.rule()}

	first := Judge(ctx, deps, Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
	assert.Equal(t, "advise", first.Decision)
	assert.Equal(t, string(advisorySpawnRuleFailed), first.Rule)
	assert.Contains(t, first.Context, "boom")

	assert.Equal(t, "pass", Judge(ctx, deps, Request{Input: claudeSpawnEnvelope, Host: "claude-code"}).Decision,
		"the failure is told once per session, then the spawn proceeds quietly")
}

// The approved rule and the working-tree rule both run and the stricter answer stands,
// so an unapproved edit can tighten but never loosen.
func TestSpawnRulesKeepTheStricterSide(t *testing.T) {
	deny := types.SpawnVerdict{Decision: types.SpawnDeny, Reason: "unnamed model"}
	allow := types.SpawnVerdict{Decision: types.SpawnAllow}
	cases := []struct {
		name           string
		live, approved *types.SpawnVerdict
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
			// The lineage saw a spawn rule before, which is what tells the guard a rule gone
			// from the working tree may still have an approved twin.
			RecordPolicy(ctx, cacheDir, "", PolicyState{Digest: "seen", SpawnRule: true}, false)
			deps := Dependencies{}
			if tc.live != nil {
				deps.SpawnRule = (&spawnRuleProbe{answer: *tc.live}).rule()
			}
			if tc.approved != nil {
				approved := (&spawnRuleProbe{answer: *tc.approved}).rule()
				deps.ApprovedSpawnRule = func(context.Context) workspace.SpawnRule { return approved }
			}
			v := Judge(ctx, deps, Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
			assert.Equal(t, tc.want, v.Decision)

			spawns := trailEvents(t, cacheDir, trail.KindAgentSpawn)
			require.Len(t, spawns, 1)
			assert.Equal(t, tc.decidedBy, spawns[0].DecidedBy, "the trail names which side decided")
		})
	}
}

// A workspace that never registered a spawn rule does not pay for the approved side,
// which can cost a VCS status and a second load of the magusfile, on every spawn.
func TestApprovedSideIsSkippedWithoutAnySpawnRule(t *testing.T) {
	ctx, _ := spawnFixture(t)
	resolved := 0
	deps := Dependencies{ApprovedSpawnRule: func(context.Context) workspace.SpawnRule { resolved++; return nil }}
	assert.Equal(t, "pass", Judge(ctx, deps, Request{Input: claudeSpawnEnvelope, Host: "claude-code"}).Decision)
	assert.Zero(t, resolved)
}

// Role is computed from the job store, never declared by the caller.
func TestSpawnRuleRoleIsComputed(t *testing.T) {
	row := types.Job{ID: "wave/worker", Criteria: "the guard", WritePaths: []string{"internal/guard/**"}, State: types.StateRunning, Registered: 1}
	ctx, _ := fleetFixture(t, row)
	cacheDir := hookLocation(ctx, Dependencies{}).cacheDir

	probe := &spawnRuleProbe{}
	Judge(ctx, Dependencies{SpawnRule: probe.rule()}, Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
	require.Len(t, probe.asked, 1)
	assert.Equal(t, types.SpawnRoleRoot, probe.asked[0].Role)
	assert.Nil(t, probe.asked[0].Lease)

	require.NoError(t, job.Checkout{CacheDir: cacheDir, Session: "8f2c6a1e"}.Bind(row.ID))
	Judge(ctx, Dependencies{SpawnRule: probe.rule()}, Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
	require.Len(t, probe.asked, 2)
	assert.Equal(t, types.SpawnRoleWorker, probe.asked[1].Role)
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
	seen := hint.MarkerPath(cacheDir, SessionKey("claude-code", "8f2c6a1e"), agentSeenKind("brisk-heron"))
	aged := time.Now().Add(-10 * time.Minute)
	require.NoError(t, os.Chtimes(seen, aged, aged))

	Judge(ctx, deps, Request{Input: claudeContinueEnvelope, Host: "claude-code"})
	require.Len(t, probe.asked, 3)
	idle := probe.asked[2].Target.IdleMs
	require.NotNil(t, idle)
	assert.GreaterOrEqual(t, *idle, (10 * time.Minute).Milliseconds())

	Judge(ctx, deps, Request{Input: claudeContinueEnvelope, Host: "claude-code"})
	assert.Less(t, *probe.asked[3].Target.IdleMs, time.Minute.Milliseconds(), "the continue restarted the clock")
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
