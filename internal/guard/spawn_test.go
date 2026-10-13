package guard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpawnRequiresTheMultiAgentBrief pins the one thing this rule may read. It asks
// whether a marker exists, never what the prompt says, because Evaluate hands it spawns
// whose prose deliberately goes unjudged.
func TestSpawnRequiresTheMultiAgentBrief(t *testing.T) {
	t.Parallel()

	t.Run("the first spawn of a session is denied and names the skill", func(t *testing.T) {
		g := hint.NewGate(t.TempDir(), "session-a")
		reason := denySpawnWithoutBrief(g, true, "")
		require.NotEmpty(t, reason)
		assert.Contains(t, reason, multiAgentSkill.String())
	})

	t.Run("loading the skill clears it for the rest of the session", func(t *testing.T) {
		g := hint.NewGate(t.TempDir(), "session-b")
		require.True(t, recordSkillLoad(g, multiAgentSkill.String()))
		assert.Empty(t, denySpawnWithoutBrief(g, true, ""))
	})

	t.Run("a different skill does not clear it", func(t *testing.T) {
		g := hint.NewGate(t.TempDir(), "session-c")
		recordSkillLoad(g, "magus-run")
		assert.NotEmpty(t, denySpawnWithoutBrief(g, true, ""),
			"any skill standing in for the brief would make the rule unfalsifiable")
	})

	t.Run("two sessions do not share the marker", func(t *testing.T) {
		dir := t.TempDir()
		recordSkillLoad(hint.NewGate(dir, "session-d"), multiAgentSkill.String())
		assert.NotEmpty(t, denySpawnWithoutBrief(hint.NewGate(dir, "session-e"), true, ""),
			"the marker is keyed on the session, so one session's load is not another's")
	})

	t.Run("a host reporting no session is not guarded here", func(t *testing.T) {
		assert.Empty(t, denySpawnWithoutBrief(hint.NewGate(t.TempDir(), ""), true, ""),
			"with no session pointer every spawn everywhere would share one bucket: the first denies and the rest pass")
		assert.False(t, recordSkillLoad(hint.NewGate(t.TempDir(), ""), multiAgentSkill.String()))
	})

	t.Run("an empty skill name records nothing", func(t *testing.T) {
		assert.False(t, recordSkillLoad(hint.NewGate(t.TempDir(), "session-f"), ""))
	})
}

// TestSpawnRuleStandsDownWhereLoadsGoUnobserved is the host-agnosticism guarantee, and the
// most important case here.
//
// Four harnesses ship and they wire hooks differently: one matches a skill tool, one wires
// four other matchers, one uses its own event names, and one wires no hook config at all.
// On a host that reports no skill load, a marker can never appear, so a rule that denied
// anyway would deny EVERY spawn for the life of the session with nothing the reader could
// do. Standing down is the only honest answer, and false is the default.
func TestSpawnRuleStandsDownWhereLoadsGoUnobserved(t *testing.T) {
	t.Parallel()

	g := hint.NewGate(t.TempDir(), "unobserved-host")
	assert.Empty(t, denySpawnWithoutBrief(g, false, ""),
		"a host that cannot report a skill load must not be held to having reported one")
	assert.NotEmpty(t, denySpawnWithoutBrief(g, true, ""),
		"the same session denies once the wiring declares it reports skills")
}

// TestEitherCopyClearsTheBrief pins that the twins are interchangeable as EVIDENCE: they
// carry the same rules, so a session that loaded the full one has read the brief.
func TestEitherCopyClearsTheBrief(t *testing.T) {
	t.Parallel()

	g := hint.NewGate(t.TempDir(), "session-full")
	require.True(t, recordSkillLoad(g, agent.FullTwinName(multiAgentSkill.String())))
	assert.Empty(t, denySpawnWithoutBrief(g, true, ""))
}

// TestDenyNamesOnlyTheAlwaysInstalledCopy pins that the reason never points at the full
// twin. One shipped harness installs the short form alone, so naming the twin there would
// send the reader after a skill that is not on disk.
func TestDenyNamesOnlyTheAlwaysInstalledCopy(t *testing.T) {
	t.Parallel()

	reason := denySpawnWithoutBrief(hint.NewGate(t.TempDir(), "session-g"), true, "")
	require.NotEmpty(t, reason)
	assert.NotContains(t, reason, agent.FullTwinName(multiAgentSkill.String()))
}

// TestDenyNamesTheInstallWhenTheSkillIsMissing pins the stale-install trap: a workspace whose
// installed skills predate the one the deny asks for. Telling that reader to load a skill no
// location carries is a deny nothing clears, so the remedy is the install that puts it there.
func TestDenyNamesTheInstallWhenTheSkillIsMissing(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	stale := filepath.Join(workspace, ".claude", "skills", "magus-delegate-ultra")
	require.NoError(t, os.MkdirAll(stale, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(stale, "SKILL.md"), []byte("old"), 0o644))

	reason := denySpawnWithoutBrief(hint.NewGate(t.TempDir(), "session-stale"), true, workspace)
	require.NotEmpty(t, reason)
	assert.Contains(t, reason, "not installed in .claude/skills")
	assert.Contains(t, reason, "magus agent install .claude/skills --force")

	current := filepath.Join(workspace, ".claude", "skills", multiAgentSkill.String())
	require.NoError(t, os.MkdirAll(current, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(current, "SKILL.md"), []byte("new"), 0o644))
	reason = denySpawnWithoutBrief(hint.NewGate(t.TempDir(), "session-installed"), true, workspace)
	assert.NotContains(t, reason, "not installed", "an installed skill only needs loading")
}

// TestSpawnIsAdvisedWhenTheCheckoutIsAlreadyHeld pins the advisory that makes the
// collision proof unskippable: an orchestrator handing out a second worker in a checkout
// somebody is already writing in is told so, with the union of the write paths to check and the
// worktree that makes the check unnecessary.
func TestSpawnIsAdvisedWhenTheCheckoutIsAlreadyHeld(t *testing.T) {
	held := types.Job{
		ID: "wave/holder", Criteria: "the store", WritePaths: []string{"internal/job", "types/job.go"},
		State: types.StateRunning, Registered: 1,
	}
	ctx, _ := fleetFixture(t, held)
	cacheDir := hookLocation(ctx, Dependencies{}).cacheDir
	at := hookLocation(ctx, Dependencies{})

	t.Run("nothing bound here says nothing", func(t *testing.T) {
		assert.Empty(t, adviseSharedCheckoutSpawn(ctx, hint.NewGate(cacheDir, "quiet"), at))
	})

	execHere(t, ctx, held.ID)

	gate := hint.NewGate(cacheDir, "orchestrator")
	full := adviseSharedCheckoutSpawn(ctx, gate, at)
	note := full.Say
	require.NotEmpty(t, note)
	assert.NotEmpty(t, full.Why, "the first firing carries the rationale for the stored advice")
	assert.Contains(t, note, "wave/holder")
	assert.Contains(t, note, "internal/job")
	assert.Contains(t, note, "types/job.go")
	assert.Contains(t, note, hint.DescribeFile.String())
	assert.Contains(t, note, "worktree")

	t.Run("it fires once per session", func(t *testing.T) {
		again := adviseSharedCheckoutSpawn(ctx, gate, at).Say
		assert.NotEqual(t, note, again, "the full text is spent once; a brief line is what repeats")
	})
}

// A spawn_agent call reaches the guard as the payload a Codex PreToolUse writes: the
// prompt is `message` (or text `items`), and there is no `prompt` key.
func TestDecodeHookEnvelopeSpawnCodex(t *testing.T) {
	t.Parallel()

	const envelope = `{"session_id":"019e4c21-7d5a-7c11-9a0e-3f5b1c2d4e60","turn_id":"019e4c21-8a10-7a52-b6a1-0d2e9c1f7b33",` +
		`"transcript_path":"/Users/dev/.codex/sessions/2026/10/13/rollout-2026-10-13T09-14-02-019e4c21.jsonl","cwd":"/Users/dev/repo",` +
		`"hook_event_name":"PreToolUse","model":"gpt-5.5","permission_mode":"default","tool_name":"spawn_agent",` +
		`"tool_input":%s,"tool_use_id":"call_Qx8aTz1uJm"}`

	for name, tc := range map[string]struct {
		input string
		want  hookRequest
	}{
		"v1 message": {
			`{"message":"Read internal/guard/spawn.go and report the exported names.","agent_type":"explorer","model":"gpt-5.5","reasoning_effort":"low","fork_context":false}`,
			hookRequest{
				Value: "Read internal/guard/spawn.go and report the exported names.", IsSpawn: true, Tool: "spawn_agent",
				Child: "explorer", DeclaredModel: "gpt-5.5", Spawn: spawnFields{AgentType: "explorer"},
			},
		},
		"v1 items": {
			`{"items":[{"type":"text","text":"Run the tests."},{"type":"skill","name":"magus-run","path":"/skills/magus-run/SKILL.md"},{"type":"text","text":"Report failures."}],"agent_type":"worker"}`,
			hookRequest{
				Value: "Run the tests.\nReport failures.", IsSpawn: true, Tool: "spawn_agent",
				Child: "worker", Spawn: spawnFields{AgentType: "worker"},
			},
		},
		"v2 task_name": {
			`{"task_name":"audit_guard","message":"Audit internal/guard.","agent_type":"explorer","fork_turns":"none"}`,
			hookRequest{
				Value: "Audit internal/guard.", IsSpawn: true, Tool: "spawn_agent",
				Child: "explorer", Spawn: spawnFields{AgentType: "explorer", Name: "audit_guard"},
			},
		},
		"v2 named only": {
			`{"task_name":"audit_guard","message":"Audit internal/guard."}`,
			hookRequest{
				Value: "Audit internal/guard.", IsSpawn: true, Tool: "spawn_agent",
				Child: "audit_guard", Spawn: spawnFields{Name: "audit_guard"},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			req, ok := decodeHookEnvelope(fmt.Sprintf(envelope, tc.input))
			require.True(t, ok)
			tc.want.Cwd = "/Users/dev/repo"
			tc.want.Who = hookAttribution{
				Session:    "019e4c21-7d5a-7c11-9a0e-3f5b1c2d4e60",
				Transcript: "/Users/dev/.codex/sessions/2026/10/13/rollout-2026-10-13T09-14-02-019e4c21.jsonl",
				Event:      "PreToolUse",
			}
			assert.Equal(t, tc.want, req)
		})
	}

	t.Run("a bare message is not a spawn", func(t *testing.T) {
		req, ok := decodeHookEnvelope(fmt.Sprintf(envelope, `{"message":"hello"}`))
		require.True(t, ok)
		assert.False(t, req.IsSpawn)
		assert.True(t, req.NothingToJudge)
	})
}

// send_input, send_message and followup_task name their addressee `target`.
func TestDecodeHookEnvelopeContinueTarget(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		toolInput, message string
	}{
		"send_input":    {`{"target":"019e4c22-1b3d-7e90-8c44-5a6f7d8e9012","message":"Also check the tests.","interrupt":false}`, "Also check the tests."},
		"followup_task": {`{"target":"/root/audit_guard","message":"Now the hook path."}`, "Now the hook path."},
		"items":         {`{"target":"/root/audit_guard","items":[{"type":"text","text":"Now the hook path."}]}`, "Now the hook path."},
		"to":            {`{"to":"brisk-heron","message":"Carry on."}`, "Carry on."},
	} {
		t.Run(name, func(t *testing.T) {
			req, ok := decodeHookEnvelope(`{"session_id":"s1","hook_event_name":"PreToolUse","tool_name":"x","tool_input":` + tc.toolInput + `}`)
			require.True(t, ok)
			assert.True(t, req.IsContinue)
			assert.False(t, req.IsSpawn)
			assert.Equal(t, tc.message, req.Value)
			assert.NotEmpty(t, req.Target)
		})
	}

	t.Run("a target with no message is not a continuation", func(t *testing.T) {
		req, ok := decodeHookEnvelope(`{"session_id":"s1","hook_event_name":"PreToolUse","tool_name":"close_agent","tool_input":{"target":"/root/audit_guard"}}`)
		require.True(t, ok)
		assert.False(t, req.IsContinue)
		assert.True(t, req.NothingToJudge)
	})
}

// Cursor loads a skill by reading its SKILL.md, so the read is the load, and a write to
// the same path is not.
func TestDecodeHookEnvelopeSkillRead(t *testing.T) {
	t.Parallel()

	const skillPath = "/Users/dev/repo/.cursor/skills/magus-multi-agent/SKILL.md"
	decode := func(t *testing.T, toolInput string) hookRequest {
		t.Helper()
		req, ok := decodeHookEnvelope(`{"conversation_id":"c1","session_id":"c1","hook_event_name":"preToolUse","tool_name":"Read","tool_input":` + toolInput + `}`)
		require.True(t, ok)
		return req
	}

	t.Run("a read of a SKILL.md", func(t *testing.T) {
		req := decode(t, `{"file_path":"`+skillPath+`"}`)
		assert.Equal(t, "magus-multi-agent", req.LoadedSkill)
		assert.Equal(t, skillPath, req.Value)
	})
	t.Run("a bounded read", func(t *testing.T) {
		assert.Equal(t, "magus-run", decode(t, `{"file_path":"/Users/dev/.agents/skills/magus-run/SKILL.md","offset":1,"limit":40}`).LoadedSkill)
	})
	t.Run("a relative path", func(t *testing.T) {
		assert.Equal(t, "magus-run", decode(t, `{"file_path":"skills/magus-run/SKILL.md"}`).LoadedSkill)
	})
	for name, toolInput := range map[string]string{
		"a write":            `{"file_path":"` + skillPath + `","content":"x"}`,
		"an edit":            `{"file_path":"` + skillPath + `","old_string":"a","new_string":"b"}`,
		"a file not named":   `{"file_path":"/Users/dev/repo/.cursor/skills/magus-run/notes.md"}`,
		"outside skills":     `{"file_path":"/Users/dev/repo/docs/magus-run/SKILL.md"}`,
		"an invalid name":    `{"file_path":"/Users/dev/repo/skills/Magus Run/SKILL.md"}`,
		"a nested directory": `{"file_path":"/Users/dev/repo/skills/a/b/SKILL.md"}`,
	} {
		t.Run(name+" loads nothing", func(t *testing.T) {
			assert.Empty(t, decode(t, toolInput).LoadedSkill)
		})
	}

	t.Run("it records the load for the session", func(t *testing.T) {
		g := hint.NewGate(t.TempDir(), "c1")
		req := decode(t, `{"file_path":"`+skillPath+`"}`)
		require.True(t, recordSkillLoad(g, req.LoadedSkill))
		assert.True(t, skillLoaded(g, multiAgentSkill))
	})
}

// A skill read is a read too: the load is recorded and the read meets the read rules, where
// it used to pass before any of them ran.
func TestSkillReadIsRecordedAndJudged(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	root := hookLocation(ctx, Dependencies{}).workspace
	skill := filepath.Join(root, ".cursor", "skills", "magus-run", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(skill), 0o755))
	require.NoError(t, os.WriteFile(skill, []byte("# magus-run\n"), 0o644))
	read := hookJSON(t, map[string]any{"conversation_id": "c1", "session_id": "c1", "cwd": root, "hook_event_name": "preToolUse",
		"tool_name": "Read", "tool_input": map[string]any{"file_path": skill}})
	var judged []types.CommandRequest
	deps := Dependencies{CommandRule: func(_ context.Context, req types.CommandRequest, _ hint.Gate) (types.GuardVerdict, error) {
		judged = append(judged, req)
		return types.GuardVerdict{Decision: types.GuardAllow}, nil
	}}
	v := Judge(ctx, deps, Request{Input: read, Host: "cursor", ReportsSkills: true})
	assert.Equal(t, "pass", v.Decision)
	assert.True(t, skillLoaded(hint.NewGate(cacheDir, hookAttribution{Host: "cursor", Session: "c1"}.skillsKey()), agent.MustSkill("magus-run")))
	require.Len(t, judged, 1, "the read reaches the command rules")
	assert.Equal(t, "cat "+skill, judged[0].Command)
}

func TestAScheduledPromptIsNoSpawn(t *testing.T) {
	t.Parallel()
	for _, input := range []map[string]any{
		{"delaySeconds": float64(270), "prompt": "check the run", "reason": "waiting on CI"},
		{"cron": "*/5 * * * *", "prompt": "check the run", "recurring": true},
	} {
		req, ok := decodeHookEnvelope(hookJSON(t, map[string]any{"session_id": "s", "hook_event_name": "PreToolUse", "tool_name": "ScheduleWakeup", "tool_input": input}))
		require.True(t, ok)
		assert.False(t, req.IsSpawn, "%v", input)
		assert.True(t, req.NothingToJudge, "%v", input)
	}
}

// A host whose spawn answers with a name and whose child's calls carry an id: the child's
// start joins the id to the spawn before it, so the child is graded under the spawn's job.
func TestAChildsStartJoinsItToTheSpawnBeforeIt(t *testing.T) {
	row := types.Job{ID: "guard-facts", State: types.StateDeclared, WritePaths: []string{"internal/guard/**"}}
	ctx, root := fleetFixture(t, row)
	target := filepath.Join(root, "internal", "guard", "guard.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(t, os.WriteFile(target, []byte("package guard\n"), 0o644))
	codex := func(env map[string]any) Verdict {
		env["session_id"], env["cwd"] = "codex-s", root
		return Judge(ctx, Dependencies{}, Request{Input: hookJSON(t, env), Host: "codex"})
	}

	spawn := codex(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "spawn_agent",
		"tool_input": map[string]any{"message": "gAAAAencrypted", "task_name": "worker", "agent_type": "default", "description": "orchestrator/impl guard-facts"}})
	require.NotEqual(t, "deny", spawn.Decision, spawn.Reason)
	codex(map[string]any{"hook_event_name": "SubagentStart", "agent_id": "019e-child"})

	write := codex(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "apply_patch", "agent_id": "019e-child",
		"tool_input": map[string]any{"command": "*** Begin Patch\n*** Update File: " + target + "\n@@\n-package guard\n+package guard // x\n*** End Patch\n"}})
	assert.Equal(t, "guard-facts", write.Lease, "the child acts under the job its spawn named")
}
