package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/handler/mcp/origin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp/mcpclient"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

const (
	clientStandInEnv = "MCP_CLIENT_STAND_IN"
	clientStateEnv   = "MCP_CLIENT_STATE"
)

// The re-executed test binary serves the client worker protocol through the same
// mcpclient.Serve the real worker calls; only opening the workspace differs, since
// the stand-in cannot reach cmd/magus's config load.
func TestClientStandInProcess(t *testing.T) {
	if os.Getenv(clientStandInEnv) != "1" {
		return
	}
	if state := os.Getenv(clientStateEnv); state != "" {
		// TestMain redirects XDG_STATE_HOME. The parent seeded the job store
		// under the path the wrapper copied here.
		_ = os.Setenv("XDG_STATE_HOME", state)
	}
	var ws *magus.Magus
	code := mcpclient.Serve(context.Background(), os.Stdin, os.Stdout, os.Stderr, func(ctx context.Context) (context.Context, error) {
		root, err := magus.FindRoot(".")
		if err != nil {
			return ctx, err
		}
		if ws, err = magus.Open(ctx, root); err != nil {
			return ctx, err
		}
		return proc.WithLease(types.WithWorkspace(ctx, ws), trail.LeaseFromEnv()), nil
	})
	if ws != nil {
		_ = ws.Close()
	}
	_ = os.RemoveAll(os.Getenv("XDG_RUNTIME_DIR"))
	os.Exit(code)
}

func useClientStandIn(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in uses a POSIX wrapper")
	}
	self, err := os.Executable()
	require.NoError(t, err)
	wrapper := filepath.Join(t.TempDir(), "magus")
	script := fmt.Sprintf("#!/bin/sh\n%s=1 %s=\"$XDG_STATE_HOME\" exec '%s' -test.run='^TestClientStandInProcess$' -- \"$@\"\n", clientStandInEnv, clientStateEnv, self)
	require.NoError(t, os.WriteFile(wrapper, []byte(script), 0o755))
	prev := magusExecutable
	magusExecutable = func() (string, error) { return wrapper, nil }
	t.Cleanup(func() { magusExecutable = prev })
}

func TestClientToolRequest(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.buzz")
	require.NoError(t, os.WriteFile(outside, []byte("x"), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link.buzz")))
	tool := &clientTool{root: root}
	for name, tc := range map[string]struct {
		params map[string]any
		want   string
	}{
		"both":           {map[string]any{"script": "x", "path": "a.buzz"}, "client: provide script or path, not both"},
		"neither":        {map[string]any{}, "client: provide script or a workspace .buzz path"},
		"args not array": {map[string]any{"script": "x", "args": "one two"}, "client: args must be an array of strings"},
		"non-string arg": {map[string]any{"script": "x", "args": []any{1.0}}, "client: args[0] must be a string"},
		"too many args":  {map[string]any{"script": "x", "args": make([]any, mcpclient.MaxArgs+1)}, "client: more than 64 args"},
		"arg too long":   {map[string]any{"script": "x", "args": []any{strings.Repeat("x", mcpclient.MaxArgBytes+1)}}, "client: args[0] exceeds"},
		"oversized":      {map[string]any{"script": strings.Repeat("x", mcpclient.MaxSourceBytes+1)}, "client: script exceeds"},
		"symlink escape": {map[string]any{"path": "link.buzz"}, "client: path \"link.buzz\" is outside the workspace"},
	} {
		_, err := tool.request(tc.params)
		require.ErrorIs(t, err, types.MCPClientFailed, name)
		assert.ErrorContains(t, err, tc.want, name)
	}

	req, err := tool.request(map[string]any{"script": "x", "args": []any{"a"}})
	require.NoError(t, err)
	assert.Equal(t, mcpclient.Request{Script: "x", Args: []string{"a"}}, req)
}

// The largest request the tool accepts fits the bound the worker reads.
func TestClientToolLargestRequestFitsTheWorker(t *testing.T) {
	args := make([]any, mcpclient.MaxArgs)
	for i := range args {
		args[i] = strings.Repeat(`"`, mcpclient.MaxArgBytes)
	}
	req, err := (&clientTool{root: t.TempDir()}).request(map[string]any{"script": strings.Repeat("\n", mcpclient.MaxSourceBytes), "args": args})
	require.NoError(t, err)
	wire, err := json.Marshal(req)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(wire), mcpclient.MaxRequestBytes)
}

// loopForever never returns; the condition keeps the checker from proving it.
const loopForever = `fun main(args: [str]) > int { while (args.len() >= 0) {} return 0; }`

// A direct call ends at the tool's bound, and says how to run a longer one.
func TestClientToolBoundsADirectCall(t *testing.T) {
	useClientStandIn(t)
	m := fixtureMagus(t)
	tool := &clientTool{root: m.Root(), timeout: 300 * time.Millisecond}
	res, err := adapt(tool)(context.Background(), callRequest(hint.ToolClient.String(), map[string]any{"script": loopForever}))
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Equal(t, "[MGS3034] client: script timed out after 300ms; run it as an MCP task, or through the Buzz CLI", firstLine(allText(res)))
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// taskServer is the real server a host talks to, driven one JSON-RPC message at a
// time the way the stdio and HTTP transports drive it.
func taskServer(t *testing.T) (*mcpserver.MCPServer, *magus.Magus, func(string) json.RawMessage) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	useClientStandIn(t)
	m := fixtureMagus(t)
	srv := buildServer(Options{Magus: m, Logger: quietLogger(), Version: "test"}, quietLogger(), &mcpserver.Hooks{}, func(context.Context) origin.Client { return origin.Client{Name: "task-test"} })
	ctx := trail.ContextWithCredential(context.Background(), types.CredentialStdio)
	send := func(frame string) json.RawMessage {
		t.Helper()
		raw, err := json.Marshal(srv.HandleMessage(ctx, []byte(frame)))
		require.NoError(t, err)
		var got rpcFrame
		require.NoError(t, json.Unmarshal(raw, &got))
		require.Empty(t, got.Error, "%s", got.Error)
		return got.Result
	}
	send(initializeFrame)
	return srv, m, send
}

func startTask(t *testing.T, send func(string) json.RawMessage, script string) string {
	t.Helper()
	call, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 10, "method": "tools/call",
		"params": map[string]any{"name": hint.ToolClient.String(), "arguments": map[string]any{"script": script}, "task": map[string]any{}},
	})
	require.NoError(t, err)
	var created mcplib.CreateTaskResult
	require.NoError(t, json.Unmarshal(send(string(call)), &created))
	require.NotEmpty(t, created.Task.TaskId)
	return created.Task.TaskId
}

// A host that runs client as a task gets its result after the tools/call request
// has returned: the call must outlive the request that started it.
func TestClientRunsAsATask(t *testing.T) {
	_, _, send := taskServer(t)
	id := startTask(t, send, `fun main(args: [str]) > int { return 7; }`)

	var result toolResult
	require.NoError(t, json.Unmarshal(send(`{"jsonrpc":"2.0","id":11,"method":"tasks/result","params":{"taskId":"`+id+`"}}`), &result))
	require.Len(t, result.Content, 1)
	require.False(t, result.IsError, result.Content[0].Text)
	var got mcpclient.Result
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].Text), &got))
	assert.JSONEq(t, `7`, string(got.JSON))
}

// tasks/cancel stops a task call that would otherwise run forever.
func TestClientTaskIsCancellable(t *testing.T) {
	_, m, send := taskServer(t)
	id := startTask(t, send, loopForever)
	time.Sleep(300 * time.Millisecond)
	send(`{"jsonrpc":"2.0","id":12,"method":"tasks/cancel","params":{"taskId":"` + id + `"}}`)

	require.Eventually(t, func() bool {
		events, err := trail.ReadRecent(m.CacheDir(), 10)
		return err == nil && len(events) == 1
	}, 20*time.Second, 50*time.Millisecond, "the cancelled call never returned")
	events, err := trail.ReadRecent(m.CacheDir(), 10)
	require.NoError(t, err)
	assert.Contains(t, events[0].Preview, "client: script cancelled")
}
