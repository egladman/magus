package mcp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	vm "github.com/egladman/magus/libs/gopherbuzz/vm"
)

// buzzStandInEnv marks the test binary re-executed as a stand-in for `magus buzz`.
const buzzStandInEnv = "MCP_BUZZ_STAND_IN"

// TestBuzzStandInProcess is not a test. Re-executed with buzzStandInEnv set, it plays
// `magus buzz` for the tool: it takes the same argv (buzz, -e <src> or <path>, --, argv)
// and answers with the same streams and exit status, over upstream Buzz's stdlib rather
// than magus's host modules, which none of these scripts use.
func TestBuzzStandInProcess(t *testing.T) {
	if os.Getenv(buzzStandInEnv) != "1" {
		return
	}
	code := standInBuzz(os.Args[slices.Index(os.Args, "--")+1:])
	// os.Exit skips TestMain's cleanup of the private runtime dir it made for this process.
	_ = os.RemoveAll(os.Getenv("XDG_RUNTIME_DIR"))
	os.Exit(code)
}

func standInBuzz(args []string) int {
	if len(args) < 3 || args[0] != "buzz" {
		fmt.Fprintf(os.Stderr, "stand-in: unexpected argv %q\n", args)
		return 2
	}
	name, rest := args[1], args[2:]
	var code string
	if name == "-e" {
		code, rest = args[2], args[3:]
	} else {
		data, err := os.ReadFile(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[error] %v\n", err)
			return 1
		}
		code = string(data)
	}
	if len(rest) == 0 || rest[0] != "--" {
		fmt.Fprintf(os.Stderr, "stand-in: no -- before the script's argv in %q\n", args)
		return 2
	}
	ctx := context.Background()
	sess := buzz.NewSession(ctx)
	defer func() { _ = sess.Close() }()
	buzzstd.Register(sess)
	if err := sess.Exec(ctx, code); err != nil {
		fmt.Fprintf(os.Stderr, "[error] %s: %v\n", name, err)
		return 1
	}
	mainFn := sess.GetGlobal("main")
	if !mainFn.IsFun() {
		return 0
	}
	items := make([]vm.Value, 0, len(rest)-1)
	for _, a := range rest[1:] {
		items = append(items, vm.StrValue(a))
	}
	ret, err := sess.CallValue(ctx, mainFn, []vm.Value{vm.ListValue(items)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[error] %s: %v\n", name, err)
		return 1
	}
	if ret.IsInt() {
		return int(ret.AsInt())
	}
	return 0
}

// useStandInMagus points the tool at the stand-in for the rest of t. Tests that call it
// must not run in parallel: magusExecutable is package state.
func useStandInMagus(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in is reached through a POSIX sh wrapper")
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

// callBuzz runs one magus_buzz call through adapt, the path every tool error takes.
func callBuzz(t *testing.T, tool *buzzTool, args map[string]any) *mcplib.CallToolResult {
	t.Helper()
	res, err := adapt(tool)(context.Background(), callRequest("magus_buzz", args))
	require.NoError(t, err)
	return res
}

func decodeBuzzResult(t *testing.T, res *mcplib.CallToolResult) buzzResult {
	t.Helper()
	require.False(t, res.IsError, allText(res))
	var got buzzResult
	require.NoError(t, json.Unmarshal([]byte(allText(res)), &got))
	return got
}

// A client hands a prior tool's JSON in on stdin and gets the script's JSON back parsed,
// over the same stdio transport `magus mcp` serves.
func TestBuzzToolRoundTripsStdinThroughServeStdio(t *testing.T) {
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
		require.NoError(t, json.Unmarshal(lines.Bytes(), &got), "stdout carried a line that is not JSON-RPC: %q", lines.Text())
		require.Empty(t, got.Error, "%s", got.Error)
		return got
	}
	exchange(initializeFrame)
	_, err := io.WriteString(inW, initializedFrame+"\n")
	require.NoError(t, err)

	call, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "magus_buzz", "arguments": map[string]any{
			"script": `import "std"; import "io";
fun main(args: [str]) > void {
    final s = io\stdin.readAll() ?? "";
    std\print("\{\"in\":{s},\"args\":\"{args.join(",")}\"\}");
}`,
			"stdin": `{"projects":["api","web"]}`,
			"args":  []any{"one", "two words"},
			"write": true,
		}},
	})
	require.NoError(t, err)
	reply := exchange(string(call))
	var result toolResult
	require.NoError(t, json.Unmarshal(reply.Result, &result))
	require.Len(t, result.Content, 1)
	require.False(t, result.IsError, result.Content[0].Text)
	var got buzzResult
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].Text), &got))
	assert.Equal(t, 0, got.ExitCode)
	assert.JSONEq(t, `{"in":{"projects":["api","web"]},"args":"one,two words"}`, string(got.JSON))
	assert.Equal(t, `{"in":{"projects":["api","web"]},"args":"one,two words"}`+"\n", got.Stdout)

	require.NoError(t, inW.Close())
	require.NoError(t, <-served)
	for lines.Scan() {
		t.Errorf("stdout carried a line nobody asked for: %q", lines.Text())
	}
}

func TestBuzzToolRunsAWorkspaceFileWithArgs(t *testing.T) {
	useStandInMagus(t)
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tools"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tools", "count.buzz"), []byte(`import "std";
fun main(args: [str]) > void { std\print("{args.len()} args"); }
`), 0o644))
	tool := &buzzTool{root: root, timeout: time.Minute}

	got := decodeBuzzResult(t, callBuzz(t, tool, map[string]any{"path": "tools/count.buzz", "args": "a b c", "write": true}))
	assert.Equal(t, buzzResult{Stdout: "3 args\n"}, got, "plain text is stdout alone, with no parsed json")
}

func TestBuzzToolCompileErrorIsAToolError(t *testing.T) {
	useStandInMagus(t)
	tool := &buzzTool{root: t.TempDir(), timeout: time.Minute}

	res := callBuzz(t, tool, map[string]any{"script": `fun main(args: [str]) > void { var x: int = "a"; }`, "write": true})
	assert.True(t, res.IsError)
	assert.Contains(t, allText(res), "magus buzz exited with code 1")
	assert.Contains(t, allText(res), "BZZ1005")
	assert.Contains(t, allText(res), "1:32", "the diagnostic keeps its line:col")
}

func TestBuzzToolRuntimeErrorIsAToolError(t *testing.T) {
	useStandInMagus(t)
	tool := &buzzTool{root: t.TempDir(), timeout: time.Minute}

	res := callBuzz(t, tool, map[string]any{"script": `import "std";
fun main(args: [str]) > void { std\print("partial"); throw "boom"; }`, "write": true})
	assert.True(t, res.IsError)
	assert.Contains(t, allText(res), "uncaught error: boom")
	assert.Contains(t, allText(res), "stdout:\npartial", "output printed before the error is kept")
}

// `fun main() > int` is the exit-status convention, so a nonzero return is a failure even
// though nothing printed a diagnostic.
func TestBuzzToolNonzeroMainIsAToolError(t *testing.T) {
	useStandInMagus(t)
	tool := &buzzTool{root: t.TempDir(), timeout: time.Minute}

	res := callBuzz(t, tool, map[string]any{"script": `fun main(args: [str]) > int { return 3; }`, "write": true})
	assert.True(t, res.IsError)
	assert.Equal(t, "magus buzz exited with code 3", allText(res))
}

func TestBuzzToolTimesOut(t *testing.T) {
	useStandInMagus(t)
	tool := &buzzTool{root: t.TempDir(), timeout: 500 * time.Millisecond}

	res := callBuzz(t, tool, map[string]any{"script": `fun main(args: [str]) > void { while (true) {} }`, "write": true})
	assert.True(t, res.IsError)
	assert.Equal(t, "mcp: magus_buzz: timed out after 500ms", allText(res))
}

// Nothing is forked without write=true: the script that would have written the file never
// starts, because `magus buzz` has no mode that could have stopped it.
func TestBuzzToolRefusesWithoutWrite(t *testing.T) {
	root := t.TempDir()
	prev := magusExecutable
	magusExecutable = func() (string, error) {
		t.Error("a refused call located the magus binary")
		return "", os.ErrNotExist
	}
	t.Cleanup(func() { magusExecutable = prev })
	tool := &buzzTool{root: root, timeout: time.Minute}
	script := `import "fs"; fun main(args: [str]) > void { fs\write("written.txt", data: "x"); }`

	for name, args := range map[string]map[string]any{
		"omitted": {"script": script},
		"false":   {"script": script, "write": false},
	} {
		res := callBuzz(t, tool, args)
		assert.True(t, res.IsError, name)
		assert.Contains(t, allText(res), "has no read-only mode", name)
	}
	assert.NoFileExists(t, filepath.Join(root, "written.txt"))
}

func TestBuzzToolArgv(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "ok.buzz"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "notes.txt"), nil, 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(root, "dir.buzz"), 0o755))
	tool := &buzzTool{root: root}

	for name, tc := range map[string]struct {
		params  map[string]any
		want    []string
		wantErr string
	}{
		"inline":            {map[string]any{"script": "x"}, []string{"buzz", "-e", "x", "--"}, ""},
		"file":              {map[string]any{"path": "ok.buzz"}, []string{"buzz", "ok.buzz", "--"}, ""},
		"absolute file":     {map[string]any{"path": filepath.Join(root, "ok.buzz")}, []string{"buzz", "ok.buzz", "--"}, ""},
		"args as a string":  {map[string]any{"script": "x", "args": " a  -b "}, []string{"buzz", "-e", "x", "--", "a", "-b"}, ""},
		"args as an array":  {map[string]any{"script": "x", "args": []any{"a b", "--c"}}, []string{"buzz", "-e", "x", "--", "a b", "--c"}, ""},
		"both":              {map[string]any{"script": "x", "path": "ok.buzz"}, nil, "takes script or path, not both"},
		"neither":           {map[string]any{}, nil, "needs script (inline source) or path"},
		"not buzz":          {map[string]any{"path": "notes.txt"}, nil, `path "notes.txt" is not a .buzz file`},
		"outside":           {map[string]any{"path": "../escape.buzz"}, nil, `path "../escape.buzz" is outside the workspace`},
		"missing":           {map[string]any{"path": "gone.buzz"}, nil, "no such file"},
		"a directory":       {map[string]any{"path": "dir.buzz"}, nil, `path "dir.buzz" is not a regular file`},
		"args of a number":  {map[string]any{"script": "x", "args": 3.0}, nil, "args is float64"},
		"an array of mixed": {map[string]any{"script": "x", "args": []any{"a", true}}, nil, "args holds bool"},
	} {
		got, err := tool.argv(tc.params)
		if tc.wantErr != "" {
			assert.ErrorContains(t, err, tc.wantErr, name)
			continue
		}
		require.NoError(t, err, name)
		assert.Equal(t, tc.want, got, name)
	}
}
