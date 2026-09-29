package mcp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp/transform"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

const buzzStandInEnv = "MCP_BUZZ_STAND_IN"

// The re-executed test binary serves the worker protocol, not the CLI.
func TestBuzzStandInProcess(t *testing.T) {
	if os.Getenv(buzzStandInEnv) != "1" {
		return
	}
	code := transform.Serve(context.Background(), os.Stdin, os.Stdout, os.Stderr)
	// os.Exit skips TestMain's cleanup of the private runtime dir it made for this process.
	_ = os.RemoveAll(os.Getenv("XDG_RUNTIME_DIR"))
	os.Exit(code)
}

func useStandInMagus(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in uses a POSIX wrapper")
	}
	self, err := os.Executable()
	require.NoError(t, err)
	wrapper := filepath.Join(t.TempDir(), "magus")
	script := fmt.Sprintf("#!/bin/sh\n%s=1 exec '%s' -test.run='^TestBuzzStandInProcess$' -- \"$@\"\n", buzzStandInEnv, self)
	require.NoError(t, os.WriteFile(wrapper, []byte(script), 0o755))
	prev := magusExecutable
	magusExecutable = func() (string, error) { return wrapper, nil }
	t.Cleanup(func() { magusExecutable = prev })
}

func callBuzz(t *testing.T, tool *buzzTool, args map[string]any) *mcplib.CallToolResult {
	t.Helper()
	res, err := adapt(tool)(context.Background(), callRequest(hint.ToolBuzz.String(), args))
	require.NoError(t, err)
	return res
}

func decodeBuzzResult(t *testing.T, res *mcplib.CallToolResult) transform.Result {
	t.Helper()
	require.False(t, res.IsError, allText(res))
	var got transform.Result
	require.NoError(t, json.Unmarshal([]byte(allText(res)), &got))
	return got
}

// descriptor returns the catalog entry for tool.
func descriptor(t *testing.T, tool hint.ToolName) ToolDescriptor {
	t.Helper()
	for _, d := range Registry {
		if d.Name == tool.String() {
			return d
		}
	}
	t.Fatalf("no Registry entry for %s", tool)
	return ToolDescriptor{}
}

func TestBuzzToolRoundTripsInputThroughServeStdio(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	useStandInMagus(t)
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
		require.NoError(t, json.Unmarshal(lines.Bytes(), &got))
		require.Empty(t, got.Error, "%s", got.Error)
		return got
	}
	exchange(initializeFrame)
	_, err := io.WriteString(inW, initializedFrame+"\n")
	require.NoError(t, err)
	call, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": hint.ToolBuzz.String(), "arguments": map[string]any{
			"script": `fun transform(input: any, args: [str]) > any { return input; }`,
			"input":  map[string]any{"projects": []string{"api", "web"}},
			"args":   []string{"one", "two words"},
		}},
	})
	require.NoError(t, err)
	reply := exchange(string(call))
	var result toolResult
	require.NoError(t, json.Unmarshal(reply.Result, &result))
	require.Len(t, result.Content, 1)
	require.False(t, result.IsError, result.Content[0].Text)
	var got transform.Result
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].Text), &got))
	assert.JSONEq(t, `{"projects":["api","web"]}`, string(got.JSON))
	require.NoError(t, inW.Close())
	require.NoError(t, <-served)
	for lines.Scan() {
		t.Errorf("stdout carried a non-protocol line: %q", lines.Text())
	}
}

func TestBuzzToolArgsAreAnArray(t *testing.T) {
	useStandInMagus(t)
	tool := &buzzTool{root: t.TempDir(), timeout: 5 * time.Second}
	got := decodeBuzzResult(t, callBuzz(t, tool, map[string]any{
		"script": `fun transform(input: any, args: [str]) > any { return args; }`,
		"args":   []any{"one", "two words"},
	}))
	assert.JSONEq(t, `["one","two words"]`, string(got.JSON))
}

// The worker's diagnostic reaches the agent once, code and remedy intact, rather
// than wrapped in a second diagnostic that repeats both.
func TestBuzzToolDirectsMagusImportToClient(t *testing.T) {
	useStandInMagus(t)
	tool := &buzzTool{root: t.TempDir(), timeout: 5 * time.Second}
	res := callBuzz(t, tool, map[string]any{
		"script": `import "magus"; fun transform(input: any, args: [str]) > any { return input; }`,
	})
	require.True(t, res.IsError)
	text := allText(res)
	assert.True(t, strings.HasPrefix(text, "[MGS3033] buzz: "), text)
	assert.Equal(t, 1, strings.Count(text, "["+string(types.MCPBuzzFailed)+"]"), text)
	assert.Equal(t, 1, strings.Count(text, "see: "), text)
	assert.Contains(t, text, "use the client tool")
}

// A param the catalog does not declare is refused, not silently dropped.
func TestBuzzToolRejectsUndeclaredParams(t *testing.T) {
	tool := &buzzTool{root: t.TempDir(), timeout: time.Second}
	h := declaredParams(descriptor(t, hint.ToolBuzz), adapt(tool))
	for _, name := range []string{"capabilities", "plan", "stdin"} {
		res, err := h(context.Background(), callRequest(hint.ToolBuzz.String(), map[string]any{"script": "x", name: true}))
		require.NoError(t, err)
		require.True(t, res.IsError, name)
		assert.Equal(t, fmt.Sprintf("buzz: unknown parameter %q", name), allText(res))
	}
}

func TestBuzzToolRequest(t *testing.T) {
	root := t.TempDir()
	tool := &buzzTool{root: root}
	for name, tc := range map[string]struct {
		params map[string]any
		want   string
	}{
		"both":           {map[string]any{"script": "x", "path": "a.buzz"}, "buzz: provide script or path, not both"},
		"neither":        {map[string]any{}, "buzz: provide script or a workspace .buzz path"},
		"args not array": {map[string]any{"script": "x", "args": "one two"}, "buzz: args must be an array of strings"},
		"non-string arg": {map[string]any{"script": "x", "args": []any{1.0}}, "buzz: args[0] must be a string"},
		"too many args":  {map[string]any{"script": "x", "args": make([]any, transform.MaxArgs+1)}, "buzz: more than 64 args"},
		"oversized":      {map[string]any{"script": strings.Repeat("x", transform.MaxSourceBytes+1)}, "buzz: script exceeds"},
		"input too big":  {map[string]any{"script": "x", "input": strings.Repeat("x", transform.MaxInputBytes)}, "buzz: input exceeds"},
	} {
		_, err := tool.request(tc.params)
		require.ErrorIs(t, err, types.MCPBuzzFailed, name)
		assert.ErrorContains(t, err, tc.want, name)
	}
}

func TestBuzzToolRejectsScriptSymlinkOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.buzz")
	require.NoError(t, os.WriteFile(outside, []byte("x"), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link.buzz")))
	tool := &buzzTool{root: root}
	_, err := tool.request(map[string]any{"path": "link.buzz"})
	require.ErrorIs(t, err, types.MCPBuzzFailed)
	assert.ErrorContains(t, err, "outside the workspace")
}

func TestBuzzToolTimesOut(t *testing.T) {
	useStandInMagus(t)
	tool := &buzzTool{root: t.TempDir(), timeout: 200 * time.Millisecond}
	res := callBuzz(t, tool, map[string]any{
		"script": `fun transform(input: any, args: [str]) > any { while (args.len() >= 0) {} return input; }`,
	})
	require.True(t, res.IsError)
	assert.Contains(t, allText(res), "buzz: script timed out after 200ms; simplify it or run it through the Buzz CLI")
}
