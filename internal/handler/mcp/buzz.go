package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp/transform"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

const buzzDefaultTimeout = 30 * time.Second

// The MCP server re-executes its own binary so a looping script can be killed
// without blocking the server's protocol stream.
var magusExecutable = os.Executable

type buzzTool struct {
	root    string
	timeout time.Duration
}

func (t *buzzTool) Name() string { return hint.ToolBuzz.String() }

func (t *buzzTool) Invoke(ctx context.Context, call spells.InvokeRequest) (spells.InvokeResponse, error) {
	req, err := t.request(call.Params)
	if err != nil {
		return spells.InvokeResponse{}, err
	}
	result, err := runWorker[transform.Result](ctx, worker{
		tool:          hint.ToolBuzz,
		code:          types.MCPBuzzFailed,
		root:          t.root,
		env:           []string{transform.WorkerEnv + "=1"},
		maxRequest:    transform.MaxRequestBytes,
		shorten:       "shorten the script, args or input",
		timeout:       t.timeout,
		timeoutRemedy: "simplify it or run it through the Buzz CLI",
	}, req)
	if err != nil {
		return spells.InvokeResponse{}, err
	}
	return spells.InvokeResponse{Data: result}, nil
}

func (t *buzzTool) request(params map[string]any) (transform.Request, error) {
	script, err := scriptSource(params, t.root, hint.ToolBuzz, types.MCPBuzzFailed, transform.MaxSourceBytes)
	if err != nil {
		return transform.Request{}, err
	}
	args, err := stringArgs(params["args"], hint.ToolBuzz, types.MCPBuzzFailed, transform.MaxArgs, transform.MaxArgBytes)
	if err != nil {
		return transform.Request{}, err
	}
	var input json.RawMessage
	if value, found := params["input"]; found {
		input, err = json.Marshal(value)
		if err != nil {
			return transform.Request{}, types.DiagnosticErrorf(types.MCPBuzzFailed, "%s: encode input: %v", hint.ToolBuzz, err)
		}
		if len(input) > transform.MaxInputBytes {
			return transform.Request{}, types.DiagnosticErrorf(types.MCPBuzzFailed, "%s: input exceeds %d bytes; pass a smaller value", hint.ToolBuzz, transform.MaxInputBytes)
		}
	}
	return transform.Request{Script: script, Input: input, Args: args}, nil
}

// worker is one forked Buzz worker: how to start it, what bounds it, and whose
// name and diagnostic code its errors carry.
type worker struct {
	tool       hint.ToolName
	code       types.DiagnosticCode
	root       string
	env        []string
	maxRequest int
	// shorten is the remedy for a request over maxRequest.
	shorten string
	// timeout bounds the call; zero leaves it to ctx.
	timeout       time.Duration
	timeoutRemedy string
}

// runWorker sends req to a fresh worker process and decodes its one response.
// A worker failure that is already a diagnostic is returned as written, so its
// code and remedy reach the caller once.
func runWorker[R any](ctx context.Context, w worker, req any) (R, error) {
	var zero R
	wire, err := json.Marshal(req)
	if err != nil {
		return zero, types.DiagnosticErrorf(w.code, "%s: encode request: %v", w.tool, err)
	}
	if len(wire) > w.maxRequest {
		return zero, types.DiagnosticErrorf(w.code, "%s: request exceeds %d bytes; %s", w.tool, w.maxRequest, w.shorten)
	}
	exe, err := magusExecutable()
	if err != nil {
		return zero, types.DiagnosticErrorf(w.code, "%s: locate magus executable: %v", w.tool, err)
	}
	if w.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, w.timeout)
		defer cancel()
	}
	res, err := run.Exec(ctx, exe, []string{"buzz"}, run.ExecOptions{
		Dir:     w.root,
		Env:     w.env,
		Stdin:   string(wire),
		Capture: true,
		Quiet:   true,
	})
	stderr := strings.TrimSpace(res.Stderr)
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return zero, types.DiagnosticErrorf(w.code, "%s: script timed out after %s; %s", w.tool, w.timeout, w.timeoutRemedy)
	case ctx.Err() != nil:
		return zero, types.DiagnosticErrorf(w.code, "%s: script cancelled", w.tool)
	case !res.Started:
		return zero, types.DiagnosticErrorf(w.code, "%s: start magus executable %q: %v", w.tool, exe, err)
	case res.Code != 0 && strings.HasPrefix(stderr, "[MGS"):
		return zero, errors.New(stderr)
	case res.Code != 0:
		return zero, types.DiagnosticErrorf(w.code, "%s: script failed: %s", w.tool, stderr)
	}
	var result R
	if err := json.UnmarshalStrict([]byte(res.Stdout), &result); err != nil {
		return zero, types.DiagnosticErrorf(w.code, "%s: worker returned an invalid response: %v", w.tool, err)
	}
	return result, nil
}

// scriptSource reads the script a call names: inline under script, or a
// workspace .buzz file under path, exactly one of them.
func scriptSource(params map[string]any, root string, tool hint.ToolName, code types.DiagnosticCode, maxBytes int) (string, error) {
	script, _ := params["script"].(string)
	path, _ := params["path"].(string)
	switch {
	case script != "" && path != "":
		return "", types.DiagnosticErrorf(code, "%s: provide script or path, not both", tool)
	case script == "" && path == "":
		return "", types.DiagnosticErrorf(code, "%s: provide script or a workspace .buzz path", tool)
	case path != "":
		return readWorkspaceScript(root, path, tool, code, maxBytes)
	case len(script) > maxBytes:
		return "", types.DiagnosticErrorf(code, "%s: script exceeds %d bytes; split it or run it through the Buzz CLI", tool, maxBytes)
	}
	return script, nil
}

func readWorkspaceScript(root, path string, tool hint.ToolName, code types.DiagnosticCode, maxBytes int) (string, error) {
	if !strings.EqualFold(filepath.Ext(path), ".buzz") {
		return "", types.DiagnosticErrorf(code, "%s: path %q must name a .buzz file", tool, path)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", types.DiagnosticErrorf(code, "%s: resolve workspace root: %v", tool, err)
	}
	name := path
	if !filepath.IsAbs(name) {
		name = filepath.Join(resolved, name)
	}
	name, err = filepath.EvalSymlinks(name)
	if err != nil {
		return "", types.DiagnosticErrorf(code, "%s: resolve script %q: %v", tool, path, err)
	}
	rel, err := filepath.Rel(resolved, name)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", types.DiagnosticErrorf(code, "%s: path %q is outside the workspace; use an inline script instead", tool, path)
	}
	info, err := os.Stat(name)
	if err != nil {
		return "", types.DiagnosticErrorf(code, "%s: stat script %q: %v", tool, path, err)
	}
	if !info.Mode().IsRegular() {
		return "", types.DiagnosticErrorf(code, "%s: path %q must name a regular file", tool, path)
	}
	if info.Size() > int64(maxBytes) {
		return "", types.DiagnosticErrorf(code, "%s: script %q exceeds %d bytes", tool, path, maxBytes)
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return "", types.DiagnosticErrorf(code, "%s: read script %q: %v", tool, path, err)
	}
	return string(data), nil
}

// stringArgs reads the args param as decoded JSON: absent, or an array of at
// most maxArgs strings of at most maxBytes each.
func stringArgs(value any, tool hint.ToolName, code types.DiagnosticCode, maxArgs, maxBytes int) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	args, ok := value.([]any)
	if !ok {
		return nil, types.DiagnosticErrorf(code, "%s: args must be an array of strings, got %T", tool, value)
	}
	if len(args) > maxArgs {
		return nil, types.DiagnosticErrorf(code, "%s: more than %d args", tool, maxArgs)
	}
	out := make([]string, len(args))
	for i, arg := range args {
		text, ok := arg.(string)
		if !ok {
			return nil, types.DiagnosticErrorf(code, "%s: args[%d] must be a string, got %T", tool, i, arg)
		}
		if len(text) > maxBytes {
			return nil, types.DiagnosticErrorf(code, "%s: args[%d] exceeds %d bytes", tool, i, maxBytes)
		}
		out[i] = text
	}
	return out, nil
}

var _ spells.Driver = (*buzzTool)(nil)
