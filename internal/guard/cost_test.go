package guard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/observability/gen/mocks"
	"github.com/egladman/magus/types"
)

// A host is believed about the main thread once it has named a subagent on some call;
// until then an unleased call is the main thread by inference, and a leased one unknown.
func TestCallerThread(t *testing.T) {
	t.Parallel()
	cache := t.TempDir()
	host := hookAttribution{Host: "claude-code", Session: "s"}

	thread, known := callerThread(cache, hookAttribution{Session: "s"}, "")
	assert.Equal(t, types.AgentThreadUnknown, thread, "a call no host made")
	assert.False(t, known)

	thread, known = callerThread(cache, host, "")
	assert.Equal(t, types.AgentThreadMain, thread)
	assert.False(t, known, "inferred: this host has named no subagent yet")

	thread, known = callerThread(cache, host, "job-a")
	assert.Equal(t, types.AgentThreadUnknown, thread, "a leased call from an unlearned host")
	assert.False(t, known)

	sub := host
	sub.Agent = "a1"
	thread, known = callerThread(cache, sub, "")
	assert.Equal(t, types.AgentThreadSub, thread)
	assert.True(t, known)

	hostGate(cache, "claude-code").MarkFired(hostAgentsKind)
	thread, known = callerThread(cache, host, "job-a")
	assert.Equal(t, types.AgentThreadMain, thread, "a learned host's call naming no subagent")
	assert.True(t, known)

	other := hookAttribution{Host: "codex", Session: "s"}
	_, known = callerThread(cache, other, "")
	assert.False(t, known, "what one host taught says nothing of another")
}

func TestDelegableClass(t *testing.T) {
	t.Parallel()
	for line, want := range map[string]string{
		"rg -n Judge internal":             delegableSearch,
		"grep -rn Judge internal":          delegableSearch,
		"find . -name '*.go'":              delegableSearch,
		"grep Judge internal/guard/x.go":   "",
		"cat internal/guard/guard.go":      delegableRead,
		"sed -n 10,40p internal/a.go":      delegableRead,
		"cat AGENTS.md":                    "",
		"git diff":                         delegableDiff,
		"git diff --stat":                  "",
		"git show HEAD":                    delegableDiff,
		"git log -p -3":                    delegableDiff,
		"git log --oneline -n 20":          "",
		"git status --short":               "",
		"./magus run test internal/guard":  delegableRun,
		"./magus affected ci":              "",
		"./magus query output out1":        "",
		"./magus job wait a3-rules":        "",
		"curl -sS https://example.com/x":   delegableFetch,
		"go version":                       "",
		"cd internal && rg -l Verdict .":   delegableSearch,
		"git diff --name-only main...HEAD": "",
	} {
		assert.Equal(t, want, delegableClass(line), line)
	}
}

func TestInputClassReadsAHostToolByShape(t *testing.T) {
	t.Parallel()
	assert.Equal(t, delegableRead, inputClass(map[string]any{"file_path": "/r/internal/a.go"}))
	assert.Empty(t, inputClass(map[string]any{"file_path": "/r/AGENTS.md"}), "orientation")
	assert.Empty(t, inputClass(map[string]any{"file_path": "/r/a.go", "content": "x"}), "a write")
	assert.Empty(t, inputClass(map[string]any{"file_path": "/r/a.go", "old_string": "a", "new_string": "b"}), "an edit")
	assert.Equal(t, delegableSearch, inputClass(map[string]any{"pattern": "Judge", "path": "/r"}))
	assert.Equal(t, delegableFetch, inputClass(map[string]any{"url": "https://example.com"}))
	assert.Equal(t, delegableDiff, inputClass(map[string]any{"command": "git diff"}))
	assert.Equal(t, delegableSearch, inputClass(map[string]any{"command": []any{"rg", "Judge"}}))
}

// appendLines appends JSONL lines to the file at path.
func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	defer f.Close()
	for _, l := range lines {
		_, err := f.WriteString(l + "\n")
		require.NoError(t, err)
	}
}

func TestReadTranscriptReadsEachLineOnce(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "main.jsonl")
	big := strings.Repeat("x", 30_000)
	appendLines(t, path,
		`{"type":"assistant","message":{"model":"claude-opus-5-5","usage":{"input_tokens":10,"cache_read_input_tokens":200000,"cache_creation_input_tokens":90},"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"rg -n Judge ."}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"`+big+`"}]}}`,
		`{"type":"assistant","isSidechain":true,"message":{"model":"claude-haiku-5-5","usage":{"input_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`,
	)
	var st costState
	got := st.readTranscript(path)
	assert.Equal(t, []toolResult{{tool: "Bash", class: delegableSearch, chars: len(big)}}, got)
	assert.Equal(t, int64(200100), st.Tokens, "a subagent's line in the main transcript is not the main thread's")
	assert.Equal(t, "claude-opus-5-5", st.Model)
	assert.Empty(t, st.Pending)

	assert.Empty(t, st.readTranscript(path), "nothing new")
	appendLines(t, path,
		`{"type":"assistant","message":{"usage":{"input_tokens":1,"cache_read_input_tokens":250000,"cache_creation_input_tokens":0},"content":[{"type":"tool_use","id":"t2","name":"Read","input":{"file_path":"/r/a.go"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":[{"type":"text","text":"package a"}]}]}}`,
	)
	got = st.readTranscript(path)
	assert.Equal(t, []toolResult{{tool: "Read", class: delegableRead, chars: len("package a")}}, got)
	assert.Equal(t, int64(250001), st.Tokens)
	assert.Equal(t, "claude-opus-5-5", st.Model, "a line naming no model keeps the last one named")
}

// A rollout reports its window beside the usage, and its calls as function call items.
func TestReadTranscriptReadsARollout(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	appendLines(t, path,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":120000},"model_context_window":272000}}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"exec","call_id":"c1","arguments":"{\"cmd\":\"git diff\"}"}}`,
		`{"type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"diff --git a/x b/x"}}`,
	)
	var st costState
	got := st.readTranscript(path)
	assert.Equal(t, []toolResult{{tool: "exec", class: delegableDiff, chars: len("diff --git a/x b/x")}}, got)
	assert.Equal(t, int64(120000), st.Tokens)
	assert.Equal(t, int64(272000), st.Window)
}

func TestResolveModelTakesAnAlias(t *testing.T) {
	t.Parallel()
	table := model.Shipped()
	row, ok := resolveModel("opus", table)
	require.True(t, ok)
	assert.Equal(t, "claude-opus-5-5", row.ID)
	row, ok = resolveModel("us.anthropic.claude-sonnet-5-5-20261001", table)
	require.True(t, ok)
	assert.Equal(t, "claude-sonnet-5-5", row.ID)
	_, ok = resolveModel("gpt-9", table)
	assert.False(t, ok)
	_, ok = resolveModel("", table)
	assert.False(t, ok)
}

// The producer: a judged main-thread call files its thread, context and billing on the
// trail and counts once in the metrics; an observation of the same call counts nothing.
func TestObserveCallRecordsAJudgedCallOnce(t *testing.T) {
	ctx, cache := spawnFixture(t)
	at := hookLocation(ctx, Dependencies{})
	transcript := filepath.Join(t.TempDir(), "main.jsonl")
	appendLines(t, transcript,
		`{"type":"assistant","message":{"model":"claude-opus-5-5","usage":{"input_tokens":1,"cache_read_input_tokens":99999,"cache_creation_input_tokens":0},"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"git diff"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"diff"}]}}`,
	)
	who := hookAttribution{Host: "claude-code", Session: "s", Transcript: transcript}
	hostGate(cache, who.Host).MarkFired(hostAgentsKind)

	var calls []observability.AgentCall
	rec := mocks.NewMockProvider(t)
	rec.EXPECT().RecordAgentCall(mock.Anything, mock.Anything).Run(func(_ context.Context, c observability.AgentCall) {
		calls = append(calls, c)
	}).Return()
	ctx = observability.WithProvider(ctx, rec)

	observed := observeCall(ctx, at, "/r/a.go", who, hookToolRead, "", Verdict{})
	assert.Equal(t, types.AgentThreadMain, observed.thread)
	assert.Nil(t, observed.context, "an observation records the thread alone")
	assert.Empty(t, calls, "and counts nothing")

	got := observeCall(ctx, at, "rg -n Judge .", who, hookToolCommand, "", Verdict{Decision: decisionPass})
	assert.Equal(t, types.AgentThreadMain, got.thread)
	assert.Equal(t, delegableSearch, got.class)
	require.NotNil(t, got.context)
	assert.Equal(t, int64(100000), got.context.Tokens)
	assert.Equal(t, int64(1_000_000), got.context.Window, "from the model table")
	assert.Len(t, got.shadow, len(types.GuardProfile("").Values()))
	require.Len(t, calls, 2, "the call, and the diff result the transcript handed back")
	assert.Equal(t, int64(100000), calls[0].ContextTokens)
	assert.Equal(t, delegableSearch, calls[0].DelegableClass)
	assert.Zero(t, calls[0].ResultChars, "a call's own result is not known before it runs")
	assert.Equal(t, observability.AgentCall{Thread: types.AgentThreadMain, DelegableClass: delegableDiff, ResultChars: 4}, calls[1])

	st := readCostState(hint.NewGate(cache, who.factsKey()))
	assert.Equal(t, 1, st.Streak)
	assert.Equal(t, 1, st.Repeats[lineDigest("rg -n Judge .")])
	assert.Equal(t, 4, st.Results["Bash"])

	observeCall(ctx, at, "rg -n Judge .", who, hookToolCommand, "", Verdict{Decision: decisionDeny})
	assert.Equal(t, 1, readCostState(hint.NewGate(cache, who.factsKey())).Streak, "a refused call never ran")
	observeCall(ctx, at, "/r/a.go", who, hookToolWrite, "", Verdict{Decision: decisionPass})
	assert.Empty(t, readCostState(hint.NewGate(cache, who.factsKey())).Repeats, "a write starts the repeats over")
	noteMainSpawn(at, who, "")
	assert.Zero(t, readCostState(hint.NewGate(cache, who.factsKey())).Streak, "a spawn starts the streak over")
}

// The request a workspace rule is handed carries the caller's thread and context.
func TestCommandRequestCarriesTheThread(t *testing.T) {
	ctx, cache := spawnFixture(t)
	at := hookLocation(ctx, Dependencies{})
	who := hookAttribution{Host: "claude-code", Session: "s"}
	facts := hint.NewGate(cache, who.factsKey())
	writeCostState(facts, costState{Tokens: 300_000, Model: "claude-sonnet-5-5"})

	req := commandRequest(ctx, commandRuleInput{command: "git diff"}, who, at, facts)
	assert.Equal(t, types.AgentThreadMain, req.Thread)
	assert.False(t, req.ThreadKnown, "inferred until the host names a subagent")
	assert.Equal(t, &types.AgentContext{Tokens: 300_000, Window: 1_000_000, Model: "claude-sonnet-5-5", Source: "transcript"}, req.Context)

	who.Agent = "a1"
	req = commandRequest(ctx, commandRuleInput{command: "git diff"}, who, at, facts)
	assert.Equal(t, types.AgentThreadSub, req.Thread)
	assert.True(t, req.ThreadKnown)
	assert.Nil(t, req.Context, "no usage was reported for the subagent")
}
