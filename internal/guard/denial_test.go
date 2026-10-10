package guard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/testkit"
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
	testkit.Isolate(t)
	root := t.TempDir()
	ctx := WithLocation(t.Context(), t.TempDir(), root, root)
	const session = "8f2c6a1e-3b7d-4c55-9e0a-5d1f2b9c7e41"
	judge := func(host, transport, input string) string {
		v := Judge(ctx, strict(testDependencies()), Request{Input: input, Host: host, Form: transport})
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
	testkit.Isolate(t)
	root := t.TempDir()
	ctx := WithLocation(t.Context(), t.TempDir(), root, root)
	const session = "8f2c6a1e-3b7d-4c55-9e0a-5d1f2b9c7e41"
	head := `{"session_id":"` + session + `","transcript_path":"/Users/dev/.claude/projects/-Users-dev-repo/` +
		session + `.jsonl","cwd":"/Users/dev/repo","permission_mode":"default","hook_event_name":"PreToolUse",`
	skill := head + `"tool_name":"Skill","tool_input":{"skill":"` + multiAgentSkill.String() + `"},"tool_use_id":"toolu_01"}`
	spawn := head + `"tool_name":"Task","tool_input":{"description":"Audit the store","prompt":"Audit internal/job",` +
		`"subagent_type":"general-purpose"},"tool_use_id":"toolu_02"}`
	judge := func(transport, event string) Verdict {
		return Judge(ctx, strict(testDependencies()), Request{Input: event, Host: "claude-code", Form: transport, ReportsSkills: true})
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
		got, ref, served := shapeDeny(t.Context(), noStore, string(denyRuleWholeTree), "verdict.", "rationale.", note, nil, false)
		assert.Equal(t, "verdict."+note+"\nrationale."+see, got, "without a cache dir every firing speaks in full")
		assert.Empty(t, ref)
		assert.Nil(t, served)
	}
	got, ref, _ := shapeDeny(t.Context(), noStore, string(denyRuleWholeTree), "verdict.", "rationale.", note, nil, true)
	assert.Equal(t, "verdict."+note, got, "a preview shows what the call would, less the ref it stores nothing under")
	assert.Empty(t, ref)

	gate := hint.NewGate(t.TempDir(), "s1")
	remedy := []hint.Next{hint.NextForDenyRemedy("whole-tree", []string{"magus", "status"}, "reads it.")}
	for _, rule := range []string{"a-workspace-rule", string(advisoryPushGate)} {
		for range 2 {
			got, ref, served := shapeDeny(t.Context(), gate, rule, "verdict.", "rationale.", note, remedy, false)
			assert.Equal(t, "verdict.\nrationale.", got, "%s is not a catalogued deny, so its reason is untouched", rule)
			assert.Empty(t, ref)
			assert.Nil(t, served, "a rule with no page serves nothing")
		}
	}

	got, ref, _ = shapeDeny(t.Context(), gate, string(denyRuleWholeTree), "verdict."+note, "", note, nil, false)
	require.NotEmpty(t, ref)
	assert.Equal(t, "verdict."+note+"\nfull verdict: "+hint.NextForDenial(ref).Run, got, "a reason already carrying the note does not repeat it")
}

// TestShapeDenyKeepsTheRationaleInTheStoredVerdict pins the inline contract: the verdict,
// the nothing-ran note, at most one next command and the ref. The rationale, every next
// with its why, and the rule's page live only in the stored verdict. A reason worded as
// one string is split at its first line.
func TestShapeDenyKeepsTheRationaleInTheStoredVerdict(t *testing.T) {
	const see = "\nsee: " + ruleDocsBase + "whole-tree/"
	const note = "\nnothing ran (2 commands)"
	cacheDir := t.TempDir()
	remedy := []hint.Next{
		hint.NextForDenyRemedy(string(denyRuleWholeTree), []string{"magus", "status"}, "reads it."),
		hint.NextForDenyRemedy(string(denyRuleWholeTree), []string{"magus", "vcs", "add"}, "stages it."),
	}

	for _, tc := range []struct{ name, reason, why string }{
		{"a Why field", "verdict.", "first reason.\nsecond reason."},
		{"one string", "verdict.\nfirst reason.\nsecond reason.", ""},
	} {
		got, ref, served := shapeDeny(t.Context(), hint.NewGate(cacheDir, tc.name), string(denyRuleWholeTree), tc.reason, tc.why, note, remedy, false)
		require.NotEmpty(t, ref, tc.name)
		assert.Equal(t, "verdict."+note+"\nnext:\n  "+remedy[0].Run+"\nfull verdict: "+hint.NextForDenial(ref).Run, got, tc.name)
		stored, err := trail.ReadBlob(cacheDir, ref)
		require.NoError(t, err)
		assert.Equal(t, "verdict."+note+"\nfirst reason.\nsecond reason."+
			"\nnext:\n  "+remedy[0].Run+"\n      reads it.\n  "+remedy[1].Run+"\n      stages it."+see, string(stored), tc.name)
		assert.Equal(t, remedy, served, "every next is served, so each is pre-authorized")
	}
}

// TestShapeDenyShowsEveryLineOfAVerdictWithAWhy pins the split a rule controls: with Why
// set, the whole Deny is shown, which is how a search denial keeps the graph's answer
// beside its verdict instead of behind the ref.
func TestShapeDenyShowsEveryLineOfAVerdictWithAWhy(t *testing.T) {
	cacheDir := t.TempDir()
	got, ref, _ := shapeDeny(t.Context(), hint.NewGate(cacheDir, "s1"), string(denyRuleSymbolSearch),
		"verdict.\nIts answer (1 result):\n  a.go", "the reason.", "", nil, false)
	require.NotEmpty(t, ref)
	assert.Equal(t, "verdict.\nIts answer (1 result):\n  a.go\nfull verdict: "+hint.NextForDenial(ref).Run, got)
}

// TestShapeDenyServesTheRemedyOnEveryFiring pins the layout a remedy adds and that it is
// journaled, which is what pre-authorizes it: its command inline on every firing, and its
// why only in the stored verdict. Both firings cite the full-verdict ref, and it resolves.
func TestShapeDenyServesTheRemedyOnEveryFiring(t *testing.T) {
	const see = "\nsee: " + ruleDocsBase + "output-pipe/"
	cacheDir := t.TempDir()
	gate := hint.NewGate(cacheDir, "s1")
	remedy := []hint.Next{hint.NextForDenyRemedy(string(denyRuleOutputPipe),
		[]string{"./magus", "run", "go-build", ".", "-s"}, "-s stays quiet until something fails.")}

	full := "one line.\nnext:\n  ./magus run go-build . -s\n      -s stays quiet until something fails."
	got, ref, served := shapeDeny(t.Context(), gate, string(denyRuleOutputPipe), "one line.", "", "", remedy, false)
	require.NotEmpty(t, ref, "the first firing stores its verdict too")
	assert.Equal(t, "one line.\nnext:\n  ./magus run go-build . -s\nfull verdict: "+hint.NextForDenial(ref).Run, got)
	stored, err := trail.ReadBlob(cacheDir, ref)
	require.NoError(t, err)
	assert.Equal(t, full+see, string(stored))
	assert.Equal(t, remedy, served)
	assert.Equal(t, "deny-output-pipe", servedNextPreauthorizes(gate, "./magus run go-build . -s"))

	got, ref, served = shapeDeny(t.Context(), gate, string(denyRuleOutputPipe), "one line.", "", "", remedy, false)
	require.NotEmpty(t, ref)
	doc, _ := Rule(string(denyRuleOutputPipe))
	assert.Equal(t, "denied again [output-pipe]: "+doc.Catches+"\nnext:\n  ./magus run go-build . -s"+
		"\nfull verdict: "+hint.NextForDenial(ref).Run, got)
	assert.Equal(t, remedy, served)
	assert.Equal(t, "deny-verdict", servedNextPreauthorizes(gate, hint.NextForDenial(ref).Run))
}

// TestWholeTreeStoresTheBackendsScratchCheckout pins the scratch checkout a whole-tree deny
// names for its backend. It sits in the stored verdict, and an hg or jj user is never told
// to add a git worktree, which they cannot follow.
func TestWholeTreeStoresTheBackendsScratchCheckout(t *testing.T) {
	for command, want := range map[string]string{
		"hg purge":         "a throwaway clone",
		"jj abandon":       "a throwaway clone",
		"git reset --hard": "a throwaway `git worktree add`",
	} {
		v := Evaluate(strict(testDependencies()), command)
		require.Equal(t, denyRuleWholeTree, v.Rule.Name, command)
		cacheDir := t.TempDir()
		shown, _, _ := shapeDeny(t.Context(), hint.NewGate(cacheDir, "s1"), v.RuleName(), v.Deny, v.Why, "", nil, false)
		stored := storedVerdict(t, cacheDir, shown)
		assert.Contains(t, shown, "destroys uncommitted work", command)
		assert.NotContains(t, shown, "throwaway", "the scratch checkout is rationale: %s", command)
		assert.Contains(t, stored, want, command)
		if !strings.HasPrefix(command, "git ") {
			assert.NotContains(t, stored, "git worktree add", command)
		}
	}
}

// TestShapeAdvisoryStoresTheRationale pins an advisory's two forms: its verdict inline with
// the ref, and the rationale and the rule's page behind that ref. An advisory with nothing
// stored behind it, a brief or a joined notice with no rationale, prints as it is.
func TestShapeAdvisoryStoresTheRationale(t *testing.T) {
	const see = "\nsee: " + ruleDocsBase + "push-gate/"
	cacheDir := t.TempDir()
	gate := hint.NewGate(cacheDir, "s1")
	whys := map[string]string{"run the gate.": "it reaches every project."}

	got, ref := shapeAdvice(t.Context(), gate, string(advisoryPushGate), "run the gate.\n\nan aside.", whys, false)
	require.NotEmpty(t, ref)
	assert.Equal(t, "run the gate.\n\nan aside.\nfull advice: "+hint.NextForDenial(ref).Run, got)
	stored, err := trail.ReadBlob(cacheDir, ref)
	require.NoError(t, err)
	assert.Equal(t, "run the gate.\nit reaches every project.\n\nan aside."+see, string(stored))
	assert.Equal(t, adviceVerdictID, servedNextPreauthorizes(gate, hint.NextForDenial(ref).Run))

	got, ref = shapeAdvice(t.Context(), gate, string(advisoryPushGate), "a brief.", whys, false)
	assert.Equal(t, "a brief.", got, "no rationale, nothing to store")
	assert.Empty(t, ref)

	got, ref = shapeAdvice(t.Context(), gate, string(advisoryPushGate), "run the gate.", whys, true)
	assert.Equal(t, "run the gate.", got, "a preview stores nothing, so it cites nothing")
	assert.Empty(t, ref)

	got, ref = shapeAdvice(t.Context(), hint.Gate{}, string(advisoryPushGate), "run the gate.", whys, false)
	assert.Equal(t, "run the gate.\nit reaches every project."+see, got, "with nowhere to store it, the advisory speaks in full")
	assert.Empty(t, ref)
}
