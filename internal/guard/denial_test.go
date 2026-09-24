package guard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/hint"
)

// claudeCodeBash is a Claude Code PreToolUse event for a Bash call, in the shape the
// host documents: https://code.claude.com/docs/en/hooks
func claudeCodeBash(session, command string) string {
	return `{"session_id":"` + session + `","prompt_id":"550e8400-e29b-41d4-a716-446655440000",` +
		`"transcript_path":"/Users/dev/.claude/projects/-Users-dev-repo/` + session + `.jsonl",` +
		`"cwd":"/Users/dev/repo","permission_mode":"default","hook_event_name":"PreToolUse",` +
		`"tool_name":"Bash","tool_input":{"command":"` + command + `","description":"Stash local changes",` +
		`"timeout":120000,"run_in_background":false},"tool_use_id":"toolu_01ABC123"}`
}

// TestRepeatedDenyIsKeptPerCaller pins who a deny's full text is spent on: the installed
// hook that called, named by host and transport, inside one session. The session id alone
// shortened a rule for a second hook form that had never been told it, and let two hosts
// that happen to present the same id spend each other's firings.
func TestRepeatedDenyIsKeptPerCaller(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	ctx := WithLocation(t.Context(), t.TempDir(), root, root)
	const session = "8f2c6a1e-3b7d-4c55-9e0a-5d1f2b9c7e41"
	judge := func(host, transport, input string) string {
		v := Judge(ctx, testDependencies(), Request{Input: input, Host: host, Form: transport})
		require.Equal(t, "deny", v.Decision, "%s/%s", host, transport)
		return v.Reason
	}
	short := func(reason string) bool { return strings.HasPrefix(reason, "denied again [whole-tree]: ") }

	event := claudeCodeBash(session, "git stash")
	assert.False(t, short(judge("claude-code", "sh", event)), "the first firing is in full")
	assert.True(t, short(judge("claude-code", "sh", event)), "the same caller gets the short repeat")
	assert.False(t, short(judge("claude-code", "buzz", event)),
		"the Buzz form in the same session is another caller and has not been told")
	assert.True(t, short(judge("claude-code", "buzz", event)))

	// Codex reports session_id in the same field: https://learn.chatgpt.com/docs/hooks
	codex := `{"session_id":"` + session + `","turn_id":"turn-1","hook_event_name":"PreToolUse",` +
		`"tool_name":"Bash","tool_use_id":"call_7","tool_input":{"command":"git stash"},` +
		`"permission_mode":"default","cwd":"/Users/dev/repo","model":"gpt-5-codex"}`
	assert.False(t, short(judge("codex", "sh", codex)),
		"another host presenting the same session id shares no markers")
}

// TestSessionFactsAreSharedAcrossTransports pins the other half of the split: a skill load
// is a fact about the session, not text a caller was shown. A session whose Skill matcher
// runs the Buzz port and whose Task matcher runs the sh copy has read the brief once, and
// a spawn through either form must see it.
func TestSessionFactsAreSharedAcrossTransports(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	ctx := WithLocation(t.Context(), t.TempDir(), root, root)
	const session = "8f2c6a1e-3b7d-4c55-9e0a-5d1f2b9c7e41"
	head := `{"session_id":"` + session + `","transcript_path":"/Users/dev/.claude/projects/-Users-dev-repo/` +
		session + `.jsonl","cwd":"/Users/dev/repo","permission_mode":"default","hook_event_name":"PreToolUse",`
	skill := head + `"tool_name":"Skill","tool_input":{"skill":"` + multiAgentSkill.String() + `"},"tool_use_id":"toolu_01"}`
	spawn := head + `"tool_name":"Task","tool_input":{"description":"Audit the store","prompt":"Audit internal/job",` +
		`"subagent_type":"general-purpose"},"tool_use_id":"toolu_02"}`
	judge := func(transport, event string) Verdict {
		return Judge(ctx, testDependencies(), Request{Input: event, Host: "claude-code", Form: transport, ObservesSkillLoads: true})
	}

	require.Equal(t, "deny", judge("sh", spawn).Decision, "fixture: an unbriefed spawn is denied")
	require.Equal(t, "pass", judge("buzz", skill).Decision)
	assert.NotEqual(t, "deny", judge("sh", spawn).Decision, "the load reported by the Buzz form clears the sh form's spawn")

	codex := hookAttribution{Host: "codex", Form: "sh", Session: session}
	assert.NotEqual(t, hookAttribution{Host: "claude-code", Session: session}.factsKey(), codex.factsKey(),
		"facts stay per host: another host presenting the same id has not read the brief")
	assert.Equal(t, "codex/"+session, codex.factsKey())
}

// TestCallerKeyEscapesTheDelimiter pins that the key cannot be forged by a part that
// carries the delimiter: without escaping, host "a/b" in transport "c" and host "a" in
// transport "b/c" would name the same caller.
func TestCallerKeyEscapesTheDelimiter(t *testing.T) {
	assert.Equal(t, "claude-code/sh/s1", hookAttribution{Host: "claude-code", Form: "sh", Session: "s1"}.callerKey())
	assert.Equal(t, "claude-code//s1", hookAttribution{Host: "claude-code", Session: " s1 "}.callerKey(),
		"a form that declares no transport is still keyed on host and session")
	assert.Empty(t, hookAttribution{Host: "claude-code", Form: "sh"}.callerKey(),
		"no session keys nothing, so the gate falls back to its anonymous window")

	forged := hookAttribution{Host: "a/b", Form: "c", Session: "s"}.callerKey()
	honest := hookAttribution{Host: "a", Form: "b/c", Session: "s"}.callerKey()
	assert.NotEqual(t, forged, honest)
	assert.Equal(t, "a%2Fb/c/s", forged)
	assert.Equal(t, "a%2Fb/s", hookAttribution{Host: "a/b", Form: "c", Session: "s"}.factsKey())
	assert.NotEqual(t,
		hookAttribution{Host: "a%2Fb", Form: "c", Session: "s"}.callerKey(), forged,
		"an already escaped part does not collide with the raw delimiter")
}

// TestShapeDenyFallsBackToTheFullReason pins every arm that must never shorten: a repeat
// with nowhere to store the verdict would cite a ref that resolves to nothing, and a rule
// the catalog does not list has no summary line or page to cite.
func TestShapeDenyFallsBackToTheFullReason(t *testing.T) {
	const see = "\nsee: " + ruleDocsBase + "whole-tree/"
	const note = "\nnothing ran (2 commands)"

	noStore := hint.NewGate("", "s1")
	for range 2 {
		got, ref := shapeDeny(t.Context(), noStore, string(denyRuleWholeTree), "why", note)
		assert.Equal(t, "why"+note+see, got, "without a cache dir every firing is the first")
		assert.Empty(t, ref)
	}

	gate := hint.NewGate(t.TempDir(), "s1")
	for _, rule := range []string{"a-workspace-rule", string(advisoryPushGate)} {
		for range 2 {
			got, ref := shapeDeny(t.Context(), gate, rule, "why", note)
			assert.Equal(t, "why", got, "%s is not a catalogued deny, so its reason is untouched", rule)
			assert.Empty(t, ref)
		}
	}

	got, _ := shapeDeny(t.Context(), gate, string(denyRuleWholeTree), "why"+note, note)
	assert.Equal(t, "why"+note+see, got, "a reason already carrying the note does not repeat it")
}
