package std

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/run"
	"github.com/egladman/magus/types"
)

//go:generate go run ../cmd/magus-bindings-gen -module magus -lang buzz -out ../hostbuzz/gen/magus.go

func init() { Register(Magus) }

// Magus declares the host-declarable subset of the magus module. The remaining
// methods (target, dispatch, deps, pry, register) are VM-infrastructure:
// they manipulate the per-VM target registry and store/invoke VM-side
// function values, so they cannot share a Go Impl across backends and remain
// as hand-written trampolines in bindings/magus.go.
var Magus = Module{
	Name: "magus",
	Doc:  "Magus core primitives.",
	Methods: []Method{
		{
			Name: "cmd",
			Doc:  "Run the magus binary with args. Output streams live and is captured; returns {stdout, stderr, code, ok}. Raises if the invocation exits non-zero (catch it for non-fatal use).",
			Args: []Arg{
				{Name: "args", Type: TypeStringSlice},
			},
			Returns: []Ret{{Type: TypeAnyMap}},
			Impl:    MagusCmd,
		},
		{
			Name: "bust_cache",
			Doc:  "Invalidate the build cache. Escape hatch — prefer modeling missing inputs as Sources. No arg clears all; a project path clears one project.",
			Args: []Arg{
				{Name: "project_path", Type: TypeString, Optional: true},
			},
			Returns: nil,
			Impl:    MagusBustCache,
		},
	},
}

// MagusBustCache invalidates cached build entries. When projectPath is empty
// all manifests are cleared; otherwise only entries for that project are removed.
// A structured warning is always emitted — this is an escape hatch, not routine.
func MagusBustCache(ctx context.Context, projectPath string) error {
	slog.Warn("magus.bust_cache called — consider modeling the missing input as a Source instead",
		"project_path", projectPath)
	c := cache.CacheFromContext(ctx)
	if c == nil {
		return nil // no cache in context (parse mode, tests)
	}
	if projectPath == "" {
		return c.Clean(ctx)
	}
	return c.Clean(ctx, projectPath)
}

// MagusCmd runs a nested magus invocation with args, yielding the caller's
// concurrency slot for the duration so the child can run. Output streams live and
// is captured: on success it returns the same {stdout, stderr, code, ok} record as
// os.exec, so a magusfile can read a subcommand's output (e.g. `magus describe
// graph -o markdown` to generate MAGUS.md). It raises (non-nil error, nil record)
// when the child can't launch or exits non-zero, mirroring os.exec — scripts that
// want non-fatal behavior catch it.
func MagusCmd(ctx context.Context, args []string) (types.ExecResult, error) {
	self, err := os.Executable()
	if err != nil {
		return types.ExecResult{}, fmt.Errorf("magus.cmd: executable: %w", err)
	}

	// Re-inject the daemon socket vars: childEnv withholds them from subprocesses
	// (the socket is unauthenticated — MGS2008), but a nested magus is a legitimate
	// recursive invocation that needs daemon access. Passed as Env overrides, which
	// childEnv layers last so they win; MAGUS/MAGUS_LEVEL are added by childEnv.
	var env []string
	for _, k := range []string{"MAGUS_DAEMON_SOCKET", "MAGUS_DAEMON_ADDRESS"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
			slog.InfoContext(ctx, types.FormatDiagnostic(types.DaemonSocketWithheld,
				"daemon socket injected into recursive magus invocation"), "var", k)
		}
	}

	lim := cache.LimiterFromContext(ctx)
	var rec types.ExecResult
	var cmdErr error
	runFn := func() error {
		res, err := run.Exec(ctx, self, args, run.ExecSpec{Env: env, Capture: true})
		switch {
		case err != nil && errors.Is(err, &types.DiagnosticError{Code: types.ExecDenied}):
			cmdErr = err
		case res.Code != 0 && !res.Started:
			// The child never launched (binary not found, permission, ctx cancelled
			// before exec) — surface the real cause, not a fabricated "code -1".
			// Mirrors os.exec's runResult.
			cmdErr = fmt.Errorf("magus.cmd: %s: %w", strings.Join(args, " "), err)
		case res.Code != 0:
			cmdErr = fmt.Errorf("magus.cmd: %s exited with code %d", strings.Join(args, " "), res.Code)
		default:
			rec = types.ExecResult{
				Stdout: strings.TrimSpace(res.Stdout),
				Stderr: strings.TrimSpace(res.Stderr),
				Code:   res.Code,
				OK:     res.Code == 0,
			}
		}
		return nil
	}
	if err := proc.RunChildSync(ctx, lim, runFn); err != nil {
		return types.ExecResult{}, fmt.Errorf("magus.cmd: %w", err)
	}
	return rec, cmdErr
}
