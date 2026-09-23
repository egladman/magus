package guard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
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
	Judge(ctx, deps, Request{Input: finishedSpawnEnvelope, Host: "claude-code"})
	seen := hint.MarkerPath(cacheDir, SessionKey("claude-code", "8f2c6a1e"), agentSeenKind("a1b2c3"))
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
	Target      string `json:"target"`
	RuleFailure string `json:"rule_failure"`
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
		approved types.SpawnVerdict
		want     string
		by       string
	}{
		{"an approved deny still denies", types.SpawnVerdict{Decision: types.SpawnDeny, Reason: "Name a model."}, "deny", decidedByApproved},
		{"an approved allow reports the failure", types.SpawnVerdict{Decision: types.SpawnAllow}, "advise", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cacheDir := spawnFixture(t)
			approved := (&spawnRuleProbe{answer: tc.approved}).rule()
			deps := Dependencies{
				LoadFailure:       loadErr,
				ApprovedSpawnRule: func(context.Context) workspace.SpawnRule { return approved },
			}
			v := Judge(ctx, deps, Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
			assert.Equal(t, tc.want, v.Decision)
			if tc.want == "advise" {
				assert.Contains(t, v.Context, "failed to load")
				assert.Contains(t, v.Context, loadErr.Error())
			}

			spawns := trailEvents(t, cacheDir, trail.KindAgentSpawn)
			require.Len(t, spawns, 1)
			assert.Equal(t, tc.by, spawns[0].DecidedBy)
			assert.Contains(t, readSpawnBlob(t, cacheDir, spawns[0]).RuleFailure, loadErr.Error())
		})
	}
}

// A workspace advise joins a built-in one rather than being dropped behind it.
func TestWorkspaceAdviseJoinsABuiltInAdvise(t *testing.T) {
	row := types.Job{ID: "wave/worker", Criteria: "the guard", WritePaths: []string{"internal/guard/**"}, State: types.StateRunning, Registered: 1}
	ctx, _ := fleetFixture(t, row)
	cacheDir := hookLocation(ctx, Dependencies{}).cacheDir
	require.NoError(t, job.Checkout{CacheDir: cacheDir, Session: "8f2c6a1e"}.Bind(row.ID))

	probe := &spawnRuleProbe{answer: types.SpawnVerdict{Decision: types.SpawnAdvise, Reason: "Add a Done when section."}}
	v := Judge(ctx, Dependencies{SpawnRule: probe.rule()}, Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
	assert.Equal(t, "advise", v.Decision)
	assert.Equal(t, string(advisorySharedCheckout), v.Rule, "the built-in advice keeps its rule")
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

// A stop for an agent magus never saw spawned still records its size, and a transcript
// with no usage records nothing rather than a zero.
func TestSubagentStopRecordsOnlyReportedUsage(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	facts := hint.NewGate(cacheDir, SessionKey("claude-code", "8f2c6a1e"))

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
	rule := func(ctx context.Context, _ types.SpawnRequest, _ hint.Gate) (types.SpawnVerdict, error) {
		snap, ok := job.SnapshotFromContext(ctx)
		require.True(t, ok, "the rule runs under the guard's rows")
		seen = append(seen, snap)
		return types.SpawnVerdict{}, nil
	}
	Judge(ctx, Dependencies{SpawnRule: rule}, Request{Input: claudeContinueEnvelope, Host: "claude-code"})
	require.Len(t, seen, 1)
	require.NoError(t, seen[0].Err)
	require.Len(t, seen[0].Rows, 1)
	assert.Equal(t, row.ID, seen[0].Rows[0].ID)
	assert.Equal(t, row.WritePaths, seen[0].Rows[0].WritePaths)
}

// A spawn titled `<parent>/<role> <job>` naming a live job attributes the child to it:
// the child's later calls are graded under that lease, and a child sharing this checkout
// has this checkout's base recorded for a job that never reported one.
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
	// Positional: the spawn title, its isolation, an explicit --lease, the stored row, and
	// what the child's write then meets.
	cases := []struct {
		name, title, isolation, lease string
		row                           types.Job
		want                          outcome
	}{
		{"a live job, shared checkout", title, "", "", running, outcome{Lease: "guard-facts", ReportedBase: base, Registered: true}},
		{"an isolated child reports its own base", title, "worktree", "", running, outcome{Lease: "guard-facts", Denied: true}},
		{"a base already reported is kept", title, "", "", reported, outcome{Lease: "guard-facts", ReportedBase: "77aa01c", Registered: true}},
		{"a finished job attributes nothing", title, "", "", finished, outcome{}},
		{"a title in another form attributes nothing", "orchestrator/brisk-heron/implement adr 0002", "", "", running, outcome{}},
		{"a title naming no role attributes nothing", "orchestrator guard-facts", "", "", running, outcome{}},
		{"an explicit lease outranks the attribution", title, "", "other", running, outcome{Lease: "other", Denied: true, ReportedBase: base, Registered: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, root := fleetFixture(t, tc.row)
			at := hookLocation(ctx, Dependencies{})
			deps := Dependencies{CheckoutBase: func(_ context.Context, got string) string {
				assert.Equal(t, root, got, "the base is read from the spawning checkout")
				return base
			}}
			target := filepath.Join(root, "internal", "guard", "guard.go")
			require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
			require.NoError(t, os.WriteFile(target, []byte("package guard\n"), 0o644))

			Judge(ctx, deps, Request{Input: finishedSpawn(t, tc.title, "brisk-heron", "sonnet", tc.isolation, "a1b2c3"), Host: "claude-code"})
			edit := map[string]any{"file_path": target, "old_string": "package guard", "new_string": "package guard // edited"}
			write := hookJSON(t, map[string]any{"session_id": "8f2c6a1e", "agent_id": "a1b2c3", "agent_type": "general-purpose", "cwd": "/Users/dev/repo", "permission_mode": "default", "hook_event_name": "PreToolUse", "tool_name": "Edit", "tool_input": edit})
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
