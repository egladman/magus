package guard

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
)

// reportedMainTranscript is the hook's transcript pointer for tests that need a
// reported Claude Code main thread.
func reportedMainTranscript(t testing.TB, session string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	path := filepath.Join(home, "projects", "guard-fixture", session+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	row := fmt.Sprintf(`{"type":"user","sessionId":%q,"isSidechain":false}`, session)
	require.NoError(t, os.WriteFile(path, []byte(row+"\n"), 0o600))
	return path
}

func fixtureMainTranscript(session string) string {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		return ""
	}
	path := filepath.Join(dir, "projects", "guard-fixture", session+".jsonl")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

func TestCodexHookAttributesTheLeaseAndUsageToTheCallingRollout(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "sessions")
	require.NoError(t, os.Mkdir(dir, 0o700))
	set := testModelTiers(t, "claude-code")
	ctx := tierFixture(t, set, fixture.FleetLeases()...)
	t.Setenv("CODEX_HOME", home)
	at := hookLocation(ctx, Dependencies{})
	root := hookAttribution{Host: "codex", Session: "parent"}
	writeCostState(hint.NewGate(at.cacheDir, root.factsKey()), costState{Tokens: 205_000})
	deps := withSetting(string(advisoryModelTierContextCap), builtin.Deny)
	deps.ModelTiers = func() *tier.Set { return set }
	writeRollout := func(id, role, source string, tokens int) string {
		t.Helper()
		path := filepath.Join(dir, fmt.Sprintf("rollout-%s.jsonl", id))
		meta := fmt.Sprintf(`{"type":"session_meta","payload":{"id":%q,"source":%s,"thread_source":%q}}`, id, source, role)
		sample := fmt.Sprintf(`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":%d},"model_context_window":400000}}}`, tokens)
		require.NoError(t, os.WriteFile(path, fmt.Appendf(nil, "%s\n%s\n", meta, sample), 0o600))
		return path
	}
	input := func(path string) Request {
		return Request{Host: "codex", Lease: "lease-a", Tools: testTools(t), Input: hookJSON(t, map[string]any{
			"session_id": "parent", "transcript_path": path, "cwd": at.workspace, "hook_event_name": "PreToolUse",
			"tool_name": "apply_patch", "tool_input": map[string]any{"command": "*** Begin Patch\n*** Update File: internal/ledger/store.go\n@@\n-x\n+y\n*** Update File: internal/ledger/types.go\n@@\n-x\n+y\n*** End Patch\n"},
		})}
	}
	parent := writeRollout("parent", "user", `"vscode"`, 205_000)
	childSource := `{"subagent":{"thread_spawn":{"parent_thread_id":"parent","depth":1}}}`
	child := writeRollout("child", "subagent", childSource, 100_000)
	verify := root
	verify.Transcript = parent
	role, owner := transcriptCaller(verify, &verify.transcriptUsage)
	require.Equal(t, "parent", owner, "role %s, home %s", role, os.Getenv("CODEX_HOME"))
	verify.Transcript = child
	role, owner = transcriptCaller(verify, &verify.transcriptUsage)
	require.Equal(t, "child", owner, "role %s", role)
	for _, path := range []string{parent, child} {
		v := Judge(ctx, deps, input(path))
		assert.NotEqual(t, verdict.Deny, v.Decision, v.Reason)
	}
	writeRollout("child", "subagent", childSource, 100_001)
	v := Judge(ctx, deps, input(child))
	assert.Equal(t, verdict.Judgment{Decision: verdict.Deny, Rule: string(advisoryModelTierContextCap)}, judgedAs(v), v.Reason)
	exec := func(path, lease string) Request {
		return Request{Host: "codex", Tools: testTools(t), Input: hookJSON(t, map[string]any{
			"session_id": "parent", "transcript_path": path, "cwd": at.workspace, "hook_event_name": "PreToolUse",
			"tool_name": "exec_command", "tool_input": map[string]any{"cmd": "./magus job exec " + lease},
		})}
	}
	sibling := writeRollout("sibling", "subagent", childSource, 50_000)
	first := exec(sibling, "lease-b")
	v = Judge(ctx, deps, first)
	require.NotEqual(t, verdict.Deny, v.Decision, v.Reason)
	require.Equal(t, "lease-b", ResolveAuthority(ctx, deps, first).Lease)
	req := input(child)
	req.Lease = ""
	require.Empty(t, ResolveAuthority(ctx, deps, req).Lease)
	v = Judge(ctx, deps, exec(child, "lease-a"))
	require.NotEqual(t, verdict.Deny, v.Decision, v.Reason)
	assert.Equal(t, "lease-a", ResolveAuthority(ctx, deps, req).Lease)
	assert.Equal(t, "lease-b", ResolveAuthority(ctx, deps, first).Lease)
	rootReq := input(parent)
	rootReq.Lease = ""
	assert.Empty(t, ResolveAuthority(ctx, deps, rootReq).Lease)
	unrelated := writeRollout("unrelated", "user", `"vscode"`, 12_000)
	v = Judge(ctx, deps, input(unrelated))
	assert.Equal(t, verdict.Judgment{Decision: verdict.Deny, Rule: string(advisoryModelTierContextCap)}, judgedAs(v))
	assert.Contains(t, v.Reason, "cannot attribute")
	disagrees := input(child)
	disagrees.Transcript = parent
	v = Judge(ctx, deps, disagrees)
	assert.Equal(t, verdict.Judgment{Decision: verdict.Deny, Rule: string(advisoryModelTierContextCap)}, judgedAs(v))
	assert.Contains(t, v.Reason, "cannot attribute")
	forged := filepath.Join(t.TempDir(), "parent.jsonl")
	require.NoError(t, os.Symlink(forged, filepath.Join(dir, "outside.jsonl")))
	require.NoError(t, os.WriteFile(forged, []byte(`{"type":"session_meta","payload":{"id":"parent","source":"vscode","thread_source":"user"}}`+"\n"), 0o600))
	v = Judge(ctx, deps, input(filepath.Join(dir, "outside.jsonl")))
	assert.Equal(t, verdict.Judgment{Decision: verdict.Deny, Rule: string(advisoryModelTierContextCap)}, judgedAs(v))
	assert.Contains(t, v.Reason, "cannot attribute")
}

// A leased subagent is advised once its reported context passes its row's tier cap, and
// the cap is read from the declaration once per session.
func TestModelTierContextCapAdvisesPastTheTiersCap(t *testing.T) {
	set := testModelTiers(t, "claude-code")
	ctx := tierFixture(t, set, fixture.FleetLeases()[0])
	at := hookLocation(ctx, Dependencies{})
	who := hookAttribution{Host: "claude-code", Session: "s", Agent: "a1"}
	facts := hint.NewGate(at.cacheDir, who.factsKey())
	asked := 0
	deps := Dependencies{ModelTiers: func() *tier.Set {
		asked++
		return set
	}}
	advise := func(lease string) advice { return adviseModelTierContext(ctx, deps, at, who, facts, lease) }

	assert.Equal(t, advice{}, advise("lease-a"), "no context reported")
	under := int64(90_000)
	writeSpawnedAgent(facts, "a1", spawnedAgent{ContextTokens: &under})
	assert.Equal(t, advice{}, advise("lease-a"), "under the economy cap")
	over := int64(120_000)
	writeSpawnedAgent(facts, "a1", spawnedAgent{ContextTokens: &over})

	got := advise("lease-a")

	assert.Equal(t, "magus workspace: lease lease-a runs at model tier economy, which caps a worker's context at 100k tokens, and yours has reached 120k: report what you have and stop.", got.Say)
	assert.Contains(t, got.Why, "model tier economy (rank 0)")
	assert.Equal(t, 1, asked, "the cap is kept for the session")
	assert.Equal(t, advice{}, advise(""), "no lease, no row")
	assert.Equal(t, advice{}, adviseModelTierContext(ctx, withSetting(string(advisoryModelTierContextCap), builtin.Off), at, who, facts, "lease-a"))
}

// A row forked with no tier, or at a tier with no cap, is never judged.
func TestModelTierContextCapNeedsATierWithACap(t *testing.T) {
	set := testModelTiers(t, "claude-code")
	lease := fixture.FleetLeases()[0]
	lease.Agent = &job.Agent{Model: job.Model{ID: "sonnet"}, Worker: job.Worker{Type: job.WorkerTypeResumed}}
	ctx := tierFixture(t, set, lease)
	at := hookLocation(ctx, Dependencies{})
	who := hookAttribution{Host: "claude-code", Session: "s", Agent: "a1"}
	facts := hint.NewGate(at.cacheDir, who.factsKey())
	over := int64(500_000)
	writeSpawnedAgent(facts, "a1", spawnedAgent{ContextTokens: &over})

	got := adviseModelTierContext(ctx, Dependencies{ModelTiers: func() *tier.Set { return set }}, at, who, facts, "lease-a")

	assert.Equal(t, advice{}, got, "standard caps no context")
	assert.Equal(t, advice{}, adviseModelTierContext(ctx, Dependencies{}, at, who, facts, "lease-a"), "no declaration")
}

// A host with no worker attribution keeps its conservative denial.
func TestModelTierContextCapRaisedToDenyRefusesWrites(t *testing.T) {
	set := testModelTiers(t, "claude-code")
	ctx := tierFixture(t, set, fixture.FleetLeases()[0])
	root := hookLocation(ctx, Dependencies{}).workspace
	who := hookAttribution{Host: "test-host", Session: "s"}
	writeCostState(hint.NewGate(hookLocation(ctx, Dependencies{}).cacheDir, who.factsKey()), costState{Tokens: 150_000})
	deps := withSetting(string(advisoryModelTierContextCap), builtin.Deny)
	deps.ModelTiers = func() *tier.Set { return set }

	v := Judge(ctx, deps, Request{Input: filepath.Join(root, "internal/ledger/store.go"), IsPath: true, Session: "s", Host: "test-host", Lease: "lease-a"})

	require.Equal(t, verdict.Judgment{Decision: verdict.Deny, Rule: string(advisoryModelTierContextCap)}, judgedAs(v))
	assert.Contains(t, v.Reason, "cannot attribute the session context to a worker")
}

func TestContextCapFollowsTheCallerAcrossLeaseChanges(t *testing.T) {
	set := testModelTiers(t, "claude-code")
	ctx := tierFixture(t, set, fixture.FleetLeases()[0])
	at := hookLocation(ctx, Dependencies{})
	main := hookAttribution{Host: "claude-code", Session: "s"}
	main.Transcript = reportedMainTranscript(t, main.Session)
	main = main.withTranscriptCaller(true)
	facts := hint.NewGate(at.cacheDir, main.factsKey())
	writeCostState(facts, costState{Tokens: 205_000})
	deps := withSetting(string(advisoryModelTierContextCap), builtin.Deny)
	deps.ModelTiers = func() *tier.Set { return set }

	checkMain := func(wantLease string) {
		t.Helper()
		lease, _ := resolveActingLease(main, at, "")
		require.Equal(t, wantLease, lease)
		assert.Equal(t, advice{}, adviseModelTierContext(ctx, deps, at, main, facts, lease))
	}
	checkMain("")
	bindOnExec(ctx, at, main, "./magus job exec lease-a")
	checkMain("lease-a")
	releaseOnEnd(at, main, "./magus job exit lease-a --stdin")
	checkMain("")

	worker := main
	worker.Agent = "worker"
	worker = worker.withTranscriptCaller(true)
	workerFacts := hint.NewGate(at.cacheDir, worker.factsKey())
	tokens := int64(100_000)
	writeSpawnedAgent(workerFacts, worker.Agent, spawnedAgent{ContextTokens: &tokens})
	assert.Equal(t, advice{}, adviseModelTierContext(ctx, deps, at, worker, workerFacts, "lease-a"))
	tokens++
	writeSpawnedAgent(workerFacts, worker.Agent, spawnedAgent{ContextTokens: &tokens})
	assert.Contains(t, adviseModelTierContext(ctx, deps, at, worker, workerFacts, "lease-a").Say, "caps a worker's context")
}
