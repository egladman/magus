package guard

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// toolEnvelope is a PreToolUse event for tool, as a catch-all hook entry forwards it whole.
func toolEnvelope(t *testing.T, session, cwd, tool string, input map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"session_id":      session,
		"cwd":             cwd,
		"tool_name":       tool,
		"tool_input":      input,
	})
	require.NoError(t, err)
	return string(raw)
}

func TestReadLineStandsForTheReadToolsWindow(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		input map[string]any
		want  string
	}{
		"no limit reads the whole file": {map[string]any{}, "cat a.go"},
		"a limit is a sed range":        {map[string]any{"offset": float64(81), "limit": float64(41)}, "sed -n 81,121p a.go"},
		"no offset starts at line one":  {map[string]any{"limit": float64(40)}, "sed -n 1,40p a.go"},
		"a limit past 300 is whole":     {map[string]any{"limit": float64(2000)}, "cat a.go"},
		"an offset alone reads to EOF":  {map[string]any{"offset": float64(50)}, "sed -n 50,$p a.go"},
		"a numeric string is a number":  {map[string]any{"offset": "10", "limit": "5"}, "sed -n 10,14p a.go"},
	} {
		assert.Equal(t, tc.want, readLine("a.go", tc.input), name)
	}
	assert.Equal(t, "cat 'my file.go'", readLine("my file.go", map[string]any{}), "the path is quoted as a shell word")
}

// A file path is a read or a write, and the shape cannot tell a delete from a read: the
// known-tool table can. A tool it does not name keeps the write judgment, the stricter one.
func TestKnownToolDecidesWhetherAPathIsReadOrWritten(t *testing.T) {
	t.Parallel()
	decode := func(tool string, input map[string]any) hookRequest {
		req, ok := decodeHookEnvelope(toolEnvelope(t, "s1", "/r", tool, input))
		require.True(t, ok, tool)
		return req
	}

	read := decode("Read", map[string]any{"file_path": "/r/a.go", "offset": float64(1), "limit": float64(40)})
	assert.False(t, read.IsPath, "a read is not a write")
	assert.Equal(t, "/r/a.go", read.Value, "the path the read reached")
	assert.Equal(t, "sed -n 1,40p /r/a.go", read.Read, "judged as the line it stands for")

	for _, tool := range []string{"Delete", "SomeoneElsesTool"} {
		req := decode(tool, map[string]any{"file_path": "/r/a.go"})
		assert.True(t, req.IsPath, "%s: a path-only call from a writer or a stranger is judged as a write", tool)
		assert.Empty(t, req.Read, tool)
	}
}

// Every tool now reaches the guard, so the table also says which carry nothing to judge on
// purpose, and the shape of their fields cannot make them something else.
func TestInertToolsCarryNothingToJudge(t *testing.T) {
	t.Parallel()
	fetch, ok := decodeHookEnvelope(toolEnvelope(t, "s1", "/r", "WebFetch", map[string]any{"url": "https://example.com", "prompt": "summarize"}))
	require.True(t, ok)
	assert.False(t, fetch.IsSpawn, "a web fetch's prompt is no spawn's brief")
	assert.True(t, fetch.NothingToJudge)
	assert.True(t, fetch.Inert, "the fast path answers it")
	assert.Empty(t, fetch.UnjudgedTool)

	skill, ok := decodeHookEnvelope(toolEnvelope(t, "s1", "/r", "Skill", map[string]any{"skill": "magus-run"}))
	require.True(t, ok)
	assert.Equal(t, "magus-run", skill.LoadedSkill, "a skill load is still recorded")
	assert.False(t, skill.Inert, "and so goes to Judge, which records it")

	stranger, ok := decodeHookEnvelope(toolEnvelope(t, "s1", "/r", "mcp__browser__click", map[string]any{"ref": "e1"}))
	require.True(t, ok)
	assert.True(t, stranger.NothingToJudge)
	assert.Equal(t, "mcp__browser__click", stranger.UnjudgedTool)
	assert.False(t, stranger.Inert)

	listing, ok := decodeHookEnvelope(toolEnvelope(t, "s1", "/r", "LS", map[string]any{"directory": "/r"}))
	require.True(t, ok)
	assert.Empty(t, listing.UnjudgedTool, "a read changes nothing, so it is not named")

	finished, ok := decodeHookEnvelope(`{"hook_event_name":"PostToolUse","session_id":"s1","tool_name":"mcp__browser__click","tool_input":{"ref":"e1"},"tool_response":"ok"}`)
	require.True(t, ok)
	assert.Empty(t, finished.UnjudgedTool, "a finished call is past stopping")
}

// The coordinator's case: a worker refused a third window with `sed -n` retried the same
// read with the host's read tool and was let through, because the refused window had been
// recorded as read. A read is judged the same whatever tool performs it.
func TestPagedReadIsJudgedTheSameWhicheverToolReadsIt(t *testing.T) {
	ctx, _ := spawnFixture(t)
	root := hookLocation(ctx, Dependencies{}).workspace
	pagedFile(t, root)
	abs := filepath.Join(root, "paged.go")
	deps := withSetting(string(denyRuleReadPaging), builtin.Deny)
	judge := func(agent, input string) types.GuardJudgment {
		return Judge(ctx, deps, Request{Input: input, Session: "paging-tools", Agent: agent, Host: "claude-code", Dir: root})
	}
	bash := func(line string) string {
		return toolEnvelope(t, "paging-tools", root, "Bash", map[string]any{"command": line})
	}
	read := func(offset, limit int) string {
		return toolEnvelope(t, "paging-tools", root, "Read", map[string]any{"file_path": abs, "offset": float64(offset), "limit": float64(limit)})
	}

	assert.NotEqual(t, "deny", judge("a1", bash("sed -n 1,40p paged.go")).Decision)
	assert.NotEqual(t, "deny", judge("a1", read(41, 40)).Decision, "a read tool's window counts with the shell's")
	refused := judge("a1", bash("sed -n 81,121p paged.go"))
	require.Equal(t, "deny", refused.Decision)
	assert.Equal(t, string(denyRuleReadPaging), refused.Rule)
	again := judge("a1", read(81, 41))
	assert.Equal(t, "deny", again.Decision, "the same window through the read tool is refused too")
	assert.Equal(t, string(denyRuleReadPaging), again.Rule)

	assert.NotEqual(t, "deny", judge("a2", read(1, 40)).Decision)
	assert.NotEqual(t, "deny", judge("a2", bash("sed -n 41,80p paged.go")).Decision)
	assert.Equal(t, "deny", judge("a2", read(81, 41)).Decision, "the third window, by the read tool")
	assert.Equal(t, "deny", judge("a2", bash("sed -n 81,121p paged.go")).Decision, "and again by the shell")
}

func TestUnjudgedToolAdvisesOnceAndDeniesWhereTheWorkspaceSaysSo(t *testing.T) {
	ctx, _ := spawnFixture(t)
	root := hookLocation(ctx, Dependencies{}).workspace
	call := func(deps Dependencies, session, tool string) types.GuardJudgment {
		return Judge(ctx, deps, Request{Input: toolEnvelope(t, session, root, tool, map[string]any{"ref": "e1"}), Host: "claude-code", Dir: root})
	}

	first := call(Dependencies{}, "unjudged-advise", "mcp__browser__click")
	assert.Equal(t, decisionAdvise, first.Decision, "the shipped default advises")
	assert.Equal(t, string(denyRuleUnjudgedTool), first.Rule)
	assert.Contains(t, first.Context, "mcp__browser__click")
	assert.Equal(t, decisionPass, call(Dependencies{}, "unjudged-advise", "mcp__browser__click").Decision, "said once a session")

	denied := call(withSetting(string(denyRuleUnjudgedTool), builtin.Deny), "unjudged-deny", "mcp__browser__click")
	assert.Equal(t, decisionDeny, denied.Decision)
	assert.Equal(t, string(denyRuleUnjudgedTool), denied.Rule)
	assert.Contains(t, denied.Reason, "mcp__browser__click")

	assert.Equal(t, decisionPass, call(withSetting(string(denyRuleUnjudgedTool), builtin.Deny), "unjudged-deny", "TodoWrite").Decision,
		"an inert tool is never named")
	assert.Equal(t, decisionPass, call(withSetting(string(denyRuleUnjudgedTool), builtin.Off), "unjudged-off", "mcp__browser__click").Decision)
}

// The read-only fast path: a call to an inert tool is answered before the rules load, which
// is the judge callback here. Anything else still reaches it.
func TestJudgeWithinAnswersAnInertCallWithoutLoadingRules(t *testing.T) {
	ctx, _ := spawnFixture(t)
	root := hookLocation(ctx, Dependencies{}).workspace
	judged := 0
	judge := func(context.Context) types.GuardJudgment {
		judged++
		return types.GuardJudgment{Decision: decisionPass}
	}
	request := func(tool string, input map[string]any) Request {
		return Request{Input: toolEnvelope(t, "fast", root, tool, input), Host: "claude-code", Form: "buzz", Session: "fast"}
	}

	v := JudgeWithin(ctx, Dependencies{}, request("TodoWrite", map[string]any{"todos": []any{}}), judge)
	assert.Equal(t, decisionPass, v.Decision)
	assert.Equal(t, 0, judged, "nothing loaded for a todo list")

	JudgeWithin(ctx, Dependencies{}, request("Skill", map[string]any{"skill": "magus-run"}), judge)
	JudgeWithin(ctx, Dependencies{}, request("mcp__browser__click", map[string]any{"ref": "e1"}), judge)
	JudgeWithin(ctx, Dependencies{}, request("Bash", map[string]any{"command": "ls"}), judge)
	assert.Equal(t, 3, judged, "a skill load, an unjudged tool and a command are judged")

	unnamed := request("TodoWrite", map[string]any{"todos": []any{}})
	unnamed.Host = ""
	JudgeWithin(ctx, Dependencies{}, unnamed, judge)
	assert.Equal(t, 4, judged, "glue that names no host is left to Judge, which refuses it")
}
