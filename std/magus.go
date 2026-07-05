//go:build !wasm

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
	"github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/types"
)

//go:generate go run ../cmd/magus-utils bindings -module magus -lang buzz -out ../host/gen/magus.go

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
			Doc:  "Escape hatch: run `magus <args>` for any subcommand, in the target's project directory. Prefer the dedicated methods (run, describe, insight, doctor) when one exists — magus.cmd warns when args name a subcommand that has one. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.quiet captures the output without echoing it to the console.",
			Args: []Arg{
				{Name: "args", Type: TypeStringSlice},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap}},
			Impl:    MagusCmd,
		},
		{
			Name: "run",
			Doc:  "Run `magus run <args>` recursively in the target's project directory and capture its output. Child invocations share the parent's concurrency budget over the local socket. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.quiet captures the output without echoing it to the console.",
			Args: []Arg{
				{Name: "args", Type: TypeStringSlice},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap}},
			Impl:    MagusRun,
		},
		{
			Name: "describe",
			Doc:  "Run `magus describe <args>` in the target's project directory and capture its output. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.quiet captures the output without echoing it to the console. Unlike a raw binary call, the working directory is always the contextual project dir, so a nested project describes itself, not the root workspace.",
			Args: []Arg{
				{Name: "args", Type: TypeStringSlice},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap}},
			Impl:    MagusDescribe,
		},
		{
			Name: "insight",
			Doc:  "Run `magus insight <args>` in the target's project directory and capture its output. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.quiet captures the output without echoing it to the console.",
			Args: []Arg{
				{Name: "args", Type: TypeStringSlice},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap}},
			Impl:    MagusInsight,
		},
		{
			Name: "doctor",
			Doc:  "Run `magus doctor <args>` in the target's project directory and capture its output. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.quiet captures the output without echoing it to the console.",
			Args: []Arg{
				{Name: "args", Type: TypeStringSlice},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap}},
			Impl:    MagusDoctor,
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
		{
			Name:     "has_charm",
			BuzzName: "has_charm",
			Doc:      "True when execution charm `name` is active, letting a target body branch on a charm carried in context (e.g. has_charm(\"rw\")).",
			Args: []Arg{
				{Name: "name", Type: TypeString},
			},
			Returns: []Ret{{Type: TypeBool}},
			Impl:    MagusHasCharm,
		},
	},
}

// MagusHasCharm reports whether the execution charm name is active in ctx. It
// backs magus.has_charm, the read side of the charm system: a function target
// can publish conditionally on has_charm("rw") or branch on a custom charm.
func MagusHasCharm(ctx context.Context, name string) (bool, error) {
	return types.HasCharm(ctx, name), nil
}

// MagusBustCache invalidates cached build entries. When projectPath is empty
// all manifests are cleared; otherwise only entries for that project are removed.
// A structured warning is always emitted: this is an escape hatch, not routine.
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

// typedMagusSubcommands are the magus subcommands that have a dedicated,
// typed magus.<name>(...) method. magus.cmd warns when its first arg names one,
// nudging authors toward the clearer, signature-stable wrapper.
var typedMagusSubcommands = map[string]bool{
	"run": true, "describe": true, "insight": true, "doctor": true,
}

// MagusCmd is the escape hatch: it runs `magus <args>` for any subcommand. It
// serves subcommands without a dedicated wrapper (status, affected, ...) but
// warns when args[0] names one that has, nudging toward the typed method. Like
// the typed methods it runs in the contextual project dir; see runMagus.
func MagusCmd(ctx context.Context, args []string, opts map[string]any) (types.ExecResult, error) {
	warnIfTypedSubcommand(ctx, args)
	return runMagus(ctx, "cmd", args, opts)
}

// warnIfTypedSubcommand warns when args[0] names a subcommand with a dedicated
// magus.<name>(...) method, nudging authors off the escape hatch. It is the pure
// decision half of MagusCmd, split out so it can be tested without the nested exec.
func warnIfTypedSubcommand(ctx context.Context, args []string) {
	if len(args) > 0 && typedMagusSubcommands[args[0]] {
		slog.WarnContext(ctx, "magus.cmd called for a subcommand with a dedicated method; prefer it for clarity and a stable signature",
			"subcommand", args[0],
			"hint", fmt.Sprintf("use magus.%s([...]) instead of magus.cmd([%q, ...])", args[0], args[0]))
	}
}

// MagusRun runs `magus run <args>` recursively; see runMagus.
func MagusRun(ctx context.Context, args []string, opts map[string]any) (types.ExecResult, error) {
	return runMagusSub(ctx, "run", args, opts)
}

// MagusDescribe runs `magus describe <args>`; see runMagus.
func MagusDescribe(ctx context.Context, args []string, opts map[string]any) (types.ExecResult, error) {
	return runMagusSub(ctx, "describe", args, opts)
}

// MagusInsight runs `magus insight <args>`; see runMagus.
func MagusInsight(ctx context.Context, args []string, opts map[string]any) (types.ExecResult, error) {
	return runMagusSub(ctx, "insight", args, opts)
}

// MagusDoctor runs `magus doctor <args>`; see runMagus.
func MagusDoctor(ctx context.Context, args []string, opts map[string]any) (types.ExecResult, error) {
	return runMagusSub(ctx, "doctor", args, opts)
}

// runMagusSub runs a nested magus invocation for subcommand sub: it prepends sub
// to args (so the subcommand name is fixed by the caller, not user-supplied) and
// hands off to runMagus.
func runMagusSub(ctx context.Context, sub string, args []string, opts map[string]any) (types.ExecResult, error) {
	return runMagus(ctx, sub, append([]string{sub}, args...), opts)
}

// runMagus runs a nested magus invocation with the full arg vector, yielding the
// caller's concurrency slot for the duration so the child can run. Output streams
// live and is captured: on success it returns the same {stdout, stderr, code, ok}
// record as os.exec, so a magusfile can read a subcommand's output (e.g. `magus
// describe graph -o markdown` to generate MAGUS.md). It raises (non-nil error, nil
// record) when the child can't launch or exits non-zero, mirroring os.exec. label
// names the calling method for error messages.
//
// The child runs in the working directory carried by ctx (WithCwd), so a nested
// project describes/insights its own project rather than the root workspace (the
// contextual-cwd contract every magus stdlib primitive honors). opts may carry
// "root" (string), emitted as the global --root flag (which precedes the subcommand).
func runMagus(ctx context.Context, label string, args []string, opts map[string]any) (types.ExecResult, error) {
	self, err := os.Executable()
	if err != nil {
		return types.ExecResult{}, fmt.Errorf("magus.%s: executable: %w", label, err)
	}

	// Global flags (e.g. --root) precede the subcommand and its args.
	var full []string
	if root, ok := opts["root"].(string); ok && root != "" {
		full = append(full, "--root", root)
	}
	full = append(full, args...)

	// Re-inject the daemon socket vars: childEnv withholds them from subprocesses
	// (the socket is unauthenticated - MGS2008), but a nested magus is a legitimate
	// recursive invocation that needs daemon access. Passed as Env overrides, which
	// childEnv layers last so they win; MAGUS/MAGUS_LEVEL are added by childEnv.
	var env []string
	for _, k := range []string{"MAGUS_DAEMON_SOCKET", "MAGUS_DAEMON_ADDRESS"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
			// Debug, not Info: this fires on every recursive invocation (a fan-out can
			// spawn many), so at default verbosity it is noise. An internal correctness
			// note, not user-actionable; surface it only at -v.
			slog.DebugContext(ctx, types.FormatDiagnostic(types.DaemonSocketWithheld,
				"daemon socket injected into recursive magus invocation"), "var", k)
		}
	}

	// Run in the contextual project dir; "" inherits the process cwd (the
	// behavior for magusfile targets that run under a process chdir).
	dir, _ := CwdFromContext(ctx)

	// opts.quiet captures the output without echoing it to the console, for a
	// command whose stdout is consumed (e.g. written to a file), not displayed.
	quiet, _ := opts["quiet"].(bool)

	lim := cache.LimiterFromContext(ctx)
	var rec types.ExecResult
	var cmdErr error
	runFn := func() error {
		res, err := run.Exec(ctx, self, full, run.ExecOptions{Dir: dir, Env: env, Capture: true, Quiet: quiet})
		switch {
		case err != nil && errors.Is(err, &types.DiagnosticError{Code: types.ExecDenied}):
			cmdErr = err
		case res.Code != 0 && !res.Started:
			// The child never launched (binary not found, permission, ctx cancelled
			// before exec); surface the real cause, not a fabricated "code -1".
			// Mirrors os.exec's runResult.
			cmdErr = fmt.Errorf("magus.%s: %s: %w", label, strings.Join(full, " "), err)
		case res.Code != 0:
			cmdErr = fmt.Errorf("magus.%s: %s exited with code %d", label, strings.Join(full, " "), res.Code)
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
		return types.ExecResult{}, fmt.Errorf("magus.%s: %w", label, err)
	}
	return rec, cmdErr
}
