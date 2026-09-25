package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/spells"
)

// buzzDefaultTimeout bounds a script when the workspace sets no target_timeout. A runaway
// guard like that setting, not a budget: the script is code an agent wrote a moment ago.
const buzzDefaultTimeout = 5 * time.Minute

// magusExecutable locates the binary a script is forked through. A variable so a test can
// point it at a stand-in, since the test binary is not magus.
var magusExecutable = os.Executable

// buzzCLI names the verb in replies on its PATH spelling: a server started as ./magus
// would otherwise send a path that resolves against the client's directory.
var buzzCLI = hint.Buzz.StringAs(hint.DefaultBinaryName)

// buzzReadOnlyFlag is what a call without write=true adds to the command line.
const buzzReadOnlyFlag = "--read-only"

// buzzTool runs a Buzz program by forking `magus buzz` rather than opening a session in
// this process. In-process, io\stdin and io\stdout are the server's own stdio, which
// over ServeStdio IS the protocol stream, and a runaway loop could only be abandoned,
// never killed.
type buzzTool struct {
	root    string
	timeout time.Duration
}

// buzzResult is a finished run that exited 0. JSON is stdout parsed, present only when
// the whole of stdout is one JSON value.
type buzzResult struct {
	ExitCode int             `json:"exit_code"`
	Stdout   string          `json:"stdout"`
	Stderr   string          `json:"stderr,omitempty"`
	JSON     json.RawMessage `json:"json,omitempty"`
}

func (t *buzzTool) Name() string { return hint.ToolBuzz.String() }

func (t *buzzTool) Invoke(ctx context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
	argv, err := t.argv(req.Params)
	if err != nil {
		return spells.InvokeResponse{}, err
	}
	exe, err := magusExecutable()
	if err != nil {
		return spells.InvokeResponse{}, fmt.Errorf("mcp: %s: locate magus: %w", hint.ToolBuzz, err)
	}

	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	// Quiet: the ctx output writers default to this process's stdout, the protocol stream
	// under ServeStdio.
	res, err := run.Exec(ctx, exe, argv, run.ExecOptions{
		Dir:     t.root,
		Stdin:   paramString(req.Params, "stdin", ""),
		Capture: true,
		Quiet:   true,
	})
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return spells.InvokeResponse{}, fmt.Errorf("mcp: %s: timed out after %s", hint.ToolBuzz, t.timeout)
	case !res.Started:
		return spells.InvokeResponse{}, fmt.Errorf("mcp: %s: start %s: %w", hint.ToolBuzz, exe, err)
	case res.Code != 0:
		return spells.InvokeResponse{}, buzzExitError(res)
	}
	out := buzzResult{Stdout: res.Stdout, Stderr: res.Stderr}
	if trimmed := strings.TrimSpace(res.Stdout); trimmed != "" && json.Valid([]byte(trimmed)) {
		out.JSON = json.RawMessage(trimmed)
	}
	return spells.InvokeResponse{Data: out}, nil
}

// argv builds the `magus buzz` command line, always ending in `--` so an argument that
// looks like a flag reaches the script instead of being parsed by magus. It carries
// --read-only unless the call passed write=true.
func (t *buzzTool) argv(params map[string]any) ([]string, error) {
	script := paramString(params, "script", "")
	path := paramString(params, "path", "")
	argv := []string{hint.Buzz.Leaf()}
	if !paramBool(params, "write", false) {
		argv = append(argv, buzzReadOnlyFlag)
	}
	switch {
	case script != "" && path != "":
		return nil, fmt.Errorf("mcp: %s takes script or path, not both", hint.ToolBuzz)
	case script != "":
		argv = append(argv, "-e", script)
	case path != "":
		rel, err := t.workspaceScript(path)
		if err != nil {
			return nil, err
		}
		argv = append(argv, rel)
	default:
		return nil, fmt.Errorf("mcp: %s needs script (inline source) or path (a .buzz file)", hint.ToolBuzz)
	}
	args, err := buzzArgs(params["args"])
	if err != nil {
		return nil, err
	}
	return append(append(argv, "--"), args...), nil
}

// workspaceScript resolves path to an existing .buzz file under the workspace root and
// returns it root-relative, which is how the CLI names it in a diagnostic.
func (t *buzzTool) workspaceScript(path string) (string, error) {
	if !strings.EqualFold(filepath.Ext(path), ".buzz") {
		return "", fmt.Errorf("mcp: %s: path %q is not a .buzz file", hint.ToolBuzz, path)
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(t.root, path)
	}
	rel, err := filepath.Rel(t.root, filepath.Clean(abs))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("mcp: %s: path %q is outside the workspace", hint.ToolBuzz, path)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("mcp: %s: %w", hint.ToolBuzz, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("mcp: %s: path %q is not a regular file", hint.ToolBuzz, path)
	}
	return rel, nil
}

// buzzArgs reads the args parameter: a space-separated string, or an array of strings
// for an argument that holds whitespace.
func buzzArgs(v any) ([]string, error) {
	switch a := v.(type) {
	case nil:
		return nil, nil
	case string:
		return strings.Fields(a), nil
	case []any:
		out := make([]string, 0, len(a))
		for _, item := range a {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("mcp: %s: args holds %T, want strings", hint.ToolBuzz, item)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("mcp: %s: args is %T, want a string or an array of strings", hint.ToolBuzz, v)
	}
}

// buzzExitError carries a failed run's diagnostic. A compile or runtime error prints it on
// stderr; a main returning a nonzero int prints nothing, so stdout rides along too.
func buzzExitError(res run.ExecResult) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s exited with code %d", buzzCLI, res.Code)
	if stderr := strings.TrimSpace(res.Stderr); stderr != "" {
		b.WriteString("\n" + stderr)
	}
	if stdout := strings.TrimSpace(res.Stdout); stdout != "" {
		b.WriteString("\nstdout:\n" + stdout)
	}
	return errors.New(b.String())
}

var _ spells.Driver = (*buzzTool)(nil)
