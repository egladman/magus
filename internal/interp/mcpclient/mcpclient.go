package mcpclient

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
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/trail"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	"github.com/egladman/magus/libs/gopherbuzz/token"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
)

const (
	// WorkerEnv selects this worker before CLI startup. Setting it can only
	// narrow the process to the client surface.
	WorkerEnv = "MAGUS_MCP_CLIENT"
	// LeaseEnv carries the MCP caller's job lease into the forked worker. An
	// empty value is a stamped absence: the worker must not inherit the server
	// process's lease. The worker copies it onto BAGGAGE and unsets it.
	LeaseEnv = "MAGUS_MCP_CLIENT_LEASE"
	// HostEnv carries the MCP client's name for the activity trail. The worker
	// stamps it and unsets it.
	HostEnv = "MAGUS_MCP_CLIENT_HOST"

	MaxSourceBytes = 128 << 10
	MaxArgs        = 64
	MaxArgBytes    = 8 << 10
	MaxOutputBytes = 1 << 20
	// MaxRequestBytes bounds the encoded Request: the largest script and args,
	// each allowed to double under JSON escaping.
	MaxRequestBytes = 2*(MaxSourceBytes+MaxArgs*MaxArgBytes) + 1<<10
)

// tool prefixes every error this package reports.
var tool = hint.ToolClient.String()

// entryName is what the script's main runs as. Upstream Buzz lets main return
// only void or an exit code, and the client returns main's value instead.
const entryName = "client_main"

// Request is the private wire format between the MCP server and its forked worker.
type Request struct {
	Script string   `json:"script"`
	Args   []string `json:"args,omitempty"`
}

// Result is main's return value. JSON is that value; Stdout is text printed
// through the safe std module.
type Result struct {
	Stdout string          `json:"stdout"`
	JSON   json.RawMessage `json:"json"`
}

// Serve is the worker's whole protocol: one Request from in, the workspace
// opened by open, one Result line on out. A failure is written to errOut and
// returns 1. WorkerEnv is unset before anything else, so a magus the script
// starts runs as the ordinary CLI rather than as another worker.
func Serve(ctx context.Context, in io.Reader, out, errOut io.Writer, open func(context.Context) (context.Context, error)) int {
	_ = os.Unsetenv(WorkerEnv)
	InstallCallerLease()
	host := os.Getenv(HostEnv)
	_ = os.Unsetenv(HostEnv)

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
	ctx, err = open(ctx)
	if err != nil {
		fmt.Fprintf(errOut, "%s: open workspace: %v\n", tool, err)
		return 1
	}
	if host != "" {
		ctx = trail.ContextWithHost(ctx, host)
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

// InstallCallerLease copies LeaseEnv onto the environment the job store reads,
// then removes LeaseEnv so a script cannot see the transport channel. A missing
// LeaseEnv leaves the environment alone, which is the stdio case: the process
// is the caller. An empty LeaseEnv is a stamped absence, so the worker does not
// inherit a lease the server process happened to carry.
func InstallCallerLease() {
	lease, ok := os.LookupEnv(LeaseEnv)
	if !ok {
		return
	}
	_ = os.Unsetenv(LeaseEnv)
	_ = os.Setenv(job.EnvStampedLease, "1")
	if lease == "" {
		_ = os.Unsetenv(trail.EnvBaggage)
		return
	}
	_ = os.Setenv(trail.EnvBaggage, trail.BaggageLease+"="+lease)
}

// Run evaluates one program. The workspace, when one is open, is already on ctx.
// The allowlist is magus\ plus pure modules; file imports and native FFI are off.
// A failure that already carries a diagnostic code is returned as that
// diagnostic, so its remedy reaches the caller once.
func Run(ctx context.Context, req Request) (Result, error) {
	if req.Script == "" || len(req.Script) > MaxSourceBytes {
		return Result{}, fmt.Errorf("%s: script must be 1-%d bytes", tool, MaxSourceBytes)
	}
	if len(req.Args) > MaxArgs {
		return Result{}, fmt.Errorf("%s: more than %d arguments", tool, MaxArgs)
	}
	for i, arg := range req.Args {
		if len(arg) > MaxArgBytes {
			return Result{}, fmt.Errorf("%s: args[%d] exceeds %d bytes", tool, i, MaxArgBytes)
		}
	}

	printed := &limitedBuffer{limit: MaxOutputBytes}
	sess := buzz.NewSession(ctx, buzz.WithoutFileImports(), buzz.WithoutFFI())
	defer func() { _ = sess.Close() }()
	stdlib, _ := bindings.PureStdlib()
	names := make([]string, len(stdlib))
	for i, module := range stdlib {
		names[i] = module.Name
	}
	denial := types.DiagnosticErrorf(types.MCPClientFailed,
		"%s: that import is outside this tool; it runs magus\\ and pure modules only (%s, and the WASM host modules except env)", tool, strings.Join(names, ", "))
	for _, path := range bindings.ClientDeniedImportPaths() {
		sess.RejectImport(path, denial)
	}
	if err := bindings.InstallClient(ctx, sess, printed); err != nil {
		return Result{}, fmt.Errorf("%s: install modules: %w", tool, err)
	}
	if err := sess.Exec(ctx, renameEntry(req.Script)); err != nil {
		return Result{}, wrapScriptErr(err, "")
	}
	entry := sess.GetGlobal(entryName)
	if !entry.IsFun() {
		return Result{}, fmt.Errorf("%s: script must define main(args: [str])", tool)
	}
	args := make([]vm.Value, len(req.Args))
	for i, arg := range req.Args {
		args[i] = vm.StrValue(arg)
	}
	value, err := sess.CallValue(ctx, entry, []vm.Value{vm.ListValue(args)})
	if err != nil {
		return Result{}, wrapScriptErr(err, "main: ")
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

// wrapScriptErr returns a coded diagnostic from err's chain as itself and
// prefixes anything else with the tool name.
func wrapScriptErr(err error, stage string) error {
	var diag *types.DiagnosticError
	if errors.As(err, &diag) {
		return diag
	}
	return fmt.Errorf("%s: %s%w", tool, stage, err)
}

// renameEntry renames the top-level `fun main` declaration to entryName and
// binds main to it again, so a recursive call still resolves. Every other main
// (in strings, comments, methods and nested functions) is left as written.
// Source that does not lex is returned unchanged, so Exec reports the error at
// its real position.
func renameEntry(src string) string {
	toks, err := token.Tokenize(src)
	if err != nil {
		return src
	}
	depth := 0
	for i, tok := range toks {
		switch tok.Kind {
		case token.LBrace:
			depth++
		case token.RBrace:
			depth--
		case token.Fun:
			if depth != 0 || i+1 == len(toks) {
				continue
			}
			name := toks[i+1]
			if name.Kind != token.Ident || name.Val != "main" {
				continue
			}
			off, ok := byteOffset(src, name.Line, name.Col)
			if !ok || !strings.HasPrefix(src[off:], "main") {
				return src
			}
			return src[:off] + entryName + src[off+len("main"):] + "\nfinal main = " + entryName + ";\n"
		}
	}
	return src
}

// byteOffset converts the lexer's 1-based line and byte column to an offset in src.
func byteOffset(src string, line, col int) (int, bool) {
	off := 0
	for range line - 1 {
		nl := strings.IndexByte(src[off:], '\n')
		if nl < 0 {
			return 0, false
		}
		off += nl + 1
	}
	off += col - 1
	if off < 0 || off > len(src) {
		return 0, false
	}
	return off, true
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
