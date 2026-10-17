package guard

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testToolRows are the rows the tests' host declares: a subset of a real host's table.
var testToolRows = []agent.HarnessTool{
	{Name: "Bash", Capability: agent.ToolRuns},
	{Name: "Monitor", Capability: agent.ToolRuns},
	{Name: "Read", Capability: agent.ToolReads},
	{Name: "Glob", Capability: agent.ToolReads},
	{Name: "Grep", Capability: agent.ToolReads},
	{Name: "LS", Capability: agent.ToolReads},
	{Name: "Write", Capability: agent.ToolWrites},
	{Name: "Edit", Capability: agent.ToolWrites},
	{Name: "Delete", Capability: agent.ToolWrites},
	{Name: "apply_patch", Capability: agent.ToolWrites},
	{Name: "Agent", Capability: agent.ToolSpawns},
	{Name: "Task", Capability: agent.ToolSpawns},
	{Name: "spawn_agent", Capability: agent.ToolSpawns},
	{Name: "mcp__browser__navigate", Capability: agent.ToolNetwork},
	{Name: "TodoWrite", Capability: agent.ToolInert},
	{Name: "WebFetch", Capability: agent.ToolInert},
	{Name: "Skill", Capability: agent.ToolInert},
}

// testTools installs testToolRows the way `magus agent harness install` does and reads them
// back the way `magus shell` does.
func testTools(t testing.TB) agent.ToolRows {
	t.Helper()
	root := t.TempDir()
	for _, f := range (agent.HarnessTools{Dir: "tools", Rows: testToolRows}).Files() {
		path := filepath.Join(root, f.Path)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(f.Content), 0o644))
	}
	rows := agent.ReadToolRows(root, "tools")
	require.NoError(t, rows.Err)
	return rows
}

// decodeTestEnvelope is decodeHookEnvelope under testToolRows.
func decodeTestEnvelope(t testing.TB, raw string) (hookRequest, bool) {
	t.Helper()
	return decodeHookEnvelope(raw, testTools(t))
}

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
// host's tool rows can. A tool they do not list keeps the write judgment, the stricter one.
func TestToolRowsDecideWhetherAPathIsReadOrWritten(t *testing.T) {
	t.Parallel()
	decode := func(tool string, input map[string]any) hookRequest {
		req, ok := decodeTestEnvelope(t, toolEnvelope(t, "s1", "/r", tool, input))
		require.True(t, ok, tool)
		return req
	}

	read := decode("Read", map[string]any{"file_path": "/r/a.go", "offset": float64(1), "limit": float64(40)})
	assert.Equal(t, hookRequest{Value: "/r/a.go", Cwd: "/r", Read: "sed -n 1,40p /r/a.go", Who: hookAttribution{Session: "s1", Event: "PreToolUse"}}, read,
		"a read is not a write: it reaches the path, judged as the line it stands for")

	for _, tool := range []string{"Delete", "SomeoneElsesTool"} {
		req := decode(tool, map[string]any{"file_path": "/r/a.go"})
		assert.True(t, req.IsPath, "%s: a path-only call from a writer or a stranger is judged as a write", tool)
		assert.Empty(t, req.Read, tool)
	}
}

// Every tool reaches the guard, so the rows also say which carry nothing to judge on
// purpose, and the shape of their fields cannot make them something else.
func TestInertToolsCarryNothingToJudge(t *testing.T) {
	t.Parallel()
	fetch, ok := decodeTestEnvelope(t, toolEnvelope(t, "s1", "/r", "WebFetch", map[string]any{"url": "https://example.com", "prompt": "summarize"}))
	require.True(t, ok)
	assert.False(t, fetch.IsSpawn, "a web fetch's prompt is no spawn's brief")
	assert.True(t, fetch.NothingToJudge)
	assert.True(t, fetch.Inert, "the fast path answers it")
	assert.Empty(t, fetch.UnjudgedTool)

	skill, ok := decodeTestEnvelope(t, toolEnvelope(t, "s1", "/r", "Skill", map[string]any{"skill": "magus-run"}))
	require.True(t, ok)
	assert.Equal(t, "magus-run", skill.LoadedSkill, "a skill load is still recorded")
	assert.False(t, skill.Inert, "and so goes to Judge, which records it")

	stranger, ok := decodeTestEnvelope(t, toolEnvelope(t, "s1", "/r", "mcp__browser__click", map[string]any{"ref": "e1"}))
	require.True(t, ok)
	assert.True(t, stranger.NothingToJudge)
	assert.Equal(t, "mcp__browser__click", stranger.UnjudgedTool)
	assert.False(t, stranger.Inert)

	browse, ok := decodeTestEnvelope(t, toolEnvelope(t, "s1", "/r", "mcp__browser__navigate", map[string]any{"url": "https://example.com"}))
	require.True(t, ok)
	assert.Empty(t, browse.UnjudgedTool, "a listed network tool is known")
	assert.False(t, browse.Inert, "and only inert tools take the fast path")

	listing, ok := decodeTestEnvelope(t, toolEnvelope(t, "s1", "/r", "LS", map[string]any{"directory": "/r"}))
	require.True(t, ok)
	assert.Empty(t, listing.UnjudgedTool, "a read changes nothing, so it is not named")

	finished, ok := decodeTestEnvelope(t, `{"hook_event_name":"PostToolUse","session_id":"s1","tool_name":"mcp__browser__click","tool_input":{"ref":"e1"},"tool_response":"ok"}`)
	require.True(t, ok)
	assert.Empty(t, finished.UnjudgedTool, "a finished call is past stopping")
}

// A wiring that names no rows directory, or one that was never installed, knows no tool:
// nothing is inert, and the refusal names the install that writes the rows.
func TestMissingToolRowsKnowNoToolAndSaySo(t *testing.T) {
	t.Parallel()
	for name, rows := range map[string]agent.ToolRows{
		"no directory named": agent.ReadToolRows(t.TempDir(), ""),
		"never installed":    agent.ReadToolRows(t.TempDir(), "tools"),
	} {
		require.Error(t, rows.Err, name)
		req, ok := decodeHookEnvelope(toolEnvelope(t, "s1", "/r", "TodoWrite", map[string]any{"todos": []any{}}), rows)
		require.True(t, ok, name)
		assert.False(t, req.Inert, name)
		assert.Equal(t, "TodoWrite", req.UnjudgedTool, name)
		assert.Contains(t, denyUnjudgedTool("TodoWrite", rows).Say, "magus agent harness install", name)
	}
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
	tools := testTools(t)
	judge := func(subagent, input string) types.GuardJudgment {
		return Judge(ctx, deps, Request{Input: input, Session: "paging-tools", Agent: subagent, Host: "claude-code", Dir: root, Tools: tools})
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
	refusedShape, _ := spoken(refused)
	require.Equal(t, types.GuardJudgment{Decision: "deny", Rule: string(denyRuleReadPaging)}, refusedShape)
	again, _ := spoken(judge("a1", read(81, 41)))
	assert.Equal(t, refusedShape, again, "the same window through the read tool is refused too")

	assert.NotEqual(t, "deny", judge("a2", read(1, 40)).Decision)
	assert.NotEqual(t, "deny", judge("a2", bash("sed -n 41,80p paged.go")).Decision)
	assert.Equal(t, "deny", judge("a2", read(81, 41)).Decision, "the third window, by the read tool")
	assert.Equal(t, "deny", judge("a2", bash("sed -n 81,121p paged.go")).Decision, "and again by the shell")
}

func TestUnjudgedToolAdvisesOnceAndDeniesWhereTheWorkspaceSaysSo(t *testing.T) {
	ctx, _ := spawnFixture(t)
	root := hookLocation(ctx, Dependencies{}).workspace
	tools := testTools(t)
	call := func(deps Dependencies, session, tool string) types.GuardJudgment {
		return Judge(ctx, deps, Request{Input: toolEnvelope(t, session, root, tool, map[string]any{"ref": "e1"}), Host: "claude-code", Dir: root, Tools: tools})
	}

	first := call(Dependencies{}, "unjudged-advise", "mcp__browser__click")
	firstShape, firstText := spoken(first)
	assert.Equal(t, types.GuardJudgment{Decision: decisionAdvise, Rule: string(denyRuleUnjudgedTool)}, firstShape, "the shipped default advises")
	assert.Contains(t, firstText, "mcp__browser__click")
	assert.Equal(t, decisionPass, call(Dependencies{}, "unjudged-advise", "mcp__browser__click").Decision, "said once a session")

	denied := call(withSetting(string(denyRuleUnjudgedTool), builtin.Deny), "unjudged-deny", "mcp__browser__click")
	deniedShape, deniedText := spoken(denied)
	assert.Equal(t, types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRuleUnjudgedTool)}, deniedShape)
	assert.Contains(t, deniedText, "mcp__browser__click")

	assert.Equal(t, decisionPass, call(withSetting(string(denyRuleUnjudgedTool), builtin.Deny), "unjudged-deny", "TodoWrite").Decision,
		"an inert tool is never named")
	assert.Equal(t, decisionPass, call(withSetting(string(denyRuleUnjudgedTool), builtin.Off), "unjudged-off", "mcp__browser__click").Decision)
}

// The read-only fast path: a call to an inert tool is answered before the rules load, which
// is the judge callback here. Anything else still reaches it.
func TestJudgeWithinAnswersAnInertCallWithoutLoadingRules(t *testing.T) {
	ctx, _ := spawnFixture(t)
	root := hookLocation(ctx, Dependencies{}).workspace
	tools := testTools(t)
	judged := 0
	judge := func(context.Context) types.GuardJudgment {
		judged++
		return types.GuardJudgment{Decision: decisionPass}
	}
	request := func(tool string, input map[string]any) Request {
		return Request{Input: toolEnvelope(t, "fast", root, tool, input), Host: "claude-code", Form: "buzz", Session: "fast", Tools: tools}
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

	norows := request("TodoWrite", map[string]any{"todos": []any{}})
	norows.Tools = agent.ToolRows{}
	JudgeWithin(ctx, Dependencies{}, norows, judge)
	assert.Equal(t, 5, judged, "with no rows nothing is known to be inert")
}
