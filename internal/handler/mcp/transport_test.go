package mcp

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// wantServerInstructions is the exact block every MCP client reads at session
// start, pinned verbatim. serverInstructions composes it from the hint.Tool*
// constants; this golden copy is what makes that composition safe to change:
// a constant that renders differently from the prose it replaced fails here
// rather than reaching a client.
const wantServerInstructions = `You are connected to a magus workspace.
magus is a build orchestrator for multi-language monorepos.

Discover:
  magus_describe          - list spells, targets, projects, workspaces, or mcp_tools
  magus_describe_file     - classify changed paths: generated output, declared source, or unclaimed
  magus_where             - resolve a fuzzy project name to its absolute path
  magus_config_get        - view the resolved workspace config (read-only)

Run:
  magus_run_target        - run build/test/lint/format/generate/ci
  magus_run_affected      - run a target on only VCS-changed projects
  magus_affected_plan     - emit a CI shard plan for the affected set
  magus_affected_explain  - explain why a project is affected by VCS changes

Inspect:
  magus_doctor            - validate the workspace health
  magus_status            - inspect the live concurrency pool
  magus_output            - fetch a target-output blob by its reference id
  magus_insight           - VCS history lenses (hotspots, ownership, trend)

Knowledge graph:
  magus_query             - search the target/spell/symbol graph
  magus_explain           - explain a single node and its relationships
  magus_path              - find a path between two graph nodes
  magus_refs              - list files that reference a symbol
  magus_stats             - summarize graph composition

Work with people and other agents:
  magus_diff              - join the review session a person has open: state, comment, suggest, resolve
  magus_memory            - the per-repository memory of decisions, plans, and ruled-out hypotheses
  magus_vcs_checkpoint    - record the working state's identity (revision, branch, patch digest)
  magus_job               - declare the job plan an orchestrator hands out: criteria, paths, states
  magus_console_present   - return a local console link when a person asks to see it

Typical flow:
  Discover first: magus_describe (list spells/targets/projects/workspaces), magus_where (resolve a fuzzy project name to a path).
  Then act: magus_run_target / magus_run_affected; magus_affected_plan (CI shard plan), magus_affected_explain (why a project is affected).
  After a run: magus_output (fetch a target's captured output by its ref).
  Understand the graph: magus_query (search) -> magus_explain (a node's edges and provenance) -> magus_path (shortest path); magus_refs (symbol defs and refs); magus_stats (graph shape).
  Health and meta: magus_status, magus_doctor, magus_config_get.

Config mutation is intentionally not exposed. Use the magus CLI for that.`

func TestServerInstructionsRenderUnchanged(t *testing.T) {
	t.Parallel()

	assert.Equal(t, wantServerInstructions, serverInstructions)
}

// TestServerInstructionsToolNamesResolve is the drift half: the golden above
// only pins what is there today, so a tool renamed on both sides would sail
// through it. Every magus_* token in the rendered block must also be a declared
// hint.ToolName bound to a real Registry entry.
func TestServerInstructionsToolNamesResolve(t *testing.T) {
	t.Parallel()

	declared := map[string]bool{}
	for _, tn := range hint.AllToolNames {
		declared[tn.String()] = true
	}
	registered := map[string]bool{}
	for _, d := range Registry {
		registered[d.Name] = true
	}

	tokens := toolTokenRe.FindAllString(serverInstructions, -1)
	assert.NotEmpty(t, tokens, "the instructions name no tools at all, so this test proves nothing")
	named := map[string]bool{}
	for _, tok := range tokens {
		named[tok] = true
		assert.Truef(t, declared[tok], "instructions name %q, which is not a declared hint.ToolName", tok)
		assert.Truef(t, registered[tok], "instructions name %q, which is not a Registry[].Name", tok)
	}
	// The other direction: a tool the catalog mounts but the instructions never mention
	// is one an agent learns about only by listing, which is the drift the hand-written
	// prose invites.
	for name := range registered {
		assert.Truef(t, named[name], "Registry mounts %q, which the instructions never name", name)
	}
}

// rpcFrame is the part of a JSON-RPC 2.0 message these tests read.
type rpcFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

// toolResult is the part of a tools/call result these tests read.
type toolResult struct {
	IsError bool `json:"isError"`
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

const (
	initializeFrame  = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"stdio-test","version":"1.0"}}}`
	initializedFrame = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	toolsListFrame   = `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	toolCallFrame    = `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"magus_describe","arguments":{"kind":"mcp_tools"}}}`
)

// A host launches `magus mcp` and speaks line-delimited JSON-RPC on its pipes. The whole
// exchange runs through ServeStdio, the function the command serves, and every line it
// writes must be a protocol frame answering a request, since a host parses each one.
func TestServeStdioRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := fixtureMagus(t)

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	served := make(chan error, 1)
	go func() {
		served <- ServeStdio(context.Background(), Options{Magus: m, Logger: quietLogger(), Version: "test"}, inR, outW)
		_ = outW.Close()
	}()
	lines := bufio.NewScanner(outR)
	lines.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	exchange := func(frame string) rpcFrame {
		t.Helper()
		_, err := io.WriteString(inW, frame+"\n")
		require.NoError(t, err)
		require.True(t, lines.Scan(), "no reply to %s: %v", frame, lines.Err())
		var got rpcFrame
		require.NoError(t, json.Unmarshal(lines.Bytes(), &got), "stdout carried a line that is not JSON-RPC: %q", lines.Text())
		require.Equal(t, "2.0", got.JSONRPC)
		require.Empty(t, got.Error, "%s", got.Error)
		require.NotNil(t, got.ID, "a reply with no id: %q", lines.Text())
		return got
	}

	init := exchange(initializeFrame)
	assert.Equal(t, 1, *init.ID)
	var initResult mcplib.InitializeResult
	require.NoError(t, json.Unmarshal(init.Result, &initResult))
	assert.Equal(t, "magus", initResult.ServerInfo.Name)

	_, err := io.WriteString(inW, initializedFrame+"\n")
	require.NoError(t, err)

	listed := exchange(toolsListFrame)
	assert.Equal(t, 2, *listed.ID)
	var tools mcplib.ListToolsResult
	require.NoError(t, json.Unmarshal(listed.Result, &tools))
	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	want := make([]string, 0, len(Registry))
	for _, d := range Registry {
		want = append(want, d.Name)
	}
	assert.ElementsMatch(t, want, names)

	call := exchange(toolCallFrame)
	assert.Equal(t, 3, *call.ID)
	var result toolResult
	require.NoError(t, json.Unmarshal(call.Result, &result))
	require.Len(t, result.Content, 1)
	assert.False(t, result.IsError, result.Content[0].Text)
	assert.Contains(t, result.Content[0].Text, "magus_config_get")

	require.NoError(t, inW.Close())
	require.NoError(t, <-served, "EOF on stdin is a clean stop")
	for lines.Scan() {
		t.Errorf("stdout carried a line nobody asked for: %q", lines.Text())
	}

	events, err := trail.ReadRecent(m.CacheDir(), 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "magus_describe", events[0].Action)
	assert.Equal(t, types.CredentialStdio, events[0].Credential)
	assert.Equal(t, types.EntryPointMCP, events[0].EntryPoint)
	assert.Equal(t, "stdio-test/1.0", events[0].Host)
}

// Cancelling the context stops the server without an error, the way a signal does.
func TestServeStdioStopsCleanlyOnCancel(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := fixtureMagus(t)
	inR, inW := io.Pipe()
	defer inW.Close()
	var out bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() {
		served <- ServeStdio(ctx, Options{Magus: m, Logger: quietLogger()}, inR, &out)
	}()
	cancel()
	require.NoError(t, <-served)
	assert.Empty(t, out.String())
}

func TestServeStdioRefusesOptionsWithoutAWorkspace(t *testing.T) {
	t.Parallel()
	err := ServeStdio(context.Background(), Options{}, strings.NewReader(""), io.Discard)
	assert.ErrorContains(t, err, "Options.Magus or Options.Unavailable is required")
}

// Every tool shares ToolNeed and the stdio caller holds exactly that, so no tool is out of
// its reach. The refusal is pinned with the credentials that fall short instead: none at
// all, and a console token's grant.
func TestAuthorizeHoldsEveryCallToToolNeed(t *testing.T) {
	t.Parallel()
	assert.Equal(t, types.GrantConnector, types.CredentialStdio.Grant, "stdio reaches the MCP surface and nothing past it")
	assert.True(t, types.CredentialStdio.Grant.Allows(ToolNeed))

	ran := 0
	h := authorize(func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		ran++
		return mcplib.NewToolResultText("ran"), nil
	})
	req := callRequest("magus_run_target", nil)
	with := func(c types.Credential) context.Context { return trail.ContextWithCredential(context.Background(), c) }

	for name, tc := range map[string]struct {
		ctx  context.Context
		want string
	}{
		"no credential":   {context.Background(), "[MGS9015] magus_run_target needs mcp=write and the caller holds nothing"},
		"console grant":   {with(types.Credential{Class: types.ClassStored, Grant: types.GrantConsole}), "[MGS9015] magus_run_target needs mcp=write and the caller holds console=write"},
		"stdio":           {with(types.CredentialStdio), "ran"},
		"connector token": {with(types.Credential{Class: types.ClassStored, Grant: types.GrantConnector}), "ran"},
		"operator token":  {with(types.Credential{Class: types.ClassOperator, Grant: types.GrantOperator}), "ran"},
	} {
		res, err := h(tc.ctx, req)
		require.NoError(t, err, name)
		assert.Equal(t, tc.want != "ran", res.IsError, name)
		assert.True(t, strings.HasPrefix(allText(res), tc.want), "%s: %s", name, allText(res))
	}
	assert.Equal(t, 3, ran, "a refused call must not reach the tool")
}

// Over HTTP the credential reaches authorize on the request context, where the server's
// bearer guard stamps it.
func TestHTTPToolCallReadsTheRequestCredential(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	h, err := HTTPHandler(Options{Magus: fixtureMagus(t), Logger: quietLogger()})
	require.NoError(t, err)

	for name, tc := range map[string]struct {
		cred      types.Credential
		wantError bool
	}{
		"connector": {types.Credential{Class: types.ClassStored, Grant: types.GrantConnector}, false},
		"console":   {types.Credential{Class: types.ClassStored, Grant: types.GrantConsole}, true},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.ServeHTTP(w, r.WithContext(trail.ContextWithCredential(r.Context(), tc.cred)))
		}))
		post := func(session, body string) (http.Header, []byte) {
			req, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(body))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			if session != "" {
				req.Header.Set("Mcp-Session-Id", session)
			}
			resp, err := srv.Client().Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			b, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			return resp.Header, b
		}
		hdr, _ := post("", initializeFrame)
		session := hdr.Get("Mcp-Session-Id")
		require.NotEmpty(t, session, name)

		_, body := post(session, toolCallFrame)
		var frame rpcFrame
		require.NoError(t, json.Unmarshal(body, &frame), "%s: %s", name, body)
		var result toolResult
		require.NoError(t, json.Unmarshal(frame.Result, &result), name)
		assert.Equal(t, tc.wantError, result.IsError, "%s: %s", name, body)
		srv.Close()
	}
}
