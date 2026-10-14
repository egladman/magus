package guard

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
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

	thread, from := callerThread(cache, hookAttribution{Session: "s"}, "")
	assert.Equal(t, types.AgentThreadUnknown, thread, "a call no host made")
	assert.Equal(t, types.ThreadInferred, from)

	thread, from = callerThread(cache, host, "")
	assert.Equal(t, types.AgentThreadMain, thread)
	assert.Equal(t, types.ThreadInferred, from, "this host has named no subagent yet")

	thread, from = callerThread(cache, host, "job-a")
	assert.Equal(t, types.AgentThreadUnknown, thread, "a leased call from an unlearned host")
	assert.Equal(t, types.ThreadInferred, from)

	sub := host
	sub.Agent = "a1"
	thread, from = callerThread(cache, sub, "")
	assert.Equal(t, types.AgentThreadSub, thread)
	assert.Equal(t, types.ThreadReported, from)

	hostGate(cache, "claude-code").MarkFired(hostAgentsKind)
	thread, from = callerThread(cache, host, "job-a")
	assert.Equal(t, types.AgentThreadMain, thread, "a learned host's call naming no subagent")
	assert.Equal(t, types.ThreadReported, from)

	other := hookAttribution{Host: "codex", Session: "s"}
	_, from = callerThread(cache, other, "")
	assert.Equal(t, types.ThreadInferred, from, "what one host taught says nothing of another")
}

func TestDelegableClass(t *testing.T) {
	t.Parallel()
	for line, want := range map[string]types.DelegableClass{
		"rg -n Judge internal":                 types.DelegableSearch,
		"grep -rn Judge internal":              types.DelegableSearch,
		"find . -name '*.go'":                  types.DelegableSearch,
		"grep Judge internal/guard/x.go":       "",
		"cat internal/guard/guard.go":          types.DelegableRead,
		"sed -n 10,40p internal/a.go":          types.DelegableRead,
		"cat AGENTS.md":                        "",
		"git diff":                             types.DelegableDiff,
		"git diff --stat":                      "",
		"git show HEAD":                        types.DelegableDiff,
		"git log -p -3":                        types.DelegableDiff,
		"git log --oneline -n 20":              "",
		"git status --short":                   "",
		"./magus run test internal/guard":      types.DelegableRun,
		"./magus affected ci":                  "",
		"./magus query output out1":            types.DelegableRead,
		"./magus query output out1 --identity": "",
		"./magus job wait a3-rules":            "",
		"./magus run go-build . -s":            "",
		"./magus run go-build .":               "",
		"./magus run test internal/guard -s":   "",
		"git commit -m x":                      "",
		"curl -sS https://example.com/x":       types.DelegableFetch,
		"go version":                           "",
		"cd internal && rg -l Verdict .":       types.DelegableSearch,
		"git diff --name-only main...HEAD":     "",
	} {
		assert.Equal(t, want, delegableClass(line), line)
	}
}

// Each line prints a ref or one line, so no cost rule that keeps output out of the main
// thread's context has anything to judge.
var costFreeLines = []string{
	"./magus run go-build . -s",
	"./magus run go-build . --silent",
	"./magus run go-build .",
	"./magus run test internal/guard -s",
	"./magus affected ci --silent",
	"./magus affected ci -s",
	"cd /r && ./magus run go-build . -s",
	"GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .",
	"./magus job fork a1",
	"./magus job exit a1 --stdin",
	"./magus job apply a1",
	"./magus job wait a1",
	"./magus describe job a1 -o name",
	"./magus describe job a1 -o=name",
	"./magus describe job a1 --output name",
	"./magus describe job a1 -o template='{{.State}}'",
	"./magus ls -o name",
	"./magus query output out1 --identity",
	"git status --short",
	"git status -sb",
	"git log --oneline -n 5",
	"git log --oneline -5",
	"git log --oneline --max-count=5",
	"git cherry-pick abc123",
	"git commit -m 'x'",
	"git branch root-allow",
}

// Each line can print a lot, or is not one of the quiet ones, and stays judged.
var costBoundLines = map[string]types.DelegableClass{
	"cat internal/guard/guard.go":                 types.DelegableRead,
	"sed -n 1,400p internal/guard/guard.go":       types.DelegableRead,
	"rg -n Judge internal":                        types.DelegableSearch,
	"grep -rn Judge internal":                     types.DelegableSearch,
	"find . -name '*.go'":                         types.DelegableSearch,
	"git diff":                                    types.DelegableDiff,
	"git show HEAD":                               types.DelegableDiff,
	"git log -p -3":                               types.DelegableDiff,
	"git log --oneline -p -n 3":                   types.DelegableDiff,
	"./magus run test internal/guard":             types.DelegableRun,
	"./magus run test internal/guard -- -s":       types.DelegableRun,
	"./magus run test internal/guard -- --silent": types.DelegableRun,
	"./magus run test internal/guard -s=false":    types.DelegableRun,
	"./magus affected ci --silent=false":          "",
	"./magus query output out1":                   types.DelegableRead,
	"./magus describe job a1 -o json":             "",
	"./magus describe job a1":                     "",
	"git status":                                  "",
	"git log --oneline":                           "",
	"cd /r && rg -n Judge internal":               types.DelegableSearch,
	"./magus run go-build . -s && rg -n Judge .":  types.DelegableSearch,
	"curl -sS https://example.com/x":              types.DelegableFetch,
}

func TestCostFree(t *testing.T) {
	t.Parallel()
	for _, line := range costFreeLines {
		assert.True(t, costFree(line), line)
		assert.Empty(t, delegableClass(line), line)
	}
	for line, class := range costBoundLines {
		assert.False(t, costFree(line), line)
		assert.Equal(t, class, delegableClass(line), line)
	}
	assert.False(t, costFree("cd /r"), "a line that only changes directory is not a quiet command")
	assert.False(t, costFree(""))
}

func TestInputClassOfAFreeCommandIsNone(t *testing.T) {
	t.Parallel()
	assert.Empty(t, inputClass(map[string]any{"command": "./magus run go-build . -s"}))
	assert.Equal(t, types.DelegableRead, inputClass(map[string]any{"command": "./magus query output out1"}))
}

func TestInputClassReadsAHostToolByShape(t *testing.T) {
	t.Parallel()
	assert.Equal(t, types.DelegableRead, inputClass(map[string]any{"file_path": "/r/internal/a.go"}))
	assert.Empty(t, inputClass(map[string]any{"file_path": "/r/AGENTS.md"}), "orientation")
	assert.Empty(t, inputClass(map[string]any{"file_path": "/r/a.go", "content": "x"}), "a write")
	assert.Empty(t, inputClass(map[string]any{"file_path": "/r/a.go", "old_string": "a", "new_string": "b"}), "an edit")
	assert.Equal(t, types.DelegableSearch, inputClass(map[string]any{"pattern": "Judge", "path": "/r"}))
	assert.Equal(t, types.DelegableFetch, inputClass(map[string]any{"url": "https://example.com"}))
	assert.Equal(t, types.DelegableDiff, inputClass(map[string]any{"command": "git diff"}))
	assert.Equal(t, types.DelegableSearch, inputClass(map[string]any{"command": []any{"rg", "Judge"}}))
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
	assert.Equal(t, []toolResult{{tool: "Bash", class: types.DelegableSearch, chars: len(big)}}, got)
	assert.Equal(t, int64(200100), st.Tokens, "a subagent's line in the main transcript is not the main thread's")
	assert.Equal(t, "claude-opus-5-5", st.Model)
	assert.Empty(t, st.Pending)

	assert.Empty(t, st.readTranscript(path), "nothing new")
	appendLines(t, path,
		`{"type":"assistant","message":{"usage":{"input_tokens":1,"cache_read_input_tokens":250000,"cache_creation_input_tokens":0},"content":[{"type":"tool_use","id":"t2","name":"Read","input":{"file_path":"/r/a.go"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":[{"type":"text","text":"package a"}]}]}}`,
	)
	got = st.readTranscript(path)
	assert.Equal(t, []toolResult{{tool: "Read", class: types.DelegableRead, chars: len("package a")}}, got)
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
	assert.Equal(t, []toolResult{{tool: "exec", class: types.DelegableDiff, chars: len("diff --git a/x b/x")}}, got)
	assert.Equal(t, int64(120000), st.Tokens)
	assert.Equal(t, int64(272000), st.Window)
}

// fixtureModelRows stand in for the rows a harness spell publishes; no table is built into
// the guard.
var fixtureModelRows = []types.ModelPricing{
	{ID: "claude-fable-5-1", Window: 1_000_000, Input: 10, CacheWrite5m: 12.50, CacheWrite1h: 20, CacheRead: 0.25, Output: 50},
	{ID: "claude-opus-5-5", Window: 1_000_000, Input: 4, CacheWrite5m: 5, CacheWrite1h: 8, CacheRead: 0.20, Output: 20},
	{ID: "claude-sonnet-5-5", Window: 1_000_000, Input: 2, CacheWrite5m: 2.50, CacheWrite1h: 4, CacheRead: 0.10, Output: 10},
	{ID: "claude-haiku-5-5", Window: 1_000_000, Input: 0.10, CacheWrite5m: 0.125, CacheWrite1h: 0.20, CacheRead: 0.01, Output: 0.50,
		LongContext: &types.PriceStep{Above: 100_000, Multiplier: 5}},
	{ID: "claude-haiku-4-5", Window: 200_000, Input: 1, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.10, Output: 5},
}

func fixtureModelTable() model.Table { return model.InEffect(fixtureModelRows, nil) }

func fixtureModelSource() []types.ModelPricing { return fixtureModelRows }

// The harness rows cost a workspace load, so billing leaves them unread; agents.models
// then overrides them field by field.
func TestModelTableReadsHarnessRowsOnlyForTheTableAndUnderAgentsModels(t *testing.T) {
	ctx, _ := spawnFixture(t)
	loads := 0
	at := hookLocation(ctx, Dependencies{ModelRows: func() []types.ModelPricing {
		loads++
		return fixtureModelRows
	}})
	require.NoError(t, os.WriteFile(filepath.Join(at.workspace, "magus.yaml"),
		[]byte("agents:\n  billing: metered\n  models:\n    claude-opus-5-5: {window: 1000000, input: 3}\n"), 0o600))

	assert.Equal(t, types.AgentBillingMetered, accountAt(at.workspace).billing)
	assert.Zero(t, loads, "billing alone never loads the harness rows")
	opus, ok := at.modelTable().Lookup("claude-opus-5-5")
	require.True(t, ok)
	assert.Equal(t, 1, loads)
	assert.Equal(t, types.ModelPricing{ID: "claude-opus-5-5", Window: 1_000_000, Input: 3, CacheWrite5m: 5, CacheWrite1h: 8, CacheRead: 0.20, Output: 20},
		opus, "agents.models wins, and a field it leaves 0 keeps the harness row")
	_, ok = at.modelTable().Lookup("claude-haiku-5-5")
	assert.True(t, ok, "rows agents.models does not mention stay")
}

// The account is cached per workspace root, and the harness rows are not part of it: each
// judgment reads its own request's rows, so a cached account never holds a request's
// closure or the ctx it captured.
func TestModelTableReadsEachRequestsOwnRows(t *testing.T) {
	ctx, _ := spawnFixture(t)
	first := hookLocation(ctx, Dependencies{ModelRows: fixtureModelSource})
	_, ok := first.modelTable().Lookup("claude-opus-5-5")
	require.True(t, ok)

	second := hookLocation(ctx, Dependencies{})

	require.Equal(t, first.workspace, second.workspace)
	_, ok = second.modelTable().Lookup("claude-opus-5-5")
	assert.False(t, ok, "a request supplying no rows knows no model, whatever an earlier one supplied")
}

func TestResolveModelTakesAnAlias(t *testing.T) {
	t.Parallel()
	table := fixtureModelTable()
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
	at := hookLocation(ctx, Dependencies{ModelRows: fixtureModelSource})
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

	observed := observeCall(ctx, at, "/r/a.go", who, hookToolRead, "", types.GuardJudgment{})
	assert.Equal(t, types.AgentFacts{Thread: types.AgentThreadMain, ThreadFrom: types.ThreadReported}, observed, "an observation records the thread alone")
	assert.Empty(t, calls, "and counts nothing")

	got := observeCall(ctx, at, "rg -n Judge .", who, hookToolCommand, "", types.GuardJudgment{Decision: decisionPass})
	assert.Equal(t, types.AgentThreadMain, got.Thread)
	assert.Equal(t, types.ThreadReported, got.ThreadFrom)
	assert.Equal(t, types.DelegableSearch, got.DelegableClass)
	assert.Equal(t, int64(100000), got.ContextTokens)
	assert.Equal(t, "claude-opus-5-5", got.ContextModel)
	assert.Nil(t, got.ResultChars, "a call's own result is not known before it runs")
	assert.Len(t, got.Shadow, len(types.GuardProfile("").Values()))
	window := callerContext(at, who, types.AgentThreadMain, hint.NewGate(cache, who.factsKey()))
	require.NotNil(t, window)
	assert.Equal(t, int64(1_000_000), window.Window, "from the harness rows")
	require.Len(t, calls, 2, "the call, and the diff result the transcript handed back")
	assert.Equal(t, observability.AgentCall{AgentFacts: got}, calls[0])
	four := 4
	assert.Equal(t, observability.AgentCall{AgentFacts: types.AgentFacts{Thread: types.AgentThreadMain, DelegableClass: types.DelegableDiff, ResultChars: &four}}, calls[1])

	st := readCostState(hint.NewGate(cache, who.factsKey()))
	assert.Equal(t, 1, st.Streak)
	assert.Equal(t, 1, st.Repeats[lineDigest("rg -n Judge .")])
	assert.Equal(t, 4, st.Results["Bash"])

	observeCall(ctx, at, "rg -n Judge .", who, hookToolCommand, "", types.GuardJudgment{Decision: decisionDeny})
	assert.Equal(t, 1, readCostState(hint.NewGate(cache, who.factsKey())).Streak, "a refused call never ran")
	observeCall(ctx, at, "/r/a.go", who, hookToolWrite, "", types.GuardJudgment{Decision: decisionPass})
	assert.Empty(t, readCostState(hint.NewGate(cache, who.factsKey())).Repeats, "a write starts the repeats over")
	noteMainSpawn(at, who, "", false)
	assert.Zero(t, readCostState(hint.NewGate(cache, who.factsKey())).Streak, "a spawn starts the streak over")
}

// Each record a session leaves proves or suggests that it orchestrates; proof counts only
// on a main thread its host reported, and the record only rises.
func TestOrchestrationByEvidence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		st   costState
		from types.ThreadSource
		want types.Orchestration
	}{
		{"no record", costState{Streak: 9}, types.ThreadReported, ""},
		{"a spawn bound to no row", costState{Orchestrates: types.OrchestrationInferred}, types.ThreadReported, types.OrchestrationInferred},
		{"a bound spawn or a confirmed fork", costState{Orchestrates: types.OrchestrationProven}, types.ThreadReported, types.OrchestrationProven},
		{"proof on a thread the host did not report", costState{Orchestrates: types.OrchestrationProven}, types.ThreadInferred, types.OrchestrationInferred},
		{"a fork the store has not shown", costState{Forks: map[string]int64{"a1": 1}}, types.ThreadReported, ""},
	} {
		assert.Equal(t, tc.want, orchestration(tc.st, tc.from), tc.name)
	}

	st := costState{Orchestrates: types.OrchestrationProven}
	st.raise(types.OrchestrationInferred)
	assert.Equal(t, types.OrchestrationProven, st.Orchestrates, "proof is never lowered")
}

// A spawn bound to a live row proves the session orchestrates, one bound to none suggests
// it, and either starts the streak over. A leased caller and a subagent record nothing.
func TestNoteMainSpawnRecordsOrchestration(t *testing.T) {
	ctx, cache := spawnFixture(t)
	at := hookLocation(ctx, Dependencies{})
	who := hookAttribution{Host: "claude-code", Session: "s"}
	hostGate(cache, who.Host).MarkFired(hostAgentsKind)
	facts := hint.NewGate(cache, who.factsKey())

	noteMainSpawn(at, who, "", false)
	assert.Equal(t, types.OrchestrationInferred, readCostState(facts).Orchestrates)
	writeCostState(facts, costState{Streak: 4, Orchestrates: types.OrchestrationInferred})
	noteMainSpawn(at, who, "", true)
	st := readCostState(facts)
	assert.Equal(t, types.OrchestrationProven, st.Orchestrates)
	assert.Zero(t, st.Streak)
	noteMainSpawn(at, who, "", false)
	assert.Equal(t, types.OrchestrationProven, readCostState(facts).Orchestrates, "proof lasts the session")

	worker := hookAttribution{Host: "claude-code", Session: "w"}
	noteMainSpawn(at, worker, "job-a", true)
	assert.Empty(t, readCostState(hint.NewGate(cache, worker.factsKey())).Orchestrates, "a leased caller is a worker")
	sub := hookAttribution{Host: "claude-code", Session: "u", Agent: "a1"}
	noteMainSpawn(at, sub, "", true)
	assert.Empty(t, readCostState(hint.NewGate(cache, sub.factsKey())).Orchestrates, "a subagent is never an orchestrator")
}

// A fork is filed from each shape that writes a row: the CLI verb, its record on stdin, a
// magus\job.put rendered from an MCP call, and either one inside a `magus buzz` script.
func TestNoteForkReadsEveryShape(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	for _, tc := range []struct {
		line string
		want []string
	}{
		{"./magus job fork a1 --parent root --write-paths x.go", []string{"a1"}},
		{"cd /r && ./magus job fork a1", []string{"a1"}},
		{"./magus job fork --stdin <<'EOF'\n{\"schema_version\": " + strconv.Itoa(types.JobSchemaVersion) + ", \"id\": \"a2\", \"read_only\": true}\nEOF", []string{"a2"}},
		{`client op=put id="a3" parent="root"`, []string{"a3"}},
		{`./magus buzz -e 'import "magus"; magus\job.put("a4", opts: {"parent": "root"});'`, []string{"a4"}},
		{"./magus job wait a1", nil},
		{"./magus job exit a1 --stdin", nil},
		{`client op=register id="a1"`, nil},
		{"rg -n fork", nil},
	} {
		var st costState
		noteFork(&st, tc.line, now)
		var got []string
		for id, at := range st.Forks {
			got = append(got, id)
			assert.Equal(t, now.UnixNano(), at, tc.line)
		}
		assert.ElementsMatch(t, tc.want, got, tc.line)
	}
}

// A fork proves nothing until the store shows its row live, created no earlier than the
// call that ran it; one the store never shows is dropped after the window.
func TestForkConfirmedOnlyAfterTheStoreShowsTheRow(t *testing.T) {
	t.Parallel()
	judged := time.Unix(1_700_000_000, 0)
	rows := map[string]types.Job{}
	live := func(id string) (types.Job, bool) {
		row, ok := rows[id]
		return row, ok
	}
	st := costState{Forks: map[string]int64{"a1": judged.UnixNano(), "old": judged.UnixNano()}}

	confirmForks(&st, live, judged.Add(time.Second))
	assert.Empty(t, st.Orchestrates, "the store shows no row yet")
	assert.Len(t, st.Forks, 2)

	rows["old"] = types.Job{ID: "old", State: types.StateRunning, Created: judged.Unix() - 60}
	rows["a1"] = types.Job{ID: "a1", State: types.StateDeclared, Created: judged.Unix()}
	confirmForks(&st, live, judged.Add(2*time.Second))
	assert.Equal(t, types.OrchestrationProven, st.Orchestrates)
	assert.Equal(t, []string{"a1"}, st.Forked)
	assert.Equal(t, map[string]int64{"old": judged.UnixNano()}, st.Forks, "a row created before the fork was not this fork's")

	confirmForks(&st, live, judged.Add(forkConfirmWindow+time.Second))
	assert.Nil(t, st.Forks, "an unconfirmed fork is dropped after the window")
	assert.Equal(t, []string{"a1"}, st.Forked)
}

// orchestratorFixture is a checkout of a git repository and a notes directory outside it,
// located at the checkout.
func orchestratorFixture(t *testing.T) (at location, notes string) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	return location{cacheDir: t.TempDir(), workspace: root, dir: root}, t.TempDir()
}

// orchestrationLines are calls a session that hands out work makes as orchestration.
func orchestrationLines(notes string) []string {
	return []string{
		"./magus job fork a1 --parent root",
		"./magus job wait a1",
		"./magus job apply a1",
		"./magus job exit a1 --stdin",
		"./magus describe job a1",
		"./magus ls jobs",
		"./magus query 'kind=spell'",
		"./magus explain internal/guard",
		"./magus refs costState --occurrences",
		"./magus path internal/guard internal/job",
		"./magus describe file internal/guard/root.go",
		"./magus affected --plan",
		"./magus graph stats",
		"./magus graph diff",
		"./magus diff --impact",
		"./magus status",
		"./magus affected ci",
		"./magus query output grd0123456789ab",
		"./magus query output out1 --identity",
		`./magus buzz -e 'import "magus"; magus\job.put("a1", opts: {"parent": "root"});'`,
		"git branch x",
		"git switch x",
		"git worktree add ../w main",
		"git push origin x",
		"gh pr create --fill",
		"git status -s",
		"git log --oneline -n 20",
		"git diff --stat",
		"git diff --name-only main...HEAD",
		"cat AGENTS.md",
		"cat internal/agent/skills/x/SKILL.md",
		"cat " + filepath.Join(notes, "plan.md"),
		// After a cd into the notes, a relative word is a note too.
		"cd " + notes + " && rg attempts .",
		"cd " + notes + " && cat RESUME.md",
		"cd " + notes + " && rg attempts",
		"sed -n 1,40p " + filepath.Join(notes, "memory.md"),
	}
}

// groundworkLines are the calls a session that hands out work leaves to its workers.
var groundworkLines = []struct {
	line  string
	class types.DelegableClass
}{
	{"rg -n Judge internal", types.DelegableSearch},
	{"grep -rn Judge internal", types.DelegableSearch},
	{"find . -name '*.go'", types.DelegableSearch},
	{"fd root.go", types.DelegableSearch},
	{"cd internal && rg -l Verdict .", types.DelegableSearch},
	{"cat internal/guard/root.go", types.DelegableRead},
	{"sed -n 1,40p internal/guard/root.go", types.DelegableRead},
	{"cat docs/guide.md", types.DelegableRead},
	{"./magus query output out1", types.DelegableRead},
	{"git diff", types.DelegableDiff},
	{"git show HEAD", types.DelegableDiff},
	{"git log -p -3", types.DelegableDiff},
	{"./magus diff", types.DelegableDiff},
	{"./magus run go-test . -s", types.DelegableRun},
	{"./magus run lint docs", types.DelegableRun},
	{"./magus affected test --silent", types.DelegableRun},
	{`./magus buzz -e 'import "fs"; print(fs\readFile("a"));'`, types.DelegableRun},
	{`./magus buzz -e 'import "magus"; magus\run(["test", "."]);'`, types.DelegableRun},
	{"curl -sS https://example.com/x", types.DelegableFetch},
	{"./magus run go-build . -s", delegableIntegrate},
	{"./magus run go::go-build .", delegableIntegrate},
	{"./magus run generate:rw .", delegableIntegrate},
	{"./magus run generate / -s", delegableIntegrate},
	{"./magus run content-generate docs", delegableIntegrate},
	{"./magus vcs add internal/guard/root.go", delegableIntegrate},
	{"git cherry-pick abc123", delegableIntegrate},
	{"git cherry-pick --continue", delegableIntegrate},
	{"git add internal/guard/root.go", delegableIntegrate},
	{"git commit -m x", delegableIntegrate},
}

func TestRootWorkClass(t *testing.T) {
	t.Parallel()
	at, notes := orchestratorFixture(t)
	for _, line := range orchestrationLines(notes) {
		assert.Empty(t, rootWorkClass(line, at), line)
	}
	for _, g := range groundworkLines {
		assert.Equal(t, g.class, rootWorkClass(g.line, at), g.line)
	}
}

func TestNotesPath(t *testing.T) {
	t.Parallel()
	at, notes := orchestratorFixture(t)
	worktree := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+filepath.Join(at.workspace, ".git", "worktrees", "w")+"\n"), 0o644))

	assert.True(t, notesPath(filepath.Join(notes, "plan.md"), at), "outside every checkout")
	assert.True(t, notesPath(filepath.Join(at.workspace, "AGENTS.md"), at))
	assert.True(t, notesPath(filepath.Join(worktree, "internal", "x", "SKILL.md"), at))
	assert.False(t, notesPath(filepath.Join(at.workspace, "internal", "new", "a.go"), at), "a file in a directory not created yet")
	assert.False(t, notesPath("docs/guide.md", at), "relative to the call")
	assert.False(t, notesPath(filepath.Join(worktree, "a.go"), at), "another checkout of the same repository")
	assert.True(t, notesPath("~/.claude/plans/p/RESUME.md", at), "under the home directory, not the call's")
}

// A root reading or searching its own plan folder under ~/.claude does no groundwork: the
// shell expands `~` before the command runs, so the path is the home's, not the checkout's.
func TestRootWorkClassPlacesAHomePathOutsideTheCheckout(t *testing.T) {
	t.Parallel()
	at, _ := orchestratorFixture(t)
	assert.Empty(t, rootWorkClass("cat ~/.claude/plans/p/RESUME.md", at))
	assert.Empty(t, rootWorkClass("rg attempts ~/.claude/plans", at))
	assert.Equal(t, types.DelegableSearch, rootWorkClass("rg attempts ~/.claude/plans internal", at), "one path inside keeps it groundwork")
	assert.Equal(t, types.DelegableSearch, rootWorkClass("rg attempts", at), "no path searches the call's directory")
}

// The check of a live row the thread forked is the orchestrator's to run, and any row's
// recorded check output its to read; anything else stays groundwork.
func TestMainWorkClearsTheForkedRowsCheck(t *testing.T) {
	t.Parallel()
	at, _ := orchestratorFixture(t)
	rows := []types.Job{
		{ID: "a1", State: types.StateRunning, Check: &types.LeaseCheck{Target: "go-test", Project: "."}},
		{ID: "a2", State: types.StatePass, Check: &types.LeaseCheck{Target: "lint", Project: "docs"},
			Result: &types.JobResult{Validation: types.JobResultValidation{OutputRef: "out9"}}},
	}
	ctx := job.WithSnapshot(t.Context(), job.Snapshot{Rows: rows})
	forked := costState{Forked: []string{"a1", "a2"}}

	assert.Empty(t, mainWork(ctx, at, forked, "./magus run go-test . -s"))
	assert.Equal(t, types.DelegableRun, mainWork(ctx, at, costState{}, "./magus run go-test . -s"), "a row another session forked")
	assert.Equal(t, types.DelegableRun, mainWork(ctx, at, forked, "./magus run lint docs"), "a row that ended")
	assert.Equal(t, types.DelegableRun, mainWork(ctx, at, forked, "./magus run go-test internal/guard"), "another project")
	assert.Empty(t, mainWork(ctx, at, costState{}, "./magus query output out9"))
	assert.Equal(t, types.DelegableRead, mainWork(ctx, at, forked, "./magus query output out1"))
}

// The request a workspace rule is handed carries the caller's thread and context.
func TestCommandRequestCarriesTheThread(t *testing.T) {
	ctx, cache := spawnFixture(t)
	at := hookLocation(ctx, Dependencies{ModelRows: fixtureModelSource})
	who := hookAttribution{Host: "claude-code", Session: "s"}
	facts := hint.NewGate(cache, who.factsKey())
	writeCostState(facts, costState{Tokens: 300_000, Model: "claude-sonnet-5-5"})

	req := commandRequest(ctx, commandRuleInput{command: "git diff"}, who, at, facts)
	assert.Equal(t, types.AgentThreadMain, req.Thread)
	assert.Equal(t, types.ThreadInferred, req.ThreadFrom, "inferred until the host names a subagent")
	assert.Equal(t, &types.ContextWindow{Tokens: 300_000, Window: 1_000_000, Model: "claude-sonnet-5-5", Source: types.ContextFromTranscript}, req.Window)

	who.Agent = "a1"
	req = commandRequest(ctx, commandRuleInput{command: "git diff"}, who, at, facts)
	assert.Equal(t, types.AgentThreadSub, req.Thread)
	assert.Equal(t, types.ThreadReported, req.ThreadFrom)
	assert.Nil(t, req.Window, "no usage was reported for the subagent")
}
