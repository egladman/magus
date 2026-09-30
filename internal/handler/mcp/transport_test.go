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
	"github.com/egladman/magus/internal/job"
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

The workspace is the client tool: a Buzz program that imports "magus" and calls its members (magus\projects, magus\targets, magus\query, magus\explain, magus\path, magus\refs, magus\stats, magus\describeFile, magus\where, magus\affected, magus\impact, magus\run, magus\clean, magus\output, magus\insight, magus\doctor, magus\job, magus\vcs). Call magus\describeModule("magus") for the signatures. The tools below are the operations that module does not cover.

  client                  - run Buzz against the magus client and return its value
  buzz                    - transform JSON with Buzz; no workspace access
  status                  - inspect the live concurrency pool
  config                  - view the resolved workspace config (read-only)
  diff                    - join the review session a person has open: state, comment, suggest, resolve
  console                 - return a local console link when a person asks to see it

Typical flow:
  Discover through client with the typed members (magus\projects, magus\targets, magus\query, magus\describeFile); they return records, not CLI text.
  Run through client (magus\affected, magus\run); magus\output fetches a captured log by its ref.
  Health: client (magus\doctor), status, config.

Config mutation is intentionally not exposed. Use the magus CLI for that.`

func TestServerInstructionsRenderUnchanged(t *testing.T) {
	t.Parallel()

	assert.Equal(t, wantServerInstructions, serverInstructions)
}

// TestServerInstructionsToolNamesResolve is the drift half: the golden above
// only pins what is there today, so a tool renamed on both sides would sail
// through it. Every declared tool name in the rendered block must be bound to a
// real Registry entry, and every Registry entry must appear.
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
	StructuredContent json.RawMessage `json:"structuredContent"`
}

const (
	initializeFrame  = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"stdio-test","version":"1.0"}}}`
	initializedFrame = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	toolsListFrame   = `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	toolCallFrame    = `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"config","arguments":{}}}`
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
	assert.Contains(t, result.Content[0].Text, "cache")
	assert.JSONEq(t, result.Content[0].Text, string(result.StructuredContent), "a host reading either copy sees the same payload")

	require.NoError(t, inW.Close())
	require.NoError(t, <-served, "EOF on stdin is a clean stop")
	for lines.Scan() {
		t.Errorf("stdout carried a line nobody asked for: %q", lines.Text())
	}

	events, err := trail.ReadRecent(m.CacheDir(), 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, hint.ToolConfig.String(), events[0].Action)
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
	req := callRequest(hint.ToolConfig.String(), nil)
	with := func(c types.Credential) context.Context { return trail.ContextWithCredential(context.Background(), c) }

	for name, tc := range map[string]struct {
		ctx  context.Context
		want string
	}{
		"no credential":   {context.Background(), "[MGS9015] config needs mcp=write and the caller holds nothing"},
		"console grant":   {with(types.Credential{Kind: types.KindStored, Grant: types.GrantConsole}), "[MGS9015] config needs mcp=write and the caller holds console=write"},
		"stdio":           {with(types.CredentialStdio), "ran"},
		"connector token": {with(types.Credential{Kind: types.KindStored, Grant: types.GrantConnector}), "ran"},
		"operator token":  {with(types.Credential{Kind: types.KindOperator, Grant: types.GrantOperator}), "ran"},
	} {
		res, err := h(tc.ctx, req)
		require.NoError(t, err, name)
		assert.Equal(t, tc.want != "ran", res.IsError, name)
		assert.True(t, strings.HasPrefix(allText(res), tc.want), "%s: %s", name, allText(res))
	}
	assert.Equal(t, 3, ran, "a refused call must not reach the tool")
}

// Over HTTP the caller's lease comes off the request's baggage header, the HTTP form of the
// BAGGAGE a local process reads, and the client tool's magus\job.put writes as that caller.
func TestHTTPStampsTheCallersLeaseFromTheBaggageHeader(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	useClientStandIn(t)
	m := fixtureMagus(t)
	jobs := job.NewStore(job.Location{CacheDir: m.CacheDir(), Root: m.Root()})
	for _, row := range []types.Job{
		{ID: "root/worker", WritePaths: []string{"internal/job"}, State: types.StateRunning},
		{ID: "root/other", WritePaths: []string{"internal/guard", "internal/hint"}, State: types.StateRunning},
	} {
		_, err := jobs.Update(t.Context(), row.ID, func(cur *types.Job) { *cur = row })
		require.NoError(t, err)
	}
	h, err := HTTPHandler(Options{Magus: m, Logger: quietLogger()})
	require.NoError(t, err)
	connector := types.Credential{Kind: types.KindStored, Grant: types.GrantConnector}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(trail.ContextWithCredential(r.Context(), connector)))
	}))
	defer srv.Close()
	post := func(baggage, session, body string) (http.Header, toolResult) {
		req, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if baggage != "" {
			req.Header.Set("baggage", baggage)
		}
		if session != "" {
			req.Header.Set("Mcp-Session-Id", session)
		}
		resp, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		var frame rpcFrame
		require.NoError(t, json.Unmarshal(raw, &frame), string(raw))
		var result toolResult
		if session != "" {
			require.NoError(t, json.Unmarshal(frame.Result, &result), string(raw))
		}
		return resp.Header, result
	}
	hdr, _ := post("", "", initializeFrame)
	session := hdr.Get("Mcp-Session-Id")
	require.NotEmpty(t, session)
	shrink := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"client","arguments":{"script":"import \"magus\";\nfun main(args: [str]) > any !> str {\n  return magus\\job\\put(\"root/other\", opts: {\"write_paths\": [\"internal/guard\"]});\n}\n"}}}`

	for name, tc := range map[string]struct {
		baggage, want string
	}{
		"another job's lease":  {"other.vendor=x,magus.lease=root/worker", "lease root/worker is bound to this session"},
		"no lease at all":      {"", "no lease stamped on it"},
		"a malformed lease id": {"magus.lease=has%20space", "no lease stamped on it"},
	} {
		_, result := post(tc.baggage, session, shrink)
		require.True(t, result.IsError, name)
		assert.Contains(t, result.Content[0].Text, tc.want, name)
	}
	_, result := post("magus.lease=root/other", session, shrink)
	require.False(t, result.IsError, result.Content)
	rows, err := jobs.List()
	require.NoError(t, err)
	var paths []string
	for _, row := range rows {
		if row.ID == "root/other" {
			paths = row.WritePaths
		}
	}
	assert.Equal(t, []string{"internal/guard"}, paths, "the row's own holder released a path")
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
		"connector": {types.Credential{Kind: types.KindStored, Grant: types.GrantConnector}, false},
		"console":   {types.Credential{Kind: types.KindStored, Grant: types.GrantConsole}, true},
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
