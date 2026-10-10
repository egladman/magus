package transform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/json"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
)

const (
	// WorkerEnv selects this restricted internal protocol before CLI startup.
	// Setting it can only remove the ordinary CLI's capabilities.
	WorkerEnv      = "MAGUS_MCP_BUZZ_PURE"
	MaxSourceBytes = 128 << 10
	MaxInputBytes  = 1 << 20
	MaxArgs        = 64
	MaxArgBytes    = 8 << 10
	MaxOutputBytes = 1 << 20
	// MaxRequestBytes bounds the encoded Request: the largest script and args,
	// each allowed to double under JSON escaping, plus the input, which is JSON
	// already.
	MaxRequestBytes = 2*(MaxSourceBytes+MaxArgs*MaxArgBytes) + MaxInputBytes + 1<<10
)

// tool prefixes every error this package reports.
var tool = hint.ToolBuzz.String()

// Request is the private wire format between the MCP server and its forked
// worker. Input is one JSON value, passed to transform as a Buzz value.
type Request struct {
	Script string          `json:"script"`
	Input  json.RawMessage `json:"input,omitempty"`
	Args   []string        `json:"args,omitempty"`
}

// Result preserves the MCP tool's stdout/json shape. JSON is transform's return
// value; Stdout is only text printed through the safe std module.
type Result struct {
	Stdout string          `json:"stdout"`
	JSON   json.RawMessage `json:"json"`
}

// Serve is the worker's whole protocol: one Request from in, one Result line on
// out. A failure is written to errOut and returns 1. It runs before workspace
// startup, so the script cannot cause a magusfile load or gain the ordinary
// CLI's host bindings.
func Serve(ctx context.Context, in io.Reader, out, errOut io.Writer) int {
	_ = os.Unsetenv(WorkerEnv)
	data, err := io.ReadAll(io.LimitReader(in, MaxRequestBytes+1))
	if err != nil {
		fmt.Fprintf(errOut, "%s: read request: %v\n", tool, err)
		return 1
	}
	if len(data) > MaxRequestBytes {
		fmt.Fprintf(errOut, "%s: request is too large\n", tool)
		return 1
	}
	var req Request
	if err := json.UnmarshalStrict(data, &req); err != nil {
		fmt.Fprintf(errOut, "%s: decode request: %v\n", tool, err)
		return 1
	}
	result, err := Run(ctx, req)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		fmt.Fprintf(errOut, "%s: encode response: %v\n", tool, err)
		return 1
	}
	if _, err := fmt.Fprintln(out, string(encoded)); err != nil {
		fmt.Fprintf(errOut, "%s: write response: %v\n", tool, err)
		return 1
	}
	return 0
}

// Run evaluates one program in a fresh session. The allowlist is explicit
// (bindings.PureStdlib): a newly added upstream module does not become an MCP
// capability.
func Run(ctx context.Context, req Request) (Result, error) {
	if req.Script == "" || len(req.Script) > MaxSourceBytes {
		return Result{}, fmt.Errorf("%s: script must be 1-%d bytes", tool, MaxSourceBytes)
	}
	if len(req.Input) > MaxInputBytes {
		return Result{}, fmt.Errorf("%s: input exceeds %d bytes", tool, MaxInputBytes)
	}
	if len(req.Args) > MaxArgs {
		return Result{}, fmt.Errorf("%s: more than %d arguments", tool, MaxArgs)
	}
	for i, arg := range req.Args {
		if len(arg) > MaxArgBytes {
			return Result{}, fmt.Errorf("%s: args[%d] exceeds %d bytes", tool, i, MaxArgBytes)
		}
	}

	input := vm.Null
	if len(req.Input) > 0 {
		var err error
		if input, err = buzzstd.JSONDecodeValue(req.Input); err != nil {
			return Result{}, fmt.Errorf("%s: decode input: %w", tool, err)
		}
	}

	printed := &limitedBuffer{limit: MaxOutputBytes}
	sess := buzz.NewSession(ctx, buzz.WithEmbedded(), buzz.WithoutFileImports(), buzz.WithoutFFI())
	defer func() { _ = sess.Close() }()
	sess.RejectImport("magus", types.DiagnosticErrorf(types.MCPBuzzFailed,
		"%s: transforms supplied data; use the %s tool for workspace queries and actions", tool, hint.ToolClient))
	stdlib, withheld := bindings.PureStdlib()
	names := make([]string, len(stdlib))
	for i, module := range stdlib {
		names[i] = module.Name
	}
	denial := types.DiagnosticErrorf(types.MCPBuzzFailed,
		"%s: that import is outside this tool; it provides only %s; use the Buzz CLI for the rest", tool, strings.Join(names, ", "))
	for _, name := range withheld {
		sess.RejectImport(name, denial)
	}
	if err := sess.Provide(buzz.ModuleEnv{Ctx: ctx, Out: printed}, stdlib...); err != nil {
		return Result{}, fmt.Errorf("%s: install modules: %w", tool, err)
	}
	if err := sess.Exec(ctx, req.Script); err != nil {
		return Result{}, wrapScriptErr(err, "")
	}
	transform := sess.GetGlobal("transform")
	if !transform.IsFun() {
		return Result{}, fmt.Errorf("%s: script must define transform(input: any, args: [str])", tool)
	}
	args := make([]vm.Value, len(req.Args))
	for i, arg := range req.Args {
		args[i] = vm.StrValue(arg)
	}
	value, err := sess.CallValue(ctx, transform, []vm.Value{input, vm.ListValue(args)})
	if err != nil {
		return Result{}, wrapScriptErr(err, "transform")
	}
	if printed.exceeded {
		return Result{}, fmt.Errorf("%s: printed output exceeds %d bytes", tool, MaxOutputBytes)
	}
	encoded, err := buzzstd.JSONEncode(value)
	if err != nil {
		return Result{}, fmt.Errorf("%s: encode result: %w", tool, err)
	}
	if len(encoded) > MaxOutputBytes {
		return Result{}, fmt.Errorf("%s: result exceeds %d bytes", tool, MaxOutputBytes)
	}
	return Result{Stdout: printed.String(), JSON: json.RawMessage(encoded)}, nil
}

// wrapScriptErr returns a coded diagnostic from err's chain as itself, so its
// remedy reaches the caller once, and prefixes anything else with the tool name.
func wrapScriptErr(err error, stage string) error {
	var diag *types.DiagnosticError
	if errors.As(err, &diag) {
		return diag
	}
	if stage == "" {
		return fmt.Errorf("%s: %w", tool, err)
	}
	return fmt.Errorf("%s: %s: %w", tool, stage, err)
}

type limitedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := max(0, b.limit-b.Len())
	if len(p) > remaining {
		b.exceeded = true
	}
	_, _ = b.Buffer.Write(p[:min(len(p), remaining)])
	return len(p), nil
}
